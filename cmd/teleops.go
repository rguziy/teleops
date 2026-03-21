package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gopkg.in/ini.v1"
)

const (
	AppVersion       = "1.0.1"
	AppName          = "teleops"
	AppDesc          = "Telegram-based Host Controller (Remote Ops via Bot)"
	stopPollInterval = time.Second
	stopTimeout      = 20 * time.Second
)

// Config holds all application settings.
type Config struct {
	Token     string
	AllowedID int64
	Commands  map[string]string
	LogPath   string
}

type program struct {
	cfg *Config
	bot *tgbotapi.BotAPI
}

// main is the entry point of the program.
//
// The available commands are:
//   - start: start the bot in foreground mode
//   - stop: request a graceful shutdown using the pid file
//   - restart: stop the running process and start it again
//   - status: show whether teleops is running, stopping, or stale
func main() {
	flag.Usage = usage

	pidFileFlag := flag.String("pid-file", "", "Path to PID file")
	forceFlag := flag.Bool("force", false, "Overwrite an existing config file during init")
	verFlag := flag.Bool("version", false, "Show version")
	helpFlag := flag.Bool("help", false, "Show help")

	flag.Parse()

	if *verFlag {
		fmt.Printf("%s v%s (%s/%s)\n", AppName, AppVersion, runtime.GOOS, runtime.GOARCH)
		return
	}

	if *helpFlag {
		usage()
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		usage()
		return
	}
	if len(args) > 1 {
		log.Fatalf("unknown arguments: %s", strings.Join(args[1:], " "))
	}

	command := strings.ToLower(strings.TrimSpace(args[0]))
	confPath, err := resolveConfigPath()
	if err != nil {
		log.Fatal(err)
	}
	pidPath := resolvePIDFilePath(confPath, *pidFileFlag)
	stopPath := resolveStopFilePath(pidPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	prg := &program{}

	switch command {
	case "init":
		if err := initConfig(confPath, *forceFlag); err != nil {
			log.Fatal(err)
		}
	case "start":
		if err := prg.start(ctx, pidPath, stopPath); err != nil {
			log.Fatal(err)
		}
	case "stop":
		if err := stopProcess(pidPath, stopPath); err != nil {
			log.Fatal(err)
		}
	case "restart":
		if err := restartProcess(ctx, prg, pidPath, stopPath); err != nil {
			log.Fatal(err)
		}
	case "status":
		if err := statusProcess(pidPath, stopPath); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		log.Fatalf("unknown command: %s", command)
	}
}

// start ensures that only one instance of the program is running,
// writes the PID file, and runs the command loop. It will listen
// for incoming commands from the allowed user ID, execute them,
// and return the output to the user. If the context is cancelled,
// it will also gracefully shutdown. If a stop request is received, it
// will also gracefully shutdown.
func (p *program) start(ctx context.Context, pidPath, stopPath string) error {
	if err := ensureSingleInstance(pidPath, stopPath); err != nil {
		return err
	}

	cfg, confPath, err := loadConfig()
	if err != nil {
		return err
	}

	if err := writePIDFile(pidPath); err != nil {
		return err
	}
	defer cleanupControlFiles(pidPath, stopPath)

	return p.run(ctx, stopPath, cfg, confPath)
}

// run loads the configuration, sets up the Telegram bot, and
// starts the command loop. It will listen for incoming
// commands from the allowed user ID, execute them, and
// return the output to the user. It will also listen for
// stop requests and gracefully shutdown when a stop request is
// received. If the context is cancelled, it will also
// gracefully shutdown.
func (p *program) run(ctx context.Context, stopPath string, cfg *Config, confPath string) error {
	p.cfg = cfg

	if cfg.LogPath != "" {
		f, err := os.OpenFile(cfg.LogPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err == nil {
			log.SetOutput(f)
			defer f.Close()
		}
	}
	log.Printf("Using config: %s", confPath)

	bot, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		return fmt.Errorf("telegram init failed: %w", err)
	}

	p.bot = bot

	offset, err := p.discardPendingUpdates()
	if err != nil {
		return err
	}

	log.Println("TeleOps is now online and listening...")

	u := tgbotapi.NewUpdate(offset)
	u.Timeout = 60
	updates := p.bot.GetUpdatesChan(u)
	defer p.bot.StopReceivingUpdates()

	stopTicker := time.NewTicker(stopPollInterval)
	defer stopTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("TeleOps is shutting down...")
			return nil
		case <-stopTicker.C:
			if fileExists(stopPath) {
				log.Println("TeleOps stop requested...")
				return nil
			}
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			if update.Message == nil || update.Message.From.ID != p.cfg.AllowedID {
				continue
			}
			go p.handleCommand(update.Message)
		}
	}
}

// discardPendingUpdates advances the Telegram update offset so startup only
// processes messages that arrive after the bot is online.
func (p *program) discardPendingUpdates() (int, error) {
	offset := 0

	for {
		updates, err := p.bot.GetUpdates(tgbotapi.UpdateConfig{Offset: offset, Limit: 100, Timeout: 0})
		if err != nil {
			return 0, fmt.Errorf("failed to fetch pending telegram updates: %w", err)
		}
		if len(updates) == 0 {
			return offset, nil
		}

		for _, update := range updates {
			if next := update.UpdateID + 1; next > offset {
				offset = next
			}
		}
	}
}

// handleCommand executes a command from the user's input text.
// It will parse the input text and execute a corresponding command
// from the configuration file. If the command is not found
// or an error occurs, it will send an error message back
// to the user. If the command succeeds, it will send the
// output of the command back to the user.
func (p *program) handleCommand(msg *tgbotapi.Message) {
	input := strings.ToLower(strings.TrimSpace(msg.Text))

	if input == "/help" || input == "/start" {
		var cmds []string
		for k := range p.cfg.Commands {
			cmds = append(cmds, k)
		}
		sort.Strings(cmds)
		reply := fmt.Sprintf("TeleOps v%s\n\nCommands:\n`%s`", AppVersion, strings.Join(cmds, "`, `"))
		p.sendMarkdown(msg.Chat.ID, reply)
		return
	}

	if input == "/version" {
		p.sendMarkdown(msg.Chat.ID, fmt.Sprintf("TeleOps Version: `%s`", AppVersion))
		return
	}

	shellCmd, exists := p.cfg.Commands[input]
	if !exists {
		p.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Unknown command. Try /help"))
		return
	}

	p.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Running..."))

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command(resolveWindowsShell(), "/C", shellCmd)
	} else {
		cmd = exec.Command(defaultUnixShell(), "-c", shellCmd)
	}

	output, err := cmd.CombinedOutput()
	result := string(output)

	if err != nil {
		errMsg := fmt.Sprintf("Error: %v\n\nOutput:\n`%s`", err, result)
		p.sendMarkdown(msg.Chat.ID, errMsg)
	} else {
		p.sendMarkdown(msg.Chat.ID, fmt.Sprintf("Finished:\n`%s`", result))
	}
}

// sendMarkdown sends a Markdown-formatted message to the user.
// It takes a chat ID and the text to be sent, and will send the
// message with Markdown formatting enabled.
func (p *program) sendMarkdown(chatID int64, text string) {
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = "Markdown"
	p.bot.Send(m)
}

// statusProcess checks the status of a running TeleOps process by
// reading the pid file and checking if the process is still running.
// If the process is running, it will also check if a stop request
// is pending. If the process is not running, it will check if
// the pid file is stale (still exists but the process is not
// running anymore). The result is printed to the console with
// a human-readable message.
func statusProcess(pidPath, stopPath string) error {
	pid, err := readPIDFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("TeleOps status: stopped (pid file: %s)\n", pidPath)
			return nil
		}
		return err
	}

	if processExists(pid) {
		state := "running"
		if fileExists(stopPath) {
			state = "stopping"
		}
		fmt.Printf("TeleOps status: %s (PID %d, pid file: %s)\n", state, pid, pidPath)
		return nil
	}

	fmt.Printf("TeleOps status: stale pid file (PID %d, pid file: %s)\n", pid, pidPath)
	return nil
}

// restartProcess stops the running TeleOps process (if it exists) and
// starts it again. It takes the context, the program instance, the
// path to the PID file, and the path to the stop request file. If the
// PID file exists, it will first stop the process using stopProcess,
// and then start it again using start. If the PID file does not
// exist, it will simply start the process using start. If an error
// occurs during stopping or starting, it will be returned.
func restartProcess(ctx context.Context, p *program, pidPath, stopPath string) error {
	if fileExists(pidPath) {
		if err := stopProcess(pidPath, stopPath); err != nil {
			return err
		}
	}
	return p.start(ctx, pidPath, stopPath)
}

// stopProcess stops the running TeleOps process (if it exists) by writing
// a stop request file next to the PID file. If the PID file exists,
// it will first check if the process is running, and if so, write
// a stop request file. If the process is not running, it will
// remove the stale PID file and return an error. If an error
// occurs while writing the stop request file, it will be returned.
// If the process does not stop within stopTimeout, it will return an
// error. If the process stops successfully, it will return nil.
func stopProcess(pidPath, stopPath string) error {
	pid, err := readPIDFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("teleops is not running")
		}
		return err
	}

	if !processExists(pid) {
		cleanupControlFiles(pidPath, stopPath)
		return fmt.Errorf("removed stale pid file at %s", pidPath)
	}

	if err := os.MkdirAll(filepath.Dir(stopPath), 0700); err != nil {
		return fmt.Errorf("failed to prepare stop file directory: %w", err)
	}
	if err := os.WriteFile(stopPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
		return fmt.Errorf("failed to write stop request file: %w", err)
	}

	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		if !fileExists(pidPath) {
			_ = os.Remove(stopPath)
			fmt.Println("TeleOps stopped.")
			return nil
		}
		if !processExists(pid) {
			cleanupControlFiles(pidPath, stopPath)
			fmt.Println("TeleOps stopped.")
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for teleops to stop")
}

// ensureSingleInstance checks if a pid file exists and if the
// process is running. If the process is running, it will
// return an error. If the process is not running, it will
// remove the pid file and the stop request file. If an error
// occurs during removal, it will be returned. If the pid
// file does not exist, it will return nil.
func ensureSingleInstance(pidPath, stopPath string) error {
	pid, err := readPIDFile(pidPath)
	if err == nil {
		if processExists(pid) {
			return fmt.Errorf("teleops is already running with PID %d; use 'teleops stop' or 'teleops restart'", pid)
		}
		cleanupControlFiles(pidPath, stopPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	_ = os.Remove(stopPath)
	return nil
}

// writePIDFile writes the current process ID to a pid file at the given path.
// It will create the directory for the pid file if it does not exist.
// If the pid file already exists, it will return an error.
// If an error occurs while writing the pid file, it will be returned.
// If the pid file is successfully written, it will print a message to the
// console indicating that TeleOps has started with the given PID and
// pid file path.
func writePIDFile(pidPath string) error {
	if err := os.MkdirAll(filepath.Dir(pidPath), 0700); err != nil {
		return fmt.Errorf("failed to prepare pid file directory: %w", err)
	}

	f, err := os.OpenFile(pidPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("pid file already exists at %s", pidPath)
		}
		return fmt.Errorf("failed to create pid file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		return fmt.Errorf("failed to write pid file: %w", err)
	}

	fmt.Printf("TeleOps started with PID %d. PID file: %s\n", os.Getpid(), pidPath)
	return nil
}

// readPIDFile reads the contents of a pid file at the given path and returns
// the process ID as an integer. If the pid file does not exist, it will
// return an error. If the contents of the pid file are invalid or
// cannot be parsed as an integer, it will return an error. If the
// process ID is less than or equal to zero, it will return an error.
// If the pid file is successfully read, it will return the process ID and
// nil.
func readPIDFile(pidPath string) (int, error) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file at %s", pidPath)
	}
	return pid, nil
}

// processExists checks if a process with the given PID exists.
// It will return true if the process exists, and false otherwise.
// On Windows, it uses the tasklist command to check if the process exists.
// On Unix-like systems, it uses os.FindProcess and os.Process.Signal to check if the process exists.
// If the given PID is less than or equal to zero, it will return false.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}

	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").CombinedOutput()
		if err != nil {
			return false
		}
		pidToken := fmt.Sprintf("\"%d\"", pid)
		return strings.Contains(string(out), ","+pidToken+",") || strings.Contains(string(out), pidToken)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// cleanupControlFiles removes the pid file and the stop request file
// at the given paths. It ignores any errors that occur during
// removal. If the files do not exist, it will return nil. If an
// error occurs during removal, it will be ignored.
func cleanupControlFiles(pidPath, stopPath string) {
	_ = os.Remove(pidPath)
	_ = os.Remove(stopPath)
}

// resolvePIDFilePath returns the path to the PID file based on the given
// configuration file path and override string. If the override string is
// not empty, it will return the override string. Otherwise, it will return
// the path to the PID file, which is the configuration file path with the
// directory and the extension removed, and replaced with the string
// "teleops.pid".
func resolvePIDFilePath(confPath, override string) string {
	if explicit := strings.TrimSpace(override); explicit != "" {
		return explicit
	}
	return filepath.Join(filepath.Dir(confPath), "teleops.pid")
}

// resolveStopFilePath returns the path to the stop request file based on the given
// PID file path. If the PID file path has an extension, it will remove the extension
// and replace it with ".stop". If the PID file path does not have an extension, it will
// append ".stop" to the end of the path.
func resolveStopFilePath(pidPath string) string {
	ext := filepath.Ext(pidPath)
	if ext == "" {
		return pidPath + ".stop"
	}
	return strings.TrimSuffix(pidPath, ext) + ".stop"
}

// fileExists checks if a file exists at the given path.
// It will return true if the file exists, and false otherwise.
// It does not check if the file is a regular file or not.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Show application help message.
func usage() {
	fmt.Printf("%s v%s - %s\n", AppName, AppVersion, AppDesc)
	fmt.Println("\nUsage:")
	fmt.Println("  teleops [--force] init")
	fmt.Println("  teleops [--pid-file path] <start|stop|restart|status>")
	fmt.Println("  teleops [-version] [-help]")

	fmt.Println("\nCommands:")
	fmt.Println("  init             Create the default config file")
	fmt.Println("  start            Start the bot in foreground mode")
	fmt.Println("  stop             Request a graceful shutdown using the pid file")
	fmt.Println("  restart          Stop the running process and start it again")
	fmt.Println("  status           Show whether teleops is running, stopping, or stale")

	fmt.Println("\nOptions:")
	fmt.Println("  --force          Overwrite the existing config file during init")
	fmt.Println("  --pid-file path  Path to PID file (default: next to config)")
	fmt.Println("  -version         Show application version")
	fmt.Println("  -help            Show this detailed help message")

	fmt.Println("\nEnvironment Variables:")
	fmt.Println("  TELEOPS_CONFIG    Path to INI config file")
	fmt.Println("  TELEOPS_TOKEN     Telegram Bot Token (highest priority)")
	fmt.Println("  TELEOPS_USER_ID   Allowed Telegram User ID")

	fmt.Println("\nConfiguration:")
	fmt.Println("  Default path:     ~/.config/teleops/teleops.conf")
	fmt.Println("  PID file default: ~/.config/teleops/teleops.pid")
	fmt.Println("  Format:           INI with [telegram], [commands], [logging] sections")
}

// loadConfig resolves the configured path and loads the existing INI file.
// It does not create a config automatically; users should run `teleops init`
// first when the config file is missing.
func loadConfig() (*Config, string, error) {
	confPath, err := resolveConfigPath()
	if err != nil {
		return nil, "", err
	}

	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		return nil, confPath, fmt.Errorf("config not found at %s; run 'teleops init' first", confPath)
	}

	iniFile, err := ini.Load(confPath)
	if err != nil {
		return nil, confPath, fmt.Errorf("failed to parse INI file: %v", err)
	}

	get := func(sec, key, env string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		return iniFile.Section(sec).Key(key).String()
	}

	token := get("telegram", "bot_token", "TELEOPS_TOKEN")
	if token == "" {
		return nil, confPath, fmt.Errorf("missing 'bot_token' in [telegram] section or TELEOPS_TOKEN env var")
	}

	var uid int64
	envID := os.Getenv("TELEOPS_USER_ID")
	if envID != "" {
		parsedID, err := strconv.ParseInt(envID, 10, 64)
		if err != nil {
			return nil, confPath, fmt.Errorf("invalid TELEOPS_USER_ID env var: %v", err)
		}
		uid = parsedID
	} else {
		uid = iniFile.Section("telegram").Key("allowed_user_id").MustInt64(0)
	}

	if uid == 0 {
		return nil, confPath, fmt.Errorf("missing or zero 'allowed_user_id' in [telegram] section or TELEOPS_USER_ID env var")
	}

	commands := iniFile.Section("commands").KeysHash()
	if len(commands) == 0 {
		log.Println("Warning: No commands defined in [commands] section")
	}

	return &Config{
		Token:     token,
		AllowedID: uid,
		Commands:  commands,
		LogPath:   get("logging", "log_path", "TELEOPS_LOG"),
	}, confPath, nil
}

// initConfig creates the default config file at the resolved path.
// If the file already exists, it requires force=true to overwrite it.
func initConfig(confPath string, force bool) error {
	if _, err := os.Stat(confPath); err == nil {
		if !force {
			return fmt.Errorf("config already exists at %s; rerun with --force to overwrite it", confPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to inspect config path %s: %w", confPath, err)
	}

	if err := createDefault(confPath); err != nil {
		return err
	}
	fmt.Printf("Default config created at %s. Edit it and then run 'teleops start'.\n", confPath)
	return nil
}

// resolveConfigPath resolves the path to the configuration file.
// It will first check for the TELEOPS_CONFIG environment variable, and
// if it is not empty, it will return the value of the variable.
// If the variable is empty, it will try to resolve the user home directory
// using os.UserHomeDir() and return a default configuration file path
// in the user's home directory. If the user home directory cannot be
// resolved, it will return an error.
func resolveConfigPath() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("TELEOPS_CONFIG")); explicit != "" {
		return explicit, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".config", "teleops", "teleops.conf"), nil
}

// resolveWindowsShell resolves the path to the Windows shell executable
// (cmd.exe) based on the following priority order:
//
// 1. The value of the COMSPEC environment variable.
// 2. The value of the SystemRoot environment variable.
// If neither of the above environment variables are set, it will
// return "cmd.exe" as the default shell executable path.
func resolveWindowsShell() string {
	if shell := strings.TrimSpace(os.Getenv("COMSPEC")); shell != "" {
		return shell
	}
	if root := strings.TrimSpace(os.Getenv("SystemRoot")); root != "" {
		return filepath.Join(root, "System32", "cmd.exe")
	}
	return "cmd.exe"
}

// defaultUnixShell returns the default shell executable path for Unix-like systems (/bin/sh).
func defaultUnixShell() string {
	return "/bin/sh"
}

// createDefault writes a default configuration file to the given path.
func createDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	shutdownCmd := "/sbin/shutdown now"
	restartCmd := "/sbin/reboot now"
	if runtime.GOOS == "windows" {
		shutdownCmd = "shutdown /s /t 0"
		restartCmd = "shutdown /r /t 0"
	}

	logPath := filepath.Join(filepath.Dir(path), "teleops.log")
	content := fmt.Sprintf(`; TeleOps configuration
; Fill in the Telegram settings before running "teleops start".

[telegram]
; Token from BotFather, for example: 123456789:AAExampleBotTokenReplaceMe
bot_token =
; Your personal Telegram numeric user ID, for example: 937778855
allowed_user_id =

[commands]
/shutdown = %s
/restart = %s

[logging]
log_path = %s
`, shutdownCmd, restartCmd, logPath)

	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write default config: %w", err)
	}
	return nil
}

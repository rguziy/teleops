# Technical Requirements
1. **Platform**: Written in Go (Golang) for cross-platform compatibility (Linux/Windows).
2. **Execution Model**:
- Runs as a **foreground console process**.
- Can be supervised externally when needed (for example systemd on Linux or a Windows service wrapper).
- Example service definitions may be stored in-repo under `services/linux` and `services/windows`.
- Supports explicit lifecycle commands: `init`, `start`, `stop`, `restart`, and `status`.
- Uses **Goroutines** for non-blocking command execution (can run multiple commands simultaneously).
- Captures `STDOUT` and `STDERR` and returns them to the Telegram user.
3. Configuration:
- Format: **INI** with sections (`[telegram]`, `[commands]`, `[logging]`).
- Explicit path override: `TELEOPS_CONFIG`.
- Default location: `~/.config/teleops/teleops.conf`.
- Default config creation is explicit via `teleops init` (or `teleops --force init` to overwrite).
- **Priority**: Environment Variables > INI Config.
4. Process Control:
- Supports `--pid-file` to override the PID file path.
- Default PID file location is next to the config file.
- Prevents duplicate starts when a live PID file already exists.
- Removes PID and stop-request files on graceful shutdown.
5. **Security**:
- Strict **User ID filtering** (only responds to the owner's Telegram ID).
- Uses `cmd.exe /C` on Windows and `/bin/sh -c` on Unix-like systems.
6. **Notifications**:
- Logs actions to a local log file.
- Returns command results and failures directly in Telegram.
7. **CLI Interface**:
- Supports `init`, `start`, `stop`, `restart`, `status`, `-version`, and `-help`.
- Running without arguments displays the help message.
8. **Service Integration Examples**:
- Linux example should target `systemd` with `Type=simple`, because `teleops start` stays in the foreground.
- Windows example should use an external wrapper such as NSSM, because TeleOps is not implemented as a native Windows Service.

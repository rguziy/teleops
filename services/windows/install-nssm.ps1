# Example installer for running TeleOps as a Windows service through NSSM.
# TeleOps is a foreground console app, so it should be wrapped by a service
# manager instead of being registered directly with the Service Control Manager.

param(
    [string]$ServiceName = "TeleOps",
    [string]$NssmPath = "C:\Tools\nssm\nssm.exe",
    [string]$TeleopsPath = "C:\Program Files\TeleOps\teleops.exe",
    [string]$ConfigPath = "C:\ProgramData\TeleOps\teleops.conf",
    [string]$WorkDir = "C:\ProgramData\TeleOps"
)

if (-not (Test-Path $NssmPath)) {
    throw "nssm.exe not found at $NssmPath"
}

if (-not (Test-Path $TeleopsPath)) {
    throw "teleops.exe not found at $TeleopsPath"
}

if (-not (Test-Path $WorkDir)) {
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
}

& $NssmPath install $ServiceName $TeleopsPath start
& $NssmPath set $ServiceName AppDirectory $WorkDir
& $NssmPath set $ServiceName AppEnvironmentExtra "TELEOPS_CONFIG=$ConfigPath"
& $NssmPath set $ServiceName AppStopMethodSkip 0
& $NssmPath set $ServiceName AppStdout "$WorkDir\teleops-service.out.log"
& $NssmPath set $ServiceName AppStderr "$WorkDir\teleops-service.err.log"
& $NssmPath set $ServiceName Start SERVICE_AUTO_START

Write-Host "Service '$ServiceName' configured."
Write-Host "Start it with: Start-Service $ServiceName"

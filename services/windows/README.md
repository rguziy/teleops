# Windows Service Example

TeleOps runs as a foreground console process. On Windows it should be hosted by a
service wrapper such as NSSM instead of being registered directly as a native
Windows service.

## Included example

`install-nssm.ps1` shows one way to install TeleOps with NSSM:

1. Install NSSM and adjust the `NssmPath` parameter.
2. Copy `teleops.exe` to a stable location.
3. Create `teleops.conf` and point `TELEOPS_CONFIG` at it.
4. Run the script in an elevated PowerShell session.

Example:

```powershell
.\services\windows\install-nssm.ps1 `
  -NssmPath "C:\Tools\nssm\nssm.exe" `
  -TeleopsPath "C:\Program Files\TeleOps\teleops.exe" `
  -ConfigPath "C:\ProgramData\TeleOps\teleops.conf" `
  -WorkDir "C:\ProgramData\TeleOps"
```

After installation:

```powershell
Start-Service TeleOps
Get-Service TeleOps
```

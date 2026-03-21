param(
    [string]$Version = "1.0.1",
    [string]$BuildDir = "build",
    [string]$BinaryName = "teleops",
    [string[]]$Targets = @(
        "linux-amd64",
        "linux-armv5",
        "linux-armv6",
        "linux-armv7",
        "windows-amd64"
    ),
    [string]$GoCommand = "go"
)

$ErrorActionPreference = "Stop"

function New-ReleaseArchive {
    param(
        [Parameter(Mandatory = $true)][string]$Target,
        [Parameter(Mandatory = $true)][string]$Version,
        [Parameter(Mandatory = $true)][string]$BuildDir,
        [Parameter(Mandatory = $true)][string]$BinaryName,
        [Parameter(Mandatory = $true)][string]$GoCommand
    )

    $parts = $Target.Split('-', 2)
    if ($parts.Count -ne 2) {
        throw "Invalid target '$Target'. Expected format <os>-<arch> or <os>-armvN."
    }

    $goos = $parts[0]
    $archPart = $parts[1]
    $goarch = $archPart
    $goarm = $null

    if ($archPart -like 'armv*') {
        $goarch = 'arm'
        $goarm = $archPart.Substring(4)
        if ([string]::IsNullOrWhiteSpace($goarm)) {
            throw "Invalid ARM target '$Target'."
        }
    }

    $ext = if ($goos -eq 'windows') { '.exe' } else { '' }
    $outName = "$BinaryName-$Version-$Target"
    $binaryPath = Join-Path $BuildDir ($outName + $ext)
    $archivePath = Join-Path $BuildDir ($outName + '.zip')

    $goarmSuffix = if ($goarm) { " GOARM=$goarm" } else { "" }
    Write-Host "Building $outName for $goos/$goarch$goarmSuffix"

    $env:GOOS = $goos
    $env:GOARCH = $goarch
    if ($goarm) {
        $env:GOARM = $goarm
    } else {
        Remove-Item Env:GOARM -ErrorAction SilentlyContinue
    }

    try {
        & $GoCommand build '-ldflags=-s -w' '-o' $binaryPath './cmd'
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed for $Target"
        }

        if (Test-Path $archivePath) {
            Remove-Item $archivePath -Force
        }
        Compress-Archive -Path $binaryPath -DestinationPath $archivePath -Force
    }
    finally {
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        Remove-Item Env:GOARM -ErrorAction SilentlyContinue
    }

    Remove-Item $binaryPath -Force
}

if (Test-Path $BuildDir) {
    Remove-Item $BuildDir -Recurse -Force
}
New-Item -ItemType Directory -Path $BuildDir | Out-Null

foreach ($target in $Targets) {
    New-ReleaseArchive -Target $target -Version $Version -BuildDir $BuildDir -BinaryName $BinaryName -GoCommand $GoCommand
}

Write-Host "Release archives written to $BuildDir"

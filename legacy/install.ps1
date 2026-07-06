# HydraQL CodeQL Installer (Windows PowerShell)
# Run in an elevated PowerShell (Run as Administrator)

param(
  [string]$CodeqlVersion = "2.17.6",
  [string]$InstallRoot = "$env:ProgramFiles\\CodeQL"
)

$ErrorActionPreference = "Stop"

function Write-Info($msg) { Write-Host ">> $msg" -ForegroundColor Cyan }
function Write-Err($msg) { Write-Host "ERROR: $msg" -ForegroundColor Red }

Write-Info "Detecting architecture..."
$arch = (Get-CimInstance Win32_Processor).AddressWidth
if ($arch -ne 64) { Write-Err "Only 64-bit Windows is supported."; exit 1 }

$zipUrl = "https://github.com/github/codeql-cli-binaries/releases/download/v$CodeqlVersion/codeql-win64.zip"
$destDir = Join-Path $InstallRoot "codeql-$CodeqlVersion"
$tmpZip = Join-Path $env:TEMP "codeql-$CodeqlVersion.zip"

Write-Info "Downloading CodeQL $CodeqlVersion from $zipUrl"
Invoke-WebRequest -Uri $zipUrl -OutFile $tmpZip

Write-Info "Expanding to $destDir"
New-Item -ItemType Directory -Force -Path $destDir | Out-Null
Expand-Archive -Path $tmpZip -DestinationPath $destDir -Force
Remove-Item $tmpZip -Force

# Create stable symlink folder "CodeQL" pointing to the versioned dir
$stable = Join-Path $InstallRoot "CodeQL"
if (Test-Path $stable) { Remove-Item $stable -Recurse -Force }
cmd /c mklink /D "$stable" "$destDir" | Out-Null

# Add to PATH (system-wide)
$codeqlBin = Join-Path $stable "codeql.exe"
$sysPath = [Environment]::GetEnvironmentVariable("Path",[System.EnvironmentVariableTarget]::Machine)
if ($sysPath -notlike "*$stable*") {
  [Environment]::SetEnvironmentVariable("Path", "$sysPath;$stable", [System.EnvironmentVariableTarget]::Machine)
  Write-Info "Added $stable to System PATH."
}

Write-Info "Verifying..."
& $codeqlBin --version
Write-Host "✅ CodeQL installed successfully." -ForegroundColor Green
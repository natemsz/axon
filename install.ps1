<#
.SYNOPSIS
  Install EveSynapse Axon on Windows (per user, no admin needed).

.DESCRIPTION
  Run from the folder holding the downloaded axon binary, or pipe the
  script from GitHub (then the binary is downloaded for you):

    irm https://raw.githubusercontent.com/natemsz/axon/main/install.ps1 | iex

  It installs axon.exe under %LOCALAPPDATA%\Programs\EveSynapse Axon,
  adds that folder to your user PATH, detects the EVE Chatlogs folder
  (and records it in an existing axon.json), creates Desktop and
  Start Menu shortcuts, and opens Axon straight to its GUI.

.PARAMETER NoLaunch
  Install without opening Axon at the end.
#>
param([switch]$NoLaunch)

$ErrorActionPreference = "Stop"
$AppName = "EveSynapse Axon"
$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\EveSynapse Axon"
$Exe = Join-Path $InstallDir "axon.exe"
$ReleaseAsset = "https://github.com/natemsz/axon/releases/latest/download/axon-windows-amd64.exe"

# 1. The program: beside this script (or an axon.exe already), else
#    downloaded from the latest release.
New-Item -ItemType Directory -Force $InstallDir | Out-Null
$here = if ($PSScriptRoot) { $PSScriptRoot } else { (Get-Location).Path }
$local = @(
  (Join-Path $here "axon-windows-amd64.exe"),
  (Join-Path $here "axon.exe")
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if ($local) {
  Copy-Item $local $Exe -Force
  Write-Host "Installed $AppName from $local"
} elseif (-not (Test-Path $Exe)) {
  Write-Host "Downloading $AppName..."
  Invoke-WebRequest -Uri $ReleaseAsset -OutFile $Exe
  Write-Host "Installed $AppName to $Exe"
}

# 2. On PATH (user PATH; no admin needed, no duplicates).
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ';') -notcontains $InstallDir) {
  [Environment]::SetEnvironmentVariable("Path", (($userPath.TrimEnd(';') + ';' + $InstallDir).Trim(';')), "User")
  Write-Host "Added $InstallDir to your PATH (new terminals will see 'axon')."
}
if (($env:Path -split ';') -notcontains $InstallDir) { $env:Path += ";$InstallDir" }

# 3. Detect the EVE Chatlogs folder, the way the program itself does
#    (watch.DefaultLogDirs): OneDrive-redirected Documents first.
$rel = "Documents\EVE\logs\Chatlogs"
$candidates = @()
if ($env:OneDrive) { $candidates += Join-Path $env:OneDrive $rel }
if ($env:USERPROFILE) { $candidates += Join-Path $env:USERPROFILE $rel }
$logs = $candidates | Where-Object { Test-Path $_ -PathType Container } | Select-Object -First 1
if ($logs) {
  Write-Host "Found EVE chat logs: $logs"
  $cfgPath = Join-Path $InstallDir "axon.json"
  if (Test-Path $cfgPath) {
    $cfg = Get-Content $cfgPath -Raw | ConvertFrom-Json
    if ($cfg.logs -ne $logs) {
      $cfg | Add-Member -NotePropertyName logs -NotePropertyValue $logs -Force
      $cfg | ConvertTo-Json -Depth 5 | Set-Content $cfgPath -Encoding UTF8
      Write-Host "Recorded it as `"logs`" in $cfgPath"
    }
  }
} else {
  Write-Host "EVE chat logs not found yet (looked in: $($candidates -join ', ')). Axon will ask or find them once the game has written logs."
}

# 4. Shortcuts: Desktop and Start Menu, opening straight to the GUI.
$shell = New-Object -ComObject WScript.Shell
$desktopDir = [Environment]::GetFolderPath("Desktop")
if (-not $desktopDir) { $desktopDir = Join-Path $env:USERPROFILE "Desktop" }
$programsDir = [Environment]::GetFolderPath("Programs")
if (-not $programsDir) { $programsDir = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs" }
$shortcutDirs = @(
  $desktopDir,
  (Join-Path $programsDir $AppName)
)
foreach ($dir in $shortcutDirs) {
  New-Item -ItemType Directory -Force $dir | Out-Null
  $lnk = $shell.CreateShortcut((Join-Path $dir "$AppName.lnk"))
  $lnk.TargetPath = $Exe
  $lnk.WorkingDirectory = $InstallDir
  $lnk.Description = "EveSynapse Axon - post your EVE intel channels to EveSynapse"
  $lnk.Save()
}
Write-Host "Shortcuts created on the Desktop and in the Start Menu."

# 5. Open straight to the GUI.
if (-not $NoLaunch) {
  Start-Process $Exe -WorkingDirectory $InstallDir
  Write-Host "Axon is opening in your browser (http://127.0.0.1:47611/)."
}
Write-Host "Done. Axon's settings page runs at http://127.0.0.1:47611/ whenever Axon is running."

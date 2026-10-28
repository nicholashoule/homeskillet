# Writes PowerShell's profile in Documents as a line that loads
# ~/.config/powershell/profile.ps1, linked from this repository. PowerShell
# reads its profile only from Documents, which OneDrive may move and sync, so
# the profile is loaded from there rather than linked. An existing profile is
# left alone.
$ErrorActionPreference = 'Stop'
$line = 'if (Test-Path "$HOME\.config\powershell\profile.ps1") { . "$HOME\.config\powershell\profile.ps1" }'
$path = $PROFILE.CurrentUserAllHosts
# Under homeskillet --home, a scratch home, write into its Documents, where
# homeskillet looks, and leave yours alone.
$selected = $env:HOMESKILLET_HOME
if ($selected -and $selected -ne [Environment]::GetFolderPath('UserProfile')) {
    $path = Join-Path $selected 'Documents\PowerShell\profile.ps1'
}
if (Test-Path -LiteralPath $path) {
    Write-Output "$path already exists; add this line to it yourself:"
    Write-Output "  $line"
    exit 0
}
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $path) | Out-Null
# Without -Force, New-Item fails rather than replace a file that appeared since.
New-Item -ItemType File -Path $path -Value "$line`n" | Out-Null
Write-Output "wrote $path"

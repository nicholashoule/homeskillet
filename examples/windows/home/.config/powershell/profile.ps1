# PowerShell's profile, loaded by the line the profile setup step writes in
# Documents.

function ll { Get-ChildItem -Force @args }

if (Get-Command homeskillet -ErrorAction SilentlyContinue) {
    homeskillet completion pwsh | Out-String | Invoke-Expression
}

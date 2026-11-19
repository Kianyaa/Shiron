# Builds Shiron for the server, copies it over SSH, and runs setup.sh there.
#   .\deploy\deploy.ps1 -Server 1.2.3.4 -Key C:\path\to\ssh-key.key
# Add -WithDatabase on the first deploy to move your current settings (stop the bot on
# this PC first). shiron.conf is copied when it exists. The token is typed on the server.
param(
    [Parameter(Mandatory)] [string] $Server,
    [Parameter(Mandatory)] [string] $Key,
    [string] $User = "ubuntu",
    [ValidateSet("arm64", "amd64")] [string] $Arch = "arm64",
    [switch] $WithDatabase
)
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$bin = "shiron-linux-$Arch"
Write-Host "Building $bin..."
$env:GOOS = "linux"; $env:GOARCH = $Arch; $env:CGO_ENABLED = "0"
try { go build -trimpath -ldflags="-s -w" -o $bin . } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED }
if ($LASTEXITCODE -ne 0) { throw "build failed" }

$files = @($bin, "deploy/setup.sh")
if (Test-Path shiron.conf) { $files += "shiron.conf" }
if ($WithDatabase) {
    if (Get-Process shiron -ErrorAction SilentlyContinue) { throw "Stop shiron.exe on this PC first, so the database is complete." }
    $files += "shiron.db"
}

$target = "$User@$Server"
ssh -i $Key $target "mkdir -p ~/shiron-upload"
scp -i $Key @files "${target}:~/shiron-upload/"
if ($LASTEXITCODE -ne 0) { throw "upload failed" }
# -t so setup.sh can ask for the token with hidden typing.
ssh -t -i $Key $target "sudo sh ~/shiron-upload/setup.sh && rm -rf ~/shiron-upload"

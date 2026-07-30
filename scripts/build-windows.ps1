param(
  [string]$Version = "dev",
  [string]$GitHubUrl = "https://github.com/y08lin4/Main-SMTP-test"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$outputDir = Join-Path $projectRoot "dist"
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

$downloadUrl = ""
if ($GitHubUrl) {
  $GitHubUrl = $GitHubUrl.TrimEnd("/")
  $downloadUrl = "$GitHubUrl/releases/latest/download/SMTP-Tester-Windows-x64.exe"
}

$linkerFlags = "-s -w -X main.version=$Version -X main.githubURL=$GitHubUrl -X main.downloadURL=$downloadUrl"
$previousOS = $env:GOOS
$previousArch = $env:GOARCH
try {
  $env:GOOS = "windows"
  foreach ($architecture in @("amd64", "arm64")) {
    $env:GOARCH = $architecture
    $suffix = if ($architecture -eq "amd64") { "x64" } else { "arm64" }
    go build -trimpath -ldflags $linkerFlags -o (Join-Path $outputDir "SMTP-Tester-Windows-$suffix.exe") $projectRoot
    if ($LASTEXITCODE -ne 0) { throw "Go build failed for $architecture" }
  }
} finally {
  $env:GOOS = $previousOS
  $env:GOARCH = $previousArch
}

Write-Host "Windows executables created in $outputDir"

[CmdletBinding()]
param()

$projectRoot = Split-Path -Parent $PSScriptRoot
$source = Join-Path $projectRoot "web"
$destination = Join-Path $projectRoot "worker\web"

if (-not (Test-Path -LiteralPath $source -PathType Container)) {
    throw "找不到前端源目录：$source"
}

New-Item -ItemType Directory -Path $destination -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $source "*") -Destination $destination -Recurse -Force
Write-Host "已同步前端静态文件到 worker/web。"

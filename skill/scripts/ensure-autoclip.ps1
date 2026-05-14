$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "../..")
$CliDir = Join-Path $Root "cli"
$Existing = Get-Command autoclip -ErrorAction SilentlyContinue
if ($Existing) {
  Write-Output $Existing.Source
  exit 0
}

$Built = Join-Path $CliDir "autoclip.exe"
if (Test-Path $Built) {
  Write-Output $Built
  exit 0
}

$Go = Get-Command go -ErrorAction SilentlyContinue
if ($Go) {
  Push-Location $CliDir
  go build -o autoclip.exe ./cmd/autoclip
  Pop-Location
  Write-Output $Built
  exit 0
}

Write-Error "未找到 autoclip，也未找到 Go 工具链来构建它。"
exit 4

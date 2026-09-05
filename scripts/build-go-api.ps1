$repoRoot = Split-Path -Parent $PSScriptRoot
$portableGo = Join-Path $repoRoot '.tools\go-1.26.8\bin\go.exe'
$systemGo = Get-Command go -ErrorAction SilentlyContinue

if ($systemGo) {
  $goExecutable = $systemGo.Source
} elseif (Test-Path -LiteralPath $portableGo) {
  $goExecutable = $portableGo
} else {
  Write-Error 'Go is not installed. Install Go 1.26.8+ or place the portable SDK at .tools\go-1.26.8.'
  exit 1
}

$apiRoot = Join-Path $repoRoot 'apps\api-go'
$outputPath = Join-Path $apiRoot 'bin\api.exe'
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $outputPath) | Out-Null

$env:GOTOOLCHAIN = 'auto'
Push-Location $apiRoot
try {
  & $goExecutable build -o $outputPath ./cmd/api
  exit $LASTEXITCODE
} finally {
  Pop-Location
}

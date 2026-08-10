param(
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$GoArgs
)

$repoRoot = Split-Path -Parent $PSScriptRoot
$portableGo = Join-Path $repoRoot '.tools\go-1.26.5\bin\go.exe'
$systemGo = Get-Command go -ErrorAction SilentlyContinue

if ($systemGo) {
  $goExecutable = $systemGo.Source
} elseif (Test-Path -LiteralPath $portableGo) {
  $goExecutable = $portableGo
} else {
  Write-Error 'Go is not installed. Install Go 1.26.5+ or place the portable SDK at .tools\go-1.26.5.'
  exit 1
}

$env:GOTOOLCHAIN = 'local'
& $goExecutable @GoArgs
exit $LASTEXITCODE

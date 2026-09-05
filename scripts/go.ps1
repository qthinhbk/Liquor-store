param(
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$GoArgs
)

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

# Allow Go to select/download the patched version required by go.mod.
$env:GOTOOLCHAIN = 'auto'
& $goExecutable @GoArgs
exit $LASTEXITCODE

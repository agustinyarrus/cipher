# build.ps1 — compila Cipher como .exe release (sin consola, con icono embebido).
# Uso:  .\build.ps1            -> genera cipher.exe
#       .\build.ps1 -Debug     -> genera cipher-debug.exe (consola + logs CIPHER_DEBUG + modo --dump)

param([switch]$Debug)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$env:GOTOOLCHAIN = 'auto'

# Recurso de icono: regenerar cipher.ico (si falta) y rsrc.syso desde el .ico.
if (-not (Test-Path cipher.ico)) {
  Write-Host "Generando cipher.ico..."
  & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'gen-icon.ps1')
}
if ((Test-Path cipher.ico) -and -not (Test-Path rsrc.syso)) {
  Write-Host "Generando rsrc.syso desde cipher.ico..."
  go run github.com/akavel/rsrc@latest -ico cipher.ico -o rsrc.syso
}

if ($Debug) {
  go build -o cipher-debug.exe .
  Write-Host "OK -> $(Resolve-Path cipher-debug.exe)   (CIPHER_DEBUG=1 para logs; --dump <archivo> vuelca el render)"
} else {
  go build -ldflags="-H windowsgui -s -w" -o cipher.exe .
  Write-Host "OK -> $(Resolve-Path cipher.exe)"
}

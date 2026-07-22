<#
install.ps1 — instala Cipher en Program Files, lo agrega al Menú de Inicio y lo registra como
opción "Abrir con" para archivos de código. NO cambia tus aplicaciones por defecto (no secuestra
.py/.js/.go/… de tu editor): sólo suma Cipher al menú "Abrir con". Se auto-eleva (UAC).

  .\install.ps1            -> instala / actualiza
  .\install.ps1 -Uninstall -> desinstala
#>
param([switch]$Uninstall)
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path

# ---- auto-elevación (UAC) ----
$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
  $psExe = (Get-Process -Id $PID).Path
  $a = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$($MyInvocation.MyCommand.Path)`"")
  if ($Uninstall) { $a += '-Uninstall' }
  Write-Host "Pidiendo elevación (UAC)..."
  Start-Process -FilePath $psExe -Verb RunAs -ArgumentList $a -Wait
  return
}

# ---- constantes ----
$appName    = 'Cipher'
$version    = '1.0.0'
$publisher  = 'Agustin Yarrus'
$installDir = Join-Path $env:ProgramFiles 'Cipher'
$exe        = Join-Path $installDir 'cipher.exe'
$ico        = Join-Path $installDir 'cipher.ico'
$progId     = 'Cipher.Source'
$cls        = 'HKLM:\Software\Classes'
$appsKey    = "$cls\Applications\cipher.exe"
$startLnk   = Join-Path ([Environment]::GetFolderPath('CommonStartMenu')) 'Programs\Cipher.lnk'
$uninstKey  = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Cipher'
$appPaths   = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\App Paths\cipher.exe'

# extensiones que Cipher ofrece en "Abrir con": TODAS las que reconoce chroma (250+ lenguajes). Se
# las pedimos al propio exe (cipher.exe --exts <archivo>) para no hardcodear ~500 y quedar en sync.
$exts = @()
$srcExe = Join-Path $here 'cipher.exe'
if (Test-Path $srcExe) {
  try {
    $tmpExts = Join-Path $env:TEMP 'cipher-exts.txt'
    Start-Process -FilePath $srcExe -ArgumentList '--exts', "`"$tmpExts`"" -WindowStyle Hidden -Wait
    if (Test-Path $tmpExts) {
      $exts = ((Get-Content -Raw $tmpExts) -split ';') | ForEach-Object { $_.Trim() } | Where-Object { $_ }
      Remove-Item $tmpExts -Force -ErrorAction SilentlyContinue
    }
  } catch {}
}
if (-not $exts -or $exts.Count -lt 10) { # fallback si el exe no respondió
  $exts = '.go','.c','.h','.cpp','.cs','.java','.class','.kt','.py','.rb','.rs','.swift','.js','.ts',
          '.jsx','.tsx','.json','.html','.css','.php','.lua','.pl','.sh','.ps1','.bat','.sql','.r',
          '.dart','.vb','.toml','.yaml','.yml','.xml','.ini','.md','.rst','.txt','.asm','.proto','.tf'
}

function Remove-Key($p) { if (Test-Path $p) { Remove-Item $p -Recurse -Force -ErrorAction SilentlyContinue } }

# cerrar la instancia instalada si está corriendo
Get-Process cipher -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe } | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 300

# ============================ DESINSTALAR ============================
if ($Uninstall) {
  foreach ($e in $exts) {
    Remove-ItemProperty "$cls\$e\OpenWithProgids" -Name $progId -ErrorAction SilentlyContinue
    Remove-Key "$cls\SystemFileAssociations\$e\shell\cipher.edit"
  }
  Remove-Key "$cls\$progId"
  Remove-Key $appsKey
  Remove-Key $uninstKey
  Remove-Key $appPaths
  Remove-Item $startLnk -Force -ErrorAction SilentlyContinue
  Remove-Key $installDir
  Write-Host "Cipher desinstalado."
  return
}

# ============================ INSTALAR ============================
if (-not (Test-Path (Join-Path $here 'cipher.exe'))) { throw "No existe cipher.exe en $here; corré build.ps1 primero." }

# 1) copiar a Program Files
New-Item -ItemType Directory -Force $installDir | Out-Null
Copy-Item (Join-Path $here 'cipher.exe') $exe -Force
foreach ($f in 'cipher.ico','README.md','LICENSE','install.ps1') {
  if (Test-Path (Join-Path $here $f)) { Copy-Item (Join-Path $here $f) (Join-Path $installDir $f) -Force }
}

# 2) ProgId propio (icono + handler). Aparece como opción de "Abrir con"; no cambia el default.
New-Item -Path "$cls\$progId\shell\open\command" -Force | Out-Null
Set-ItemProperty "$cls\$progId" '(default)' 'Código fuente'
Set-ItemProperty "$cls\$progId" 'FriendlyTypeName' 'Código fuente'
New-Item -Path "$cls\$progId\DefaultIcon" -Force | Out-Null
Set-ItemProperty "$cls\$progId\DefaultIcon" '(default)' $ico
Set-ItemProperty "$cls\$progId\shell\open\command" '(default)' "`"$exe`" `"%1`""

# 3) registro de la aplicación (nombre amable + tipos soportados en "Abrir con")
New-Item -Path "$appsKey\shell\open\command" -Force | Out-Null
Set-ItemProperty $appsKey 'FriendlyAppName' $appName
New-Item -Path "$appsKey\DefaultIcon" -Force | Out-Null
Set-ItemProperty "$appsKey\DefaultIcon" '(default)' $ico
Set-ItemProperty "$appsKey\shell\open\command" '(default)' "`"$exe`" `"%1`""
New-Item -Path "$appsKey\SupportedTypes" -Force | Out-Null
foreach ($e in $exts) { Set-ItemProperty "$appsKey\SupportedTypes" $e '' }

# 4) sumar Cipher al menú "Abrir con" de cada extensión (sin tocar el default actual)
foreach ($e in $exts) {
  $owp = "$cls\$e\OpenWithProgids"
  New-Item -Path $owp -Force | Out-Null
  Set-ItemProperty $owp $progId ''
}

# 4b) verbo "Editar con Cipher" en el clic derecho de cada extensión de código (via
#     SystemFileAssociations: no toca el default ni el ProgID del tipo). En el menú nuevo de
#     Win11 aparece dentro de "Mostrar más opciones"; con el menú clásico restaurado, directo.
foreach ($e in $exts) {
  $verb = "$cls\SystemFileAssociations\$e\shell\cipher.edit"
  New-Item -Path "$verb\command" -Force | Out-Null
  Set-ItemProperty $verb '(default)' 'Editar con Cipher'
  Set-ItemProperty $verb 'Icon' "`"$exe`""
  Set-ItemProperty "$verb\command" '(default)' "`"$exe`" `"%1`""
}

# 5) App Paths (permite ejecutar "cipher" desde Ejecutar/iniciar)
New-Item -Path $appPaths -Force | Out-Null
Set-ItemProperty $appPaths '(default)' $exe

# 6) acceso directo en el Menú de Inicio
$wsh = New-Object -ComObject WScript.Shell
$lnk = $wsh.CreateShortcut($startLnk)
$lnk.TargetPath       = $exe
$lnk.WorkingDirectory = $installDir
$lnk.IconLocation     = "$exe,0"
$lnk.Description       = 'Visor de código dark y minimalista'
$lnk.Save()

# 7) entrada en Agregar o quitar programas
New-Item -Path $uninstKey -Force | Out-Null
Set-ItemProperty $uninstKey DisplayName     $appName
Set-ItemProperty $uninstKey DisplayIcon     "$exe,0"
Set-ItemProperty $uninstKey DisplayVersion  $version
Set-ItemProperty $uninstKey Publisher       $publisher
Set-ItemProperty $uninstKey InstallLocation $installDir
Set-ItemProperty $uninstKey UninstallString "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$installDir\install.ps1`" -Uninstall"
Set-ItemProperty $uninstKey NoModify 1 -Type DWord
Set-ItemProperty $uninstKey NoRepair 1 -Type DWord
try { Set-ItemProperty $uninstKey EstimatedSize ([math]::Round((Get-Item $exe).Length/1KB)) -Type DWord } catch {}

Write-Host "Cipher $version instalado en $installDir" -ForegroundColor Green
Write-Host "Acceso directo: $startLnk"
Write-Host "Para abrir un archivo: clic derecho -> 'Editar con Cipher' (en Win11, dentro de 'Mostrar más opciones'), 'Abrir con -> Cipher', o arrastralo a la ventana."

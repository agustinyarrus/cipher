# gen-icon.ps1 — genera el glifo </> de Cipher (squircle azul-noche + chevrons + slash).
# PowerShell 5.1 / System.Drawing. Produce cipher.ico multi-tamano con frames PNG (Vista+).
#   .\gen-icon.ps1
#
# Nota PS: el operador coma liga MAS fuerte que '*', asi que toda expresion 'n*$k' dentro de una
# lista de argumentos separada por comas va entre parentesis (si no, multiplica arrays y revienta).

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
Add-Type -AssemblyName System.Drawing

function New-RoundedPath([single]$x, [single]$y, [single]$w, [single]$h, [single]$r) {
  $p = New-Object System.Drawing.Drawing2D.GraphicsPath
  $d = $r * 2
  $p.AddArc($x, $y, $d, $d, 180, 90)
  $p.AddArc(($x + $w - $d), $y, $d, $d, 270, 90)
  $p.AddArc(($x + $w - $d), ($y + $h - $d), $d, $d, 0, 90)
  $p.AddArc($x, ($y + $h - $d), $d, $d, 90, 90)
  $p.CloseFigure()
  return $p
}

function New-Frame([int]$s) {
  $bmp = New-Object System.Drawing.Bitmap($s, $s, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.SmoothingMode     = 'AntiAlias'
  $g.InterpolationMode = 'HighQualityBicubic'
  $g.PixelOffsetMode   = 'HighQuality'
  $k = $s / 256.0

  # ---- fondo squircle con degrade azul-noche ----
  $bgPath = New-RoundedPath (8*$k) (8*$k) (240*$k) (240*$k) (58*$k)
  $rect = New-Object System.Drawing.RectangleF(([single](8*$k)), ([single](8*$k)), ([single](240*$k)), ([single](240*$k)))
  $c1 = [System.Drawing.Color]::FromArgb(255, 18, 22, 36)
  $c2 = [System.Drawing.Color]::FromArgb(255, 8, 9, 13)
  $bgBrush = New-Object System.Drawing.Drawing2D.LinearGradientBrush($rect, $c1, $c2, [single]90)
  $g.FillPath($bgBrush, $bgPath)
  $bd = New-Object System.Drawing.Pen([System.Drawing.Color]::FromArgb(45, 122, 162, 247), [single](2*$k))
  $g.DrawPath($bd, $bgPath)

  # ---- glifo </> ----
  $accent = [System.Drawing.Color]::FromArgb(255, 122, 162, 247) # slash
  $chev   = [System.Drawing.Color]::FromArgb(255, 130, 143, 188) # chevrons (slate)

  $cp = New-Object System.Drawing.Pen($chev, [single](15*$k))
  $cp.StartCap = 'Round'; $cp.EndCap = 'Round'; $cp.LineJoin = 'Round'
  # chevron <  y  chevron >  (polilineas)
  $left  = @( (New-Object System.Drawing.PointF(([single](104*$k)),([single](82*$k)))),
              (New-Object System.Drawing.PointF(([single](62*$k)), ([single](128*$k)))),
              (New-Object System.Drawing.PointF(([single](104*$k)),([single](174*$k)))) )
  $right = @( (New-Object System.Drawing.PointF(([single](152*$k)),([single](82*$k)))),
              (New-Object System.Drawing.PointF(([single](194*$k)),([single](128*$k)))),
              (New-Object System.Drawing.PointF(([single](152*$k)),([single](174*$k)))) )
  $g.DrawLines($cp, $left)
  $g.DrawLines($cp, $right)

  # slash /  (acento, con glow sutil)
  $glow = New-Object System.Drawing.Pen([System.Drawing.Color]::FromArgb(70, 122, 162, 247), [single](24*$k))
  $glow.StartCap = 'Round'; $glow.EndCap = 'Round'
  $g.DrawLine($glow, [single](150*$k), [single](70*$k), [single](106*$k), [single](186*$k))
  $ap = New-Object System.Drawing.Pen($accent, [single](15*$k))
  $ap.StartCap = 'Round'; $ap.EndCap = 'Round'
  $g.DrawLine($ap, [single](150*$k), [single](70*$k), [single](106*$k), [single](186*$k))

  $g.Dispose()
  $ms = New-Object System.IO.MemoryStream
  $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  return ,$ms.ToArray()   # coma: preserva el Byte[] (sin ella PS lo desenrolla a Object[])
}

$sizes = 16, 24, 32, 48, 64, 128, 256
$frames = @()
foreach ($s in $sizes) { $frames += , (New-Frame $s) }

$out = New-Object System.IO.MemoryStream
$bw = New-Object System.IO.BinaryWriter($out)
$bw.Write([UInt16]0); $bw.Write([UInt16]1); $bw.Write([UInt16]$sizes.Count)
$offset = 6 + 16 * $sizes.Count
for ($i = 0; $i -lt $sizes.Count; $i++) {
  $s = $sizes[$i]; $f = $frames[$i]
  if ($s -ge 256) { $dim = 0 } else { $dim = $s }
  $bw.Write([byte]$dim); $bw.Write([byte]$dim)
  $bw.Write([byte]0); $bw.Write([byte]0)
  $bw.Write([UInt16]1); $bw.Write([UInt16]32)
  $bw.Write([UInt32]$f.Length); $bw.Write([UInt32]$offset)
  $offset += $f.Length
}
foreach ($f in $frames) { $bw.Write($f) }
$bw.Flush()
[System.IO.File]::WriteAllBytes((Join-Path $PSScriptRoot 'cipher.ico'), $out.ToArray())
$bw.Dispose()
Write-Host ("cipher.ico generado ({0:N0} bytes, {1} tamanos)" -f (Get-Item cipher.ico).Length, $sizes.Count)

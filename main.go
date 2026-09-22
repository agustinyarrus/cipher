package main

// Cipher — visor de código ultraminimalista, dark, frameless. Hermano de Folio / Lumen / Lux.
//
// Un solo .exe: embebe la UI (carpeta ui/) y la sirve por un server HTTP local a una ventana
// WebView2 SIN marco del sistema. La barra de titulo y los botones min/max/cerrar los dibuja la
// pagina; aca exponemos el puente JS -> Win32 (mover, redimensionar, botones, pantalla completa),
// resaltamos el codigo con chroma (250+ lenguajes, ver render.go) y, para binarios legibles como
// .class, lo decompilamos antes (ver decompile.go). Con recarga en vivo: si el archivo cambia en
// disco, la vista se actualiza.
//
// Frameless (igual que Lumen / el host de IA History Reader): subclasamos el WndProc y devolvemos
// 0 en WM_NCCALCSIZE para que el area cliente ocupe toda la ventana; drag/resize via
// WM_NCLBUTTONDOWN (mantiene Aero Snap).

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var debugLog = os.Getenv("CIPHER_DEBUG") != ""
var startTime = time.Now()

func dlog(args ...any) {
	if debugLog {
		ms := fmt.Sprintf("[cipher +%dms]", time.Since(startTime).Milliseconds())
		fmt.Fprintln(os.Stderr, append([]any{ms}, args...)...)
	}
}

//go:embed ui
var uiFS embed.FS

const (
	wmNCCALCSIZE      = 0x0083
	wmNCLBUTTONDOWN   = 0x00A1
	wmCLOSE           = 0x0010
	wmERASEBKGND      = 0x0014
	whCBT             = 5
	hcbtCREATEWND     = 3
	smCXSCREEN        = 0
	smCYSCREEN        = 1
	smXVIRTUALSCREEN  = 76
	smYVIRTUALSCREEN  = 77
	smCXVIRTUALSCREEN = 78
	smCYVIRTUALSCREEN = 79
	smCMONITORS       = 80
	spiGETWORKAREA    = 0x0030
	htCAPTION         = 2
	htLEFT            = 10
	htRIGHT           = 11
	htTOP             = 12
	htTOPLEFT         = 13
	htTOPRIGHT        = 14
	htBOTTOM          = 15
	htBOTTOMLEFT      = 16
	htBOTTOMRIGHT     = 17
	swHIDE            = 0
	swSHOW            = 5
	swMINIMIZE        = 6
	swMAXIMIZE        = 3
	swRESTORE         = 9
	swSHOWMAXIMIZED   = 3
	swSHOWMINIMIZED   = 2
	smCXFRAME         = 32
	smCYFRAME         = 33
	smCXPADDEDBORDER  = 92
	swpFRAMECHANGED   = 0x0020
	swpNOMOVE         = 0x0002
	swpNOSIZE         = 0x0001
	swpNOZORDER       = 0x0004
	swpSHOWWINDOW     = 0x0040

	hwndTop      = 0
	hwndTopmost  = ^uintptr(0)     // (HWND)-1
	hwndNoTopmst = ^uintptr(0) - 1 // (HWND)-2
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	pSetWindowSubclass        = comctl32.NewProc("SetWindowSubclass")
	pDefSubclassProc          = comctl32.NewProc("DefSubclassProc")
	pSetWindowPos             = user32.NewProc("SetWindowPos")
	pShowWindow               = user32.NewProc("ShowWindow")
	pSendMessageW             = user32.NewProc("SendMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pReleaseCapture           = user32.NewProc("ReleaseCapture")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	pGetWindowPlacement       = user32.NewProc("GetWindowPlacement")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pGetClientRect            = user32.NewProc("GetClientRect")
	pFillRect                 = user32.NewProc("FillRect")
	pCreateSolidBrush         = gdi32.NewProc("CreateSolidBrush")
	pSelectObject             = gdi32.NewProc("SelectObject")
	pDeleteObject             = gdi32.NewProc("DeleteObject")
	pCreatePen                = gdi32.NewProc("CreatePen")
	pPolyline                 = gdi32.NewProc("Polyline")
	pInvalidateRect           = user32.NewProc("InvalidateRect")
	pUpdateWindow             = user32.NewProc("UpdateWindow")
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pSetWindowsHookExW        = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx           = user32.NewProc("CallNextHookEx")
	pGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
	pAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
	pDwmSetWindowAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")

	// bus SSE único hacia la página: "open\t<ruta>" (handoff del daemon) y "change\t<ruta>" (el
	// archivo cambió en disco). UN SOLO EventSource del lado del navegador: Chromium limita a 6 las
	// conexiones HTTP/1.1 por host, y con un SSE por pestaña la 7ª request quedaba encolada PARA
	// SIEMPRE (pasó: ráfaga de handoffs → 5 pestañas + bus = 6 conexiones vivas → el /render
	// siguiente jamás salía y las aperturas restantes morían en silencio). Multiplexar lo cura de
	// raíz y además es más liviano. Más robusto que un Eval directo cuando la ventana venía oculta
	// (daemon caliente): la conexión SSE sobrevive y reconecta.
	busSubsMu   sync.Mutex
	busSubs     = map[chan string]struct{}{}
	pendingOpen string // última ruta pedida; se reenvía a cada cliente que (re)conecta al bus

	// lista de vigilancia declarativa: la página la repone entera en cada cambio de pestañas
	// (/api/watch) y UN goroutine la pollea; cambió -> "change" por el bus; desapareció -> "gone".
	watchMu  sync.Mutex
	watchSet = map[string]fileStamp{}

	darkBrush  uintptr
	subclassCB uintptr // callback de subclassProc; lo instala el CBT hook al crearse la ventana
	uiScale    = 1.0   // factor DPI, para el splash nativo
	splashDone bool    // una vez listo el contenido, el host deja de dibujar la marca
	fullscreen bool
	savedPlc   windowPlacement
	savedOK    bool

	// spawnX/spawnY: posición final (px físicos) con la que NACE la ventana. La setea main() antes
	// de crearla y la aplica cbtProc sobre el CREATESTRUCT, para que el primer pixel ya esté en el
	// lugar definitivo (guardado o centrado) y showWin no tenga que moverla -> sin "salto".
	spawnX, spawnY int32
	spawnPosSet    bool

	offscreenSpawn = os.Getenv("CIPHER_OFFSCREEN") != ""
)

type rect struct{ left, top, right, bottom int32 }
type point struct{ x, y int32 }
type nccalcsizeParams struct {
	rgrc  [3]rect
	lppos uintptr
}
type windowPlacement struct {
	length           uint32
	flags            uint32
	showCmd          uint32
	ptMinPosition    point
	ptMaxPosition    point
	rcNormalPosition rect
}

func sysMetric(i int) int32 {
	r, _, _ := pGetSystemMetrics.Call(uintptr(i))
	return int32(r)
}

// workArea devuelve el área de trabajo del monitor primario en px físicos (bajo Per-Monitor-DPI-v2),
// excluyendo la barra de tareas. Fallback a la pantalla completa si SPI_GETWORKAREA no responde.
func workArea() rect {
	var rc rect
	r, _, _ := pSystemParametersInfoW.Call(uintptr(spiGETWORKAREA), 0, uintptr(unsafe.Pointer(&rc)), 0)
	if r == 0 || rc.right-rc.left <= 0 || rc.bottom-rc.top <= 0 {
		return rect{0, 0, sysMetric(smCXSCREEN), sysMetric(smCYSCREEN)}
	}
	return rc
}

// clampWinSize recorta el tamaño (px físicos) para que nunca supere el área de trabajo del monitor
// primario. Sin esto, una geometría guardada mayor que la pantalla (snap, arrastre del borde más
// allá del monitor, o haber cerrado maximizada) reabría la ventana con los bordes de redimensión
// fuera de vista -> imposible de agarrar para reajustarla.
func clampWinSize(w, h uint) (uint, uint) {
	wa := workArea()
	if mw := uint(wa.right - wa.left); mw > 0 && w > mw {
		w = mw
	}
	if mh := uint(wa.bottom - wa.top); mh > 0 && h > mh {
		h = mh
	}
	return w, h
}

type createstructW struct {
	lpCreateParams uintptr
	hInstance      uintptr
	hMenu          uintptr
	hwndParent     uintptr
	cy, cx, y, x   int32
	style          int32
	lpszName       uintptr
	lpszClass      uintptr
	dwExStyle      uint32
}
type cbtCreatewnd struct {
	lpcs            uintptr
	hwndInsertAfter uintptr
}

func u16ptrToString(p uintptr) string {
	if p == 0 {
		return ""
	}
	buf := make([]uint16, 0, 24)
	for i := uintptr(0); ; i += 2 {
		c := *(*uint16)(unsafe.Pointer(p + i))
		if c == 0 {
			break
		}
		buf = append(buf, c)
	}
	return windows.UTF16ToString(buf)
}

// cbtProc engancha el nacimiento de NUESTRA ventana (CBT hook, corre dentro de CreateWindowEx, antes
// del ShowWindow que hace go-webview2): la subclasa al instante para que nazca frameless + oscura, y
// le fija en el CREATESTRUCT la posición final (guardada/centrada). go-webview2 sólo expone Center
// (no X/Y) y muestra la ventana enseguida, así que sin esto nacería en CW_USEDEFAULT y recién showWin
// la movería al lugar guardado: se vería "saltar". (offscreen = -32000 queda como escape de debug.)
func cbtProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) == hcbtCREATEWND && lParam != 0 {
		cbt := (*cbtCreatewnd)(unsafe.Pointer(lParam))
		if cbt.lpcs != 0 {
			cs := (*createstructW)(unsafe.Pointer(cbt.lpcs))
			if cs.hwndParent == 0 && cs.lpszClass > 0xFFFF && u16ptrToString(cs.lpszClass) == "webview" {
				// Subclasar AL INSTANTE de crearse: así la ventana nace frameless + oscura (con la
				// marca) desde el primer pixel, sin el flash de barra de título nativa + fondo claro
				// mientras WebView2 hace su cold-start (wParam = hwnd de la ventana naciendo).
				if subclassCB != 0 {
					pSetWindowSubclass.Call(wParam, subclassCB, 1, 0)
				}
				// Nacer YA en la posición final (guardada o centrada). Sin esto, go-webview2 crea la
				// ventana en CW_USEDEFAULT (cascada arriba-izquierda) y recién showWin la mueve al lugar
				// guardado: se la ve "saltar". offscreen queda como escape de depuración.
				if offscreenSpawn {
					cs.x, cs.y = -32000, -32000
				} else if spawnPosSet {
					cs.x, cs.y = spawnX, spawnY
				}
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

// Declarar Per-Monitor-V2 ANTES de crear ventanas; si no, WebView2 se renderiza a 96 DPI y
// Windows lo estira -> borroso.
func setDpiAware() {
	if p := user32.NewProc("SetProcessDpiAwarenessContext"); p.Find() == nil {
		if r, _, _ := p.Call(^uintptr(0) - 3); r != 0 { // PER_MONITOR_AWARE_V2 (-4)
			return
		}
	}
	if p := shcore.NewProc("SetProcessDpiAwareness"); p.Find() == nil {
		if r, _, _ := p.Call(2); r == 0 {
			return
		}
	}
	user32.NewProc("SetProcessDPIAware").Call()
}

func getDpiForSystem() int {
	if p := user32.NewProc("GetDpiForSystem"); p.Find() == nil {
		if r, _, _ := p.Call(); r != 0 {
			return int(r)
		}
	}
	return 96
}

// roundCorners pide a DWM esquinas redondeadas estilo Win11 (sólo build 22000+). Funciona aunque la
// ventana sea frameless (es un atributo de composición, ajeno al cálculo de área cliente) y Windows
// las cuadra solo al maximizar/snapear. En Win10 el atributo no existe -> devuelve error y se ignora.
func roundCorners(hwnd uintptr) {
	const dwmwaWindowCornerPreference = 33 // DWMWA_WINDOW_CORNER_PREFERENCE
	const dwmwcpRound = 2                  // DWMWCP_ROUND (redondeo estándar; 3 = ROUNDSMALL)
	pref := int32(dwmwcpRound)
	r, _, _ := pDwmSetWindowAttribute.Call(hwnd, dwmwaWindowCornerPreference,
		uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
	dlog("roundCorners hr=", int32(r))
}

// setDarkFrame oscurece el MARCO que DWM dibuja alrededor de la ventana en Win11. Sin esto, el borde
// (y la línea fina del contorno redondeado) sigue el tema del SISTEMA y sale claro/blancuzco sobre
// la app oscura. (1) modo oscuro inmersivo -> el frame se renderiza dark; (2) color de borde explícito
// (#262B36, COLORREF 0x00BBGGRR) -> borde dark sutil y determinista sin importar el tema del sistema.
func setDarkFrame(hwnd uintptr) {
	const (
		dwmwaUseImmersiveDarkMode = 20 // Win11/Win10 2004+ (en builds viejos era 19; devuelve error e ignora)
		dwmwaBorderColor          = 34 // DWMWA_BORDER_COLOR (build 22000+)
	)
	on := int32(1)
	pDwmSetWindowAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on))
	border := uint32(0x00000000) // negro (COLORREF 0x00BBGGRR)
	r, _, _ := pDwmSetWindowAttribute.Call(hwnd, dwmwaBorderColor, uintptr(unsafe.Pointer(&border)), unsafe.Sizeof(border))
	dlog("setDarkFrame borderHr=", int32(r))
}

func isMaximized(hwnd uintptr) bool {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	return wp.showCmd == swSHOWMAXIMIZED
}

func isMinimized(hwnd uintptr) bool {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	return wp.showCmd == swSHOWMINIMIZED
}

// captureGeom guarda la geometría de la ventana en PÍXELES FÍSICOS (GetWindowRect, que bajo
// Per-Monitor-DPI-v2 devuelve device px) para que el round-trip con SetWindowPos (también device
// px) sea exacto en DPI altos. GetWindowPlacement, en cambio, devuelve unidades lógicas y descuadra
// la posición. El tamaño se recrea como W*scale en WindowOptions (go-webview2 lo divide por scale).
func captureGeom(hwnd uintptr) winGeom {
	max := isMaximized(hwnd)
	var rc rect
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	return winGeom{X: rc.left, Y: rc.top, W: rc.right - rc.left, H: rc.bottom - rc.top, Max: max}
}

// targetWindowPos decide dónde va la ventana de tamaño ww*hh (px físicos): la posición guardada del
// último cierre, o centrada en el monitor primario si no hay. La usan POR IGUAL el nacimiento de la
// ventana (cbtProc) y el reveal (showWin), así nunca difieren y no hay salto. El clamp se hace contra
// el escritorio VIRTUAL (no sólo el primario) para respetar posiciones en un monitor secundario
// (vx/vy pueden ser negativos) sin dejar la ventana fuera de la vista.
func targetWindowPos(ww, hh int32) (x, y int32, maximized bool) {
	if g := gCfg.Window; g != nil && g.W > 200 && g.H > 150 {
		x, y, maximized = g.X, g.Y, g.Max
	} else {
		sw, sh := sysMetric(smCXSCREEN), sysMetric(smCYSCREEN)
		x, y = (sw-ww)/2, (sh-hh)/2
	}
	// Límites de clamp: con UN monitor usamos su área de trabajo (deja la ventana entera SOBRE la
	// barra de tareas, con TODOS los bordes de redimensión alcanzables). Con varios usamos el
	// escritorio virtual completo, para no impedir colocarla en un monitor secundario (x/y < 0 ok).
	var bx, by, bw, bh int32
	if sysMetric(smCMONITORS) <= 1 {
		wa := workArea()
		bx, by, bw, bh = wa.left, wa.top, wa.right-wa.left, wa.bottom-wa.top
	} else {
		bx, by = sysMetric(smXVIRTUALSCREEN), sysMetric(smYVIRTUALSCREEN)
		bw, bh = sysMetric(smCXVIRTUALSCREEN), sysMetric(smCYVIRTUALSCREEN)
		if bw <= 0 || bh <= 0 { // fallback si el escritorio virtual no responde
			bx, by, bw, bh = 0, 0, sysMetric(smCXSCREEN), sysMetric(smCYSCREEN)
		}
	}
	// Primero que los bordes derecho/inferior no se salgan; luego que la esquina sup-izq no quede
	// fuera (tiene prioridad). Con el tamaño ya acotado (clampWinSize), la ventana entra entera.
	if x+ww > bx+bw {
		x = bx + bw - ww
	}
	if y+hh > by+bh {
		y = by + bh - hh
	}
	if x < bx {
		x = bx
	}
	if y < by {
		y = by
	}
	return
}

// bringToFront restaura (si está minimizada) y trae la ventana al frente de forma fiable.
// Corre SIEMPRE en el worker (ver frontReq), nunca en el hilo de UI: SetWindowPos(TOPMOST) /
// SetForegroundWindow pueden negociar sincrónicamente con ventanas de otros procesos (incluida la
// hija de WebView2); si eso se demora y el hilo que espera es el DUEÑO de la ventana (que debería
// estar bombeando mensajes), la app entera queda congelada. Desde una goroutine aparte, lo peor
// que puede pasar es que el raise tarde.
func bringToFront(hwnd uintptr) {
	if isMinimized(hwnd) {
		pShowWindow.Call(hwnd, swRESTORE)
	} else {
		pShowWindow.Call(hwnd, swSHOW)
	}
	dlog("btf: shown")
	pSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, uintptr(swpNOMOVE|swpNOSIZE|swpSHOWWINDOW))
	dlog("btf: topmost")
	pSetWindowPos.Call(hwnd, hwndNoTopmst, 0, 0, 0, 0, uintptr(swpNOMOVE|swpNOSIZE))
	dlog("btf: notopmost")
	pSetForegroundWindow.Call(hwnd)
	dlog("btf: done")
}

// frontReq alimenta al worker de "traer al frente": un canal de capacidad 1 coalesce ráfagas
// (diez handoffs seguidos = un raise, quizá dos) y el worker corre en su goroutine para que un
// SetWindowPos lento/trabado jamás congele el hilo de UI ni pierda aperturas.
var frontReq = make(chan struct{}, 1)

func requestFront() {
	select {
	case frontReq <- struct{}{}:
	default: // ya hay un raise pendiente: alcanza
	}
}

// ---- instancia única (daemon caliente) ---------------------------------
// El primer cipher.exe queda corriendo; cada invocación siguiente le manda la ruta por HTTP y
// sale al instante (sin pagar el cold-start de WebView2). El lock guarda "puerto\npid".

func lockPath() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "Cipher", "instance.lock")
}

func writeLock(port string) {
	p := lockPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(port+"\n"+strconv.Itoa(os.Getpid())), 0o644)
}

func removeLock() { os.Remove(lockPath()) }

func readLock() (port string, pid int, ok bool) {
	b, err := os.ReadFile(lockPath())
	if err != nil {
		return "", 0, false
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)
	if len(parts) < 2 || parts[0] == "" {
		return "", 0, false
	}
	pid, _ = strconv.Atoi(parts[1])
	return parts[0], pid, true
}

// tryHandoff devuelve true si había una instancia viva que aceptó mostrar el documento.
// hlSpec viaja junto con la ruta para que el daemon marque las mismas líneas (--hl).
func tryHandoff(path, hlSpec string) bool {
	port, pid, ok := readLock()
	if !ok {
		return false
	}
	if pid > 0 {
		pAllowSetForegroundWindow.Call(uintptr(pid))
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/show?path=" + url.QueryEscape(path) +
		"&hl=" + url.QueryEscape(hlSpec))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ---- líneas marcadas (--hl) --------------------------------------------
// Spec por archivo: "12-15,40,60-72". La guarda el server (CLI inicial o /api/show del handoff) y
// /render la devuelve parseada; abrir el mismo archivo SIN --hl la limpia (spec vacía = sin marcas).

var (
	hlMu    sync.Mutex
	hlSpecs = map[string]string{}
)

func setHL(path, spec string) {
	hlMu.Lock()
	defer hlMu.Unlock()
	if strings.TrimSpace(spec) == "" {
		delete(hlSpecs, path)
		return
	}
	hlSpecs[path] = spec
}

func getHL(path string) string {
	hlMu.Lock()
	defer hlMu.Unlock()
	return hlSpecs[path]
}

// parseHLSpec convierte "12-15,+40,-7-9" en tripletas [ini,fin,tipo] 1-based, ordenadas y con
// solapados del MISMO tipo fusionados. Tipo por prefijo del item: sin prefijo = 0 neutral crema
// (modificado), '+' = 1 verde (agregado), '-' = 2 rojo (borrado) — semántica de diff. Entradas
// inválidas se ignoran en silencio (la spec viene de línea de comandos).
func parseHLSpec(spec string) [][3]int {
	out := [][3]int{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kind := 0
		switch part[0] {
		case '+':
			kind, part = 1, strings.TrimSpace(part[1:])
		case '-':
			kind, part = 2, strings.TrimSpace(part[1:])
		}
		if part == "" {
			continue
		}
		var a, b int
		var errA, errB error
		if i := strings.IndexByte(part, '-'); i > 0 {
			a, errA = strconv.Atoi(strings.TrimSpace(part[:i]))
			b, errB = strconv.Atoi(strings.TrimSpace(part[i+1:]))
		} else {
			a, errA = strconv.Atoi(part)
			b = a
		}
		if errA != nil || errB != nil { // "5-x": antes el fin invalido valia 0 y quedaba el rango 0-5
			continue
		}
		if a < 1 && b >= 1 {
			a = 1
		}
		if a < 1 {
			continue
		}
		if b < a {
			a, b = b, a
		}
		out = append(out, [3]int{a, b, kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	merged := out[:0]
	for _, r := range out {
		if n := len(merged); n > 0 && r[2] == merged[n-1][2] && r[0] <= merged[n-1][1]+1 {
			if r[1] > merged[n-1][1] {
				merged[n-1][1] = r[1]
			}
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// drawSplashMark dibuja el glifo de Cipher (</>) con GDI, centrado, sobre el HDC dado.
// Se usa durante el cold-start de WebView2: la ventana host esta visible y todavia NO la tapa el
// child del navegador, asi que en vez de un recuadro vacio se ve la marca al instante. Empalma con
// el splash web (misma marca) cuando la pagina pinta.
func drawSplashMark(hdc uintptr, rc rect) {
	const psSolid = 0
	cw, ch := rc.right-rc.left, rc.bottom-rc.top
	if cw < 60 || ch < 60 {
		return
	}
	s := (80.0 * uiScale) / 52.0 // alto del glifo </> en su espacio 0..100 (~22..78)
	tb := 38.0 * uiScale         // alto de la barra de titulo, para alinear con el splash web
	ox := float64(rc.left+cw/2) - 52*s
	oy := float64(rc.top) + (float64(ch)+tb)/2 - 50*s
	P := func(gx, gy float64) point { return point{int32(ox + gx*s), int32(oy + gy*s)} }

	w := int32(3.4 * uiScale)
	if w < 2 {
		w = 2
	}
	faint, _, _ := pCreatePen.Call(psSolid, uintptr(w), 0x004D4D4D)  // #4D4D4D
	accent, _, _ := pCreatePen.Call(psSolid, uintptr(w), 0x00FFFFFF) // #FFFFFF
	defer pDeleteObject.Call(faint)
	defer pDeleteObject.Call(accent)
	poly := func(pen uintptr, pts []point) {
		old, _, _ := pSelectObject.Call(hdc, pen)
		pPolyline.Call(hdc, uintptr(unsafe.Pointer(&pts[0])), uintptr(len(pts)))
		pSelectObject.Call(hdc, old)
	}
	poly(faint, []point{P(38, 28), P(20, 50), P(38, 72)}) // chevron <
	poly(faint, []point{P(66, 28), P(84, 50), P(66, 72)}) // chevron >
	poly(accent, []point{P(60, 22), P(44, 78)})           // slash /
}

func subclassProc(hwnd, msg, wParam, lParam, uID, dwRef uintptr) uintptr {
	if msg == wmNCCALCSIZE && wParam != 0 {
		if !fullscreen && isMaximized(hwnd) {
			p := (*nccalcsizeParams)(unsafe.Pointer(lParam))
			cx := sysMetric(smCXFRAME) + sysMetric(smCXPADDEDBORDER)
			cy := sysMetric(smCYFRAME) + sysMetric(smCXPADDEDBORDER)
			p.rgrc[0].left += cx
			p.rgrc[0].top += cy
			p.rgrc[0].right -= cx
			p.rgrc[0].bottom -= cy
		}
		return 0
	}
	if msg == wmERASEBKGND && darkBrush != 0 {
		var rc rect
		pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		pFillRect.Call(wParam, uintptr(unsafe.Pointer(&rc)), darkBrush)
		if !splashDone { // mientras WebView2 arranca, mostramos la marca en vez de un recuadro vacio
			drawSplashMark(wParam, rc)
		}
		return 1
	}
	if msg == wmCLOSE {
		saveWindowGeom(captureGeom(hwnd)) // recordar tamaño/posición para el próximo arranque
	}
	r, _, _ := pDefSubclassProc.Call(hwnd, msg, wParam, lParam)
	return r
}

func htCode(dir string) uintptr {
	switch dir {
	case "l":
		return htLEFT
	case "r":
		return htRIGHT
	case "t":
		return htTOP
	case "b":
		return htBOTTOM
	case "tl":
		return htTOPLEFT
	case "tr":
		return htTOPRIGHT
	case "bl":
		return htBOTTOMLEFT
	case "br":
		return htBOTTOMRIGHT
	}
	return htCAPTION
}

func enterFullscreen(hwnd uintptr) {
	if fullscreen {
		return
	}
	savedPlc.length = uint32(unsafe.Sizeof(savedPlc))
	pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&savedPlc)))
	savedOK = true
	fullscreen = true
	rc := monitorRect(hwnd)
	pSetWindowPos.Call(hwnd, hwndTopmost,
		uintptr(rc.left), uintptr(rc.top), uintptr(rc.right-rc.left), uintptr(rc.bottom-rc.top),
		uintptr(swpFRAMECHANGED|swpSHOWWINDOW))
}

func exitFullscreen(hwnd uintptr) {
	if !fullscreen {
		return
	}
	fullscreen = false
	if savedOK {
		pSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&savedPlc)))
	}
	pSetWindowPos.Call(hwnd, hwndNoTopmst, 0, 0, 0, 0,
		uintptr(swpNOMOVE|swpNOSIZE|swpFRAMECHANGED))
}

func main() {
	runtime.LockOSThread()
	setDpiAware()
	scale := float64(getDpiForSystem()) / 96.0
	uiScale = scale
	loadConfig()

	// modo headless para depurar el render (cipher-debug.exe --dump <archivo>): imprime metadatos
	// + el principio del HTML y sale. Sólo útil en el build con consola.
	if len(os.Args) > 2 && os.Args[1] == "--dump" {
		dumpRender(os.Args[2])
		return
	}
	// modo --exts [archivo]: vuelca todas las extensiones que chroma reconoce (";"-separadas) para que
	// install.ps1 registre Cipher en "Abrir con" de cada una. Con archivo escribe ahí (sirve aun en el
	// build GUI sin consola); sin archivo, a stdout (build con consola).
	if len(os.Args) > 1 && os.Args[1] == "--exts" {
		dumpExts(os.Args)
		return
	}

	// argumentos: [archivo...] [--hl RANGOS]. Varios archivos = una pestaña cada uno. --hl marca
	// líneas del PRIMER archivo (p.ej. "12-15,+40,-60-72"): zonas resaltadas + salto a la primera,
	// para señalar dónde se modificó (diffs, reviews). Prefijo por item: sin prefijo = crema
	// (modificado), '+' = verde (agregado), '-' = rojo (borrado).
	var initialPaths []string
	initialHL := ""
	cliArgs := os.Args[1:]
	for i := 0; i < len(cliArgs); i++ {
		a := cliArgs[i]
		switch {
		case a == "--hl" && i+1 < len(cliArgs):
			i++
			initialHL = cliArgs[i]
		case strings.HasPrefix(a, "--hl="):
			initialHL = strings.TrimPrefix(a, "--hl=")
		case strings.TrimSpace(a) != "" && !strings.HasPrefix(a, "--"):
			if abs, err := filepath.Abs(a); err == nil {
				initialPaths = append(initialPaths, abs)
			}
		}
	}
	if len(initialPaths) > 0 {
		setHL(initialPaths[0], initialHL)
	}

	if os.Getenv("CIPHER_NEW") == "" {
		if len(initialPaths) == 0 {
			if tryHandoff("", "") {
				dlog("handoff a instancia existente; saliendo")
				return
			}
		} else if tryHandoff(initialPaths[0], initialHL) {
			// el daemon vivo aceptó el primero: mandarle el resto (una pestaña por archivo)
			for _, p := range initialPaths[1:] {
				tryHandoff(p, "")
			}
			dlog("handoff a instancia existente; saliendo")
			return
		}
	}

	addr := startServer(initialPaths)
	pageURL := "http://" + addr + "/"
	dlog("server addr", addr, "initial", initialPaths)
	if _, portStr, err := net.SplitHostPort(addr); err == nil {
		writeLock(portStr)
		defer removeLock()
	}

	// dark + callback listos ANTES del hook: el CBT hook subclasa la ventana al nacer, para que sea
	// frameless + oscura (con la marca) desde el primer pixel, sin flash de barra nativa / fondo claro.
	darkBrush, _, _ = pCreateSolidBrush.Call(0x00000000) // COLORREF de #000000 (fondo Onyx)
	subclassCB = windows.NewCallback(subclassProc)

	tid, _, _ := pGetCurrentThreadId.Call()
	cbtHook, _, _ := pSetWindowsHookExW.Call(uintptr(whCBT), windows.NewCallback(cbtProc), 0, tid)

	dataPath := ""
	if d, err := os.UserCacheDir(); err == nil {
		dataPath = filepath.Join(d, "Cipher", "WebView2")
	}
	// Flags de Chromium: que NO frene el render con la ventana oculta/ocluida (clave para que la
	// página pinte mientras la mantenemos invisible hasta estar lista). Se anexan aunque el entorno
	// ya traiga args (p.ej. --remote-debugging-port en verificación).
	renderFlags := "--no-first-run --disable-background-networking --disable-component-update " +
		"--disable-backgrounding-occluded-windows --disable-renderer-backgrounding"
	if extra := os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"); extra == "" {
		os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", renderFlags)
	} else {
		os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", extra+" "+renderFlags)
	}

	// tamaño de ventana: el guardado del último cierre, o el default centrado.
	// WindowOptions.Width/Height son px FÍSICOS (por eso el default es 1180*scale). captureGeom
	// guarda px físicos (GetWindowRect), así que recreamos con g.W/g.H tal cual: round-trip estable.
	winW, winH := uint(1180*scale), uint(840*scale)
	centerWin := true
	if g := gCfg.Window; g != nil && g.W > 200 && g.H > 150 {
		centerWin = false
		if !g.Max {
			winW, winH = uint(g.W), uint(g.H)
		}
		// Si se cerró maximizada queda el tamaño default como "restaurado": el rect guardado es el
		// del estado maximizado (más grande que el área de trabajo) y al des-maximizar la ventana
		// quedaba con los bordes fuera de la pantalla. showWin la vuelve a maximizar al revelarla.
	}
	winW, winH = clampWinSize(winW, winH)
	// Posición definitiva ANTES de crear: cbtProc la clava en el CREATESTRUCT para que la ventana
	// nazca ahí. (Center sigue como fallback por si el hook no llegara a correr.)
	spawnX, spawnY, _ = targetWindowPos(int32(winW), int32(winH))
	spawnPosSet = true

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     debugLog,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "Cipher",
			Width:  winW,
			Height: winH,
			Center: centerWin,
			IconId: 1, // RT_GROUP_ICON embebido por rsrc.syso (cipher.ico)
		},
	})
	if cbtHook != 0 {
		pUnhookWindowsHookEx.Call(cbtHook)
	}
	if w == nil {
		panic("no se pudo crear WebView2")
	}
	defer w.Destroy()

	hwnd := uintptr(w.Window())
	roundCorners(hwnd)          // esquinas redondeadas Win11 (frameless-friendly; no-op en Win10)
	setDarkFrame(hwnd)          // borde/marco DWM en oscuro (si no, sale claro siguiendo el tema del sistema)
	setWebViewDarkBackground(w) // about:blank OSCURO (la ventana ya nació dark+frameless por el hook)
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, uintptr(swpNOMOVE|swpNOSIZE|swpNOZORDER|swpFRAMECHANGED))
	// pintar la marca de Folio YA (el host esta visible y WebView2 todavia no lo tapa)
	pInvalidateRect.Call(hwnd, 0, 1)
	pUpdateWindow.Call(hwnd)

	w.SetSize(int(640*scale), int(480*scale), webview2.HintMin)

	// ---- puentes JS -> ventana ----
	w.Bind("cipherMin", func() {
		w.Dispatch(func() { pShowWindow.Call(hwnd, swMINIMIZE) })
	})
	w.Bind("cipherMaxToggle", func() {
		w.Dispatch(func() {
			if isMaximized(hwnd) {
				pShowWindow.Call(hwnd, swRESTORE)
			} else {
				pShowWindow.Call(hwnd, swMAXIMIZE)
			}
		})
	})
	w.Bind("cipherClose", func() {
		w.Dispatch(func() { pPostMessageW.Call(hwnd, wmCLOSE, 0, 0) })
	})
	w.Bind("cipherDrag", func() {
		w.Dispatch(func() {
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLBUTTONDOWN, htCAPTION, 0)
		})
	})
	w.Bind("cipherResize", func(dir string) {
		w.Dispatch(func() {
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLBUTTONDOWN, htCode(dir), 0)
		})
	})
	w.Bind("cipherFullscreen", func(on bool) {
		w.Dispatch(func() {
			if on {
				enterFullscreen(hwnd)
			} else {
				exitFullscreen(hwnd)
			}
		})
	})
	// Dialogo nativo de apertura (todos los archivos por defecto, multi-seleccion: una pestaña
	// por archivo elegido).
	w.Bind("cipherPick", func() {
		w.Dispatch(func() {
			if ps := pickCode(hwnd); len(ps) > 0 {
				if b, err := json.Marshal(ps); err == nil {
					w.Eval("window.__cipherOpen(" + string(b) + ")")
				}
			}
		})
	})
	// Abrir un enlace externo en el navegador del sistema.
	w.Bind("cipherOpenExternal", func(target string) {
		go shellOpen(target)
	})
	// Abrir un archivo local con la app por defecto del sistema.
	w.Bind("cipherOpenPath", func(p string) {
		go shellOpen(p)
	})

	// mostrar/centrar la ventana recien cuando la pagina aviso que pinto (evita flash en blanco)
	var shownOnce sync.Once
	showWin := func() {
		shownOnce.Do(func() {
			splashDone = true // el contenido ya esta: dejamos de dibujar la marca nativa
			var rc rect
			pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
			ww, hh := rc.right-rc.left, rc.bottom-rc.top
			// MISMA posición con la que nació la ventana (cbtProc usó este mismo cálculo): este
			// SetWindowPos no la mueve, sólo la trae al frente y dispara el nudge del swapchain.
			cx, cy, maximized := targetWindowPos(ww, hh)
			after := uintptr(0)
			flags := uintptr(swpNOSIZE | swpNOZORDER | swpSHOWWINDOW)
			if os.Getenv("CIPHER_TOPMOST") != "" { // solo para captura/verificacion
				after = hwndTopmost
				flags = uintptr(swpNOSIZE | swpSHOWWINDOW)
			}
			pSetWindowPos.Call(hwnd, after, uintptr(cx), uintptr(cy), 0, 0, flags)
			pSetForegroundWindow.Call(hwnd)
			if maximized {
				pShowWindow.Call(hwnd, swMAXIMIZE) // ShowWindow ya dispara WM_SIZE (compone el swapchain)
			} else {
				// Nudge de tamaño: fuerza WM_SIZE -> WebView2 re-presenta su swapchain en la
				// ventana ya visible (sin esto el contenido se renderiza pero no se compone).
				pSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(ww), uintptr(hh+1), uintptr(swpNOMOVE|swpNOZORDER))
				pSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(ww), uintptr(hh), uintptr(swpNOMOVE|swpNOZORDER))
			}
		})
	}
	w.Bind("cipherReady", func() { w.Dispatch(showWin) })
	time.AfterFunc(8*time.Second, func() { w.Dispatch(showWin) })

	rs, wrap := uiPrefs()
	initJS := "window.__CIPHER_HOST__=true;"
	initJS += fmt.Sprintf("window.__CIPHER_RSCALE__=%.3f;window.__CIPHER_WRAP__=%t;", rs, wrap)
	if debugLog {
		initJS += "window.__CIPHER_DEBUG__=true;"
	}
	// worker del raise: bringToFront FUERA del hilo de UI (ver el comentario de bringToFront) y
	// coalescido, para que una ráfaga de handoffs no lo spamee ni pueda deadlockear la ventana.
	go func() {
		for range frontReq {
			bringToFront(hwnd)
			time.Sleep(150 * time.Millisecond)
		}
	}()

	w.Init(initJS)
	dlog("navigating to", pageURL)
	w.Navigate(pageURL)
	dlog("entering run loop")
	w.Run()
}

// ----------------------------------------------------------------------
// Server HTTP local: UI embebida + render del Markdown + assets + recarga en vivo (SSE).
// ----------------------------------------------------------------------

func startServer(initialPaths []string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}

	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// rutas iniciales (argumentos de CLI) que la pagina consulta al cargar: una pestaña por cada una
	mux.HandleFunc("/api/initial", func(wr http.ResponseWriter, r *http.Request) {
		writeJSON(wr, map[string]any{"paths": initialPaths})
	})

	// preferencias persistentes (tamaño de letra, word-wrap). Ver config.go. El tamaño de letra
	// se inyecta además en el initJS para no parpadear al abrir.
	mux.HandleFunc("/api/settings", func(wr http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body struct {
				RScale float64 `json:"rscale"`
				Wrap   bool    `json:"wrap"`
			}
			if json.NewDecoder(r.Body).Decode(&body) == nil {
				setUIPrefs(body.RScale, body.Wrap)
			}
			wr.WriteHeader(http.StatusNoContent)
			return
		}
		rs, wrap := uiPrefs()
		writeJSON(wr, map[string]any{"rscale": rs, "wrap": wrap})
	})

	// instancia única: otra invocación de cipher.exe nos manda acá el documento a mostrar.
	// broadcastOpen y requestFront son seguros desde esta goroutine y desde el arranque mismo:
	// si la página todavía no se suscribió, pendingOpen le guarda la ruta; si el worker del raise
	// todavía no corre, frontReq le deja el pedido hecho. Nada depende del hilo de UI.
	mux.HandleFunc("/api/show", func(wr http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			// spec de líneas marcadas de ESTA invocación (vacía = limpiar las anteriores)
			setHL(p, r.URL.Query().Get("hl"))
			dlog("show", p)
			broadcastOpen(p)
		}
		requestFront()
		wr.WriteHeader(http.StatusOK)
	})

	// render: lee el archivo (o lo decompila) y devuelve HTML resaltado + metadatos.
	mux.HandleFunc("/render", func(wr http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" {
			writeJSON(wr, map[string]any{"ok": false, "error": "sin ruta"})
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			wr.WriteHeader(http.StatusNotFound)
			writeJSON(wr, map[string]any{"ok": false, "error": "no se pudo abrir"})
			return
		}
		name := filepath.Base(p)
		var src []byte
		if decompilerFor(p) == nil { // los decompilables se leen dentro de RenderFile (usa la ruta)
			if src, err = readBounded(p); err != nil {
				wr.WriteHeader(http.StatusInternalServerError)
				writeJSON(wr, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
		res, err := RenderFile(p, name, src, pathKey(p))
		if err != nil {
			wr.WriteHeader(http.StatusInternalServerError)
			writeJSON(wr, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(wr, map[string]any{
			"ok":         true,
			"html":       res.HTML,
			"lang":       res.Lang,
			"lines":      res.Lines,
			"bytes":      res.Bytes,
			"chars":      res.Chars,
			"decompiled": res.Decompiled,
			"tool":       res.Tool,
			"binary":     res.Binary,
			"truncated":  res.Truncated,
			"crlf":       res.CRLF,
			"eol":        res.EOL,
			"encoding":   res.Encoding,
			"chunks":     res.Chunks,
			"hlJob":      res.HLJob,
			"hlFrom":     res.HLFrom,
			"plain":      res.Plain,
			"path":       p,
			"dir":        filepath.Dir(p),
			"name":       name,
			"size":       fi.Size(),
			"mtime":      fi.ModTime().UnixMilli(),
			"hl":         parseHLSpec(getHL(p)), // zonas marcadas (--hl): pares [ini,fin] 1-based
		})
	})

	// render-text: resalta texto crudo (arrastrar-y-soltar, sin ruta en disco).
	mux.HandleFunc("/render-text", func(wr http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			wr.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("name")
		if strings.TrimSpace(name) == "" {
			name = "snippet.txt"
		}
		src, err := io.ReadAll(http.MaxBytesReader(wr, r.Body, 32<<20))
		if err != nil {
			wr.WriteHeader(http.StatusBadRequest)
			writeJSON(wr, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		res, err := RenderText(src, name)
		if err != nil {
			wr.WriteHeader(http.StatusInternalServerError)
			writeJSON(wr, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(wr, map[string]any{
			"ok": true, "html": res.HTML, "lang": res.Lang, "lines": res.Lines,
			"bytes": res.Bytes, "chars": res.Chars, "binary": res.Binary, "truncated": res.Truncated,
			"crlf": res.CRLF, "eol": res.EOL, "encoding": res.Encoding, "chunks": res.Chunks,
			"hlJob": res.HLJob, "hlFrom": res.HLFrom, "plain": res.Plain,
			"path": "", "dir": "", "name": filepath.Base(name),
		})
	})

	// hl: bloques que el resaltado en segundo plano ya termino (ver hljob.go). La pagina los pide
	// al recibir "hl" por el bus y los reemplaza en su lugar; cada bloque se entrega una sola vez.
	mux.HandleFunc("/api/hl", func(wr http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.URL.Query().Get("job"), 10, 64)
		from, err2 := strconv.Atoi(r.URL.Query().Get("from"))
		if err != nil || err2 != nil || from < 0 {
			wr.WriteHeader(http.StatusBadRequest)
			return
		}
		chunks, done, ok := takeChunks(id, from)
		if !ok {
			wr.WriteHeader(http.StatusNotFound) // cancelado o ya entregado entero
			return
		}
		writeJSON(wr, map[string]any{"from": from, "chunks": chunks, "done": done})
	})

	// asset: sirve un archivo local referenciado por el documento (imagenes relativas, etc.).
	mux.HandleFunc("/asset", func(wr http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" {
			wr.WriteHeader(http.StatusForbidden)
			return
		}
		f, err := os.Open(p)
		if err != nil {
			wr.WriteHeader(http.StatusNotFound)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			wr.WriteHeader(http.StatusNotFound)
			return
		}
		wr.Header().Set("Cache-Control", "max-age=3600")
		http.ServeContent(wr, r, filepath.Base(p), st.ModTime(), f)
	})

	// CSS de resaltado de codigo (chroma) generado del estilo Folio.
	mux.HandleFunc("/chroma.css", func(wr http.ResponseWriter, r *http.Request) {
		wr.Header().Set("Content-Type", "text/css; charset=utf-8")
		wr.Header().Set("Cache-Control", "max-age=86400")
		fmt.Fprint(wr, ChromaCSS())
	})

	// vigilancia de archivos: la página declara acá TODAS las rutas abiertas (una por pestaña);
	// el poller único avisa los cambios por el bus. Reponer la lista entera evita contabilidad
	// incremental (y sus fugas): lo que no está, deja de vigilarse.
	mux.HandleFunc("/api/watch", func(wr http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			wr.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Paths []string `json:"paths"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			wr.WriteHeader(http.StatusBadRequest)
			return
		}
		open := make(map[string]bool, len(body.Paths))
		watchMu.Lock()
		next := make(map[string]fileStamp, len(body.Paths))
		for _, p := range body.Paths {
			if p == "" {
				continue
			}
			open[pathKey(p)] = true
			if last, ok := watchSet[p]; ok {
				next[p] = last // ya vigilada: conservar el estado conocido
			} else {
				st, _ := stampOf(p) // nueva: arrancar desde el estado actual (sin falso "change")
				next[p] = st
			}
		}
		watchSet = next
		watchMu.Unlock()
		cancelJobsExcept(open) // una pestaña cerrada no sigue gastando CPU en resaltar
		wr.WriteHeader(http.StatusNoContent)
	})

	// poller único de la lista de vigilancia (recarga en vivo de todas las pestañas)
	go pollWatched()

	// bus: EL único SSE de la página (aperturas del daemon + cambios en disco). Ver el comentario
	// de busSubs por qué multiplexado: el límite de 6 conexiones por host de Chromium es real.
	mux.HandleFunc("/bus", func(wr http.ResponseWriter, r *http.Request) {
		flusher, ok := wr.(http.Flusher)
		if !ok {
			wr.WriteHeader(http.StatusInternalServerError)
			return
		}
		h := wr.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		// buffer holgado: una rafaga de handoffs (muchos archivos a la vez) no debe perder aperturas
		ch := make(chan string, 64)
		busSubsMu.Lock()
		busSubs[ch] = struct{}{}
		pend := pendingOpen
		busSubsMu.Unlock()
		defer func() {
			busSubsMu.Lock()
			delete(busSubs, ch)
			busSubsMu.Unlock()
		}()
		fmt.Fprint(wr, ": ok\n\n")
		if pend != "" { // si reconectó tras ocultarse, recupera la última ruta pedida
			fmt.Fprintf(wr, "data: open\t%s\n\n", pend)
		}
		flusher.Flush()
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-ch:
				fmt.Fprintf(wr, "data: %s\n\n", m)
				flusher.Flush()
			}
		}
	})

	// canal de logs desde la pagina (window.onerror / pasos de arranque)
	mux.HandleFunc("/log", func(wr http.ResponseWriter, r *http.Request) {
		dlog("JS:", r.URL.Query().Get("m"))
		wr.WriteHeader(http.StatusNoContent)
	})

	var handler http.Handler = onlyLocalHost(ln.Addr().String(), mux)
	if debugLog {
		guarded := handler
		handler = http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
			dlog("HTTP", r.Method, r.URL.Path)
			guarded.ServeHTTP(wr, r)
		})
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	return ln.Addr().String()
}

// writeJSON sin escapar HTML: por defecto encoding/json cambia cada < > & por \u003c y
// compañía, y un HTML de resaltado es casi todo etiquetas: la respuesta pesaba más del doble. Acá
// el JSON lo lee fetch().json(), nunca se incrusta en una página: no hay nada que proteger.
func writeJSON(wr http.ResponseWriter, v any) {
	wr.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(wr)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		dlog("writeJSON:", err)
	}
}

// onlyLocalHost deja pasar solo los pedidos dirigidos a NUESTRA direccion. El servidor escucha
// en 127.0.0.1, pero una pagina cualquiera abierta en el navegador podria apuntarle con DNS
// rebinding (un dominio suyo que pasa a resolver a 127.0.0.1) y, como para el navegador seria el
// mismo origen, leer via /render o /asset cualquier archivo del disco. Ese pedido llega con el Host
// del atacante: aca muere. (La ventana de Cipher y el handoff usan 127.0.0.1:puerto.)
func onlyLocalHost(addr string, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(addr)
	allowed := map[string]bool{
		"127.0.0.1:" + port: true,
		"localhost:" + port: true,
	}
	return http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		if !allowed[strings.ToLower(r.Host)] {
			http.Error(wr, "host no permitido", http.StatusForbidden)
			return
		}
		next.ServeHTTP(wr, r)
	})
}

// pathKey: identidad de un archivo en Windows (mayúsculas y separadores no importan).
func pathKey(p string) string { return strings.ToLower(filepath.Clean(p)) }

// readBounded lee a lo sumo maxRenderBytes+1 bytes (más uno: para que RenderFile sepa que sobra
// y avise "recortado"). Antes era os.ReadFile: un .log de 3 GB se subía entero a memoria.
func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxRenderBytes+1))
}

// ---- recarga en vivo -----------------------------------------------------------

const (
	watchInterval = 400 * time.Millisecond // cada cuanto se miran los archivos abiertos
	settleStep    = 60 * time.Millisecond  // paso de la espera a que termine un guardado
	settleRounds  = 8                      // ~0,5 s como mucho
)

// fileStamp identifica una version del archivo en disco (fecha + tamaño; el cero = no existe).
// Mirar solo si la fecha AUMENTO no alcanza: restaurar una copia la hace retroceder, y un
// guardado dentro del mismo tick de reloj puede cambiar solo el tamaño.
type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

func (a fileStamp) same(b fileStamp) bool {
	return a.ok == b.ok && a.size == b.size && a.mod.Equal(b.mod)
}

func stampOf(path string) (fileStamp, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, false
	}
	return fileStamp{fi.ModTime(), fi.Size(), true}, true
}

// settle espera a que el archivo deje de cambiar: los editores que guardan en dos pasos
// (truncar y escribir, o temporal + renombrar) dejan un instante el archivo vacío o a medias, y
// recargar justo ahí pintaba la pestaña en blanco por un parpadeo.
func settle(path string, st fileStamp) fileStamp {
	for i := 0; i < settleRounds; i++ {
		time.Sleep(settleStep)
		cur, _ := stampOf(path)
		if cur.same(st) {
			return cur
		}
		st = cur
	}
	return st
}

// pollWatched mira todos los archivos abiertos en cada tick. Los stat van EN PARALELO y FUERA
// del lock: antes un recurso de red colgado bloqueaba /api/watch y demoraba la recarga de las
// demás pestañas. Cambió -> "change"; desapareció -> "gone" (la pestaña lo muestra).
func pollWatched() {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for range ticker.C {
		watchMu.Lock()
		paths := make([]string, 0, len(watchSet))
		prev := make([]fileStamp, 0, len(watchSet))
		for p, st := range watchSet {
			paths = append(paths, p)
			prev = append(prev, st)
		}
		watchMu.Unlock()

		cur := make([]fileStamp, len(paths))
		var wg sync.WaitGroup
		for i, p := range paths {
			wg.Add(1)
			go func(i int, p string) {
				defer wg.Done()
				st, _ := stampOf(p)
				if st.ok && !st.same(prev[i]) {
					st = settle(p, st)
				}
				cur[i] = st
			}(i, p)
		}
		wg.Wait()

		var msgs []string
		watchMu.Lock()
		for i, p := range paths {
			old, still := watchSet[p]
			if !still || !old.same(prev[i]) || cur[i].same(prev[i]) {
				continue // la pestaña se cerró, o /api/watch la reinició, o no cambió nada
			}
			watchSet[p] = cur[i]
			if cur[i].ok {
				msgs = append(msgs, "change\t"+p)
			} else {
				msgs = append(msgs, "gone\t"+p)
			}
		}
		watchMu.Unlock()
		for _, m := range msgs {
			broadcastBus(m)
		}
	}
}

// broadcastBus empuja un mensaje ("open\truta" / "change\truta") a todos los suscriptos al bus.
func broadcastBus(msg string) {
	busSubsMu.Lock()
	defer busSubsMu.Unlock()
	for ch := range busSubs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// broadcastOpen pide a la página abrir una ruta (handoff del daemon) y la recuerda para el replay
// de reconexión del bus.
func broadcastOpen(path string) {
	busSubsMu.Lock()
	pendingOpen = path
	busSubsMu.Unlock()
	broadcastBus("open\t" + path)
}

// dumpRender (modo --dump): imprime metadatos + el principio del HTML resaltado de un archivo, para
// depurar el render/decompilado desde la consola sin levantar la ventana.
func dumpRender(path string) {
	abs, _ := filepath.Abs(path)
	name := filepath.Base(abs)
	var src []byte
	if decompilerFor(abs) == nil {
		src, _ = os.ReadFile(abs)
	}
	res, err := RenderFile(abs, name, src, "")
	if err != nil {
		fmt.Println("ERR", err)
		return
	}
	fmt.Printf("lang=%q lines=%d bytes=%d decompiled=%v tool=%q binary=%v truncated=%v crlf=%v enc=%q\n",
		res.Lang, res.Lines, res.Bytes, res.Decompiled, res.Tool, res.Binary, res.Truncated,
		res.CRLF, res.Encoding)
	h := res.HTML
	if len(h) > 3000 {
		h = h[:3000]
	}
	fmt.Println("----- HTML (primeros 3000) -----")
	fmt.Println(h)
}

// dumpExts (modo --exts [archivo]): imprime/escribe las extensiones que chroma reconoce, separadas
// por ";". install.ps1 lo usa para registrar Cipher en "Abrir con" de todos esos tipos.
func dumpExts(args []string) {
	exts := AllExtensions()
	out := strings.Join(exts, ";")
	if len(args) > 2 && strings.TrimSpace(args[2]) != "" {
		os.WriteFile(args[2], []byte(out), 0o644)
		return
	}
	fmt.Println(out)
	fmt.Fprintf(os.Stderr, "(%d extensiones)\n", len(exts))
}

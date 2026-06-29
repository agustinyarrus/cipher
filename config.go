package main

// config.go — preferencias persistentes en %AppData%\Cipher\config.json.
//
// Server-side y no localStorage: el server escucha en un puerto EFÍMERO distinto cada arranque
// (127.0.0.1:0) y localStorage está particionado por origen (incluye el puerto), así que cada
// apertura sería un origen nuevo y se perdería todo. Guardando acá sobrevive a los reinicios:
//   - rscale : tamaño de letra del código (Ctrl +/-)
//   - wrap   : ajuste de línea (word-wrap) on/off
//   - window : geometría de la ventana (se guarda al cerrar, en WM_CLOSE)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// appDir: subcarpeta propia en AppData/Cache/Temp (config, lock de instancia, datos de WebView2,
// cfr.jar extraído). La comparten config.go, main.go y decompile.go.
const appDir = "Cipher"

type winGeom struct {
	X   int32 `json:"x"`
	Y   int32 `json:"y"`
	W   int32 `json:"w"`
	H   int32 `json:"h"`
	Max bool  `json:"max"`
}

type appConfig struct {
	RScale float64  `json:"rscale"`
	Wrap   *bool    `json:"wrap"`
	Window *winGeom `json:"window"`
}

var (
	gCfg   appConfig
	gCfgMu sync.Mutex
)

func configPath() string {
	d, err := os.UserConfigDir()
	if err != nil || d == "" {
		if d, err = os.UserCacheDir(); err != nil {
			d = os.TempDir()
		}
	}
	return filepath.Join(d, appDir, "config.json")
}

func loadConfig() {
	gCfgMu.Lock()
	defer gCfgMu.Unlock()
	gCfg = appConfig{RScale: 1.0}
	b, err := os.ReadFile(configPath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &gCfg)
	if gCfg.RScale <= 0 {
		gCfg.RScale = 1.0
	}
}

// saveConfigLocked escribe el config a disco. El llamador debe tener gCfgMu tomado.
func saveConfigLocked() {
	p := configPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	b, err := json.MarshalIndent(gCfg, "", "  ")
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, p) // reemplazo atómico
	}
}

func setUIPrefs(rscale float64, wrap bool) {
	gCfgMu.Lock()
	defer gCfgMu.Unlock()
	if rscale > 0 {
		gCfg.RScale = rscale
	}
	w := wrap
	gCfg.Wrap = &w
	saveConfigLocked()
}

func saveWindowGeom(g winGeom) {
	if g.W < 200 || g.H < 150 {
		return // tamaño absurdo (minimizado/transitorio): no guardar
	}
	gCfgMu.Lock()
	defer gCfgMu.Unlock()
	gCfg.Window = &g
	saveConfigLocked()
}

func uiPrefs() (rscale float64, wrap bool) {
	gCfgMu.Lock()
	defer gCfgMu.Unlock()
	rscale = gCfg.RScale
	wrap = false
	if gCfg.Wrap != nil {
		wrap = *gCfg.Wrap
	}
	return
}

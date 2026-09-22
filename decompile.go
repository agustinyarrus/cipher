package main

// decompile.go — capa de decompilación. Para extensiones binarias "legibles" (bytecode, etc.) no
// mostramos los bytes: corremos un decompilador externo, obtenemos código fuente y lo devolvemos a
// render.go para resaltarlo con chroma como cualquier archivo.
//
// Diseño extensible: decompilerFor(path) mapea extensión -> decompilador. Hoy:
//   .class  -> CFR (jar embebido en el .exe) usando el java del sistema; fallback a javap (JDK).
// Para sumar otros (.jar, .pyc, .wasm, .dll .NET) basta agregar una entrada y su función run().
//
// El visor de CÓDIGO funciona sin ninguna toolchain; sólo decompilar .class necesita Java instalado.

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// decompileTimeout: una JVM colgada no puede dejar la pestaña esperando para siempre.
	decompileTimeout = 30 * time.Second
	// decompileMemoSize: decompilaciones recordadas. Arrancar la JVM + CFR cuesta ~1 s; volver a
	// una pestaña, reabrir el mismo .class o una recarga sin cambios sale de la memoria.
	decompileMemoSize = 32
)

// CFR (github.com/leibnitz27/cfr, MIT) embebido para que el .exe siga siendo portable. Se extrae a
// la caché del usuario la primera vez que se decompila un .class.
//
//go:embed tools/cfr.jar
var toolsFS embed.FS

type decompiler struct {
	lang     string                                           // nombre legible (barra de estado)
	langHint string                                           // alias de lenguaje para chroma
	tool     string                                           // herramienta principal (para mensajes de error)
	run      func(path string) (code, tool string, err error) // devuelve la fuente + la herramienta usada
}

// decompilerFor devuelve el decompilador para la extensión de path, o nil si no aplica.
func decompilerFor(path string) *decompiler {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".class":
		return &decompiler{lang: "Java", langHint: "java", tool: "CFR", run: decompileClass}
	}
	return nil
}

// IsDecompilable indica si una extensión se decompila (para el filtro del diálogo y el cliente).
func IsDecompilable(name string) bool { return decompilerFor(name) != nil }

// ---- memoria de decompilaciones ---------------------------------------------

// decompKey identifica una VERSION del archivo: si cambia en disco, cambia la clave.
type decompKey struct {
	path string
	mod  int64
	size int64
}

type decompResult struct{ code, tool string }

var decompMemo = struct {
	sync.Mutex
	m     map[decompKey]decompResult
	order []decompKey // FIFO: suficiente para un tope chico
}{m: map[decompKey]decompResult{}}

// decompileCached corre el decompilador salvo que ya se haya decompilado ESA version del archivo.
// Los errores no se recuerdan: instalar Java y reabrir tiene que andar.
func decompileCached(dec *decompiler, path string) (string, string, error) {
	fi, statErr := os.Stat(path)
	if statErr != nil {
		return dec.run(path)
	}
	key := decompKey{strings.ToLower(path), fi.ModTime().UnixNano(), fi.Size()}
	decompMemo.Lock()
	if r, ok := decompMemo.m[key]; ok {
		decompMemo.Unlock()
		return r.code, r.tool, nil
	}
	decompMemo.Unlock()

	code, tool, err := dec.run(path)
	if err != nil {
		return code, tool, err
	}
	decompMemo.Lock()
	if _, dup := decompMemo.m[key]; !dup {
		decompMemo.m[key] = decompResult{code, tool}
		decompMemo.order = append(decompMemo.order, key)
		if len(decompMemo.order) > decompileMemoSize {
			delete(decompMemo.m, decompMemo.order[0])
			decompMemo.order = decompMemo.order[1:]
		}
	}
	decompMemo.Unlock()
	return code, tool, nil
}

// ---- .class (Java) ------------------------------------------------------

func decompileClass(path string) (string, string, error) {
	java, err := javaExe()
	if err != nil {
		return "", "", err
	}
	// 1) CFR -> fuente Java de alto nivel
	if jar, jerr := cfrJar(); jerr == nil {
		if out, derr := runTool(java, "-jar", jar, path); derr == nil && strings.TrimSpace(out) != "" {
			return out, "CFR", nil
		}
	}
	// 2) fallback: javap -p -c (desensamblado de bytecode; menos lindo pero siempre disponible en el JDK)
	if out, derr := runTool(javapExe(java), "-p", "-c", path); derr == nil && strings.TrimSpace(out) != "" {
		return out, "javap", nil
	}
	return "", "", errors.New("no se pudo decompilar el .class (CFR y javap fallaron)")
}

// ---- localización de la toolchain Java ---------------------------------

func javaExe() (string, error) {
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		if p := filepath.Join(jh, "bin", "java.exe"); fileExists(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath("java"); err == nil {
		return p, nil
	}
	return "", errors.New("Para decompilar .class hace falta Java.\n" +
		"No se encontró 'java' en el PATH ni en JAVA_HOME.\n\n" +
		"Instalá un JDK/JRE (por ejemplo Temurin / OpenJDK) y reabrí el archivo.")
}

func javapExe(java string) string {
	if jp := filepath.Join(filepath.Dir(java), "javap.exe"); fileExists(jp) {
		return jp
	}
	if p, err := exec.LookPath("javap"); err == nil {
		return p
	}
	return "javap"
}

// ---- CFR jar embebido ---------------------------------------------------

var (
	cfrOnce sync.Once
	cfrPath string
	cfrErr  error
)

func cfrJar() (string, error) {
	cfrOnce.Do(func() {
		data, err := toolsFS.ReadFile("tools/cfr.jar")
		if err != nil {
			cfrErr = err
			return
		}
		dir := appCacheDir()
		os.MkdirAll(dir, 0o755)
		dst := filepath.Join(dir, "cfr.jar")
		// reescribir sólo si falta o cambió el tamaño (evita IO en cada decompilación)
		if fi, e := os.Stat(dst); e != nil || fi.Size() != int64(len(data)) {
			if werr := os.WriteFile(dst, data, 0o644); werr != nil {
				cfrErr = werr
				return
			}
		}
		cfrPath = dst
	})
	return cfrPath, cfrErr
}

// ---- helpers ------------------------------------------------------------

// runTool ejecuta un binario externo SIN abrir consola y devuelve su stdout (con stderr anexado si
// falla). Con tope de tiempo: si la herramienta se cuelga, se la mata y se informa.
func runTool(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), decompileTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), fmt.Errorf("%s no respondió en %v", filepath.Base(name), decompileTimeout)
	}
	if err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.String(), fmt.Errorf("%s: %s", filepath.Base(name), firstLine(msg))
		}
		return out.String(), err
	}
	return out.String(), nil
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func appCacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, appDir)
}

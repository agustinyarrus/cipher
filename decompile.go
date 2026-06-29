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
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

// runTool ejecuta un binario externo SIN abrir consola y devuelve su stdout (con stderr anexado si falla).
func runTool(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
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

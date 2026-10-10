package main

import (
	"strings"
	"testing"

	"cipher/i18n"
)

// enIngles corre la prueba con la interfaz en inglés y la deja en castellano al terminar (el resto
// de las pruebas asume el idioma fuente).
func enIngles(t *testing.T) {
	t.Helper()
	i18n.Set(i18n.EN)
	t.Cleanup(func() { i18n.Set(i18n.ES) })
}

// Lo que el Go le manda a la barra de estado sale en el idioma de la interfaz: la codificación
// detectada y el motivo de no resaltar. Castellano ya lo cubre engine_test.go.
func TestServidorEnIngles(t *testing.T) {
	enIngles(t)
	le := []byte{}
	for _, r := range "Windows Registry Editor Version 5.00\r\n[HKEY_CURRENT_USER]\r\n" {
		le = append(le, byte(r), 0)
	}
	if _, enc := decodeText(le); enc != "UTF-16 LE without BOM" {
		t.Errorf("codificación en inglés = %q", enc)
	}
	minJS := "var a=1;" + strings.Repeat("a=a+1;", 400_000) // un solo renglón enorme: va plano
	if res, _ := RenderText([]byte(minJS), "min.js"); res.Plain != "minified" {
		t.Errorf("motivo en inglés = %q", res.Plain)
	}
}

// El aviso de Java faltante es largo y con saltos de línea: que no se cuele nada en castellano.
func TestAvisoJavaEnIngles(t *testing.T) {
	enIngles(t)
	msg := i18n.T("Para decompilar .class hace falta Java.\n" +
		"No se encontró 'java' en el PATH ni en JAVA_HOME.\n\n" +
		"Instalá un JDK/JRE (por ejemplo Temurin / OpenJDK) y reabrí el archivo.")
	if !strings.HasPrefix(msg, "Decompiling .class files needs Java.") || strings.ContainsAny(msg, "áéíóú") {
		t.Errorf("aviso de Java en inglés = %q", msg)
	}
}

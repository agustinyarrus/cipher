package main

import (
	"strings"
	"testing"
)

// utf16le arma el contenido de un archivo tal como lo escribe regedit: BOM + UTF-16 LE.
func utf16le(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, r := range s {
		out = append(out, byte(r), 0)
	}
	return out
}

// Un .reg exportado por regedit viene en UTF-16: sin decodificar, la heuristica del
// byte NUL lo daba por binario y Cipher no mostraba nada.
func TestRegUTF16NoEsBinario(t *testing.T) {
	reg := "Windows Registry Editor Version 5.00\r\n\r\n" +
		"[HKEY_CURRENT_USER\\Control Panel\\Desktop]\r\n" +
		"\"CaptionHeight\"=\"-330\"\r\n"
	res, err := RenderText(utf16le(reg), "backup.reg")
	if err != nil {
		t.Fatal(err)
	}
	if res.Binary {
		t.Fatal("el .reg en UTF-16 se reporto como binario")
	}
	if res.Encoding != "UTF-16 LE" {
		t.Errorf("encoding = %q, esperaba UTF-16 LE", res.Encoding)
	}
	if !res.CRLF {
		t.Error("no detecto CRLF")
	}
	if !strings.Contains(res.HTML, "CaptionHeight") {
		t.Error("el contenido no aparece en el HTML")
	}
	if !strings.Contains(strings.ToLower(res.Lang), "reg") {
		t.Errorf("lang = %q, esperaba el lexer de registro", res.Lang)
	}
}

func TestDecodeText(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
		enc  string
	}{
		{"utf8 pelado", []byte("hola"), "hola", ""},
		{"utf8 con bom", []byte{0xEF, 0xBB, 0xBF, 'h', 'i'}, "hi", "UTF-8 BOM"},
		{"utf16 le", utf16le("hi"), "hi", "UTF-16 LE"},
		{"utf16 be", []byte{0xFE, 0xFF, 0, 'h', 0, 'i'}, "hi", "UTF-16 BE"},
		{"vacio", []byte{}, "", ""},
		{"impar", []byte{0xFF, 0xFE, 'a'}, "", "UTF-16 LE"},
	}
	for _, c := range cases {
		got, enc := decodeText(c.in)
		if string(got) != c.want || enc != c.enc {
			t.Errorf("%s: (%q,%q), esperaba (%q,%q)", c.name, got, enc, c.want, c.enc)
		}
	}
}

// Un binario de verdad sigue siendo binario.
func TestBinarioSigueSiendoBinario(t *testing.T) {
	exe := []byte{'M', 'Z', 0x90, 0x00, 0x03, 0x00, 0x00, 0x00}
	res, err := RenderText(exe, "algo.exe")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Binary {
		t.Error("un .exe deberia seguir dando binario")
	}
}

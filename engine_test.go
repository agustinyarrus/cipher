package main

// engine_test.go — el motor nuevo contra el de siempre: el formateador en streaming tiene que
// decir LO MISMO que el de chroma (mismo texto, mismas clases) y el resaltado progresivo tiene que
// terminar byte a byte igual que un resaltado entero hecho de una vez.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
)

// ---- equivalencia con el formateador de chroma ----------------------------------

var (
	chunkTagRe = regexp.MustCompile(`<div class="cx" data-c="\d+" style="--w:\d+(?:;--n:\d+)?">|</div>`)
	emptySpan  = regexp.MustCompile(`<span class="[a-z0-9]+"></span>`)
)

// reference: lo que producia el formateador de chroma (con los \n sacados, como antes).
func reference(t *testing.T, code, hint string) string {
	t.Helper()
	f := chromahtml.New(chromahtml.WithClasses(true), chromahtml.WithLineNumbers(true),
		chromahtml.LineNumbersInTable(false), chromahtml.TabWidth(4))
	it, err := chroma.Coalesce(pickLexer(code, hint)).Tokenise(nil, code)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := f.Format(&b, cipherStyle, it); err != nil {
		t.Fatal(err)
	}
	out := strings.ReplaceAll(b.String(), "\n", "")
	// los <span class="w"></span> vacios que dejaba cada salto se van; el .cl de un renglon
	// vacio se queda (es estructura: todos los renglones tienen su columna de codigo)
	const keep = "\x00cl\x00"
	out = strings.ReplaceAll(out, `<span class="cl"></span>`, keep)
	out = emptySpan.ReplaceAllString(out, "")
	return strings.ReplaceAll(out, keep, `<span class="cl"></span>`)
}

var samples = map[string]string{
	"x.go":   "package main\n\n/* un comentario\n   de varias lineas */\nfunc main() {\n\ts := `raw\nmultilinea`\n\tprintln(\"hola\\n\", s) // fin\n}\n",
	"x.py":   "def f(x):\n    \"\"\"docstring\n\n    con blanco\"\"\"\n    return x ** 2  # ñandú\n",
	"x.js":   "const a = `plantilla ${1 + 2}\nsigue`;\nfunction f() { return /re+gex/g.test('ok'); }",
	"x.sql":  "SELECT a, 'texto'' con comilla'\nFROM t -- comentario\nWHERE b = 1;\n",
	"x.html": "<!doctype html>\n<p class=\"x\">&amp; <b>hola</b></p>\n<script>let x = 1 < 2;</script>\n",
	"x.json": "{\n  \"a\": [1, 2.5e3, true, null],\n  \"b\": \"\\u00f1\"\n}\n",
	"x.md":   "# Titulo\n\n- item con `codigo`\n\n```go\nfunc x() {}\n```\n",
	"x.txt":  "texto plano\tcon tab\n\nultima linea sin salto",
	"x.c":    "#include <stdio.h>\nint main(void) {\n  /* a */ printf(\"%d\\n\", 1);\n}\n",
	"x.yaml": "clave: valor\nlista:\n  - 'uno'\n  - \"dos\"\n",
	"x.ps1":  "param([switch]$X)\n# comentario\nWrite-Host \"hola $X\" -ForegroundColor Green\n",
	"x.rs":   "fn main() {\n    let s = r#\"crudo\n\"#;\n    println!(\"{}\", s);\n}\n",
	"vacio":  "",
	"x.go2":  "a\n\n\n",
}

func TestStreamingFormatterMatchesChroma(t *testing.T) {
	for name, code := range samples {
		hint := strings.TrimSuffix(name, "2")
		got := highlight(code, hint, "")
		want := reference(t, code, hint)
		mine := chunkTagRe.ReplaceAllString(got.HTML, "")
		if mine != want {
			t.Errorf("%s: el formateador propio difiere del de chroma\n  propio: %s\n  chroma: %s", name, cut(mine), cut(want))
		}
	}
}

func cut(s string) string {
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}

// ---- resaltado progresivo -----------------------------------------------------------

// bigGo arma un .go grande con comentarios de bloque que CRUZAN el borde entre bloques: si el
// trabajo en segundo plano no heredara el estado del lexer, esos renglones saldrian mal pintados.
func bigGo(lines int) string {
	var b strings.Builder
	b.WriteString("package main\n\n")
	for i := 0; b.Len() < lines*40; i++ {
		fmt.Fprintf(&b, "// funcion %d\nfunc f%d(x int) int {\n\t/* bloque que\n\tcruza renglones */\n\treturn x * %d // fin\n}\n\n", i, i, i)
	}
	return b.String()
}

func TestProgressiveEqualsFullHighlight(t *testing.T) {
	code := bigGo(20000)

	// referencia: todo sincronico
	saved := syncBudget
	syncBudget = 1 << 40
	full := highlight(code, "big.go", "")
	syncBudget = saved
	if full.HLJob != 0 {
		t.Fatal("con presupuesto infinito no deberia quedar trabajo pendiente")
	}

	// progresivo: parte sincronica + trabajo
	syncBudget = 64 << 10
	defer func() { syncBudget = saved }()
	res := highlight(code, "big.go", pathKey(`C:\x\big.go`))
	if res.HLJob == 0 || res.HLFrom == 0 || res.HLFrom >= res.Chunks {
		t.Fatalf("esperaba trabajo pendiente: job=%d from=%d chunks=%d", res.HLJob, res.HLFrom, res.Chunks)
	}
	chunks := collect(t, res.HLJob, res.HLFrom, res.Chunks)

	// empalmar: reemplazar los bloques planos por los resaltados
	html := res.HTML
	for i, c := range chunks {
		idx := res.HLFrom + i
		start := strings.Index(html, fmt.Sprintf(`<div class="cx" data-c="%d"`, idx))
		end := start + strings.Index(html[start:], `</div>`) + len(`</div>`)
		html = html[:start] + c + html[end:]
	}
	if html != full.HTML {
		t.Fatalf("el resultado progresivo no coincide con el completo (%d vs %d bytes)", len(html), len(full.HTML))
	}
	if _, _, ok := takeChunks(res.HLJob, res.Chunks); ok {
		t.Error("el trabajo ya entregado entero deberia haberse descartado")
	}
}

// collect pide los bloques hasta tenerlos todos (como hace la pagina con cada aviso del bus).
func collect(t *testing.T, id uint64, from, total int) []string {
	t.Helper()
	var out []string
	deadline := time.Now().Add(60 * time.Second)
	for from+len(out) < total {
		if time.Now().After(deadline) {
			t.Fatalf("el trabajo no termino: %d de %d bloques", from+len(out), total)
		}
		got, _, ok := takeChunks(id, from+len(out))
		if !ok {
			t.Fatalf("el trabajo desaparecio con %d de %d bloques", from+len(out), total)
		}
		out = append(out, got...)
		if len(got) == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	return out
}

func TestNewRenderCancelsPreviousJob(t *testing.T) {
	saved := syncBudget
	syncBudget = 8 << 10
	defer func() { syncBudget = saved }()
	code := bigGo(30000)
	a := highlight(code, "a.go", "clave")
	b := highlight(code, "a.go", "clave")
	if a.HLJob == 0 || b.HLJob == 0 || a.HLJob == b.HLJob {
		t.Fatalf("jobs: %d %d", a.HLJob, b.HLJob)
	}
	if _, _, ok := takeChunks(a.HLJob, a.HLFrom); ok {
		t.Error("el trabajo del render anterior tenia que cancelarse")
	}
	cancelJobsExcept(map[string]bool{}) // recien nacido: la gracia lo protege
	if _, _, ok := takeChunks(b.HLJob, b.HLFrom); !ok {
		t.Error("un trabajo recien nacido no se cancela por una sincronizacion de pestañas")
	}
}

// ---- archivos grandes: los tiempos que motivaron el cambio ------------------------------

func TestLargeFilesAreFast(t *testing.T) {
	if testing.Short() {
		t.Skip("archivos de varios MB")
	}
	logSrc := []byte(strings.Repeat("2026-09-22 10:00:00 INFO algo paso en el sistema id=12345 ok\n", 12<<20/62))
	minJS := []byte("var a=" + strings.Repeat("{b:1,c:'x',d:[1,2,3]},", 2<<20/22) + "0;")
	goSrc := []byte(bigGo(150000))
	for _, c := range []struct {
		name  string
		src   []byte
		limit time.Duration
	}{
		{"log-12MB.log", logSrc, 3 * time.Second},     // antes: 69 s (Analyse sobre el archivo entero)
		{"min-2MB.js", minJS, 1 * time.Second},        // antes: 12 s (chroma en un renglon de 2 MB)
		{"go-5MB.go", goSrc, 2500 * time.Millisecond}, // antes: 8,8 s
	} {
		t0 := time.Now()
		res, _ := RenderText(c.src, c.name)
		d := time.Since(t0)
		t.Logf("%-13s %6.1f MB -> %6.1f MB de HTML en %v (plano=%q, job=%d)", c.name,
			float64(len(c.src))/1e6, float64(len(res.HTML))/1e6, d.Round(time.Millisecond), res.Plain, res.HLJob)
		if d > c.limit {
			t.Errorf("%s tardo %v (tope %v)", c.name, d, c.limit)
		}
	}
	if res, _ := RenderText(minJS, "min.js"); res.Plain != "minificado" {
		t.Errorf("un renglon de 2 MB tenia que ir plano, fue %q", res.Plain)
	}
}

// ---- decodificacion, finales de linea, recorte --------------------------------------------

func TestUTF16WithoutBOM(t *testing.T) {
	le := []byte{}
	for _, r := range "Windows Registry Editor Version 5.00\r\n[HKEY_CURRENT_USER]\r\n" {
		le = append(le, byte(r), 0)
	}
	got, enc := decodeText(le)
	if enc != "UTF-16 LE sin BOM" || !strings.HasPrefix(string(got), "Windows Registry") {
		t.Errorf("LE sin BOM: enc=%q texto=%q", enc, got)
	}
	be := []byte{}
	for _, r := range "hola mundo, texto largo en big endian" {
		be = append(be, 0, byte(r))
	}
	if _, enc := decodeText(be); enc != "UTF-16 BE sin BOM" {
		t.Errorf("BE sin BOM: enc=%q", enc)
	}
	// un binario con ceros por todos lados NO es UTF-16
	exe := append([]byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff\x00\x00"), make([]byte, 64)...)
	if _, enc := decodeText(exe); enc != "" {
		t.Errorf("un binario se tomo por UTF-16: %q", enc)
	}
	if res, _ := RenderText(exe, "a.exe"); !res.Binary {
		t.Error("el binario tiene que seguir siendo binario")
	}
}

func TestDetectEOL(t *testing.T) {
	for in, want := range map[string]string{
		"":              "",
		"sin salto":     "",
		"a\nb\n":        "LF",
		"a\r\nb\r\n":    "CRLF",
		"a\rb\r":        "CR",
		"a\r\nb\nc\r\n": "Mixto",
	} {
		if got, _ := detectEOL([]byte(in)); got != want {
			t.Errorf("detectEOL(%q) = %q, esperaba %q", in, got, want)
		}
	}
}

func TestTruncateAtLine(t *testing.T) {
	src := []byte("uno\ndos\ntres\n")
	if got := string(truncateAtLine(src, 9)); got != "uno\ndos\n" {
		t.Errorf("corte en renglon: %q", got)
	}
	ene := []byte("ññññ") // 8 bytes, sin saltos: no partir una ñ
	if got := truncateAtLine(ene, 5); string(got) != "ññ" {
		t.Errorf("corte UTF-8: %q", got)
	}
}

func TestLinesAndChunks(t *testing.T) {
	for code, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\n\n": 2} {
		if got := highlight(code, "x.txt", "").Lines; got != want {
			t.Errorf("Lines(%q) = %d, esperaba %d", code, got, want)
		}
	}
	code := strings.Repeat("x\n", chunkLines*2+7)
	res := highlight(code, "x.txt", "")
	if res.Chunks != 3 || strings.Count(res.HTML, `class="cx"`) != 3 || !strings.Contains(res.HTML, `;--n:7"`) {
		t.Errorf("bloques: %d, html con %d cx", res.Chunks, strings.Count(res.HTML, `class="cx"`))
	}
}

// ---- --hl ---------------------------------------------------------------------------------

func TestParseHLSpec(t *testing.T) {
	cases := map[string][][3]int{
		"":               {},
		"12-15,40":       {{12, 15, 0}, {40, 40, 0}},
		"+3-5,-9,20-18":  {{3, 5, 1}, {9, 9, 2}, {18, 20, 0}},
		"1-3,2-6,+4":     {{1, 6, 0}, {4, 4, 1}}, // fusiona solo el mismo tipo
		"x,-,+,0,5-x,7":  {{7, 7, 0}},            // basura: se ignora
		" 10 - 12 , +11": {{10, 12, 0}, {11, 11, 1}},
	}
	for in, want := range cases {
		got := parseHLSpec(in)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("parseHLSpec(%q) = %v, esperaba %v", in, got, want)
		}
	}
}

// ---- server ----------------------------------------------------------------------------

func TestOnlyLocalHost(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := onlyLocalHost("127.0.0.1:7777", ok)
	for host, want := range map[string]int{
		"127.0.0.1:7777": 204, "localhost:7777": 204, "evil.com:7777": 403, "127.0.0.1:1": 403, "": 403,
	} {
		req := httptest.NewRequest("GET", "/render?path=C:/x", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: %d, esperaba %d", host, rec.Code, want)
		}
	}
}

func TestSettleAndStamp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, _ := stampOf(p)
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = os.WriteFile(p, []byte(""), 0o644)
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(p, []byte("final"), 0o644)
	}()
	got := settle(p, first)
	if got.size != 5 {
		t.Errorf("settle devolvio tamaño %d, esperaba el guardado final (5)", got.size)
	}
	if missing, _ := stampOf(filepath.Join(t.TempDir(), "no")); missing.ok || !missing.same(fileStamp{}) {
		t.Error("un archivo inexistente tiene que dar el sello cero")
	}
	_ = context.Background
}

package main

// render.go — motor de resaltado de código. Todo el trabajo vive en Go (chroma), compilado dentro
// del .exe: sin CDN, sin JS de parsing que vendorizar. chroma trae 250+ lenguajes (port de Pygments).
//
// Flujo: leer el archivo -> ¿es decompilable (.class, …)? -> sí: pasar por la capa de decompilación
// (ver decompile.go), que devuelve código fuente legible + el lenguaje en que resaltarlo. ¿no?:
// detectar binario (se muestra un aviso) o resaltar como texto. La detección de lenguaje usa el
// nombre del archivo (extensión + nombres especiales tipo Dockerfile/Makefile) y, si no alcanza,
// el análisis del contenido. Devolvemos HTML con números de línea + metadatos (lenguaje, líneas,
// bytes, si vino decompilado y con qué herramienta).
//
// El HTML sale en BLOQUES de chunkLines renglones (<div class="cx">): el navegador saltea el
// layout de los que no se ven (content-visibility, ver style.css) y son la unidad del resaltado
// progresivo de los archivos grandes (ver hljob.go). El formateador es propio y en streaming:
// mismas clases y mismo escapado que el de chroma, pero sin juntar el millón de tokens de un
// archivo grande en un slice ni formatear cada uno con fmt.

import (
	"bytes"
	"html"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
)

const (
	// maxRenderBytes: tope de tamaño que mostramos; más allá se trunca (en un renglón entero) y se avisa.
	maxRenderBytes = 12 << 20 // 12 MiB
	// chunkLines: renglones por bloque. Unidad de content-visibility y de resaltado progresivo.
	chunkLines = 256
	// longLine: un renglón más largo que esto es un minificado. chroma se vuelve lentísimo ahí
	// (2 MB en una línea eran 12 s) y nadie lee eso resaltado: el archivo va plano.
	longLine = 16 << 10
	// analyseWindow: lo que miran los analizadores de chroma para adivinar el lenguaje. Sobre el
	// archivo entero, un .log de 12 MB tardaba 66 s en decidir que era texto; sobre 16 KB, 3 ms.
	analyseWindow = 16 << 10
)

// syncBudget: bytes de fuente que se resaltan ANTES de responder. chroma tokeniza a ~0,6 MB/s,
// así que esto son las primeras pantallas en ~0,4 s; el resto llega resaltado en segundo plano.
// (Variable y no constante sólo para que las pruebas puedan forzar el resaltado entero.)
var syncBudget = 256 << 10

// extrasExts: extensiones útiles que chroma no lista como tales pero que igual queremos abrir
// (decompilables + texto suelto).
var extrasExts = []string{".class", ".txt", ".log", ".text", ".env", ".gitignore", ".gitattributes"}

// validExt acepta sólo extensiones ASCII "sanas" (.go, .c++, .6pl) y descarta rarezas que romperían
// el registro de Windows (emoji como el .🔥 de Mojo, .µcad, etc.).
func validExt(e string) bool {
	if len(e) < 2 || e[0] != '.' {
		return false
	}
	for _, r := range e[1:] {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '#', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// AllExtensions devuelve, ordenadas y sin repetir, TODAS las extensiones que chroma reconoce
// (de los Filenames/AliasFilenames de cada lexer) más las extras. La fuente única de verdad para el
// filtro del diálogo y para el registro de "Abrir con": así Cipher cubre los 250+ lenguajes de chroma.
func AllExtensions() []string {
	set := map[string]bool{}
	add := func(globs []string) {
		for _, g := range globs {
			if !strings.HasPrefix(g, "*.") { // sólo patrones de extensión (no "Dockerfile", "Makefile", …)
				continue
			}
			ext := strings.ToLower(g[strings.LastIndex(g, "."):]) // última extensión: "*.html.erb" -> ".erb"
			if validExt(ext) {
				set[ext] = true
			}
		}
	}
	for _, name := range lexers.Names(false) {
		l := lexers.Get(name)
		if l == nil {
			continue
		}
		cfg := l.Config()
		add(cfg.Filenames)
		add(cfg.AliasFilenames)
	}
	for _, e := range extrasExts {
		set[e] = true
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// CodeGlob arma el patrón "*.ext;*.ext;…" (todas las extensiones de chroma) para el diálogo "Abrir".
func CodeGlob() string {
	exts := AllExtensions()
	parts := make([]string, len(exts))
	for i, e := range exts {
		parts[i] = "*" + e
	}
	return strings.Join(parts, ";")
}

// RenderResult: HTML resaltado + metadatos para la barra de estado del cliente.
type RenderResult struct {
	HTML       string `json:"html"`
	Lang       string `json:"lang"`             // nombre legible del lenguaje (p.ej. "Go", "Java")
	Lines      int    `json:"lines"`            // cantidad de líneas
	Bytes      int    `json:"bytes"`            // tamaño del texto mostrado
	Chars      int    `json:"chars"`            // cantidad de caracteres (runas) del texto mostrado
	Decompiled bool   `json:"decompiled"`       // true si el contenido salió de un decompilador
	Tool       string `json:"tool"`             // herramienta de decompilación usada (CFR, javap, …)
	Binary     bool   `json:"binary"`           // true si el archivo es binario y no se pudo mostrar
	Truncated  bool   `json:"truncated"`        // true si se recortó por tamaño
	CRLF       bool   `json:"crlf"`             // hay saltos CRLF (compatibilidad: ver EOL)
	EOL        string `json:"eol"`              // "LF", "CRLF", "CR", "Mixto" o "" (un solo renglón)
	Encoding   string `json:"encoding"`         // "" = UTF-8; si no, "UTF-8 BOM" / "UTF-16 LE" / "UTF-16 BE"…
	Chunks     int    `json:"chunks"`           // bloques de chunkLines renglones
	HLJob      uint64 `json:"hlJob,omitempty"`  // trabajo que sigue resaltando en segundo plano
	HLFrom     int    `json:"hlFrom,omitempty"` // primer bloque que llegó plano (lo completa HLJob)
	Plain      string `json:"plain,omitempty"`  // por qué no se resaltó ("minificado")
}

// decodeText normaliza la entrada a UTF-8 sin BOM y dice con qué se encontró.
//
// Hace falta de verdad en Windows: un .reg exportado por regedit, un .ps1 guardado por el ISE o
// un .txt del Bloc de notas suelen venir en UTF-16 LE. Sin esto, isBinary ve los bytes NUL de cada
// caracter ASCII y declara binario un archivo de texto perfectamente legible. Algunas herramientas
// ni siquiera ponen el BOM: eso se reconoce por dónde caen los NUL (ver utf16Sniff).
func decodeText(src []byte) ([]byte, string) {
	switch {
	case len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF:
		return src[3:], "UTF-8 BOM"
	case len(src) >= 2 && src[0] == 0xFF && src[1] == 0xFE:
		return utf16ToUTF8(src[2:], false), "UTF-16 LE"
	case len(src) >= 2 && src[0] == 0xFE && src[1] == 0xFF:
		return utf16ToUTF8(src[2:], true), "UTF-16 BE"
	}
	if le, ok := utf16Sniff(src); ok {
		if le {
			return utf16ToUTF8(src, false), "UTF-16 LE sin BOM"
		}
		return utf16ToUTF8(src, true), "UTF-16 BE sin BOM"
	}
	return src, ""
}

// utf16Sniff reconoce UTF-16 SIN BOM en un texto mayormente latino: en UTF-16 LE los bytes
// impares de cada caracter ASCII son 0 y los pares no; en BE, al revés. Un binario de verdad
// tiene ceros por todos lados. Mira los primeros 4 KB: O(1).
func utf16Sniff(src []byte) (littleEndian, ok bool) {
	n := min(len(src), 4096) &^ 1
	if n < 8 {
		return false, false
	}
	var zeroEven, zeroOdd int
	for i := 0; i < n; i += 2 {
		if src[i] == 0 {
			zeroEven++
		}
		if src[i+1] == 0 {
			zeroOdd++
		}
	}
	pairs := n / 2
	switch {
	case zeroOdd*10 >= pairs*9 && zeroEven*10 <= pairs: // ≥90 % de impares en 0, ≤10 % de pares
		return true, true
	case zeroEven*10 >= pairs*9 && zeroOdd*10 <= pairs:
		return false, true
	}
	return false, false
}

func utf16ToUTF8(b []byte, bigEndian bool) []byte {
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		if bigEndian {
			u[i] = uint16(b[i*2])<<8 | uint16(b[i*2+1])
		} else {
			u[i] = uint16(b[i*2+1])<<8 | uint16(b[i*2])
		}
	}
	return []byte(string(utf16.Decode(u)))
}

// ---- estilo chroma (sólo para el CSS de los tokens) -------------------------------

// Las mismas opciones que el formateador original (renglones numerados en linea): WriteCSS
// emite tambien las reglas de .line/.ln/.cl y tienen que ser exactamente las de siempre.
var cssFormatter = chromahtml.New(
	chromahtml.WithClasses(true),
	chromahtml.WithLineNumbers(true),
	chromahtml.LineNumbersInTable(false),
	chromahtml.TabWidth(4),
)

// RenderFile lee la fuente (ya provista en src) y devuelve el HTML resaltado + metadatos. Decide
// solo si decompilar, si es binario o si resaltar como texto. jobKey identifica el archivo para el
// resaltado progresivo (un render nuevo del mismo archivo cancela el trabajo anterior).
func RenderFile(path, name string, src []byte, jobKey string) (RenderResult, error) {
	// 1) ¿extensión decompilable? -> capa de decompilación (usa la ruta en disco, no src)
	if dec := decompilerFor(path); dec != nil {
		code, tool, err := decompileCached(dec, path)
		if err != nil {
			return RenderResult{
				HTML:       errorBlock(err.Error()),
				Lang:       dec.lang,
				Decompiled: true,
				Tool:       dec.tool,
			}, nil
		}
		res := highlight(code, dec.langHint, jobKey)
		res.Decompiled = true
		res.Tool = tool
		if res.Lang == "" || strings.EqualFold(res.Lang, "plaintext") {
			res.Lang = dec.lang
		}
		return res, nil
	}
	// 2) archivo de texto / binario
	return renderSource(src, name, jobKey), nil
}

// RenderText resalta texto crudo (arrastrar-y-soltar, sin ruta en disco). El nombre da la pista
// de lenguaje.
func RenderText(src []byte, name string) (RenderResult, error) {
	return renderSource(src, name, ""), nil
}

// renderSource: decodificar, descartar binarios, recortar y resaltar. Lo comparten el archivo en
// disco y el texto soltado en la ventana.
func renderSource(src []byte, name, jobKey string) RenderResult {
	src, enc := decodeText(src)
	if isBinary(src) {
		return RenderResult{Binary: true, Bytes: len(src)}
	}
	truncated := false
	if len(src) > maxRenderBytes {
		src = truncateAtLine(src, maxRenderBytes)
		truncated = true
	}
	eol, crlf := detectEOL(src)
	res := highlight(string(src), name, jobKey)
	res.Truncated = truncated
	res.CRLF = crlf
	res.EOL = eol
	res.Encoding = enc
	return res
}

// truncateAtLine corta en el último salto de línea antes del tope (y, si no hay ninguno, en un
// borde de caracter UTF-8): cortar a ciegas dejaba un renglón a medias y, a veces, un caracter
// partido que el navegador pintaba como �.
func truncateAtLine(src []byte, limit int) []byte {
	cut := src[:limit]
	if i := bytes.LastIndexByte(cut, '\n'); i > 0 {
		return cut[:i+1]
	}
	for len(cut) > 0 { // sin saltos: al menos no partir un caracter (a lo sumo 3 bytes atras)
		if r, size := utf8.DecodeLastRune(cut); r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut
}

// detectEOL cuenta los finales de línea del archivo ORIGINAL (antes de normalizar): LF, CRLF,
// CR suelto o Mixto — un archivo con finales mezclados es un clásico de los merges y conviene verlo.
func detectEOL(src []byte) (eol string, hasCRLF bool) {
	var lf, crlf, cr int
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			lf++
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				crlf++
				i++
			} else {
				cr++
			}
		}
	}
	kinds := 0
	for _, n := range []int{lf, crlf, cr} {
		if n > 0 {
			kinds++
		}
	}
	switch {
	case kinds == 0:
		return "", false
	case kinds > 1:
		return "Mixto", crlf > 0
	case crlf > 0:
		return "CRLF", true
	case cr > 0:
		return "CR", false
	}
	return "LF", false
}

// ---- estructura de renglones --------------------------------------------------------

// lineIndex: dónde arranca cada renglón (una pasada vectorizada con IndexByte), el más largo en
// bytes y, por bloque, el ancho del renglón más ancho en COLUMNAS. Ese ancho viaja al navegador:
// un bloque fuera de pantalla (content-visibility) no se mide, y sin él el ancho del documento
// —y la barra de scroll horizontal— cambiaba a medida que aparecían renglones largos.
type lineIndex struct {
	starts    []int32 // offset de byte del inicio de cada renglón
	longest   int
	chunkCols []int // columnas del renglón más ancho de cada bloque
}

const tabStop = 4 // igual que el tab-size del CSS de chroma

func indexLines(code string) lineIndex {
	if code == "" {
		return lineIndex{}
	}
	li := lineIndex{starts: make([]int32, 0, strings.Count(code, "\n")+1)}
	for pos := 0; pos < len(code); {
		li.starts = append(li.starts, int32(pos))
		nl := strings.IndexByte(code[pos:], '\n')
		end := len(code)
		if nl >= 0 {
			end = pos + nl
		}
		li.longest = max(li.longest, end-pos)
		cols := displayCols(code[pos:end])
		if k := (len(li.starts) - 1) / chunkLines; k == len(li.chunkCols) {
			li.chunkCols = append(li.chunkCols, cols)
		} else {
			li.chunkCols[k] = max(li.chunkCols[k], cols)
		}
		if nl < 0 {
			break
		}
		pos = end + 1
	}
	return li
}

// displayCols: columnas que ocupa un renglón en una fuente monoespaciada (tabs a la parada de 4).
// Camino rápido para ASCII sin tabs, que es casi todo el código.
func displayCols(line string) int {
	ascii := true
	for i := 0; i < len(line); i++ {
		if c := line[i]; c >= 0x80 || c == '\t' {
			ascii = false
			break
		}
	}
	if ascii {
		return len(line)
	}
	col := 0
	for _, r := range line {
		if r == '\t' {
			col += tabStop - col%tabStop
		} else {
			col++
		}
	}
	return col
}

func (li lineIndex) lines() int { return len(li.starts) }

// lineText devuelve el renglón i sin su salto.
func (li lineIndex) lineText(code string, i int) string {
	end := len(code)
	if i+1 < len(li.starts) {
		end = int(li.starts[i+1])
	}
	return strings.TrimSuffix(code[li.starts[i]:end], "\n")
}

// Apertura/cierre de bloque y de renglón. Todos los renglones y bloques comparten forma, así
// que un bloque plano y el mismo bloque resaltado miden exactamente lo mismo.
func openChunk(b *strings.Builder, idx, lines, cols int) {
	b.WriteString(`<div class="cx" data-c="`)
	b.WriteString(strconv.Itoa(idx))
	b.WriteString(`" style="--w:`) // ancho en columnas: el tamaño que ocupa aunque no se dibuje
	b.WriteString(strconv.Itoa(cols))
	if lines != chunkLines { // el último, más corto: su alto estimado se ajusta (ver style.css)
		b.WriteString(`;--n:`)
		b.WriteString(strconv.Itoa(lines))
	}
	b.WriteString(`">`)
}

func openLine(b *strings.Builder, number, digits int) {
	b.WriteString(`<span class="line"><span class="ln">`)
	num := strconv.Itoa(number)
	for pad := digits - len(num); pad > 0; pad-- { // mismo relleno que chroma: gutter de ancho fijo
		b.WriteByte(' ')
	}
	b.WriteString(num)
	b.WriteString(`</span><span class="cl">`)
}

const closeLine = `</span></span>`

// chunkBounds: renglones [from, to) del bloque idx.
func chunkBounds(idx, total int) (from, to int) {
	from = idx * chunkLines
	return from, min(from+chunkLines, total)
}

// writePlainChunk: el bloque sin resaltar (texto escapado), O(bytes).
func writePlainChunk(b *strings.Builder, code string, li lineIndex, idx, digits int) {
	from, to := chunkBounds(idx, li.lines())
	openChunk(b, idx, to-from, li.chunkCols[idx])
	for i := from; i < to; i++ {
		openLine(b, i+1, digits)
		b.WriteString(html.EscapeString(li.lineText(code, i)))
		b.WriteString(closeLine)
	}
	b.WriteString(`</div>`)
}

// ---- formateador en streaming ---------------------------------------------------

// lineWriter consume el iterador de chroma renglón por renglón. Guarda el resto de un token que
// cruza renglones (un comentario de bloque) para el renglón siguiente: por eso el trabajo en
// segundo plano puede seguir EXACTAMENTE donde terminó la respuesta, con el estado del lexer intacto.
type lineWriter struct {
	next     chroma.Iterator
	carry    chroma.Token
	hasCarry bool
	eof      bool
	line     int // próximo renglón (1-based)
	digits   int
	total    int   // renglones del archivo
	cols     []int // ancho en columnas de cada bloque (ver lineIndex)
	consumed int   // bytes de fuente ya escritos
	classes  map[chroma.TokenType]string
}

func newLineWriter(it chroma.Iterator, li lineIndex, digits int) *lineWriter {
	return &lineWriter{next: it, line: 1, digits: digits, total: li.lines(), cols: li.chunkCols,
		classes: map[chroma.TokenType]string{}}
}

func (w *lineWriter) token() (chroma.Token, bool) {
	if w.hasCarry {
		w.hasCarry = false
		return w.carry, true
	}
	if w.eof {
		return chroma.Token{}, false
	}
	t := w.next()
	if t == chroma.EOF {
		w.eof = true
		return chroma.Token{}, false
	}
	return t, true
}

// class: la clase CSS del tipo, calculada como el formateador de chroma (StandardTypes subiendo
// por los padres) y memoizada: un archivo grande repite unos pocos cientos de tipos millones de veces.
func (w *lineWriter) class(t chroma.TokenType) string {
	if c, ok := w.classes[t]; ok {
		return c
	}
	cls := ""
	for tt := t; ; tt = tt.Parent() {
		if c, ok := chroma.StandardTypes[tt]; ok {
			cls = c
			break
		}
		if tt == 0 {
			break
		}
	}
	w.classes[t] = cls
	return cls
}

func (w *lineWriter) writeToken(b *strings.Builder, t chroma.TokenType, text string) {
	if text == "" {
		return // chroma dejaba <span class="w"></span> vacíos por cada salto: no aportan nada
	}
	if cls := w.class(t); cls != "" {
		b.WriteString(`<span class="`)
		b.WriteString(cls)
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(text))
		b.WriteString(`</span>`)
		return
	}
	b.WriteString(html.EscapeString(text))
}

func (w *lineWriter) writeLine(b *strings.Builder) {
	openLine(b, w.line, w.digits)
	for {
		tok, ok := w.token()
		if !ok {
			break
		}
		if i := strings.IndexByte(tok.Value, '\n'); i >= 0 {
			w.writeToken(b, tok.Type, tok.Value[:i])
			w.consumed += i + 1
			if rest := tok.Value[i+1:]; rest != "" {
				w.carry, w.hasCarry = chroma.Token{Type: tok.Type, Value: rest}, true
			}
			break
		}
		w.writeToken(b, tok.Type, tok.Value)
		w.consumed += len(tok.Value)
	}
	b.WriteString(closeLine)
	w.line++
}

// writeChunk escribe el bloque idx resaltado. Tiene que llamarse en orden (el iterador avanza).
func (w *lineWriter) writeChunk(b *strings.Builder, idx int) {
	from, to := chunkBounds(idx, w.total)
	openChunk(b, idx, to-from, w.cols[idx])
	for i := from; i < to; i++ {
		w.writeLine(b)
	}
	b.WriteString(`</div>`)
}

// highlight tokeniza code y devuelve el HTML resaltado + lenguaje + cantidad de líneas.
// langHint puede ser un nombre de archivo (para Match por extensión) o un nombre de lenguaje.
func highlight(code, langHint, jobKey string) RenderResult {
	// normalizar saltos: chroma trabaja con \n; mostramos LF (el cliente sabe si el original era CRLF).
	if strings.IndexByte(code, '\r') >= 0 {
		code = strings.ReplaceAll(code, "\r\n", "\n")
		code = strings.ReplaceAll(code, "\r", "\n")
	}
	lexer := pickLexer(code, langHint)
	li := indexLines(code)
	total := li.lines()
	digits := len(strconv.Itoa(max(total, 1)))
	chunks := (total + chunkLines - 1) / chunkLines

	res := RenderResult{
		Lang:   lexer.Config().Name,
		Lines:  total,
		Bytes:  len(code),
		Chars:  utf8.RuneCountInString(code),
		Chunks: chunks,
	}

	var b strings.Builder
	b.Grow(len(code) + total*48 + 64) // texto + armazón de cada renglón (sin re-allocs)
	b.WriteString(`<pre class="chroma"><code>`)
	plain := li.longest > longLine || lexer == lexers.Fallback
	if li.longest > longLine {
		res.Plain = "minificado"
	}
	var w *lineWriter
	if !plain {
		it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
		if err != nil {
			plain = true
		} else {
			w = newLineWriter(it, li, digits)
		}
	}
	c := 0
	if w != nil { // resaltado sincrónico hasta el presupuesto (al menos el primer bloque)
		for ; c < chunks && (c == 0 || w.consumed < syncBudget); c++ {
			w.writeChunk(&b, c)
		}
	}
	for k := c; k < chunks; k++ { // el resto, plano (misma grilla: nada se mueve al completarse)
		writePlainChunk(&b, code, li, k, digits)
	}
	b.WriteString(`</code></pre>`)
	res.HTML = b.String()

	if w != nil && c < chunks {
		job := startHighlightJob(jobKey, w, c, chunks)
		res.HLJob, res.HLFrom = job.id, c
	}
	return res
}

// pickLexer elige el lexer: primero por nombre de archivo / lenguaje explícito, después por análisis
// del contenido (sólo el principio: es lo que miran los analizadores y sobre el archivo entero era
// carísimo), y por último el genérico (texto plano).
func pickLexer(code, hint string) chroma.Lexer {
	if hint != "" {
		if l := lexers.Match(hint); l != nil { // por nombre de archivo (glob: *.go, Dockerfile, …)
			return l
		}
		if l := lexers.Get(hint); l != nil { // por nombre/alias de lenguaje (go, java, python, …)
			return l
		}
	}
	window := code
	if len(window) > analyseWindow {
		window = window[:analyseWindow]
	}
	if l := lexers.Analyse(window); l != nil { // heurística sobre el contenido
		return l
	}
	return lexers.Fallback // texto plano
}

// isBinary: heurística clásica (la de git) — si hay un byte NUL en los primeros 8 KiB, es binario.
func isBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

// errorBlock arma un <pre> con un mensaje (p.ej. cuando falla la decompilación).
func errorBlock(msg string) string {
	return `<pre class="chroma cipher-msg"><code>` + html.EscapeString(msg) + `</code></pre>`
}

// ---- CSS de tokens ------------------------------------------------------
var (
	chromaCSSOnce sync.Once
	chromaCSS     string
)

// ChromaCSS devuelve (y cachea) el CSS de los tokens para el estilo Cipher.
func ChromaCSS() string {
	chromaCSSOnce.Do(func() {
		var b bytes.Buffer
		_ = cssFormatter.WriteCSS(&b, cipherStyle)
		chromaCSS = b.String()
	})
	return chromaCSS
}

// cipherStyle: código en arcoíris PASTEL ("Prism" suave) sobre el cromo Onyx negro. Matices
// desaturados y calmos sobre #090909 (familia Catppuccin), repartidos para que ningún lenguaje
// quede dominado por un solo color: malva=keywords/tags, celeste=funciones, crema=tipos/clases/
// atributos, salvia=strings, durazno=números/constantes, cielo=operadores/labels, menta=builtins,
// rosa=escapes/decoradores/preproc, rojo suave=errores; nombres casi blancos, puntuación y
// comentarios en gris neutro.
var cipherStyle = chroma.MustNewStyle("cipher", chroma.StyleEntries{
	chroma.Background:            "#d8d8d8 bg:#090909",
	chroma.LineHighlight:         "bg:#161616",
	chroma.LineNumbers:           "#3a3a3a",
	chroma.LineNumbersTable:      "#3a3a3a",
	chroma.Comment:               "italic #666666",
	chroma.CommentHashbang:       "italic #666666",
	chroma.CommentMultiline:      "italic #666666",
	chroma.CommentPreproc:        "#f5c2e7",
	chroma.Keyword:               "#cba6f7",
	chroma.KeywordConstant:       "#fab387",
	chroma.KeywordDeclaration:    "#cba6f7",
	chroma.KeywordNamespace:      "#cba6f7",
	chroma.KeywordType:           "#f9e2af",
	chroma.Operator:              "#89dceb",
	chroma.OperatorWord:          "#cba6f7",
	chroma.Punctuation:           "#8a8a8a",
	chroma.Name:                  "#d8d8d8",
	chroma.NameAttribute:         "#f9e2af",
	chroma.NameBuiltin:           "#94e2d5",
	chroma.NameBuiltinPseudo:     "#94e2d5",
	chroma.NameClass:             "#f9e2af",
	chroma.NameConstant:          "#fab387",
	chroma.NameDecorator:         "#f5c2e7",
	chroma.NameException:         "#f38ba8",
	chroma.NameFunction:          "#89b4fa",
	chroma.NameLabel:             "#89dceb",
	chroma.NameNamespace:         "#d8d8d8",
	chroma.NameTag:               "#cba6f7",
	chroma.NameVariable:          "#d8d8d8",
	chroma.NameVariableInstance:  "#eba0ac",
	chroma.LiteralString:         "#a6e3a1",
	chroma.LiteralStringEscape:   "#f5c2e7",
	chroma.LiteralStringInterpol: "#f5c2e7",
	chroma.LiteralStringRegex:    "#f5c2e7",
	chroma.LiteralStringSymbol:   "#a6e3a1",
	chroma.LiteralNumber:         "#fab387",
	chroma.GenericHeading:        "#b4befe",
	chroma.GenericSubheading:     "#89dceb",
	chroma.GenericDeleted:        "#f38ba8 bg:#1c1013",
	chroma.GenericInserted:       "#a6e3a1 bg:#101a12",
	chroma.GenericEmph:           "italic",
	chroma.GenericStrong:         "#efefef",
	chroma.Error:                 "#f38ba8",
})

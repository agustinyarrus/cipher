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

import (
	"bytes"
	"sort"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
)

// maxRenderBytes: tope de tamaño que resaltamos. Más allá, chroma se vuelve lento y la vista pesada;
// truncamos y avisamos. Los archivos de código real rara vez se acercan a esto.
const maxRenderBytes = 12 << 20 // 12 MiB

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
	Lang       string `json:"lang"`       // nombre legible del lenguaje (p.ej. "Go", "Java")
	Lines      int    `json:"lines"`      // cantidad de líneas
	Bytes      int    `json:"bytes"`      // tamaño del texto mostrado
	Decompiled bool   `json:"decompiled"` // true si el contenido salió de un decompilador
	Tool       string `json:"tool"`       // herramienta de decompilación usada (CFR, javap, …)
	Binary     bool   `json:"binary"`     // true si el archivo es binario y no se pudo mostrar
	Truncated  bool   `json:"truncated"`  // true si se recortó por tamaño
	CRLF       bool   `json:"crlf"`       // saltos de línea CRLF (Windows) vs LF
}

// ---- formateador chroma -------------------------------------------------
// Números de línea INLINE (no en tabla): cada línea sale como <span class="line"> con su <span
// class="ln">N</span>. Así el cliente puede (a) fijar el gutter con position:sticky, (b) activar
// word-wrap por CSS sin desalinear los números (cada número vive en su línea), y (c) excluir .ln
// de la selección/copia y de la búsqueda. TabWidth 4 para que las tabs se vean parejas.
var codeFormatter = chromahtml.New(
	chromahtml.WithClasses(true),
	chromahtml.WithLineNumbers(true),
	chromahtml.LineNumbersInTable(false),
	chromahtml.TabWidth(4),
)

// RenderFile lee la fuente (ya provista en src) y devuelve el HTML resaltado + metadatos. Decide
// solo si decompilar, si es binario o si resaltar como texto.
func RenderFile(path, name string, src []byte) (RenderResult, error) {
	// 1) ¿extensión decompilable? -> capa de decompilación (usa la ruta en disco, no src)
	if dec := decompilerFor(path); dec != nil {
		code, tool, err := dec.run(path)
		if err != nil {
			return RenderResult{
				HTML:       errorBlock(err.Error()),
				Lang:       dec.lang,
				Decompiled: true,
				Tool:       dec.tool,
			}, nil
		}
		res := highlight(code, dec.langHint)
		res.Decompiled = true
		res.Tool = tool
		if res.Lang == "" || strings.EqualFold(res.Lang, "plaintext") {
			res.Lang = dec.lang
		}
		return res, nil
	}

	// 2) archivo de texto / binario
	if isBinary(src) {
		return RenderResult{Binary: true, Bytes: len(src)}, nil
	}
	truncated := false
	if len(src) > maxRenderBytes {
		src = src[:maxRenderBytes]
		truncated = true
	}
	res := highlight(string(src), name)
	res.Truncated = truncated
	res.CRLF = bytes.Contains(src, []byte("\r\n"))
	return res, nil
}

// RenderText resalta texto crudo (arrastrar-y-soltar, sin ruta en disco). El nombre da la pista
// de lenguaje.
func RenderText(src []byte, name string) (RenderResult, error) {
	if isBinary(src) {
		return RenderResult{Binary: true, Bytes: len(src)}, nil
	}
	truncated := false
	if len(src) > maxRenderBytes {
		src = src[:maxRenderBytes]
		truncated = true
	}
	res := highlight(string(src), name)
	res.Truncated = truncated
	res.CRLF = bytes.Contains(src, []byte("\r\n"))
	return res, nil
}

// highlight tokeniza code y devuelve el HTML resaltado + lenguaje + cantidad de líneas.
// langHint puede ser un nombre de archivo (para Match por extensión) o un nombre de lenguaje.
func highlight(code, langHint string) RenderResult {
	// normalizar saltos: chroma trabaja con \n; mostramos LF (el cliente sabe si el original era CRLF).
	code = strings.ReplaceAll(code, "\r\n", "\n")
	code = strings.ReplaceAll(code, "\r", "\n")

	lexer := pickLexer(code, langHint)
	langName := lexer.Config().Name

	lexer = chroma.Coalesce(lexer)
	var buf bytes.Buffer
	it, err := lexer.Tokenise(nil, code)
	if err == nil {
		err = codeFormatter.Format(&buf, cipherStyle, it)
	}

	var out string
	if err == nil {
		// Quitar los \n: en modo line-numbers chroma ya parte cada línea en su <span class="line">
		// y el único \n de cada una es su terminador. Sacándolos, el cliente puede mostrar las líneas
		// como filas flex (gutter sticky + word-wrap por columna) sin doble salto ni números corridos.
		out = strings.ReplaceAll(buf.String(), "\n", "")
	} else {
		// fallback crudo: texto escapado en un <pre> plano (sin gutter)
		out = `<pre class="chroma cipher-plain"><code>` + htmlEscape(code) + `</code></pre>`
	}

	lines := strings.Count(code, "\n")
	if len(code) > 0 && !strings.HasSuffix(code, "\n") {
		lines++
	}
	return RenderResult{
		HTML:  out,
		Lang:  langName,
		Lines: lines,
		Bytes: len(code),
	}
}

// pickLexer elige el lexer: primero por nombre de archivo / lenguaje explícito, después por análisis
// del contenido, y por último el genérico (texto plano).
func pickLexer(code, hint string) chroma.Lexer {
	if hint != "" {
		if l := lexers.Match(hint); l != nil { // por nombre de archivo (glob: *.go, Dockerfile, …)
			return l
		}
		if l := lexers.Get(hint); l != nil { // por nombre/alias de lenguaje (go, java, python, …)
			return l
		}
	}
	if l := lexers.Analyse(code); l != nil { // heurística sobre el contenido
		return l
	}
	return lexers.Fallback // texto plano
}

// isBinary: heurística clásica (la de git) — si hay un byte NUL en los primeros 8 KiB, es binario.
func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// errorBlock arma un <pre> con un mensaje (p.ej. cuando falla la decompilación).
func errorBlock(msg string) string {
	return `<pre class="chroma cipher-msg"><code>` + htmlEscape(msg) + `</code></pre>`
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
		_ = codeFormatter.WriteCSS(&b, cipherStyle)
		chromaCSS = b.String()
	})
	return chromaCSS
}

// cipherStyle: paleta Onyx — monocromo sobre negro puro. La jerarquía la da el BRILLO, no el color:
// keywords/tags en blanco puro, funciones/clases casi blanco, tipos/builtins/números un paso abajo,
// nombres en gris claro, strings receden, operadores/puntuación se apagan, comentarios en sombra.
// Color sólo funcional: rojo mudo (error) y verde mudo (diff insertado).
var cipherStyle = chroma.MustNewStyle("cipher", chroma.StyleEntries{
	chroma.Background:            "#cfcfcf bg:#090909",
	chroma.LineHighlight:         "bg:#161616",
	chroma.LineNumbers:           "#3a3a3a",
	chroma.LineNumbersTable:      "#3a3a3a",
	chroma.Comment:               "italic #565656",
	chroma.CommentHashbang:       "italic #565656",
	chroma.CommentMultiline:      "italic #565656",
	chroma.CommentPreproc:        "#e8e8e8",
	chroma.Keyword:               "#ffffff",
	chroma.KeywordConstant:       "#eaeaea",
	chroma.KeywordDeclaration:    "#ffffff",
	chroma.KeywordNamespace:      "#ffffff",
	chroma.KeywordType:           "#dedede",
	chroma.Operator:              "#8a8a8a",
	chroma.OperatorWord:          "#ffffff",
	chroma.Punctuation:           "#787878",
	chroma.Name:                  "#cfcfcf",
	chroma.NameAttribute:         "#b5b5b5",
	chroma.NameBuiltin:           "#dedede",
	chroma.NameBuiltinPseudo:     "#dedede",
	chroma.NameClass:             "#f5f5f5",
	chroma.NameConstant:          "#eaeaea",
	chroma.NameDecorator:         "#b5b5b5",
	chroma.NameException:         "#ffffff",
	chroma.NameFunction:          "#f5f5f5",
	chroma.NameLabel:             "#dedede",
	chroma.NameNamespace:         "#cfcfcf",
	chroma.NameTag:               "#ffffff",
	chroma.NameVariable:          "#cfcfcf",
	chroma.NameVariableInstance:  "#dedede",
	chroma.LiteralString:         "#a3a3a3",
	chroma.LiteralStringEscape:   "#d8d8d8",
	chroma.LiteralStringInterpol: "#d8d8d8",
	chroma.LiteralStringRegex:    "#b5b5b5",
	chroma.LiteralStringSymbol:   "#a3a3a3",
	chroma.LiteralNumber:         "#eaeaea",
	chroma.GenericHeading:        "bold #ffffff",
	chroma.GenericSubheading:     "bold #e8e8e8",
	chroma.GenericDeleted:        "#e5484d bg:#1c0f10",
	chroma.GenericInserted:       "#58a76b bg:#0e1a12",
	chroma.GenericEmph:           "italic",
	chroma.GenericStrong:         "bold",
	chroma.Error:                 "#e5484d",
})

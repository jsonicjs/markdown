/* Copyright (c) 2021-2025 Richard Rodger, MIT License */

// Package markdown is a jsonic plugin that parses markdown text into a
// structured array of block nodes (heading, paragraph, code, list,
// blockquote, hr). It mirrors the TypeScript src/markdown.ts implementation.
package markdown

import (
	"fmt"
	"regexp"
	"strings"

	jsonic "github.com/jsonicjs/jsonic/go"
)

const Version = "0.1.0"

// --- BEGIN EMBEDDED markdown-grammar.jsonic ---
const grammarText = `
# Markdown Grammar Definition
# Parsed by a standard Jsonic instance and passed to jsonic.grammar()
# Function references (@ prefixed) are resolved against the refs map
#
# The custom line-scanning lexer (see buildMarkdownLineMatcher in src/markdown.ts)
# emits one token per markdown line, classified by block type:
#
#   #MH - heading line       val: { level, text }
#   #MR - horizontal rule    val: null
#   #MC - fenced code block  val: { lang, text }
#   #ML - list item line     val: { ordered, text }
#   #MQ - blockquote line    val: string
#   #MT - plain text line    val: string
#   #MB - blank line         val: null
#   #ZZ - end of input       (built-in)
#
# The grammar then groups adjacent same-type tokens into block structures:
#   consecutive #MT  -> paragraph (lines joined with \n)
#   consecutive #ML  -> list      (items accumulated)
#   consecutive #MQ  -> blockquote (lines joined with \n)
# Standalone block tokens (#MH, #MR, #MC) form single blocks.

{
  rule: md: open: [
    { s: '#ZZ' g: 'md,empty' }
    { p: blocks g: 'md,content' }
  ]

  rule: blocks: open: [
    { s: '#ZZ' g: 'md,blocks,end' }
    { p: block g: 'md,blocks,push' }
  ]
  rule: blocks: close: [
    { s: '#ZZ' g: 'md,blocks,end' }
    { r: blocks g: 'md,blocks,more' }
  ]

  rule: block: open: [
    { s: '#MB'  g: 'md,blank' }
    { s: '#MH'  a: '@heading'    g: 'md,heading' }
    { s: '#MR'  a: '@hr'         g: 'md,hr' }
    { s: '#MC'  a: '@code'       g: 'md,code' }
    { s: '#MIC' a: '@icode-start' p: icode-tail g: 'md,icode' }
    { s: '#ML'  a: '@list-start'  p: list-tail  g: 'md,list' }
    { s: '#MQ'  a: '@quote-start' p: quote-tail g: 'md,quote' }
    { s: '#MT'  a: '@para-start'  p: para-tail  g: 'md,para' }
    { s: '#MSX' g: 'md,setext,stray' }
  ]

  rule: para-tail: open: [
    { s: '#MSX' a: '@setext-promote' g: 'md,setext,close' }
    { s: '#MT'  a: '@para-append' r: para-tail g: 'md,para,more' }
    { g: 'md,para,end' }
  ]

  rule: list-tail: open: [
    { s: '#ML' a: '@list-append' r: list-tail g: 'md,list,more' }
    { g: 'md,list,end' }
  ]

  rule: quote-tail: open: [
    { s: '#MQ' a: '@quote-append' r: quote-tail g: 'md,quote,more' }
    { g: 'md,quote,end' }
  ]

  rule: icode-tail: open: [
    { s: '#MIC' a: '@icode-append' r: icode-tail g: 'md,icode,more' }
    { g: 'md,icode,end' }
  ]
}
`
// --- END EMBEDDED markdown-grammar.jsonic ---

// Markdown is a jsonic plugin that adds markdown parsing support.
// Options are pre-merged with Defaults by jsonic.UseDefaults.
func Markdown(j *jsonic.Jsonic, options map[string]any) error {
	// Guard against re-invocation: SetOptions calls re-run plugins.
	if j.Decoration("markdown-init") != nil {
		return nil
	}
	j.Decorate("markdown-init", true)

	codeFence, _ := options["codeFence"].(string)
	if codeFence == "" {
		codeFence = "```"
	}
	trim, _ := options["trim"].(bool)
	emitHTML, _ := options["html"].(bool)

	falseVal := false

	// Configure jsonic: disable built-in lexers and install the markdown
	// line matcher with low priority so it runs before all built-ins.
	j.SetOptions(jsonic.Options{
		Rule: &jsonic.RuleOptions{
			Start:   "md",
			Exclude: "jsonic,imp",
		},
		Number:  &jsonic.NumberOptions{Lex: &falseVal},
		Value:   &jsonic.ValueOptions{Lex: &falseVal},
		Comment: &jsonic.CommentOptions{Lex: &falseVal},
		String:  &jsonic.StringOptions{Lex: &falseVal},
		Line:    &jsonic.LineOptions{Lex: &falseVal},
		Space:   &jsonic.SpaceOptions{Lex: &falseVal},
		Text:    &jsonic.TextOptions{Lex: &falseVal},
		Lex: &jsonic.LexOptions{
			EmptyResult: []any{},
			Match: map[string]*jsonic.MatchSpec{
				"markdown": {Order: 1, Make: buildMarkdownLineMatcher(codeFence)},
			},
		},
	})

	// Pre-register custom token names so the grammar parser can resolve them.
	j.Token("#MH")
	j.Token("#MR")
	j.Token("#MC")
	j.Token("#ML")
	j.Token("#MQ")
	j.Token("#MT")
	j.Token("#MB")
	j.Token("#MSX")
	j.Token("#MIC")

	// Named function references for declarative grammar definition.
	refs := map[jsonic.FuncRef]any{

		// Initialize the result array at the top of parsing.
		"@md-bo": jsonic.StateAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.Node = make([]any, 0)
		}),

		"@heading": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			block := map[string]any{
				"type":  "heading",
				"level": v["level"],
				"text":  v["text"],
			}
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
		}),

		"@hr": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			block := map[string]any{"type": "hr"}
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
		}),

		"@code": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			block := map[string]any{
				"type": "code",
				"lang": v["lang"],
				"text": v["text"],
			}
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
		}),

		"@list-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			block := map[string]any{
				"type":    "list",
				"ordered": v["ordered"],
				"items":   []any{map[string]any{"text": v["text"]}},
			}
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
			ensureMeta(ctx)["mdCurrent"] = block
		}),

		"@list-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			items, _ := cur["items"].([]any)
			cur["items"] = append(items, map[string]any{"text": v["text"]})
			if emitHTML {
				cur["html"] = RenderHTML(cur)
			}
		}),

		"@quote-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			block := map[string]any{"type": "blockquote", "text": v}
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
			ensureMeta(ctx)["mdCurrent"] = block
		}),

		"@quote-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			text, _ := cur["text"].(string)
			cur["text"] = text + "\n" + v
			if emitHTML {
				cur["html"] = RenderHTML(cur)
			}
		}),

		"@para-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			if trim {
				v = strings.TrimSpace(v)
			}
			block := map[string]any{"type": "paragraph", "text": v}
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
			ensureMeta(ctx)["mdCurrent"] = block
		}),

		"@para-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			if trim {
				v = strings.TrimSpace(v)
			}
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			text, _ := cur["text"].(string)
			cur["text"] = text + "\n" + v
			if emitHTML {
				cur["html"] = RenderHTML(cur)
			}
		}),

		// Setext underline encountered while accumulating a paragraph:
		// rewrite the current block in place as a heading.
		"@setext-promote": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			cur["type"] = "heading"
			cur["level"] = v["level"]
			if text, ok := cur["text"].(string); ok {
				cur["text"] = strings.TrimSpace(text)
			}
			if emitHTML {
				cur["html"] = RenderHTML(cur)
			}
		}),

		"@icode-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			block := map[string]any{"type": "code", "lang": "", "text": v}
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
			ensureMeta(ctx)["mdCurrent"] = block
		}),

		"@icode-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			text, _ := cur["text"].(string)
			cur["text"] = text + "\n" + v
			if emitHTML {
				cur["html"] = RenderHTML(cur)
			}
		}),
	}

	// Parse embedded grammar definition using a separate standard Jsonic instance,
	// attach the refs map, and register with the current instance.
	gs, err := parseGrammarText(grammarText, refs)
	if err != nil {
		return err
	}
	if err := j.Grammar(gs); err != nil {
		return fmt.Errorf("failed to apply markdown grammar: %w", err)
	}

	return nil
}

// pushBlock appends a block onto the result array carried by the rule node
// chain and propagates the new slice header up to all ancestors that share it.
// Go slices need an explicit re-assign because append may relocate the
// backing array.
func pushBlock(r *jsonic.Rule, block map[string]any) {
	arr, ok := r.Node.([]any)
	if !ok {
		arr = []any{}
	}
	arr = append(arr, block)
	r.Node = arr
	// Propagate updated slice header up the rule stack so the root sees it.
	for p := r.Parent; p != nil && p != jsonic.NoRule; p = p.Parent {
		if _, ok := p.Node.([]any); ok {
			p.Node = arr
		}
	}
}

func ensureMeta(ctx *jsonic.Context) map[string]any {
	if ctx.Meta == nil {
		ctx.Meta = make(map[string]any)
	}
	return ctx.Meta
}

// Pre-compiled line classification regexes.
var (
	reBlank      = regexp.MustCompile(`^[ \t]*$`)
	reATX        = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	reSetext     = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	reOrdered    = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)
	reUnordered  = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	reBlockquote = regexp.MustCompile(`^\s*>\s?(.*)$`)
)

// stripIndent removes up to 3 leading spaces from a line. CommonMark allows
// that much indentation on most block-level constructs before they count as
// indented code.
func stripIndent(s string) string {
	i := 0
	for i < 3 && i < len(s) && s[i] == ' ' {
		i++
	}
	return s[i:]
}

// isHorizontalRule reports whether s is a markdown horizontal rule line:
// at most 3 leading spaces/tabs, then 3+ of the same `-`, `*`, or `_`
// character (with optional spaces/tabs between), and only spaces/tabs after.
// Go's RE2 lacks backreferences so this is checked imperatively.
func isHorizontalRule(s string) bool {
	leading := 0
	for leading < len(s) && (s[leading] == ' ' || s[leading] == '\t') {
		leading++
	}
	if leading > 3 || leading == len(s) {
		return false
	}
	c := s[leading]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	count := 0
	for i := leading; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t':
			// skip
		case c:
			count++
		default:
			return false
		}
	}
	return count >= 3
}

// lexState is the per-parse per-lexer state used for context-sensitive
// classification (indented code vs. paragraph continuation, setext
// recognition). A fresh map is keyed by *jsonic.Lex pointer so state
// resets on each new parse.
type lexState struct {
	last string // last emitted token kind
}

var lexStates = map[*jsonic.Lex]*lexState{}

// buildMarkdownLineMatcher returns a custom lexer matcher that emits one
// token per markdown line. Fenced code blocks are consumed in full and
// emitted as a single #MC token.
func buildMarkdownLineMatcher(fence string) jsonic.MakeLexMatcher {
	return func(cfg *jsonic.LexConfig, opts *jsonic.Options) jsonic.LexMatcher {
		return func(lex *jsonic.Lex, rule *jsonic.Rule) *jsonic.Token {
			pnt := lex.Cursor()
			src := lex.Src
			sI := pnt.SI
			rI := pnt.RI
			cI := pnt.CI
			srclen := len(src)

			// No input left: let jsonic emit #ZZ.
			if sI >= srclen {
				delete(lexStates, lex)
				return nil
			}

			// Per-parse state keyed on the lex instance.
			state, ok := lexStates[lex]
			if !ok {
				state = &lexState{last: "start"}
				lexStates[lex] = state
			}

			// Read the current line (exclusive of trailing \n).
			lineEnd := sI
			for lineEnd < srclen && src[lineEnd] != '\n' {
				lineEnd++
			}
			consumeEnd := lineEnd
			if lineEnd < srclen {
				consumeEnd = lineEnd + 1
			}

			lineContent := src[sI:lineEnd]
			if strings.HasSuffix(lineContent, "\r") {
				lineContent = lineContent[:len(lineContent)-1]
			}

			var tkn *jsonic.Token
			kind := "text"

			switch {
			// Blank line.
			case reBlank.MatchString(lineContent):
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MB", tinFor(lex, "#MB"), nil, srcPart)
				kind = "blank"

			// Fenced code block.
			case strings.HasPrefix(stripIndent(lineContent), fence):
				stripped := stripIndent(lineContent)
				lang := strings.TrimSpace(stripped[len(fence):])
				codeLines := []string{}
				codeEnd := consumeEnd

				for codeEnd < srclen {
					innerEnd := codeEnd
					for innerEnd < srclen && src[innerEnd] != '\n' {
						innerEnd++
					}
					innerLine := src[codeEnd:innerEnd]
					if strings.HasSuffix(innerLine, "\r") {
						innerLine = innerLine[:len(innerLine)-1]
					}
					nextEnd := innerEnd
					if innerEnd < srclen {
						nextEnd = innerEnd + 1
					}
					if strings.HasPrefix(stripIndent(innerLine), fence) {
						codeEnd = nextEnd
						break
					}
					codeLines = append(codeLines, innerLine)
					codeEnd = nextEnd
				}

				codeText := strings.Join(codeLines, "\n")
				consumeEnd = codeEnd
				srcPart := src[sI:consumeEnd]
				val := map[string]any{"lang": lang, "text": codeText}
				tkn = lex.Token("#MC", tinFor(lex, "#MC"), val, srcPart)
				kind = "code"

			// ATX heading: 0-3 leading spaces, 1-6 `#`, optional space+text,
			// optional trailing `#` sequence preceded by whitespace.
			case reATX.MatchString(lineContent):
				m := reATX.FindStringSubmatch(lineContent)
				text := strings.TrimSpace(m[2])
				val := map[string]any{"level": len(m[1]), "text": text}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MH", tinFor(lex, "#MH"), val, srcPart)
				kind = "heading"

			// Setext underline: only valid immediately after a paragraph line.
			case state.last == "text" && reSetext.MatchString(lineContent):
				trimmed := strings.TrimLeft(lineContent, " \t")
				level := 2
				if len(trimmed) > 0 && trimmed[0] == '=' {
					level = 1
				}
				val := map[string]any{"level": level}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MSX", tinFor(lex, "#MSX"), val, srcPart)
				kind = "setext"

			// Horizontal rule.
			case isHorizontalRule(lineContent):
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MR", tinFor(lex, "#MR"), nil, srcPart)
				kind = "hr"

			// Ordered list item.
			case reOrdered.MatchString(lineContent):
				m := reOrdered.FindStringSubmatch(lineContent)
				val := map[string]any{"ordered": true, "text": m[2]}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#ML", tinFor(lex, "#ML"), val, srcPart)
				kind = "list"

			// Unordered list item.
			case reUnordered.MatchString(lineContent):
				m := reUnordered.FindStringSubmatch(lineContent)
				val := map[string]any{"ordered": false, "text": m[1]}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#ML", tinFor(lex, "#ML"), val, srcPart)
				kind = "list"

			// Blockquote line.
			case reBlockquote.MatchString(lineContent):
				m := reBlockquote.FindStringSubmatch(lineContent)
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MQ", tinFor(lex, "#MQ"), m[1], srcPart)
				kind = "quote"

			// Indented line (4+ spaces or tab): code block unless it
			// continues a paragraph.
			case strings.HasPrefix(lineContent, "    ") || strings.HasPrefix(lineContent, "\t"):
				if state.last == "text" {
					srcPart := src[sI:consumeEnd]
					tkn = lex.Token("#MT", tinFor(lex, "#MT"), lineContent, srcPart)
					kind = "text"
				} else {
					var stripped string
					if strings.HasPrefix(lineContent, "\t") {
						stripped = lineContent[1:]
					} else {
						stripped = lineContent[4:]
					}
					srcPart := src[sI:consumeEnd]
					tkn = lex.Token("#MIC", tinFor(lex, "#MIC"), stripped, srcPart)
					kind = "icode"
				}

			// Plain text line.
			default:
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MT", tinFor(lex, "#MT"), lineContent, srcPart)
				kind = "text"
			}

			state.last = kind

			// Advance the lex cursor past the consumed span, tracking row/column.
			for i := sI; i < consumeEnd; i++ {
				if src[i] == '\n' {
					rI++
					cI = 1
				} else {
					cI++
				}
			}
			pnt.SI = consumeEnd
			pnt.RI = rI
			pnt.CI = cI

			return tkn
		}
	}
}

// tinFor resolves a custom token Tin from its name via the lexer config.
// Returns 0 if not registered (the token will still be created with the name).
func tinFor(lex *jsonic.Lex, name string) jsonic.Tin {
	if lex.Config.TinNames == nil {
		return 0
	}
	for tin, n := range lex.Config.TinNames {
		if n == name {
			return tin
		}
	}
	return 0
}

// Defaults matches the TS Markdown.defaults. Used with jsonic.UseDefaults.
var Defaults = map[string]any{
	"codeFence": "```",
	"trim":      false,
	"html":      false,
}

// RenderHTML produces a CommonMark-style HTML fragment for a single block.
// Block text is routed through renderInline so backslash escapes, entity
// references, and hard line breaks are handled. Inline emphasis, links,
// code spans, and autolinks are not yet implemented.
func RenderHTML(block map[string]any) string {
	switch block["type"] {
	case "heading":
		level, _ := block["level"].(int)
		text, _ := block["text"].(string)
		return fmt.Sprintf("<h%d>%s</h%d>", level, renderInline(text), level)

	case "paragraph":
		text, _ := block["text"].(string)
		return "<p>" + renderInline(text) + "</p>"

	case "hr":
		return "<hr />"

	case "code":
		text, _ := block["text"].(string)
		lang, _ := block["lang"].(string)
		cls := ""
		if lang != "" {
			cls = ` class="language-` + escapeHTML(lang) + `"`
		}
		trailing := "\n"
		if text == "" || strings.HasSuffix(text, "\n") {
			trailing = ""
		}
		return "<pre><code" + cls + ">" + escapeHTML(text) + trailing + "</code></pre>"

	case "list":
		tag := "ul"
		if ordered, _ := block["ordered"].(bool); ordered {
			tag = "ol"
		}
		items, _ := block["items"].([]any)
		var b strings.Builder
		b.WriteString("<")
		b.WriteString(tag)
		b.WriteString(">\n")
		for i, it := range items {
			if i > 0 {
				b.WriteByte('\n')
			}
			m, _ := it.(map[string]any)
			text, _ := m["text"].(string)
			b.WriteString("<li>")
			b.WriteString(renderInline(text))
			b.WriteString("</li>")
		}
		b.WriteString("\n</")
		b.WriteString(tag)
		b.WriteString(">")
		return b.String()

	case "blockquote":
		text, _ := block["text"].(string)
		return "<blockquote>\n<p>" + renderInline(text) + "</p>\n</blockquote>"
	}
	return ""
}

// ASCII punctuation set recognized as a backslash escape target per
// CommonMark (§ 6.1).
const backslashEscapable = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// Named HTML entities recognized by the inline parser. A small common
// subset rather than the full HTML5 named entity table; numeric references
// are always decoded by decodeEntity.
var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'",
	"nbsp": "\u00A0", "copy": "\u00A9", "reg": "\u00AE", "trade": "\u2122",
	"hellip": "\u2026", "mdash": "\u2014", "ndash": "\u2013",
	"lsquo": "\u2018", "rsquo": "\u2019", "ldquo": "\u201C", "rdquo": "\u201D",
	"laquo": "\u00AB", "raquo": "\u00BB", "para": "\u00B6", "sect": "\u00A7",
	"middot": "\u00B7", "bull": "\u2022", "deg": "\u00B0", "plusmn": "\u00B1",
	"times": "\u00D7", "divide": "\u00F7", "pound": "\u00A3", "euro": "\u20AC",
	"yen": "\u00A5", "cent": "\u00A2",
	"Auml": "\u00C4", "Ouml": "\u00D6", "Uuml": "\u00DC",
	"auml": "\u00E4", "ouml": "\u00F6", "uuml": "\u00FC", "szlig": "\u00DF",
	"agrave": "\u00E0", "eacute": "\u00E9", "egrave": "\u00E8",
	"aring": "\u00E5", "oslash": "\u00F8", "AElig": "\u00C6", "aelig": "\u00E6",
	"frac12": "\u00BD", "frac14": "\u00BC", "frac34": "\u00BE",
	"iexcl": "\u00A1", "iquest": "\u00BF",
}

var reEntityRef = regexp.MustCompile(
	`^&(#[xX][0-9a-fA-F]{1,6};|#[0-9]{1,7};|[a-zA-Z][a-zA-Z0-9]{1,31};)`,
)

// Inline autolink / raw HTML recognition. Patterns match the subset of
// CommonMark §6.6–6.7 that does not require backreferences (unavailable in
// RE2) and keeps regexes tractable.
var (
	reAutolinkURI = regexp.MustCompile(
		`^<([a-zA-Z][a-zA-Z0-9.+-]{1,31}:[^\s<>\x00-\x1f\x7f]*)>`,
	)
	reAutolinkEmail = regexp.MustCompile(
		`^<([a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*)>`,
	)
	reHTMLComment = regexp.MustCompile(`^<!--(?s:.)*?-->`)
	reHTMLPI      = regexp.MustCompile(`^<\?(?s:.)*?\?>`)
	reHTMLCDATA   = regexp.MustCompile(`^<!\[CDATA\[(?s:.)*?\]\]>`)
	reHTMLDecl    = regexp.MustCompile(`^<![A-Z][^>]*>`)
	reHTMLOpenTag = regexp.MustCompile(
		`^<[a-zA-Z][a-zA-Z0-9-]*(?:\s+[a-zA-Z_:][a-zA-Z0-9_.:-]*(?:\s*=\s*(?:[^\s"'=<>` + "`" + `]+|'[^']*'|"[^"]*"))?)*\s*/?>`,
	)
	reHTMLCloseTag = regexp.MustCompile(`^</[a-zA-Z][a-zA-Z0-9-]*\s*>`)
)

// decodeEntity returns the Unicode string for a full entity reference
// (including the leading & and trailing ;), or empty string + ok=false
// if the reference is not recognized. Invalid or zero-code-point numeric
// references return U+FFFD.
func decodeEntity(ref string) (string, bool) {
	if len(ref) < 3 || ref[0] != '&' || ref[len(ref)-1] != ';' {
		return "", false
	}
	body := ref[1 : len(ref)-1]
	if len(body) > 1 && body[0] == '#' && (body[1] == 'x' || body[1] == 'X') {
		var n int64
		fmt.Sscanf(body[2:], "%x", &n)
		if n == 0 || n > 0x10ffff {
			return "\uFFFD", true
		}
		return string(rune(n)), true
	}
	if len(body) > 0 && body[0] == '#' {
		var n int64
		fmt.Sscanf(body[1:], "%d", &n)
		if n == 0 || n > 0x10ffff {
			return "\uFFFD", true
		}
		return string(rune(n)), true
	}
	if v, ok := namedEntities[body]; ok {
		return v, true
	}
	return "", false
}

// inlineSegKind enumerates the kinds of segments produced by the inline
// tokenizer.
type inlineSegKind int

const (
	segText inlineSegKind = iota
	segHTML
	segDelim
	segBracket
)

// inlineSeg is a segment produced by the inline tokenizer. Text segments
// need HTML-escaping at render time; HTML segments are already-safe HTML
// atoms (code span output, decoded entities, escape output, hard breaks);
// delim segments are `*`/`_` runs that the emphasis pass may consume;
// bracket segments are `[`, `![`, or `]` markers consumed by the link pass.
type inlineSeg struct {
	kind     inlineSegKind
	value    string
	char     byte
	length   int
	canOpen  bool
	canClose bool
	// Bracket-specific:
	open   bool
	image  bool
	active bool
	url    string
	title  string
	hasURL bool
}

// asciiPunct reports whether b is an ASCII punctuation character used by
// CommonMark's flanking classification (approximation; the full spec uses
// Unicode punctuation).
func asciiPunct(b byte) bool {
	return (b >= '!' && b <= '/') ||
		(b >= ':' && b <= '@') ||
		(b >= '[' && b <= '`') ||
		(b >= '{' && b <= '~')
}

func isInlineWS(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// parseLinkTarget parses `(URL[ "TITLE"])` starting at position i in s and
// returns (url, title, end, true) on success.
func parseLinkTarget(s string, i int) (string, string, int, bool) {
	if i >= len(s) || s[i] != '(' {
		return "", "", 0, false
	}
	j := i + 1
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
		j++
	}

	var urlB strings.Builder
	if j < len(s) && s[j] == '<' {
		k := j + 1
		for k < len(s) && s[k] != '>' && s[k] != '<' && s[k] != '\n' {
			if s[k] == '\\' && k+1 < len(s) {
				urlB.WriteByte(s[k+1])
				k += 2
				continue
			}
			urlB.WriteByte(s[k])
			k++
		}
		if k >= len(s) || s[k] != '>' {
			return "", "", 0, false
		}
		j = k + 1
	} else {
		depth := 0
		for j < len(s) {
			c := s[j]
			if c == '\\' && j+1 < len(s) {
				urlB.WriteByte(s[j+1])
				j += 2
				continue
			}
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				break
			}
			if c == '(' {
				depth++
			} else if c == ')' {
				if depth == 0 {
					break
				}
				depth--
			} else if c < 0x20 || c == 0x7f {
				break
			}
			urlB.WriteByte(c)
			j++
		}
		if urlB.Len() == 0 && (j >= len(s) || s[j] != ')') {
			return "", "", 0, false
		}
	}

	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
		j++
	}

	var titleB strings.Builder
	if j < len(s) && (s[j] == '"' || s[j] == '\'' || s[j] == '(') {
		openQ := s[j]
		closeQ := openQ
		if openQ == '(' {
			closeQ = ')'
		}
		k := j + 1
		for k < len(s) && s[k] != closeQ {
			if s[k] == '\\' && k+1 < len(s) {
				titleB.WriteByte(s[k+1])
				k += 2
				continue
			}
			titleB.WriteByte(s[k])
			k++
		}
		if k >= len(s) || s[k] != closeQ {
			return "", "", 0, false
		}
		j = k + 1
	}

	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
		j++
	}

	if j >= len(s) || s[j] != ')' {
		return "", "", 0, false
	}
	return urlB.String(), titleB.String(), j + 1, true
}

var reLooseAmp = regexp.MustCompile(`&(?:#x?[0-9a-fA-F]+;|[a-zA-Z][a-zA-Z0-9]*;)`)

// encodeLinkUrl applies minimal URL normalization for href/src attributes.
func encodeLinkUrl(url string) string {
	// Preserve well-formed entities; escape bare `&`.
	// Simple pass: replace `&` not part of an entity with `&amp;`.
	out := ""
	i := 0
	for i < len(url) {
		c := url[i]
		if c == '&' {
			m := reLooseAmp.FindStringIndex(url[i:])
			if m != nil && m[0] == 0 {
				out += url[i : i+m[1]]
				i += m[1]
				continue
			}
			out += "&amp;"
			i++
			continue
		}
		switch c {
		case '<':
			out += "&lt;"
		case '>':
			out += "&gt;"
		case '"':
			out += "%22"
		case ' ':
			out += "%20"
		default:
			out += string(c)
		}
		i++
	}
	return out
}

// innerText extracts a best-effort plain-text rendering of a segment list,
// used as alt text for images.
func innerText(segs []*inlineSeg) string {
	var b strings.Builder
	for _, s := range segs {
		switch s.kind {
		case segText:
			b.WriteString(s.value)
		case segDelim:
			b.WriteString(strings.Repeat(string(s.char), s.length))
		case segBracket:
			if s.open {
				if s.image {
					b.WriteString("![")
				} else {
					b.WriteString("[")
				}
			} else {
				b.WriteString("]")
			}
		case segHTML:
			// Strip tags, keep text content.
			stripped := s.value
			for {
				lt := strings.IndexByte(stripped, '<')
				if lt < 0 {
					b.WriteString(stripped)
					break
				}
				b.WriteString(stripped[:lt])
				gt := strings.IndexByte(stripped[lt:], '>')
				if gt < 0 {
					break
				}
				stripped = stripped[lt+gt+1:]
			}
		}
	}
	return b.String()
}

// tokenizeInline produces a flat segment list. Code spans, entity refs,
// backslash escapes, and hard breaks are resolved into HTML segments;
// plain text accumulates into text segments; runs of `*`/`_` are classified
// and emitted as delim segments; `[`, `![`, and `]` become bracket segments.
func tokenizeInline(s string) []*inlineSeg {
	segs := []*inlineSeg{}
	appendText := func(t string) {
		if len(segs) > 0 && segs[len(segs)-1].kind == segText {
			segs[len(segs)-1].value += t
			return
		}
		segs = append(segs, &inlineSeg{kind: segText, value: t})
	}

	i := 0
	n := len(s)

	for i < n {
		c := s[i]

		// Backslash escape or hard line break via backslash.
		if c == '\\' && i+1 < n {
			next := s[i+1]
			if next == '\n' {
				segs = append(segs, &inlineSeg{kind: segHTML, value: "<br />\n"})
				i += 2
				continue
			}
			if strings.IndexByte(backslashEscapable, next) >= 0 {
				segs = append(segs, &inlineSeg{kind: segHTML, value: escapeHTMLByte(next)})
				i += 2
				continue
			}
		}

		// Code span.
		if c == '`' {
			openLen := 1
			for i+openLen < n && s[i+openLen] == '`' {
				openLen++
			}
			j := i + openLen
			found := -1
			for j < n {
				if s[j] == '`' {
					closeLen := 1
					for j+closeLen < n && s[j+closeLen] == '`' {
						closeLen++
					}
					if closeLen == openLen {
						found = j
						break
					}
					j += closeLen
				} else {
					j++
				}
			}
			if found >= 0 {
				content := s[i+openLen : found]
				content = strings.ReplaceAll(content, "\r\n", " ")
				content = strings.ReplaceAll(content, "\n", " ")
				content = strings.ReplaceAll(content, "\r", " ")
				if len(content) >= 2 &&
					content[0] == ' ' &&
					content[len(content)-1] == ' ' &&
					strings.IndexFunc(content, func(r rune) bool { return r != ' ' }) >= 0 {
					content = content[1 : len(content)-1]
				}
				segs = append(segs, &inlineSeg{
					kind:  segHTML,
					value: "<code>" + escapeHTMLString(content) + "</code>",
				})
				i = found + openLen
				continue
			}
			appendText(strings.Repeat("`", openLen))
			i += openLen
			continue
		}

		// Entity or numeric character reference.
		if c == '&' {
			m := reEntityRef.FindStringIndex(s[i:])
			if m != nil {
				ref := s[i : i+m[1]]
				if decoded, ok := decodeEntity(ref); ok {
					segs = append(segs, &inlineSeg{kind: segHTML, value: escapeHTMLString(decoded)})
					i += m[1]
					continue
				}
			}
		}

		// Autolink / raw HTML. When `<` starts one of these patterns it's
		// emitted as a pre-rendered html segment; otherwise it falls
		// through to be escaped as `&lt;`.
		if c == '<' {
			rest := s[i:]

			if m := reAutolinkURI.FindStringSubmatchIndex(rest); m != nil && m[0] == 0 {
				url := rest[m[2]:m[3]]
				segs = append(segs, &inlineSeg{
					kind:  segHTML,
					value: `<a href="` + encodeLinkUrl(url) + `">` + escapeHTMLString(url) + `</a>`,
				})
				i += m[1]
				continue
			}
			if m := reAutolinkEmail.FindStringSubmatchIndex(rest); m != nil && m[0] == 0 {
				email := rest[m[2]:m[3]]
				segs = append(segs, &inlineSeg{
					kind:  segHTML,
					value: `<a href="mailto:` + encodeLinkUrl(email) + `">` + escapeHTMLString(email) + `</a>`,
				})
				i += m[1]
				continue
			}
			if m := reHTMLComment.FindStringIndex(rest); m != nil && m[0] == 0 {
				segs = append(segs, &inlineSeg{kind: segHTML, value: rest[:m[1]]})
				i += m[1]
				continue
			}
			if m := reHTMLPI.FindStringIndex(rest); m != nil && m[0] == 0 {
				segs = append(segs, &inlineSeg{kind: segHTML, value: rest[:m[1]]})
				i += m[1]
				continue
			}
			if m := reHTMLCDATA.FindStringIndex(rest); m != nil && m[0] == 0 {
				segs = append(segs, &inlineSeg{kind: segHTML, value: rest[:m[1]]})
				i += m[1]
				continue
			}
			if m := reHTMLDecl.FindStringIndex(rest); m != nil && m[0] == 0 {
				segs = append(segs, &inlineSeg{kind: segHTML, value: rest[:m[1]]})
				i += m[1]
				continue
			}
			if m := reHTMLOpenTag.FindStringIndex(rest); m != nil && m[0] == 0 {
				segs = append(segs, &inlineSeg{kind: segHTML, value: rest[:m[1]]})
				i += m[1]
				continue
			}
			if m := reHTMLCloseTag.FindStringIndex(rest); m != nil && m[0] == 0 {
				segs = append(segs, &inlineSeg{kind: segHTML, value: rest[:m[1]]})
				i += m[1]
				continue
			}
			// Fall through: literal `<`.
		}

		// Image open `![`.
		if c == '!' && i+1 < n && s[i+1] == '[' {
			segs = append(segs, &inlineSeg{
				kind:   segBracket,
				open:   true,
				image:  true,
				active: true,
			})
			i += 2
			continue
		}

		// Link open `[`.
		if c == '[' {
			segs = append(segs, &inlineSeg{
				kind:   segBracket,
				open:   true,
				image:  false,
				active: true,
			})
			i++
			continue
		}

		// Link close `]` — if followed by `(url[ "title"])`, consume and
		// stash the parsed target on the bracket segment.
		if c == ']' {
			if url, title, end, ok := parseLinkTarget(s, i+1); ok {
				segs = append(segs, &inlineSeg{
					kind:   segBracket,
					open:   false,
					active: true,
					url:    url,
					title:  title,
					hasURL: true,
				})
				i = end
			} else {
				segs = append(segs, &inlineSeg{
					kind:   segBracket,
					open:   false,
					active: true,
				})
				i++
			}
			continue
		}

		// Emphasis delimiter run.
		if c == '*' || c == '_' {
			length := 1
			for i+length < n && s[i+length] == c {
				length++
			}
			var before, after byte = ' ', ' '
			if i > 0 {
				before = s[i-1]
			}
			if i+length < n {
				after = s[i+length]
			}
			beforeWS := isInlineWS(before)
			afterWS := isInlineWS(after)
			beforeP := asciiPunct(before)
			afterP := asciiPunct(after)

			leftFlanking := !afterWS && (!afterP || beforeWS || beforeP)
			rightFlanking := !beforeWS && (!beforeP || afterWS || afterP)

			canOpen := leftFlanking
			canClose := rightFlanking
			if c == '_' {
				canOpen = leftFlanking && (!rightFlanking || beforeP)
				canClose = rightFlanking && (!leftFlanking || afterP)
			}

			segs = append(segs, &inlineSeg{
				kind:     segDelim,
				char:     c,
				length:   length,
				canOpen:  canOpen,
				canClose: canClose,
			})
			i += length
			continue
		}

		// Hard line break via 2+ trailing spaces.
		if c == '\n' {
			if len(segs) > 0 {
				last := segs[len(segs)-1]
				if last.kind == segText && strings.HasSuffix(last.value, "  ") {
					last.value = strings.TrimRight(last.value, " ")
					if last.value == "" {
						segs = segs[:len(segs)-1]
					}
					segs = append(segs, &inlineSeg{kind: segHTML, value: "<br />\n"})
					i++
					continue
				}
			}
			appendText("\n")
			i++
			continue
		}

		appendText(string(c))
		i++
	}

	return segs
}

// processLinks walks the segment list forward, matching each link/image
// closing bracket (that carries a parsed inline URL) with the most recent
// active opener, and replacing the span with a single html segment
// wrapping the rendered inner content in an <a> or <img> element.
func processLinks(segs []*inlineSeg) []*inlineSeg {
	i := 0
	for i < len(segs) {
		close := segs[i]
		if close.kind != segBracket || close.open || !close.hasURL {
			i++
			continue
		}

		openIdx := -1
		for j := i - 1; j >= 0; j-- {
			op := segs[j]
			if op.kind == segBracket && op.open && op.active {
				openIdx = j
				break
			}
		}
		if openIdx < 0 {
			i++
			continue
		}

		op := segs[openIdx]
		inner := append([]*inlineSeg{}, segs[openIdx+1:i]...)
		inner = processLinks(inner)
		inner = processEmphasis(inner)

		var html string
		if op.image {
			alt := innerText(inner)
			titleAttr := ""
			if close.title != "" {
				titleAttr = ` title="` + escapeHTMLString(close.title) + `"`
			}
			html = `<img src="` + encodeLinkUrl(close.url) + `" alt="` +
				escapeHTMLString(alt) + `"` + titleAttr + " />"
		} else {
			inside := renderSegments(inner)
			titleAttr := ""
			if close.title != "" {
				titleAttr = ` title="` + escapeHTMLString(close.title) + `"`
			}
			html = `<a href="` + encodeLinkUrl(close.url) + `"` + titleAttr +
				">" + inside + "</a>"
		}

		replacement := &inlineSeg{kind: segHTML, value: html}
		tail := append([]*inlineSeg{}, segs[i+1:]...)
		segs = append(segs[:openIdx], append([]*inlineSeg{replacement}, tail...)...)

		// Deactivate all earlier link openers to prevent nested links.
		if !op.image {
			for k := 0; k < openIdx; k++ {
				s2 := segs[k]
				if s2.kind == segBracket && s2.open && !s2.image {
					s2.active = false
				}
			}
		}

		i = openIdx + 1
	}
	return segs
}

// processEmphasis walks the segment list, matching close delimiters with
// prior opening delimiters to wrap enclosed content in <em> / <strong>.
// Follows CommonMark's delimiter-stack algorithm (§ 6.4) with an ASCII
// punctuation approximation.
func processEmphasis(segs []*inlineSeg) []*inlineSeg {
	i := 0
	for i < len(segs) {
		closer := segs[i]
		if closer.kind != segDelim || !closer.canClose {
			i++
			continue
		}

		j := i - 1
		matched := -1
		for j >= 0 {
			op := segs[j]
			if op.kind == segDelim && op.canOpen && op.char == closer.char {
				bothCanOpenClose := (op.canOpen && op.canClose) || (closer.canOpen && closer.canClose)
				sum := op.length + closer.length
				if !bothCanOpenClose ||
					sum%3 != 0 ||
					(op.length%3 == 0 && closer.length%3 == 0) {
					matched = j
					break
				}
			}
			j--
		}

		if matched < 0 {
			i++
			continue
		}

		op := segs[matched]
		useStrong := op.length >= 2 && closer.length >= 2
		consume := 1
		tag := "em"
		if useStrong {
			consume = 2
			tag = "strong"
		}

		inner := append([]*inlineSeg{}, segs[matched+1:i]...)
		op.length -= consume
		closer.length -= consume

		replacement := []*inlineSeg{}
		if op.length > 0 {
			replacement = append(replacement, op)
		}
		replacement = append(replacement, &inlineSeg{kind: segHTML, value: "<" + tag + ">"})
		replacement = append(replacement, inner...)
		replacement = append(replacement, &inlineSeg{kind: segHTML, value: "</" + tag + ">"})
		if closer.length > 0 {
			replacement = append(replacement, closer)
		}

		// Splice: replace segs[matched..i] with replacement.
		tail := append([]*inlineSeg{}, segs[i+1:]...)
		segs = append(segs[:matched], append(replacement, tail...)...)

		openerRetained := 0
		if op.length > 0 {
			openerRetained = 1
		}
		i = matched + openerRetained + 1
	}
	return segs
}

// renderSegments produces the final HTML string. Leftover delim and
// bracket segments (unmatched) are rendered as their literal characters.
func renderSegments(segs []*inlineSeg) string {
	var b strings.Builder
	for _, s := range segs {
		switch s.kind {
		case segText:
			b.WriteString(escapeHTMLString(s.value))
		case segHTML:
			b.WriteString(s.value)
		case segDelim:
			b.WriteString(escapeHTMLString(strings.Repeat(string(s.char), s.length)))
		case segBracket:
			if s.open {
				if s.image {
					b.WriteString(escapeHTMLString("!["))
				} else {
					b.WriteString(escapeHTMLString("["))
				}
			} else {
				b.WriteString(escapeHTMLString("]"))
			}
		}
	}
	return b.String()
}

// renderInline processes block-level text as inline markdown. It handles:
//   - backslash escapes (\<punct> or \<newline>)
//   - entity / numeric character references
//   - hard line breaks
//   - code spans
//   - inline links `[text](url "title")` and images `![alt](url "title")`
//   - emphasis and strong (`*` / `_` delimiter runs)
// Reference-style links, autolinks, and raw HTML are not yet implemented.
func renderInline(s string) string {
	segs := tokenizeInline(s)
	segs = processLinks(segs)
	segs = processEmphasis(segs)
	return renderSegments(segs)
}

func escapeHTMLByte(c byte) string {
	switch c {
	case '&':
		return "&amp;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '"':
		return "&quot;"
	}
	return string(c)
}

func escapeHTMLString(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ToHTML concatenates per-block html fields, each followed by a newline.
// Blocks that have no html field (parser was run without html:true) are
// skipped.
func ToHTML(blocks []any) string {
	var b strings.Builder
	for _, v := range blocks {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if h, ok := m["html"].(string); ok {
			b.WriteString(h)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// parseGrammarText parses grammar text and builds a GrammarSpec with Ref support.
// (Duplicated from go/csv.go to keep the markdown package self-contained.)
func parseGrammarText(text string, refs map[jsonic.FuncRef]any) (*jsonic.GrammarSpec, error) {
	parsed, err := jsonic.Make().Parse(text)
	if err != nil {
		return nil, fmt.Errorf("failed to parse grammar text: %w", err)
	}
	parsedMap, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("grammar text did not parse to a map")
	}
	gs := &jsonic.GrammarSpec{Ref: refs}
	ruleMap, ok := parsedMap["rule"].(map[string]any)
	if !ok {
		return gs, nil
	}
	gs.Rule = make(map[string]*jsonic.GrammarRuleSpec, len(ruleMap))
	for name, rDef := range ruleMap {
		rd, ok := rDef.(map[string]any)
		if !ok {
			continue
		}
		grs := &jsonic.GrammarRuleSpec{}
		if openDef, ok := rd["open"]; ok {
			grs.Open = buildGrammarAlts(openDef)
		}
		if closeDef, ok := rd["close"]; ok {
			grs.Close = buildGrammarAlts(closeDef)
		}
		gs.Rule[name] = grs
	}
	return gs, nil
}

func buildGrammarAlts(def any) []*jsonic.GrammarAltSpec {
	arr, ok := def.([]any)
	if !ok {
		return nil
	}
	alts := make([]*jsonic.GrammarAltSpec, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			alts = append(alts, &jsonic.GrammarAltSpec{})
			continue
		}
		ga := &jsonic.GrammarAltSpec{}
		if s, ok := m["s"]; ok {
			switch sv := s.(type) {
			case string:
				ga.S = sv
			case []any:
				strs := make([]string, len(sv))
				for i, v := range sv {
					strs[i], _ = v.(string)
				}
				ga.S = strs
			}
		}
		if b, ok := m["b"]; ok {
			switch bv := b.(type) {
			case float64:
				ga.B = int(bv)
			case int:
				ga.B = bv
			}
		}
		if p, ok := m["p"].(string); ok {
			ga.P = p
		}
		if r, ok := m["r"].(string); ok {
			ga.R = r
		}
		if a, ok := m["a"].(string); ok {
			ga.A = jsonic.FuncRef(a)
		}
		if c, ok := m["c"]; ok {
			switch cv := c.(type) {
			case string:
				ga.C = cv
			case map[string]any:
				ga.C = cv
			}
		}
		if n, ok := m["n"].(map[string]any); ok {
			ga.N = make(map[string]int, len(n))
			for k, v := range n {
				if nv, ok := v.(float64); ok {
					ga.N[k] = int(nv)
				} else if nv, ok := v.(int); ok {
					ga.N[k] = nv
				}
			}
		}
		if g, ok := m["g"].(string); ok {
			ga.G = g
		}
		alts = append(alts, ga)
	}
	return alts
}

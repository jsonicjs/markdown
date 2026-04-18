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
    { s: '#MB' g: 'md,blank' }
    { s: '#MH' a: '@heading'    g: 'md,heading' }
    { s: '#MR' a: '@hr'         g: 'md,hr' }
    { s: '#MC' a: '@code'       g: 'md,code' }
    { s: '#ML' a: '@list-start' p: list-tail  g: 'md,list' }
    { s: '#MQ' a: '@quote-start' p: quote-tail g: 'md,quote' }
    { s: '#MT' a: '@para-start' p: para-tail  g: 'md,para' }
  ]

  rule: para-tail: open: [
    { s: '#MT' a: '@para-append' r: para-tail g: 'md,para,more' }
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
			pushBlock(r, block)
		}),

		"@hr": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			pushBlock(r, map[string]any{"type": "hr"})
		}),

		"@code": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			block := map[string]any{
				"type": "code",
				"lang": v["lang"],
				"text": v["text"],
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
			pushBlock(r, block)
			ensureMeta(ctx)["mdCurrent"] = block
		}),

		"@list-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			items, _ := cur["items"].([]any)
			cur["items"] = append(items, map[string]any{"text": v["text"]})
		}),

		"@quote-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			block := map[string]any{"type": "blockquote", "text": v}
			pushBlock(r, block)
			ensureMeta(ctx)["mdCurrent"] = block
		}),

		"@quote-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			text, _ := cur["text"].(string)
			cur["text"] = text + "\n" + v
		}),

		"@para-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			if trim {
				v = strings.TrimSpace(v)
			}
			block := map[string]any{"type": "paragraph", "text": v}
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
	reBlank      = regexp.MustCompile(`^\s*$`)
	reHeading    = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reOrdered    = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)
	reUnordered  = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	reBlockquote = regexp.MustCompile(`^\s*>\s?(.*)$`)
	reTrailHash  = regexp.MustCompile(`\s+#+\s*$`)
)

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
				return nil
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

			switch {
			// Blank line.
			case reBlank.MatchString(lineContent):
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MB", tinFor(lex, "#MB"), nil, srcPart)

			// Fenced code block.
			case strings.HasPrefix(lineContent, fence):
				lang := strings.TrimSpace(lineContent[len(fence):])
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
					if strings.HasPrefix(innerLine, fence) {
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

			// Heading.
			case reHeading.MatchString(lineContent):
				m := reHeading.FindStringSubmatch(lineContent)
				text := strings.TrimSpace(reTrailHash.ReplaceAllString(m[2], ""))
				val := map[string]any{"level": len(m[1]), "text": text}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MH", tinFor(lex, "#MH"), val, srcPart)

			// Horizontal rule.
			case isHorizontalRule(lineContent):
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MR", tinFor(lex, "#MR"), nil, srcPart)

			// Ordered list item.
			case reOrdered.MatchString(lineContent):
				m := reOrdered.FindStringSubmatch(lineContent)
				val := map[string]any{"ordered": true, "text": m[2]}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#ML", tinFor(lex, "#ML"), val, srcPart)

			// Unordered list item.
			case reUnordered.MatchString(lineContent):
				m := reUnordered.FindStringSubmatch(lineContent)
				val := map[string]any{"ordered": false, "text": m[1]}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#ML", tinFor(lex, "#ML"), val, srcPart)

			// Blockquote line.
			case reBlockquote.MatchString(lineContent):
				m := reBlockquote.FindStringSubmatch(lineContent)
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MQ", tinFor(lex, "#MQ"), m[1], srcPart)

			// Plain text line.
			default:
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MT", tinFor(lex, "#MT"), lineContent, srcPart)
			}

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

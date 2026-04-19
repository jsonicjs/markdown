/* Copyright (c) 2021-2025 Richard Rodger, MIT License */

// Package markdown is a jsonic plugin that parses markdown text into a
// structured array of block nodes (heading, paragraph, code, list,
// blockquote, hr). It mirrors the TypeScript src/markdown.ts implementation.
package markdown

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

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
    { s: '#MB'  a: '@block-blank-seen' g: 'md,blank' }
    { s: '#MH'  a: '@heading'    g: 'md,heading' }
    { s: '#MR'  a: '@hr'         g: 'md,hr' }
    { s: '#MC'  a: '@code'       g: 'md,code' }
    { s: '#MHB' a: '@htmlblock'  g: 'md,htmlblock' }
    { s: '#MIC' a: '@icode-start' p: icode-tail g: 'md,icode' }
    { s: '#ML'  a: '@list-start'  p: list-tail  g: 'md,list' }
    { s: '#MQ'  a: '@quote-start' p: quote-tail g: 'md,quote' }
    { s: '#MT'  a: '@para-start'  p: para-tail  g: 'md,para' }
    { s: '#MSX' g: 'md,setext,stray' }
  ]

  rule: para-tail: open: [
    { s: '#MSX' c: '@setext-has-content' a: '@setext-promote' g: 'md,setext,close' }
    { s: '#MSX' a: '@setext-as-para' r: para-tail g: 'md,setext,stray' }
    { s: '#MT'  a: '@para-append' r: para-tail g: 'md,para,more' }
    { g: 'md,para,end' }
  ]

  rule: list-tail: open: [
    { s: '#ML'  a: '@list-append' r: list-tail g: 'md,list,more' }
    { s: '#MLC' a: '@list-cont'   r: list-tail g: 'md,list,cont' }
    { s: '#MB'  a: '@list-blank'  r: list-tail-blank g: 'md,list,blank' }
    { s: '#MT'  a: '@list-lazy'   r: list-tail g: 'md,list,lazy' }
    { g: 'md,list,end' }
  ]

  rule: list-tail-blank: open: [
    { s: '#ML'  a: '@list-append' r: list-tail g: 'md,list,blank,item' }
    { s: '#MLC' a: '@list-cont'   r: list-tail g: 'md,list,blank,cont' }
    { s: '#MB'  a: '@list-blank'  r: list-tail-blank g: 'md,list,blank,more' }
    { a: '@list-blank-escape' g: 'md,list,blank,end' }
  ]

  rule: quote-tail: open: [
    { s: '#MQ' a: '@quote-append' r: quote-tail g: 'md,quote,more' }
    { s: '#MT' a: '@quote-append' r: quote-tail c: '@quote-lazy-ok' g: 'md,quote,lazy' }
    { s: '#MSX' a: '@quote-lazy-msx' r: quote-tail c: '@quote-lazy-ok' g: 'md,quote,lazy,msx' }
    { s: '#MIC' a: '@quote-lazy-mic' r: quote-tail c: '@quote-lazy-para-ok' g: 'md,quote,lazy,mic' }
    { g: 'md,quote,end' }
  ]

  rule: icode-tail: open: [
    { s: '#MIC' a: '@icode-append' r: icode-tail g: 'md,icode,more' }
    { s: ['#MB' '#MIC'] b: 1 a: '@icode-blank' r: icode-tail g: 'md,icode,blank-mic' }
    { s: ['#MB' '#MB']  b: 1 a: '@icode-blank' r: icode-tail g: 'md,icode,blank-blank' }
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
	j.Token("#MHB")
	j.Token("#MLC")

	// Named function references for declarative grammar definition.
	refs := map[jsonic.FuncRef]any{

		// Initialize the result array at the top of parsing.
		"@md-bo": jsonic.StateAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.Node = make([]any, 0)
		}),

		// Mark that a blank line separated the previous block from whatever
		// block the `block` rule is about to open. Each block-start action
		// consumes this flag and stashes `_precededByBlank: true` on the
		// newly-opened block so renderListItem can determine loose-vs-tight
		// dynamically.
		"@block-blank-seen": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			ensureMeta(ctx)["blockBlankSeen"] = true
		}),

		"@heading": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			block := map[string]any{
				"type":  "heading",
				"level": v["level"],
				"text":  v["text"],
			}
			markPrecededByBlank(block, ctx)
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
		}),

		"@hr": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			block := map[string]any{"type": "hr"}
			markPrecededByBlank(block, ctx)
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
			markPrecededByBlank(block, ctx)
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
		}),

		"@htmlblock": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			block := map[string]any{"type": "html", "text": v}
			markPrecededByBlank(block, ctx)
			if emitHTML {
				block["html"] = v
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
			if ord, _ := v["ordered"].(bool); ord {
				if start, ok := v["start"].(int); ok && start != 1 {
					block["start"] = start
				}
			}
			markPrecededByBlank(block, ctx)
			pushBlock(r, block)
			meta := ensureMeta(ctx)
			meta["mdCurrent"] = block
			meta["mdCurrentMarkerId"], _ = v["markerId"].(string)
			// Reset blank-tracking so a pending blank left over from a
			// prior sibling list doesn't promote this list to loose.
			meta["listPendingBlank"] = false
			meta["listPendingBlanks"] = 0
		}),

		"@list-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			prevMarker, _ := ctx.Meta["mdCurrentMarkerId"].(string)
			newMarker, _ := v["markerId"].(string)
			if newMarker != "" && newMarker != prevMarker {
				newBlock := map[string]any{
					"type":    "list",
					"ordered": v["ordered"],
					"items":   []any{map[string]any{"text": v["text"]}},
				}
				if ord, _ := v["ordered"].(bool); ord {
					if start, ok := v["start"].(int); ok && start != 1 {
						newBlock["start"] = start
					}
				}
				pushBlock(r, newBlock)
				ctx.Meta["mdCurrent"] = newBlock
				ctx.Meta["mdCurrentMarkerId"] = newMarker
				ctx.Meta["listPendingBlank"] = false
				ctx.Meta["listPendingBlanks"] = 0
				return
			}
			if pending, _ := ctx.Meta["listPendingBlank"].(bool); pending {
				cur["loose"] = true
				ctx.Meta["listPendingBlank"] = false
				ctx.Meta["listPendingBlanks"] = 0
			}
			items, _ := cur["items"].([]any)
			cur["items"] = append(items, map[string]any{"text": v["text"]})
		}),

		"@list-cont": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			items, _ := cur["items"].([]any)
			last, _ := items[len(items)-1].(map[string]any)
			text, _ := last["text"].(string)
			pending, _ := ctx.Meta["listPendingBlank"].(bool)
			pendingBlanks, _ := ctx.Meta["listPendingBlanks"].(int)
			if pending {
				// Preserve the exact number of blank lines so nested code
				// fences and other constructs render with the right
				// internal spacing. Looseness is decided at render time.
				last["text"] = text + strings.Repeat("\n", pendingBlanks+1) + v
				ctx.Meta["listPendingBlank"] = false
				ctx.Meta["listPendingBlanks"] = 0
			} else if text == "" {
				last["text"] = v
			} else {
				last["text"] = text + "\n" + v
			}
		}),

		"@list-blank": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			meta := ensureMeta(ctx)
			meta["listPendingBlank"] = true
			cnt, _ := meta["listPendingBlanks"].(int)
			meta["listPendingBlanks"] = cnt + 1
		}),

		// list-tail-blank fell through: no further item / continuation /
		// blank consumed the dangling blank(s). The list ends here and
		// the blank effectively separates the list from whatever block
		// follows, so the next block should render as preceded-by-blank.
		"@list-blank-escape": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if pending, _ := ctx.Meta["listPendingBlank"].(bool); pending {
				ctx.Meta["blockBlankSeen"] = true
			}
		}),

		// Lazy paragraph continuation: a plain text line inside list-tail
		// attaches to the current item's text, matching CommonMark's
		// paragraph-continuation-inside-list-item rule.
		"@list-lazy": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			items, _ := cur["items"].([]any)
			last, _ := items[len(items)-1].(map[string]any)
			text, _ := last["text"].(string)
			if text == "" {
				last["text"] = v
			} else {
				last["text"] = text + "\n" + v
			}
		}),

		"@quote-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			block := map[string]any{"type": "blockquote", "text": v}
			markPrecededByBlank(block, ctx)
			pushBlock(r, block)
			ensureMeta(ctx)["mdCurrent"] = block
			// HTML for blockquotes is deferred to ToHTML so the nested
			// parse the blockquote renderer performs cannot re-enter
			// during the outer parse and disrupt grammar state.
		}),

		"@quote-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			text, _ := cur["text"].(string)
			cur["text"] = text + "\n" + v
		}),

		// A `===` / `---` line inside a blockquote cannot attach as a
		// setext underline to a lazy-continuation paragraph (CM § 4.3).
		// Fold it onto the blockquote's accumulated text prefixed with
		// four spaces so the blockquote sub-parse sees the line as a
		// lazy continuation of the paragraph rather than a fresh setext
		// underline (example 93).
		"@quote-lazy-msx": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			text, _ := cur["text"].(string)
			raw, _ := v["raw"].(string)
			cur["text"] = text + "\n    " + strings.TrimLeft(raw, " \t")
		}),

		// Indented line inside a blockquote without strict `>` prefix:
		// a lazy continuation of the blockquote's open paragraph
		// (example 238). Re-indent with four spaces so the blockquote
		// sub-parse treats it as lazy paragraph text rather than a
		// fresh block (list, heading, etc.).
		"@quote-lazy-mic": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			text, _ := cur["text"].(string)
			cur["text"] = text + "\n    " + v
		}),

		// Guard: lazy continuation of a blockquote paragraph applies
		// only when the blockquote has an open paragraph — the
		// accumulated text must be non-empty and its last line must
		// not itself be indented code (which signals the inner
		// paragraph has closed).
		"@quote-lazy-para-ok": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			if cur == nil {
				return false
			}
			text, _ := cur["text"].(string)
			if text == "" || strings.HasSuffix(text, "\n") {
				return false
			}
			lastNL := strings.LastIndex(text, "\n")
			lastLine := text
			if lastNL >= 0 {
				lastLine = text[lastNL+1:]
			}
			if lastLine == "" {
				return false
			}
			if strings.HasPrefix(lastLine, "    ") || strings.HasPrefix(lastLine, "\t") {
				return false
			}
			return true
		}),

		// Lazy continuation of a blockquote paragraph is only allowed when
		// the accumulated content doesn't already end with an empty line.
		// Lazy continuation of a blockquote paragraph is only allowed when
		// the accumulated content doesn't already end with an empty line.
		"@quote-lazy-ok": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			if cur == nil {
				return true
			}
			text, _ := cur["text"].(string)
			if strings.HasSuffix(text, "\n") {
				return false
			}
			// Lazy continuation only applies to an open paragraph. If
			// the blockquote's last line itself opens a non-paragraph
			// block (fenced code etc.) then a following line without
			// `>` must terminate the quote (spec example 237).
			lastNL := strings.LastIndex(text, "\n")
			lastLine := text
			if lastNL >= 0 {
				lastLine = text[lastNL+1:]
			}
			if reQuoteFenceStart.MatchString(lastLine) {
				return false
			}
			return true
		}),

		"@para-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			if trim {
				v = strings.TrimSpace(v)
			}
			block := map[string]any{"type": "paragraph", "text": v}
			markPrecededByBlank(block, ctx)
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

		// Guard: the current paragraph has promotable content (something
		// beyond leading link-reference definitions). Only when this
		// holds does the setext underline actually produce a heading.
		"@setext-has-content": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			if cur == nil {
				return false
			}
			text, _ := cur["text"].(string)
			for {
				_, _, _, length, ok := parseLinkRefDef(text)
				if !ok {
					break
				}
				text = text[length:]
			}
			return strings.TrimSpace(text) != ""
		}),

		// Setext underline encountered while accumulating a paragraph:
		// rewrite the current block in place as a heading.
		"@setext-promote": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			// Strip leading link reference definitions. Stash the
			// extracted defs on the block itself as `_implicitRefDefs`
			// so the document-level ref gathering picks them up even
			// though the enclosing paragraph became a heading.
			text, _ := cur["text"].(string)
			var defs []map[string]string
			for {
				label, url, title, length, ok := parseLinkRefDef(text)
				if !ok {
					break
				}
				defs = append(defs, map[string]string{
					"label": label, "url": url, "title": title,
				})
				text = text[length:]
			}
			if len(defs) > 0 {
				cur["_implicitRefDefs"] = defs
			}
			text = strings.TrimSpace(text)
			cur["type"] = "heading"
			cur["level"] = v["level"]
			cur["text"] = text
		}),

		// Setext underline encountered when the paragraph is entirely
		// link reference definitions — the underline cannot promote to
		// a heading (nothing to promote), so fold the underline's raw
		// text onto the paragraph as its next line and keep going.
		"@setext-as-para": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(map[string]any)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			raw, _ := v["raw"].(string)
			line := raw
			if trim {
				line = strings.TrimSpace(line)
			}
			text, _ := cur["text"].(string)
			if text == "" {
				cur["text"] = line
			} else {
				cur["text"] = text + "\n" + line
			}
			if emitHTML {
				cur["html"] = RenderHTML(cur)
			}
		}),

		"@icode-start": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			block := map[string]any{"type": "code", "lang": "", "text": v}
			markPrecededByBlank(block, ctx)
			if emitHTML {
				block["html"] = RenderHTML(block)
			}
			pushBlock(r, block)
			meta := ensureMeta(ctx)
			meta["mdCurrent"] = block
			meta["icodePendingBlanks"] = 0
		}),

		"@icode-append": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			v, _ := r.O0.Val.(string)
			cur, _ := ctx.Meta["mdCurrent"].(map[string]any)
			text, _ := cur["text"].(string)
			pending, _ := ctx.Meta["icodePendingBlanks"].(int)
			cur["text"] = text + strings.Repeat("\n", pending+1) + v
			ctx.Meta["icodePendingBlanks"] = 0
			if emitHTML {
				cur["html"] = RenderHTML(cur)
			}
		}),

		"@icode-blank": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			meta := ensureMeta(ctx)
			pending, _ := meta["icodePendingBlanks"].(int)
			meta["icodePendingBlanks"] = pending + 1
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

// markPrecededByBlank stamps the newly-opened block with
// `_precededByBlank: true` if the `block` rule just consumed a `#MB`.
// Used by list rendering to decide loose-vs-tight dynamically: a list
// item whose sub-parse has top-level blocks separated by blanks forces
// the enclosing list to render loose, whereas blanks that belong to
// nested content stay inside the nested block. Only sub-parsed blocks
// are marked — the top-level parse swallows the flag so users never
// see the internal `_precededByBlank` key on returned block maps.
func markPrecededByBlank(block map[string]any, ctx *jsonic.Context) {
	if seen, _ := ctx.Meta["blockBlankSeen"].(bool); seen {
		if subparseActive {
			block["_precededByBlank"] = true
		}
		ctx.Meta["blockBlankSeen"] = false
	}
}

// renderListItemFromBlocks renders a list item from its already-parsed
// sub-blocks. In tight lists a singleton paragraph is rendered without
// its `<p>` wrapper (matching CommonMark).
func renderListItemFromBlocks(cleaned []any, nestedRefs LinkRefMap, loose bool, refs LinkRefMap) string {
	if len(cleaned) == 0 {
		return "<li></li>"
	}
	merged := LinkRefMap{}
	for k, v := range refs {
		merged[k] = v
	}
	for k, v := range nestedRefs {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	var parts []string
	for _, v := range cleaned {
		nb, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if !loose && nb["type"] == "paragraph" {
			txt, _ := nb["text"].(string)
			parts = append(parts, renderInline(txt, merged))
		} else {
			parts = append(parts, renderBlockHTML(nb, merged))
		}
	}
	if len(parts) == 0 {
		return "<li></li>"
	}
	if !loose && len(parts) == 1 && (len(parts[0]) == 0 || parts[0][0] != '<') {
		return "<li>" + parts[0] + "</li>"
	}
	// Tight-mode inline text at the first or last slot sits flush
	// against the `<li>` boundary (no leading/trailing newline). This
	// matches CommonMark's `<li>a\n<ul>...</ul>\n</li>` and
	// `<li>\n<h2>Bar</h2>\nbaz</li>` shapes.
	firstInline := !loose && len(parts) > 0 && (len(parts[0]) == 0 || parts[0][0] != '<')
	last := parts[len(parts)-1]
	lastInline := !loose && (len(last) == 0 || last[0] != '<')
	open := "<li>\n"
	if firstInline {
		open = "<li>" + parts[0] + "\n"
	}
	var mid string
	if firstInline {
		mid = strings.Join(parts[1:], "\n")
	} else {
		mid = strings.Join(parts, "\n")
	}
	close := "\n</li>"
	if lastInline {
		close = "</li>"
	}
	return open + mid + close
}

// parseNested runs the markdown parser on a substring for use inside a
// blockquote or other nested block. The parser instance is cached so
// repeated nested parses don't rebuild the grammar on each call.
var nestedParser *jsonic.Jsonic

func parseNested(src string) []any {
	if nestedParser == nil {
		j := jsonic.Make()
		if err := j.UseDefaults(Markdown, Defaults); err != nil {
			return nil
		}
		nestedParser = j
	}
	prev := subparseActive
	subparseActive = true
	result, err := nestedParser.Parse(src)
	subparseActive = prev
	if err != nil {
		return nil
	}
	if arr, ok := result.([]any); ok {
		return arr
	}
	return nil
}

// subparseActive is set true while parseNested is running so that
// markPrecededByBlank stamps its flag only on sub-parsed blocks. Go
// maps lack non-enumerable properties, so leaking `_precededByBlank`
// onto user-visible top-level blocks would pollute the output.
var subparseActive bool

// splitParagraphs splits a loose-list item's accumulated text on runs of
// two or more newlines. Empty trailing chunks are dropped.
func splitParagraphs(s string) []string {
	out := []string{}
	chunk := strings.Builder{}
	blanks := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			blanks++
			if blanks == 1 {
				continue
			}
			if chunk.Len() > 0 {
				out = append(out, chunk.String())
				chunk.Reset()
			}
			continue
		}
		if blanks == 1 && chunk.Len() > 0 {
			chunk.WriteByte('\n')
		}
		blanks = 0
		chunk.WriteByte(s[i])
	}
	if chunk.Len() > 0 {
		out = append(out, chunk.String())
	}
	return out
}

// Pre-compiled line classification regexes.
var (
	reBlank         = regexp.MustCompile(`^[ \t]*$`)
	reATX           = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$`)
	reATXTrailHash  = regexp.MustCompile(`[ \t]+#+[ \t]*$`)
	reATXAllHash    = regexp.MustCompile(`^#+$`)
	reSetext        = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	reOrderedFull   = regexp.MustCompile(`^( {0,3})(\d{1,9})([.)])([ \t]+)(.*)$`)
	reUnorderedFull = regexp.MustCompile(`^( {0,3})([-*+])([ \t]+)(.*)$`)
	reOrderedBare   = regexp.MustCompile(`^( {0,3})(\d{1,9})([.)])[ \t]*$`)
	reUnorderedBare     = regexp.MustCompile(`^( {0,3})[-*+][ \t]*$`)
	reBlockquote        = regexp.MustCompile(`^ {0,3}>( ?)(.*)$`)
	reOrderedFullStart1 = regexp.MustCompile(`^ {0,3}1[.)][ \t]`)
	reLabelBlankLine    = regexp.MustCompile(`\n[ \t]*\n`)
	reQuoteFenceStart   = regexp.MustCompile("^ {0,3}(?:`{3,}|~{3,})")
)

// computeListItem determines the effective content column and first-line
// text for a list item. Per CommonMark § 5.2, if the marker is followed by
// 1-4 spaces the content column is (marker_end + spaces); if 5+ spaces,
// the content column is (marker_end + 1) and the rest of the whitespace
// becomes part of the item's content (forming an indented code block).
func computeListItem(markerEnd, spacesAfter int, rest string) (int, string) {
	if rest == "" {
		return markerEnd + 1, ""
	}
	if spacesAfter >= 5 {
		return markerEnd + 1, strings.Repeat(" ", spacesAfter-1) + rest
	}
	return markerEnd + spacesAfter, rest
}

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

// expandLeadingTabs replaces tabs in the line's leading whitespace with
// spaces that advance to the next tab stop at multiples of 4. Non-leading
// tabs are preserved.
func expandLeadingTabs(s string) string {
	var b strings.Builder
	col := 0
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' {
			b.WriteByte(' ')
			col++
			i++
		} else if c == '\t' {
			spaces := 4 - (col % 4)
			for k := 0; k < spaces; k++ {
				b.WriteByte(' ')
			}
			col += spaces
			i++
		} else {
			break
		}
	}
	b.WriteString(s[i:])
	return b.String()
}

// HTML block recognition (CommonMark § 4.6). Types 1-5 have distinct end
// markers; type 7 is any well-formed open or close tag on a line by
// itself, terminated by a blank line.
var (
	reHTMLBlockT1Open  = regexp.MustCompile(`(?i)^ {0,3}<(?:script|pre|style|textarea)(?:[\s>]|$)`)
	reHTMLBlockT1Close = regexp.MustCompile(`(?i)</(?:script|pre|style|textarea)>`)
	reHTMLBlockT2Open  = regexp.MustCompile(`^ {0,3}<!--`)
	reHTMLBlockT2Close = regexp.MustCompile(`-->`)
	reHTMLBlockT3Open  = regexp.MustCompile(`^ {0,3}<\?`)
	reHTMLBlockT3Close = regexp.MustCompile(`\?>`)
	reHTMLBlockT4Open  = regexp.MustCompile(`^ {0,3}<![A-Za-z]`)
	reHTMLBlockT4Close = regexp.MustCompile(`>`)
	reHTMLBlockT5Open  = regexp.MustCompile(`^ {0,3}<!\[CDATA\[`)
	reHTMLBlockT5Close = regexp.MustCompile(`\]\]>`)
	reHTMLBlockT6Open  = regexp.MustCompile(
		`(?i)^ {0,3}</?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?:\s|/?>|$)`,
	)
	reHTMLBlockT7Open = regexp.MustCompile(
		`^ {0,3}(?:<[a-zA-Z][a-zA-Z0-9-]*(?:\s+[a-zA-Z_:][a-zA-Z0-9_.:-]*(?:\s*=\s*(?:[^\s"'=<>` + "`" + `]+|'[^']*'|"[^"]*"))?)*\s*/?>|</[a-zA-Z][a-zA-Z0-9-]*\s*>)[ \t]*$`,
	)
)

// Fenced code block regexes. Backtick and tilde fences are matched
// separately so the info-string restriction can apply only to backticks.
var (
	reFenceOpen       = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})(.*)$")
	reFenceCloseBack  = regexp.MustCompile("^ {0,3}(`{3,})[ \\t]*$")
	reFenceCloseTilde = regexp.MustCompile(`^ {0,3}(~{3,})[ \t]*$`)
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

// lexState is the per-parse per-lexer state used for context-sensitive
// classification (indented code vs. paragraph continuation, setext
// recognition). A fresh map is keyed by *jsonic.Lex pointer so state
// resets on each new parse.
type lexState struct {
	last           string // last emitted token kind
	listContentCol int    // content column of current list item, 0 if none
	listItemEmpty  bool   // current item has no content yet
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
			lineContent = expandLeadingTabs(lineContent)

			var tkn *jsonic.Token
			kind := "text"

			switch {
			// Blank line. If we're currently inside an indented-code
			// block and this "blank" line actually has at least 4
			// leading whitespace chars, preserve the stripped content
			// as a code line instead of as a blank.
			case reBlank.MatchString(lineContent):
				if state.last == "icode" &&
					(strings.HasPrefix(lineContent, "    ") || strings.HasPrefix(lineContent, "\t")) {
					var stripped string
					if strings.HasPrefix(lineContent, "\t") {
						stripped = lineContent[1:]
					} else {
						stripped = lineContent[4:]
					}
					srcPart := src[sI:consumeEnd]
					tkn = lex.Token("#MIC", tinFor(lex, "#MIC"), stripped, srcPart)
					kind = "icode"
				} else {
					srcPart := src[sI:consumeEnd]
					tkn = lex.Token("#MB", tinFor(lex, "#MB"), nil, srcPart)
					kind = "blank"
				}

			// HTML block: consume contiguous lines through the type's
			// end marker (types 1-5) or through the next blank line
			// (type 7).
			case reHTMLBlockT1Open.MatchString(lineContent) ||
				reHTMLBlockT2Open.MatchString(lineContent) ||
				reHTMLBlockT3Open.MatchString(lineContent) ||
				reHTMLBlockT5Open.MatchString(lineContent) ||
				reHTMLBlockT4Open.MatchString(lineContent) ||
				reHTMLBlockT6Open.MatchString(lineContent) ||
				(state.last != "text" && reHTMLBlockT7Open.MatchString(lineContent)):

				var closeRe *regexp.Regexp
				switch {
				case reHTMLBlockT1Open.MatchString(lineContent):
					closeRe = reHTMLBlockT1Close
				case reHTMLBlockT2Open.MatchString(lineContent):
					closeRe = reHTMLBlockT2Close
				case reHTMLBlockT3Open.MatchString(lineContent):
					closeRe = reHTMLBlockT3Close
				case reHTMLBlockT5Open.MatchString(lineContent):
					closeRe = reHTMLBlockT5Close
				case reHTMLBlockT4Open.MatchString(lineContent):
					closeRe = reHTMLBlockT4Close
				}
				// Types 6 and 7 have closeRe == nil; terminate on blank.

				htmlLines := []string{}
				htmlEnd := sI
				for htmlEnd < srclen {
					le := htmlEnd
					for le < srclen && src[le] != '\n' {
						le++
					}
					innerLine := src[htmlEnd:le]
					if strings.HasSuffix(innerLine, "\r") {
						innerLine = innerLine[:len(innerLine)-1]
					}
					nextEnd := le
					if le < srclen {
						nextEnd = le + 1
					}

					if closeRe == nil && strings.TrimLeft(innerLine, " \t") == "" {
						break
					}
					htmlLines = append(htmlLines, innerLine)
					htmlEnd = nextEnd
					if closeRe != nil && closeRe.MatchString(innerLine) {
						break
					}
				}

				htmlText := strings.Join(htmlLines, "\n")
				consumeEnd = htmlEnd
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MHB", tinFor(lex, "#MHB"), htmlText, srcPart)
				kind = "htmlblock"

			// List item continuation at the active list's content column.
			// When we're inside a list, a line indented to at least the
			// item's content column is always a continuation, even if
			// its stripped form would otherwise be a new block (HR, ATX
			// heading, list marker, etc.).
			case state.listContentCol > 0 &&
				len(lineContent) >= state.listContentCol &&
				strings.TrimLeft(lineContent[:state.listContentCol], " ") == "":
				stripped := lineContent[state.listContentCol:]
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MLC", tinFor(lex, "#MLC"), stripped, srcPart)
				kind = "listcont"

			// Fenced code block: 3+ backticks or 3+ tildes with up to 3
			// leading spaces. Close must use same char, length >= open.
			case reFenceOpen.MatchString(lineContent):
				m := reFenceOpen.FindStringSubmatch(lineContent)
				openIndent := len(m[1])
				fenceRun := m[2]
				fenceChar := fenceRun[0]
				fenceLen := len(fenceRun)
				infoRaw := m[3]
				if fenceChar == '`' && strings.IndexByte(infoRaw, '`') >= 0 {
					// Back-tick fence with backtick in info: fall through
					// to text.
					srcPart := src[sI:consumeEnd]
					tkn = lex.Token("#MT", tinFor(lex, "#MT"), lineContent, srcPart)
					kind = "text"
				} else {
					lang := strings.TrimSpace(infoRaw)
					if idx := strings.IndexAny(lang, " \t"); idx >= 0 {
						lang = lang[:idx]
					}
					lang = decodeLinkText(lang)
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

						var closeRe *regexp.Regexp
						if fenceChar == '`' {
							closeRe = reFenceCloseBack
						} else {
							closeRe = reFenceCloseTilde
						}
						if cm := closeRe.FindStringSubmatch(innerLine); cm != nil {
							if len(cm[1]) >= fenceLen {
								codeEnd = nextEnd
								break
							}
						}

						strip := 0
						for strip < openIndent &&
							strip < len(innerLine) &&
							innerLine[strip] == ' ' {
							strip++
						}
						codeLines = append(codeLines, innerLine[strip:])
						codeEnd = nextEnd
					}

					// Each content line contributes `<line>\n`, so the
					// joined text needs a terminal newline too (matters
					// when the final line is blank — Join drops the
					// trailing separator).
					codeText := ""
					if len(codeLines) > 0 {
						codeText = strings.Join(codeLines, "\n") + "\n"
					}
					consumeEnd = codeEnd
					srcPart := src[sI:consumeEnd]
					val := map[string]any{"lang": lang, "text": codeText}
					tkn = lex.Token("#MC", tinFor(lex, "#MC"), val, srcPart)
					kind = "code"
				}

			// ATX heading: 0-3 leading spaces, 1-6 `#`, optional space+text.
			// Trailing `#` closure is stripped.
			case reATX.MatchString(lineContent):
				m := reATX.FindStringSubmatch(lineContent)
				text := m[2]
				text = reATXTrailHash.ReplaceAllString(text, "")
				if reATXAllHash.MatchString(text) {
					text = ""
				}
				text = strings.TrimSpace(text)
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
				val := map[string]any{"level": level, "raw": lineContent}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MSX", tinFor(lex, "#MSX"), val, srcPart)
				kind = "setext"

			// Horizontal rule.
			case isHorizontalRule(lineContent):
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MR", tinFor(lex, "#MR"), nil, srcPart)
				kind = "hr"


			// Ordered list item. An ordered list can only interrupt a
			// paragraph with start == 1.
			case reOrderedFull.MatchString(lineContent) &&
				(state.last != "text" ||
					reOrderedFullStart1.MatchString(lineContent)):
				m := reOrderedFull.FindStringSubmatch(lineContent)
				markerEnd := len(m[1]) + len(m[2]) + 1
				cc, text := computeListItem(markerEnd, len(m[4]), m[5])
				state.listContentCol = cc
				start := 0
				fmt.Sscanf(m[2], "%d", &start)
				val := map[string]any{
					"ordered":  true,
					"text":     text,
					"start":    start,
					"markerId": "o" + m[3],
				}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#ML", tinFor(lex, "#ML"), val, srcPart)
				kind = "list"

			// Unordered list item.
			case reUnorderedFull.MatchString(lineContent):
				m := reUnorderedFull.FindStringSubmatch(lineContent)
				markerEnd := len(m[1]) + len(m[2])
				cc, text := computeListItem(markerEnd, len(m[3]), m[4])
				state.listContentCol = cc
				val := map[string]any{
					"ordered":  false,
					"text":     text,
					"markerId": "u" + m[2],
				}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#ML", tinFor(lex, "#ML"), val, srcPart)
				kind = "list"

			// Bare list marker with no content. Empty-content list markers
			// cannot interrupt a paragraph (CommonMark § 5.2).
			case state.last != "text" && reUnorderedBare.MatchString(lineContent):
				m := reUnorderedBare.FindStringSubmatch(lineContent)
				state.listContentCol = len(m[1]) + 2
				val := map[string]any{
					"ordered":  false,
					"text":     "",
					"markerId": "u" + string(lineContent[len(m[1])]),
				}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#ML", tinFor(lex, "#ML"), val, srcPart)
				kind = "list"

			case state.last != "text" && reOrderedBare.MatchString(lineContent):
				m := reOrderedBare.FindStringSubmatch(lineContent)
				state.listContentCol = len(m[1]) + len(m[2]) + 2
				start := 0
				fmt.Sscanf(m[2], "%d", &start)
				val := map[string]any{
					"ordered":  true,
					"text":     "",
					"start":    start,
					"markerId": "o" + m[3],
				}
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#ML", tinFor(lex, "#ML"), val, srcPart)
				kind = "list"

			// Blockquote line.
			case reBlockquote.MatchString(lineContent):
				m := reBlockquote.FindStringSubmatch(lineContent)
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MQ", tinFor(lex, "#MQ"), m[2], srcPart)
				kind = "quote"

			// Indented line (4+ spaces or tab): indented code block,
			// unless we're currently inside a paragraph OR inside a
			// list item accumulating a paragraph (lazy continuation).
			case strings.HasPrefix(lineContent, "    ") || strings.HasPrefix(lineContent, "\t"):
				if state.last == "text" ||
					(state.listContentCol > 0 &&
						(state.last == "list" || state.last == "listcont")) {
					stripped := strings.TrimLeft(lineContent, " \t")
					srcPart := src[sI:consumeEnd]
					tkn = lex.Token("#MT", tinFor(lex, "#MT"), stripped, srcPart)
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

			// Plain text line. Leading whitespace on paragraph lines is
			// not significant — strip it.
			default:
				stripped := strings.TrimLeft(lineContent, " \t")
				srcPart := src[sI:consumeEnd]
				tkn = lex.Token("#MT", tinFor(lex, "#MT"), stripped, srcPart)
				kind = "text"
			}

			state.last = kind
			// Reset list continuation tracking when we leave list context.
			if kind != "list" && kind != "listcont" && kind != "blank" {
				state.listContentCol = 0
				state.listItemEmpty = false
			}

			// Track whether the most recently opened list item is empty
			// so a blank line following it ends the list per CommonMark
			// § 5.2 ("A list item can begin with at most one blank line").
			switch kind {
			case "list":
				if v, ok := tkn.Val.(map[string]any); ok {
					txt, _ := v["text"].(string)
					state.listItemEmpty = txt == ""
				} else {
					state.listItemEmpty = true
				}
			case "listcont":
				state.listItemEmpty = false
			case "blank":
				if state.listItemEmpty {
					state.listContentCol = 0
					state.listItemEmpty = false
				}
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
	"html":      false,
}

// RenderHTML produces a CommonMark-style HTML fragment for a single block.
// Block text is routed through renderInline. A non-nil refs map resolves
// reference-style links.
func RenderHTML(block map[string]any) string {
	return renderBlockHTML(block, nil)
}

func renderBlockHTML(block map[string]any, refs LinkRefMap) string {
	switch block["type"] {
	case "heading":
		level, _ := block["level"].(int)
		text, _ := block["text"].(string)
		return fmt.Sprintf("<h%d>%s</h%d>", level, renderInline(text, refs), level)

	case "paragraph":
		text, _ := block["text"].(string)
		// Trailing whitespace on the paragraph's final line is not
		// significant (hard-break spaces are always followed by \n).
		text = strings.TrimRight(text, " \t")
		return "<p>" + renderInline(text, refs) + "</p>"

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
		ordered, _ := block["ordered"].(bool)
		if ordered {
			tag = "ol"
		}
		loose, _ := block["loose"].(bool)
		items, _ := block["items"].([]any)
		startAttr := ""
		if ordered {
			if start, ok := block["start"].(int); ok && start != 1 {
				startAttr = fmt.Sprintf(` start="%d"`, start)
			}
		}
		// Pre-parse each item so we can (a) inspect top-level blocks for
		// `_precededByBlank` to decide dynamic looseness and (b) reuse the
		// parse result when rendering.
		type parsedItem struct {
			cleaned []any
			refs    LinkRefMap
		}
		parsedItems := make([]parsedItem, len(items))
		for i, it := range items {
			m, _ := it.(map[string]any)
			text, _ := m["text"].(string)
			if text == "" {
				parsedItems[i] = parsedItem{cleaned: nil, refs: LinkRefMap{}}
				continue
			}
			nested := parseNested(text)
			// Detect blank-separated top-level blocks BEFORE extracting
			// link-reference definitions — ref defs sometimes occupy the
			// blank-preceded slot and must still count toward looseness.
			if !loose {
				for j := 1; j < len(nested); j++ {
					nb, ok := nested[j].(map[string]any)
					if !ok {
						continue
					}
					if blank, _ := nb["_precededByBlank"].(bool); blank {
						loose = true
						break
					}
				}
			}
			nestedRefs, cleaned := ExtractLinkRefsAndClean(nested)
			parsedItems[i] = parsedItem{cleaned: cleaned, refs: nestedRefs}
		}
		var b strings.Builder
		b.WriteString("<")
		b.WriteString(tag)
		b.WriteString(startAttr)
		b.WriteString(">\n")
		for i, p := range parsedItems {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(renderListItemFromBlocks(p.cleaned, p.refs, loose, refs))
		}
		b.WriteString("\n</")
		b.WriteString(tag)
		b.WriteString(">")
		return b.String()

	case "blockquote":
		text, _ := block["text"].(string)
		// Re-parse the blockquote's content as markdown so nested
		// headings, lists, code blocks, and further blockquotes render
		// correctly. Refs defined inside merge with the outer refs.
		nestedBlocks := parseNested(text)
		nestedRefs, cleaned := ExtractLinkRefsAndClean(nestedBlocks)
		merged := LinkRefMap{}
		for k, v := range refs {
			merged[k] = v
		}
		for k, v := range nestedRefs {
			if _, ok := merged[k]; !ok {
				merged[k] = v
			}
		}
		var b strings.Builder
		b.WriteString("<blockquote>\n")
		for _, nb := range cleaned {
			m, ok := nb.(map[string]any)
			if !ok {
				continue
			}
			h := renderBlockHTML(m, merged)
			if h != "" {
				b.WriteString(h)
				b.WriteByte('\n')
			}
		}
		b.WriteString("</blockquote>")
		return b.String()

	case "html":
		text, _ := block["text"].(string)
		return text
	}
	return ""
}

// ASCII punctuation set recognized as a backslash escape target per
// CommonMark (§ 6.1).
const backslashEscapable = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// Named HTML entities recognized by the inline parser. A broad subset
// covering ASCII punctuation, common Latin-1 accents, arrows, currency,
// mathematical operators, Greek letters, and a set of less-common names
// that appear in the CommonMark conformance spec.
var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'",
	"nbsp": "\u00A0", "iexcl": "\u00A1", "cent": "\u00A2", "pound": "\u00A3",
	"curren": "\u00A4", "yen": "\u00A5", "brvbar": "\u00A6", "sect": "\u00A7",
	"uml": "\u00A8", "copy": "\u00A9", "ordf": "\u00AA", "laquo": "\u00AB",
	"not": "\u00AC", "shy": "\u00AD", "reg": "\u00AE", "macr": "\u00AF",
	"deg": "\u00B0", "plusmn": "\u00B1", "sup2": "\u00B2", "sup3": "\u00B3",
	"acute": "\u00B4", "micro": "\u00B5", "para": "\u00B6", "middot": "\u00B7",
	"cedil": "\u00B8", "sup1": "\u00B9", "ordm": "\u00BA", "raquo": "\u00BB",
	"frac14": "\u00BC", "frac12": "\u00BD", "frac34": "\u00BE", "iquest": "\u00BF",
	"Agrave": "\u00C0", "Aacute": "\u00C1", "Acirc": "\u00C2", "Atilde": "\u00C3",
	"Auml": "\u00C4", "Aring": "\u00C5", "AElig": "\u00C6", "Ccedil": "\u00C7",
	"Egrave": "\u00C8", "Eacute": "\u00C9", "Ecirc": "\u00CA", "Euml": "\u00CB",
	"Igrave": "\u00CC", "Iacute": "\u00CD", "Icirc": "\u00CE", "Iuml": "\u00CF",
	"ETH": "\u00D0", "Ntilde": "\u00D1", "Ograve": "\u00D2", "Oacute": "\u00D3",
	"Ocirc": "\u00D4", "Otilde": "\u00D5", "Ouml": "\u00D6", "times": "\u00D7",
	"Oslash": "\u00D8", "Ugrave": "\u00D9", "Uacute": "\u00DA", "Ucirc": "\u00DB",
	"Uuml": "\u00DC", "Yacute": "\u00DD", "THORN": "\u00DE", "szlig": "\u00DF",
	"agrave": "\u00E0", "aacute": "\u00E1", "acirc": "\u00E2", "atilde": "\u00E3",
	"auml": "\u00E4", "aring": "\u00E5", "aelig": "\u00E6", "ccedil": "\u00E7",
	"egrave": "\u00E8", "eacute": "\u00E9", "ecirc": "\u00EA", "euml": "\u00EB",
	"igrave": "\u00EC", "iacute": "\u00ED", "icirc": "\u00EE", "iuml": "\u00EF",
	"eth": "\u00F0", "ntilde": "\u00F1", "ograve": "\u00F2", "oacute": "\u00F3",
	"ocirc": "\u00F4", "otilde": "\u00F5", "ouml": "\u00F6", "divide": "\u00F7",
	"oslash": "\u00F8", "ugrave": "\u00F9", "uacute": "\u00FA", "ucirc": "\u00FB",
	"uuml": "\u00FC", "yacute": "\u00FD", "thorn": "\u00FE", "yuml": "\u00FF",
	"OElig": "\u0152", "oelig": "\u0153", "Scaron": "\u0160", "scaron": "\u0161",
	"Dcaron": "\u010E", "dcaron": "\u010F",
	"Yuml": "\u0178", "fnof": "\u0192",
	"ensp": "\u2002", "emsp": "\u2003", "thinsp": "\u2009",
	"zwnj": "\u200C", "zwj": "\u200D", "lrm": "\u200E", "rlm": "\u200F",
	"ndash": "\u2013", "mdash": "\u2014", "lsquo": "\u2018", "rsquo": "\u2019",
	"sbquo": "\u201A", "ldquo": "\u201C", "rdquo": "\u201D", "bdquo": "\u201E",
	"dagger": "\u2020", "Dagger": "\u2021", "bull": "\u2022", "hellip": "\u2026",
	"permil": "\u2030", "prime": "\u2032", "Prime": "\u2033", "lsaquo": "\u2039",
	"rsaquo": "\u203A", "oline": "\u203E", "euro": "\u20AC", "trade": "\u2122",
	"HilbertSpace": "\u210B", "DifferentialD": "\u2146",
	"ClockwiseContourIntegral": "\u2232",
	"larr": "\u2190", "uarr": "\u2191", "rarr": "\u2192", "darr": "\u2193",
	"harr": "\u2194", "crarr": "\u21B5",
	"forall": "\u2200", "part": "\u2202", "exist": "\u2203", "empty": "\u2205",
	"nabla": "\u2207", "isin": "\u2208", "notin": "\u2209", "ni": "\u220B",
	"prod": "\u220F", "sum": "\u2211", "minus": "\u2212", "lowast": "\u2217",
	"radic": "\u221A", "prop": "\u221D", "infin": "\u221E", "ang": "\u2220",
	"and": "\u2227", "or": "\u2228", "cap": "\u2229", "cup": "\u222A",
	"int": "\u222B", "there4": "\u2234", "sim": "\u223C", "cong": "\u2245",
	"asymp": "\u2248", "ne": "\u2260", "equiv": "\u2261", "le": "\u2264",
	"ge": "\u2265", "sub": "\u2282", "sup": "\u2283", "nsub": "\u2284",
	"sube": "\u2286", "supe": "\u2287", "oplus": "\u2295", "otimes": "\u2297",
	"perp": "\u22A5", "sdot": "\u22C5",
	"ngE": "\u2267\u0338",
	"lceil": "\u2308", "rceil": "\u2309", "lfloor": "\u230A", "rfloor": "\u230B",
	"lang": "\u27E8", "rang": "\u27E9",
	"loz": "\u25CA", "spades": "\u2660", "clubs": "\u2663",
	"hearts": "\u2665", "diams": "\u2666",
	"circ": "\u02C6", "tilde": "\u02DC",
	"Alpha": "\u0391", "Beta": "\u0392", "Gamma": "\u0393", "Delta": "\u0394",
	"Epsilon": "\u0395", "Zeta": "\u0396", "Eta": "\u0397", "Theta": "\u0398",
	"Iota": "\u0399", "Kappa": "\u039A", "Lambda": "\u039B", "Mu": "\u039C",
	"Nu": "\u039D", "Xi": "\u039E", "Omicron": "\u039F", "Pi": "\u03A0",
	"Rho": "\u03A1", "Sigma": "\u03A3", "Tau": "\u03A4", "Upsilon": "\u03A5",
	"Phi": "\u03A6", "Chi": "\u03A7", "Psi": "\u03A8", "Omega": "\u03A9",
	"alpha": "\u03B1", "beta": "\u03B2", "gamma": "\u03B3", "delta": "\u03B4",
	"epsilon": "\u03B5", "zeta": "\u03B6", "eta": "\u03B7", "theta": "\u03B8",
	"iota": "\u03B9", "kappa": "\u03BA", "lambda": "\u03BB", "mu": "\u03BC",
	"nu": "\u03BD", "xi": "\u03BE", "omicron": "\u03BF", "pi": "\u03C0",
	"rho": "\u03C1", "sigmaf": "\u03C2", "sigma": "\u03C3", "tau": "\u03C4",
	"upsilon": "\u03C5", "phi": "\u03C6", "chi": "\u03C7", "psi": "\u03C8",
	"omega": "\u03C9",
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
	reHTMLComment = regexp.MustCompile(`^<!--(?:-?>|(?s:.)*?-->)`)
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
// A closing bracket may carry either inline target info (url/title) or
// reference info (refLabel filled = full, refCollapsed = `[]`,
// refShortcut = bare `[text]`).
type inlineSeg struct {
	kind     inlineSegKind
	value    string
	char     byte
	length   int
	canOpen  bool
	canClose bool
	// Bracket-specific:
	open         bool
	image        bool
	active       bool
	url          string
	title        string
	hasURL       bool
	refLabel     string
	refCollapsed bool
	refShortcut  bool
	hasRef       bool
	// Original source text consumed for this bracket segment; rendered
	// verbatim when the segment ends up unmatched.
	srcText string
	// Position (in the enclosing inline source string) of the first
	// character after the bracket marker (openers) or of the closing
	// `]` (closers). Used to reconstruct raw label text for shortcut
	// links, whose label must be matched with backslash escapes kept
	// intact per CommonMark's label equality rule. -1 means unset.
	srcPos int
	// Opener removed from the "delimiter stack" by a preceding closer
	// that found it but failed to form a link. Subsequent closers skip
	// it entirely (per CommonMark's pop-on-fail rule).
	removed bool
}

// LinkRef is a link reference definition extracted from the block list.
type LinkRef struct {
	URL   string
	Title string
}

// LinkRefMap is label (normalized) to definition mapping.
type LinkRefMap = map[string]LinkRef

// isFlankingPunct reports whether r is punctuation or symbol per
// CommonMark's flanking classification: ASCII punctuation plus any
// Unicode character in general category P (punctuation) or S (symbol).
func isFlankingPunct(r rune) bool {
	if r < 0x80 {
		return (r >= '!' && r <= '/') ||
			(r >= ':' && r <= '@') ||
			(r >= '[' && r <= '`') ||
			(r >= '{' && r <= '~')
	}
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// isFlankingWS reports whether r is whitespace per CommonMark's
// flanking classification: ASCII whitespace plus any Unicode Zs
// (Space Separator) character such as NBSP.
func isFlankingWS(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return unicode.Is(unicode.Zs, r)
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
				urlB.WriteByte(s[k])
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
				urlB.WriteByte(s[j])
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
				titleB.WriteByte(s[k])
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

// decodeLinkText decodes backslash escapes and well-formed entity
// references in link/title text. Invalid sequences are passed through.
func decodeLinkText(s string) string {
	var b strings.Builder
	i := 0
	n := len(s)
	for i < n {
		c := s[i]
		if c == '\\' && i+1 < n && strings.IndexByte(backslashEscapable, s[i+1]) >= 0 {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == '&' {
			m := reEntityRef.FindStringIndex(s[i:])
			if m != nil {
				ref := s[i : i+m[1]]
				if decoded, ok := decodeEntity(ref); ok {
					b.WriteString(decoded)
					i += m[1]
					continue
				}
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// urlSafe reports whether r is preserved verbatim in CommonMark's URL
// normalization. Other characters are percent-encoded as UTF-8.
func urlSafe(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z':
		return true
	case r >= 'a' && r <= 'z':
		return true
	case r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '-', '.', '_', '~', '!', '$', '&', '\'', '(', ')',
		'*', '+', ',', ';', '=', ':', '@', '/', '?', '#':
		return true
	}
	return false
}

// parseReferenceLabel parses `[label]` or `[]` starting at position i.
func parseReferenceLabel(s string, i int) (string, bool, int, bool) {
	if i >= len(s) || s[i] != '[' {
		return "", false, 0, false
	}
	j := i + 1
	var lb strings.Builder
	for j < len(s) && s[j] != ']' {
		if s[j] == '\\' && j+1 < len(s) {
			lb.WriteByte(s[j])
			lb.WriteByte(s[j+1])
			j += 2
			continue
		}
		if s[j] == '[' {
			return "", false, 0, false
		}
		lb.WriteByte(s[j])
		j++
	}
	if j >= len(s) || s[j] != ']' {
		return "", false, 0, false
	}
	label := lb.String()
	collapsed := strings.TrimSpace(label) == ""
	return label, collapsed, j + 1, true
}

// normalizeLinkLabel applies CommonMark's label equality rule: strip
// leading/trailing whitespace, collapse interior whitespace to single
// spaces, case-fold. The German eszett (`ß`/`ẞ`) folds to `ss` via an
// explicit replacement. Backslash escapes are decoded so raw labels
// from parseReferenceLabel compare against decoded labels from
// parseLinkRefDef.
func normalizeLinkLabel(s string) string {
	// Backslash escapes are preserved (so `foo\!` ≠ `foo!`), per
	// CommonMark's label equality rule — see spec example 545.
	s = strings.ReplaceAll(s, "ẞ", "ss")
	s = strings.ReplaceAll(s, "ß", "ss")
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	// Collapse any run of whitespace to a single space.
	var b strings.Builder
	inWS := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inWS {
				b.WriteByte(' ')
				inWS = true
			}
			continue
		}
		b.WriteRune(r)
		inWS = false
	}
	return b.String()
}

// parseLinkRefDef attempts to match `[label]: destination[ "title"]` at
// the start of text and returns (label, url, title, length, true).
func parseLinkRefDef(text string) (string, string, string, int, bool) {
	i := 0
	// Up to 3 leading spaces.
	for i < len(text) && i < 3 && text[i] == ' ' {
		i++
	}
	if i >= len(text) || text[i] != '[' {
		return "", "", "", 0, false
	}
	i++
	var lb strings.Builder
	labelEnd := -1
	for i < len(text) {
		c := text[i]
		if c == '\n' {
			lb.WriteByte(c)
			i++
			// A link label cannot contain a blank line (two line-endings
			// separated only by whitespace). Multi-line labels are
			// otherwise fine — `[` ... `]` may wrap over several lines.
			if reLabelBlankLine.MatchString(lb.String()) {
				return "", "", "", 0, false
			}
			continue
		}
		if c == ']' {
			labelEnd = i
			break
		}
		if c == '\\' && i+1 < len(text) {
			// Preserve the backslash so the stored label matches
			// CommonMark's label equality rule, which treats `foo\!`
			// and `foo!` as distinct labels (see spec example 545).
			lb.WriteByte(text[i])
			lb.WriteByte(text[i+1])
			i += 2
			continue
		}
		if c == '[' {
			return "", "", "", 0, false
		}
		lb.WriteByte(c)
		i++
	}
	if labelEnd < 0 {
		return "", "", "", 0, false
	}
	label := lb.String()
	if strings.TrimSpace(label) == "" {
		return "", "", "", 0, false
	}
	i = labelEnd + 1
	if i >= len(text) || text[i] != ':' {
		return "", "", "", 0, false
	}
	i++
	// Optional whitespace (at most one newline).
	nls := 0
	for i < len(text) {
		c := text[i]
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		if c == '\n' || c == '\r' {
			if c == '\n' {
				nls++
				if nls > 1 {
					return "", "", "", 0, false
				}
			}
			i++
			continue
		}
		break
	}
	// URL: backslash-escapes are retained verbatim; encodeLinkUrl decodes
	// them at render time, but the backslash still protects the following
	// character from being treated as a delimiter.
	var urlB strings.Builder
	if i < len(text) && text[i] == '<' {
		k := i + 1
		for k < len(text) && text[k] != '>' && text[k] != '\n' && text[k] != '<' {
			if text[k] == '\\' && k+1 < len(text) {
				urlB.WriteByte(text[k])
				urlB.WriteByte(text[k+1])
				k += 2
				continue
			}
			urlB.WriteByte(text[k])
			k++
		}
		if k >= len(text) || text[k] != '>' {
			return "", "", "", 0, false
		}
		i = k + 1
	} else {
		for i < len(text) {
			c := text[i]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				break
			}
			if c < 0x20 || c == 0x7f {
				break
			}
			if c == '\\' && i+1 < len(text) {
				urlB.WriteByte(text[i])
				urlB.WriteByte(text[i+1])
				i += 2
				continue
			}
			urlB.WriteByte(c)
			i++
		}
		if urlB.Len() == 0 {
			return "", "", "", 0, false
		}
	}

	// Optional title. Requires whitespace between URL and opening quote.
	title := ""
	urlEndPos := i
	titleEnd := i
	j := i
	for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
		j++
	}
	if j < len(text) && text[j] == '\n' {
		j++
		for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
	}
	hadWhitespaceAfterURL := j > urlEndPos
	if hadWhitespaceAfterURL && j < len(text) && (text[j] == '"' || text[j] == '\'' || text[j] == '(') {
		openQ := text[j]
		closeQ := openQ
		if openQ == '(' {
			closeQ = ')'
		}
		k := j + 1
		var tb strings.Builder
		ok := false
		for k < len(text) {
			c := text[k]
			if c == '\\' && k+1 < len(text) {
				tb.WriteByte(text[k])
				tb.WriteByte(text[k+1])
				k += 2
				continue
			}
			if c == closeQ {
				ok = true
				break
			}
			if c == '\n' {
				// A link title may span multiple lines but cannot
				// contain a blank line. Peek past any trailing
				// spaces/tabs on the next line — if the next non-
				// whitespace character is another newline, the title
				// would span a blank line, so terminate here.
				p := k + 1
				for p < len(text) && (text[p] == ' ' || text[p] == '\t') {
					p++
				}
				if p < len(text) && text[p] == '\n' {
					break
				}
			}
			if openQ == '(' && c == '(' {
				break
			}
			tb.WriteByte(c)
			k++
		}
		if ok {
			candidate := tb.String()
			endAfter := k + 1
			m := endAfter
			valid := true
			for m < len(text) && text[m] != '\n' {
				if text[m] != ' ' && text[m] != '\t' {
					valid = false
					break
				}
				m++
			}
			if valid {
				title = candidate
				titleEnd = endAfter
			}
		}
	}

	end := titleEnd
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	if end < len(text) && text[end] != '\n' {
		if title != "" {
			// Title invalid in trailing position; back out.
			title = ""
			end = i
			for end < len(text) && (end < len(text) && (text[end] == ' ' || text[end] == '\t')) {
				end++
			}
			if end < len(text) && text[end] != '\n' {
				return "", "", "", 0, false
			}
		} else {
			return "", "", "", 0, false
		}
	}
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return label, urlB.String(), title, end, true
}

// ExtractLinkRefsAndClean pulls leading `[label]: url "title"` definitions
// from paragraph blocks and returns the ref map plus the surviving blocks.
func ExtractLinkRefsAndClean(blocks []any) (LinkRefMap, []any) {
	refs := LinkRefMap{}
	out := make([]any, 0, len(blocks))
	for _, v := range blocks {
		b, ok := v.(map[string]any)
		if !ok {
			out = append(out, v)
			continue
		}
		if t, _ := b["type"].(string); t != "paragraph" {
			out = append(out, v)
			continue
		}
		text, _ := b["text"].(string)
		for {
			label, url, title, length, matched := parseLinkRefDef(text)
			if !matched {
				break
			}
			norm := normalizeLinkLabel(label)
			if norm != "" {
				if _, exists := refs[norm]; !exists {
					refs[norm] = LinkRef{URL: url, Title: title}
				}
			}
			text = text[length:]
		}
		if len(text) > 0 {
			nb := map[string]any{}
			for k, v := range b {
				nb[k] = v
			}
			nb["text"] = text
			out = append(out, nb)
		}
	}
	return refs, out
}

// encodeLinkUrl performs CommonMark-style URL normalization: backslash
// escapes and entities are decoded (unless decodeEscapes is false, as for
// autolinks which are literal), existing percent-encoded sequences are
// preserved (uppercased), and any other character outside the safe set
// is percent-encoded as its UTF-8 byte sequence.
func encodeLinkUrl(url string, decodeEscapes ...bool) string {
	decode := true
	if len(decodeEscapes) > 0 {
		decode = decodeEscapes[0]
	}
	var decoded string
	if decode {
		decoded = decodeLinkText(url)
	} else {
		decoded = url
	}
	var b strings.Builder
	i := 0
	for i < len(decoded) {
		// Preserve a well-formed `%XX` percent-encoded byte.
		if decoded[i] == '%' && i+2 < len(decoded) &&
			isHexDigit(decoded[i+1]) && isHexDigit(decoded[i+2]) {
			b.WriteByte('%')
			b.WriteByte(upperHex(decoded[i+1]))
			b.WriteByte(upperHex(decoded[i+2]))
			i += 3
			continue
		}
		if decoded[i] == '&' {
			b.WriteString("&amp;")
			i++
			continue
		}
		r, size := decodeRune(decoded, i)
		if size == 1 && r < 128 && urlSafe(r) {
			b.WriteByte(byte(r))
			i++
			continue
		}
		// Percent-encode the UTF-8 bytes for this character.
		for k := 0; k < size; k++ {
			fmt.Fprintf(&b, "%%%02X", decoded[i+k])
		}
		i += size
	}
	return b.String()
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func upperHex(b byte) byte {
	if b >= 'a' && b <= 'f' {
		return b - 32
	}
	return b
}

// decodeRune returns the rune at i and its UTF-8 byte length. Uses
// utf8.DecodeRuneInString to honor multi-byte characters.
func decodeRune(s string, i int) (rune, int) {
	if i >= len(s) {
		return 0, 0
	}
	b := s[i]
	if b < 0x80 {
		return rune(b), 1
	}
	// Minimal UTF-8 decoder.
	switch {
	case b&0xE0 == 0xC0 && i+1 < len(s):
		r := rune(b&0x1F)<<6 | rune(s[i+1]&0x3F)
		return r, 2
	case b&0xF0 == 0xE0 && i+2 < len(s):
		r := rune(b&0x0F)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F)
		return r, 3
	case b&0xF8 == 0xF0 && i+3 < len(s):
		r := rune(b&0x07)<<18 | rune(s[i+1]&0x3F)<<12 | rune(s[i+2]&0x3F)<<6 | rune(s[i+3]&0x3F)
		return r, 4
	}
	return rune(b), 1
}

// scanFollowingBracketPair checks whether the segments at `start` form a
// `[label]` or `[]` bracket pair and, if so, returns its inner-text label
// (empty for `[]`) and the index of the closing bracket segment.
func scanFollowingBracketPair(segs []*inlineSeg, start int) (string, int, bool) {
	if start >= len(segs) {
		return "", 0, false
	}
	open := segs[start]
	if open.kind != segBracket || !open.open || open.image {
		return "", 0, false
	}
	depth := 1
	for j := start + 1; j < len(segs); j++ {
		s := segs[j]
		if s.kind == segBracket {
			if s.open {
				depth++
			} else {
				depth--
				if depth == 0 {
					inner := segs[start+1 : j]
					return innerText(inner), j, true
				}
			}
		}
	}
	return "", 0, false
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
			} else if len(s.srcText) > 1 {
				// Unmatched inline / ref-style closer: render the full
				// source text so the suffix (e.g. `](uri2)`) remains
				// visible in the outer alt string (spec example 520).
				b.WriteString(s.srcText)
			} else {
				b.WriteString("]")
			}
		case segHTML:
			// Strip tags, but pull `alt="..."` from `<img>` so nested
			// images contribute their alt text to the outer alt.
			stripped := reImgAltAttr.ReplaceAllString(s.value, "$1")
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

var reImgAltAttr = regexp.MustCompile(`<img\s[^>]*\balt="([^"]*)"[^>]*>`)

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
					value: `<a href="` + encodeLinkUrl(url, false) + `">` + escapeHTMLString(url) + `</a>`,
				})
				i += m[1]
				continue
			}
			if m := reAutolinkEmail.FindStringSubmatchIndex(rest); m != nil && m[0] == 0 {
				email := rest[m[2]:m[3]]
				segs = append(segs, &inlineSeg{
					kind:  segHTML,
					value: `<a href="mailto:` + encodeLinkUrl(email, false) + `">` + escapeHTMLString(email) + `</a>`,
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
				kind:    segBracket,
				open:    true,
				image:   true,
				active:  true,
				srcText: "![",
				srcPos:  i + 2,
			})
			i += 2
			continue
		}

		// Link open `[`.
		if c == '[' {
			segs = append(segs, &inlineSeg{
				kind:    segBracket,
				open:    true,
				image:   false,
				active:  true,
				srcText: "[",
				srcPos:  i + 1,
			})
			i++
			continue
		}

		// Link close `]` — try inline target, then reference label,
		// then fall back to a shortcut reference marker.
		if c == ']' {
			if url, title, end, ok := parseLinkTarget(s, i+1); ok {
				segs = append(segs, &inlineSeg{
					kind:    segBracket,
					open:    false,
					active:  true,
					url:     url,
					title:   title,
					hasURL:  true,
					srcText: s[i:end],
					srcPos:  i,
				})
				i = end
				continue
			}
			if label, collapsed, end, ok := parseReferenceLabel(s, i+1); ok {
				seg := &inlineSeg{
					kind:         segBracket,
					open:         false,
					active:       true,
					refCollapsed: collapsed,
					hasRef:       true,
					srcText:      s[i:end],
					srcPos:       i,
				}
				if !collapsed {
					seg.refLabel = label
				}
				segs = append(segs, seg)
				i = end
				continue
			}
			segs = append(segs, &inlineSeg{
				kind:        segBracket,
				open:        false,
				active:      true,
				refShortcut: true,
				hasRef:      true,
				srcText:     "]",
				srcPos:      i,
			})
			i++
			continue
		}

		// Emphasis delimiter run.
		if c == '*' || c == '_' {
			length := 1
			for i+length < n && s[i+length] == c {
				length++
			}
			// Flanking classification needs the full rune before/after
			// the delimiter run, not just the adjacent byte — otherwise
			// multi-byte characters (e.g. NBSP, £, €, Cyrillic letters)
			// would be mistaken for ASCII non-space non-punctuation.
			var before rune = ' '
			if i > 0 {
				r, _ := utf8.DecodeLastRuneInString(s[:i])
				before = r
			}
			var after rune = ' '
			if i+length < n {
				r, _ := utf8.DecodeRuneInString(s[i+length:])
				after = r
			}
			beforeWS := isFlankingWS(before)
			afterWS := isFlankingWS(after)
			beforeP := isFlankingPunct(before)
			afterP := isFlankingPunct(after)

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

		// Hard line break via 2+ trailing spaces, or plain newline. A
		// single trailing space/tab before a newline is insignificant per
		// CommonMark — strip it before emitting the newline.
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
				if last.kind == segText &&
					(strings.HasSuffix(last.value, " ") || strings.HasSuffix(last.value, "\t")) {
					last.value = strings.TrimRight(last.value, " \t")
					if last.value == "" {
						segs = segs[:len(segs)-1]
					}
				}
			}
			appendText("\n")
			i++
			continue
		}

		// Plain character — consume a full UTF-8 rune so multi-byte
		// characters survive as-is rather than being split into the
		// individual bytes of their UTF-8 encoding.
		if c < 0x80 {
			appendText(string(c))
			i++
		} else {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size <= 1 {
				appendText(string(c))
				i++
			} else {
				appendText(s[i : i+size])
				i += size
			}
		}
	}

	return segs
}

// processLinks walks the segment list forward, matching each link/image
// closing bracket with the most recent active opener. Inline links
// (carrying a parsed URL) resolve directly; reference-style brackets
// resolve their label through the supplied refs map.
func processLinks(segs []*inlineSeg, refs LinkRefMap, src string) []*inlineSeg {
	// rawLabel extracts the original source text between an opener
	// bracket and its matching closer `]`, used for shortcut/collapsed
	// reference matching where CommonMark preserves backslash escapes
	// literally (so `[foo\!]` ≠ `[foo!]`). Falls back to the decoded
	// inner text when positions are unavailable.
	rawLabel := func(op, cl *inlineSeg, inner []*inlineSeg) string {
		if src != "" && op.srcPos >= 0 && cl.srcPos >= 0 &&
			op.srcPos <= cl.srcPos && cl.srcPos <= len(src) {
			return src[op.srcPos:cl.srcPos]
		}
		return innerText(inner)
	}
	i := 0
	for i < len(segs) {
		close := segs[i]
		if close.kind != segBracket || close.open {
			i++
			continue
		}

		// Per CM § 6.4: pop the topmost (nearest) opener that is still
		// on the stack (`!removed`). If it's inactive, discard it and
		// render this `]` as literal; further closers then cannot reach
		// any still-active opener behind it (spec example 520).
		openIdx := -1
		nearestIdx := -1
		for j := i - 1; j >= 0; j-- {
			cand := segs[j]
			if cand.kind == segBracket && cand.open && !cand.removed {
				nearestIdx = j
				if cand.active {
					openIdx = j
				}
				break
			}
		}
		if openIdx < 0 && nearestIdx >= 0 {
			segs[nearestIdx].removed = true
		}

		var op *inlineSeg
		var inner []*inlineSeg
		if openIdx >= 0 {
			op = segs[openIdx]
			inner = append([]*inlineSeg{}, segs[openIdx+1:i]...)
		}

		var url, title string
		hasTarget := false
		consumeThrough := i
		if openIdx >= 0 {
			switch {
			case close.hasURL:
				url = close.url
				title = close.title
				hasTarget = true
			case close.refLabel != "" && refs != nil:
				if ref, ok := refs[normalizeLinkLabel(close.refLabel)]; ok {
					url = ref.URL
					title = ref.Title
					hasTarget = true
				}
			case close.refCollapsed && refs != nil:
				if ref, ok := refs[normalizeLinkLabel(rawLabel(op, close, inner))]; ok {
					url = ref.URL
					title = ref.Title
					hasTarget = true
				}
			case refs != nil:
				// Shortcut candidate. If followed by `[label]` or `[]`,
				// that's a full/collapsed ref attempt which wins over
				// shortcut — even if the label doesn't resolve, the
				// shortcut is NOT tried.
				if label, endIdx, ok := scanFollowingBracketPair(segs, i+1); ok {
					useLabel := label
					if label == "" {
						useLabel = rawLabel(op, close, inner)
					}
					if ref, ok2 := refs[normalizeLinkLabel(useLabel)]; ok2 {
						url = ref.URL
						title = ref.Title
						hasTarget = true
						consumeThrough = endIdx
					}
				} else {
					if ref, ok := refs[normalizeLinkLabel(rawLabel(op, close, inner))]; ok {
						url = ref.URL
						title = ref.Title
						hasTarget = true
					}
				}
			}
		}

		if !hasTarget {
			// On no match, if this close bracket consumed a `[label]` or
			// `[]` reference suffix, split it back into separate bracket
			// segments so the enclosed `[label]` can still form its own
			// link in a later iteration.
			if close.refLabel != "" || close.refCollapsed {
				suffix := close.srcText
				if len(suffix) > 0 {
					suffix = suffix[1:] // drop leading `]`
				}
				// Remember closer position in `src` so we can shift the
				// re-tokenized segments' `srcPos` fields to still refer
				// into the outer source string (rawLabel relies on it).
				suffixStart := -1
				if close.srcPos >= 0 {
					suffixStart = close.srcPos + 1
				}
				close.refLabel = ""
				close.refCollapsed = false
				close.refShortcut = true
				close.srcText = "]"
				if len(suffix) > 0 {
					extra := tokenizeInline(suffix)
					if suffixStart >= 0 {
						for _, s2 := range extra {
							if s2.kind == segBracket && s2.srcPos >= 0 {
								s2.srcPos += suffixStart
							}
						}
					}
					tail := append([]*inlineSeg{}, segs[i+1:]...)
					segs = append(segs[:i+1], append(extra, tail...)...)
				}
			}
			if op != nil {
				// Pop this opener off the delimiter stack so a later
				// `]` can't sneak back to an earlier active opener
				// (spec example 520).
				op.active = false
				op.removed = true
			}
			i++
			continue
		}

		inner = processLinks(inner, refs, src)
		inner = processEmphasis(inner)

		var html string
		if op.image {
			alt := innerText(inner)
			titleAttr := ""
			if title != "" {
				titleAttr = ` title="` + escapeHTMLString(decodeLinkText(title)) + `"`
			}
			html = `<img src="` + encodeLinkUrl(url) + `" alt="` +
				escapeHTMLString(alt) + `"` + titleAttr + " />"
		} else {
			inside := renderSegments(inner)
			titleAttr := ""
			if title != "" {
				titleAttr = ` title="` + escapeHTMLString(decodeLinkText(title)) + `"`
			}
			html = `<a href="` + encodeLinkUrl(url) + `"` + titleAttr +
				">" + inside + "</a>"
		}

		replacement := &inlineSeg{kind: segHTML, value: html}
		tail := append([]*inlineSeg{}, segs[consumeThrough+1:]...)
		segs = append(segs[:openIdx], append([]*inlineSeg{replacement}, tail...)...)

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
		// Per CM rule 15, any delimiter segments that ended up inside
		// this matched pair are removed from the delimiter stack —
		// they can no longer match further closers. Their characters
		// still render as literals via renderSegments.
		for _, s2 := range inner {
			if s2.kind == segDelim {
				s2.canOpen = false
				s2.canClose = false
			}
		}
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
			// Render the original source verbatim (escaped) when
			// unmatched.
			if s.srcText != "" {
				b.WriteString(escapeHTMLString(s.srcText))
			} else {
				// (Should not happen, but keep safe fallback.)
			}
		}
	}
	return b.String()
}

// renderInline processes block-level text as inline markdown with optional
// link reference map. Handles the full inline repertoire (escapes,
// entities, hard breaks, code spans, inline and reference links/images,
// autolinks, raw HTML, emphasis and strong).
func renderInline(s string, refs LinkRefMap) string {
	segs := tokenizeInline(s)
	segs = processLinks(segs, refs, s)
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

// GatherAllLinkRefs walks the top-level blocks (and recursively through
// blockquote content and list item content) and collects every link
// reference definition. Refs defined inside a blockquote or list item
// are visible to the document as a whole.
func GatherAllLinkRefs(blocks []any) LinkRefMap {
	refs := LinkRefMap{}
	absorb := func(b map[string]any) {
		defs, _ := b["_implicitRefDefs"].([]map[string]string)
		for _, d := range defs {
			norm := normalizeLinkLabel(d["label"])
			if norm == "" {
				continue
			}
			if _, ok := refs[norm]; !ok {
				refs[norm] = LinkRef{URL: d["url"], Title: d["title"]}
			}
		}
	}
	var visit func(bs []any)
	visit = func(bs []any) {
		for _, v := range bs {
			b, ok := v.(map[string]any)
			if !ok {
				continue
			}
			// Implicit defs stashed on the block by @setext-promote.
			absorb(b)
			switch b["type"] {
			case "paragraph":
				text, _ := b["text"].(string)
				for {
					label, url, title, length, matched := parseLinkRefDef(text)
					if !matched {
						break
					}
					norm := normalizeLinkLabel(label)
					if norm != "" {
						if _, exists := refs[norm]; !exists {
							refs[norm] = LinkRef{URL: url, Title: title}
						}
					}
					text = text[length:]
				}
			case "blockquote":
				text, _ := b["text"].(string)
				visit(parseNested(text))
			case "list":
				items, _ := b["items"].([]any)
				for _, it := range items {
					m, _ := it.(map[string]any)
					text, _ := m["text"].(string)
					visit(parseNested(text))
				}
			}
		}
	}
	visit(blocks)
	return refs
}

// ToHTML gathers link reference definitions from the full block tree,
// extracts top-level defs to drop defs-only paragraphs, and renders
// each surviving block against the merged ref map.
func ToHTML(blocks []any) string {
	allRefs := GatherAllLinkRefs(blocks)
	topRefs, cleaned := ExtractLinkRefsAndClean(blocks)
	merged := LinkRefMap{}
	for k, v := range allRefs {
		merged[k] = v
	}
	for k, v := range topRefs {
		merged[k] = v
	}
	var b strings.Builder
	for _, v := range cleaned {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		h := renderBlockHTML(m, merged)
		if h != "" {
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

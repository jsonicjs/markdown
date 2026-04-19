/* Copyright (c) 2021-2025 Richard Rodger, MIT License */

// Import Jsonic types used by plugins.
import {
  Jsonic,
  Rule,
  Plugin,
  Context,
  Config,
  Options,
  Lex,
} from 'jsonic'

// See defaults below for commentary.
type MarkdownOptions = {
  codeFence: string
  trim: boolean
  html: boolean
}

// The data structure produced by this plugin.
type MdBlock =
  | { type: 'heading'; level: number; text: string; html?: string }
  | { type: 'paragraph'; text: string; html?: string }
  | { type: 'code'; lang: string; text: string; html?: string }
  | {
      type: 'list'
      ordered: boolean
      items: { text: string }[]
      loose?: boolean
      start?: number
      html?: string
    }
  | { type: 'blockquote'; text: string; html?: string }
  | { type: 'hr'; html?: string }
  | { type: 'html'; text: string; html?: string }

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
#   consecutive #MT  -> paragraph (lines joined with \\n)
#   consecutive #ML  -> list      (items accumulated)
#   consecutive #MQ  -> blockquote (lines joined with \\n)
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

// Plugin implementation.
const Markdown: Plugin = (jsonic: Jsonic, options: MarkdownOptions) => {

  // Register the custom line-scanning lexer. Low order runs before built-ins so
  // it owns the source stream and emits one token per markdown line.
  jsonic.options({
    rule: {
      start: 'md',
      exclude: 'jsonic,imp',
    },
    number: { lex: false },
    value: { lex: false },
    comment: { lex: false },
    string: { lex: false },
    line: { lex: false },
    space: { lex: false },
    text: { lex: false },
    lex: {
      emptyResult: [],
      match: {
        markdown: { order: 1, make: buildMarkdownLineMatcher(options) },
      },
    },
  })

  const emitHtml = !!options.html

  // Named function references for declarative grammar definition.
  const refs: Record<string, Function> = {

    // === State actions (auto-wired by @rulename-{bo,ao,bc,ac} convention) ===

    // Initialize the result array at the top of parsing.
    '@md-bo': (r: Rule) => {
      r.node = []
    },

    // Mark that a blank line separated the previous block from whatever
    // block the `block` rule is about to open. Each block-start action
    // consumes this flag and stashes `_precededByBlank: true` on the
    // newly-opened block so renderListItem can determine loose-vs-tight
    // dynamically.
    '@block-blank-seen': (_r: Rule, ctx: Context) => {
      ctx.u.blockBlankSeen = true
    },


    // === Alt actions ===

    '@heading': (r: Rule, ctx: Context) => {
      const v = r.o0.val as { level: number; text: string }
      const block: any = { type: 'heading', level: v.level, text: v.text }
      markPrecededByBlank(block, ctx)
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
    },

    '@hr': (r: Rule, ctx: Context) => {
      const block: any = { type: 'hr' }
      markPrecededByBlank(block, ctx)
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
    },

    '@code': (r: Rule, ctx: Context) => {
      const v = r.o0.val as { lang: string; text: string }
      const block: any = { type: 'code', lang: v.lang, text: v.text }
      markPrecededByBlank(block, ctx)
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
    },

    '@htmlblock': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block: any = { type: 'html', text: v }
      markPrecededByBlank(block, ctx)
      if (emitHtml) block.html = v
      r.node.push(block)
    },

    '@list-start': (r: Rule, ctx: Context) => {
      const v = r.o0.val as {
        ordered: boolean
        text: string
        start?: number
        markerId?: string
      }
      const block: any = {
        type: 'list',
        ordered: v.ordered,
        items: [{ text: v.text }],
      }
      Object.defineProperty(block, '_markerId', {
        value: v.markerId,
        enumerable: false,
        writable: true,
      })
      if (v.ordered && v.start !== undefined && v.start !== 1) {
        block.start = v.start
      }
      markPrecededByBlank(block, ctx)
      r.node.push(block)
      ctx.u.mdCurrent = block
      // Reset blank-tracking so a pending blank left over from a prior
      // sibling list doesn't spuriously promote this new list to loose.
      ctx.u.listPendingBlank = false
      ctx.u.listPendingBlanks = 0
    },

    '@list-append': (r: Rule, ctx: Context) => {
      const v = r.o0.val as {
        ordered: boolean
        text: string
        start?: number
        markerId?: string
      }
      const block = ctx.u.mdCurrent
      if (v.markerId !== undefined && v.markerId !== block._markerId) {
        const newBlock: any = {
          type: 'list',
          ordered: v.ordered,
          items: [{ text: v.text }],
        }
        Object.defineProperty(newBlock, '_markerId', {
          value: v.markerId,
          enumerable: false,
          writable: true,
        })
        if (v.ordered && v.start !== undefined && v.start !== 1) {
          newBlock.start = v.start
        }
        r.node.push(newBlock)
        ctx.u.mdCurrent = newBlock
        ctx.u.listPendingBlank = false
        ctx.u.listPendingBlanks = 0
        return
      }
      if (ctx.u.listPendingBlank) {
        block.loose = true
        ctx.u.listPendingBlank = false
        ctx.u.listPendingBlanks = 0
      }
      block.items.push({ text: v.text })
    },

    '@list-cont': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block = ctx.u.mdCurrent
      const items = block.items
      const last = items[items.length - 1]
      const pendingBlanks = (ctx.u.listPendingBlanks as number) || 0
      if (ctx.u.listPendingBlank) {
        // Preserve the exact number of blank lines so nested code fences
        // and other constructs render with the right internal spacing.
        // Looseness is decided at render time based on whether the
        // sub-parsed item has top-level blocks separated by blanks.
        last.text += '\n'.repeat(pendingBlanks + 1) + v
        ctx.u.listPendingBlank = false
        ctx.u.listPendingBlanks = 0
      } else if (last.text.length === 0) {
        last.text = v
      } else {
        last.text += '\n' + v
      }
    },

    // A blank line inside a list counts the pending blanks so a
    // subsequent item or continuation can use them.
    '@list-blank': (_r: Rule, ctx: Context) => {
      ctx.u.listPendingBlank = true
      ctx.u.listPendingBlanks =
        ((ctx.u.listPendingBlanks as number) || 0) + 1
    },

    // list-tail-blank fell through: no further item / continuation / blank
    // consumed the dangling blank(s). The list ends here and the blank
    // effectively separates the list from whatever block follows, so the
    // next block should render as preceded-by-blank.
    '@list-blank-escape': (_r: Rule, ctx: Context) => {
      if (ctx.u.listPendingBlank) {
        ctx.u.blockBlankSeen = true
      }
    },

    // A plain #MT line inside list-tail is a lazy continuation of the
    // current item's paragraph (the line was not indented enough to
    // become an MLC). Append it to the last item's text; the sub-parse
    // handles turning it into paragraph continuation content.
    '@list-lazy': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block = ctx.u.mdCurrent
      const items = block.items
      const last = items[items.length - 1]
      // CM: a lazy continuation line cannot start a new block inside
      // the list item's paragraph. Re-indent with four spaces so the
      // item's sub-parse treats the line as lazy paragraph text rather
      // than a fresh list / heading / fence (spec example 312).
      const line = '    ' + v
      if (last.text.length === 0) last.text = line
      else last.text += '\n' + line
    },


    '@quote-start': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block: any = { type: 'blockquote', text: v }
      markPrecededByBlank(block, ctx)
      r.node.push(block)
      ctx.u.mdCurrent = block
      // HTML is deferred until toHtml runs so that the nested parse the
      // blockquote renderer performs cannot re-enter during the outer
      // parse and disrupt grammar state.
    },

    '@quote-append': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      ctx.u.mdCurrent.text += '\n' + v
    },

    // A `===` / `---` line inside a blockquote cannot attach as a
    // setext underline to a lazy-continuation paragraph (CM § 4.3).
    // Fold it onto the blockquote's accumulated text prefixed with
    // four spaces so the blockquote sub-parse sees the line as a
    // lazy continuation of the paragraph rather than a fresh setext
    // underline (example 93).
    '@quote-lazy-msx': (r: Rule, ctx: Context) => {
      const v = r.o0.val as { level: number; raw: string }
      ctx.u.mdCurrent.text += '\n    ' + v.raw.trimStart()
    },

    // Guard: lazy continuation of a blockquote paragraph applies only
    // when the blockquote has an open paragraph — the accumulated text
    // is non-empty AND its last line is not itself indented code
    // (which would signal the inner paragraph has closed).
    '@quote-lazy-para-ok': (_r: Rule, ctx: Context) => {
      const text = (ctx.u.mdCurrent?.text ?? '') as string
      if (text.length === 0 || /\n$/.test(text)) return false
      // Find the last line; if it's 4+ spaces indented OR empty,
      // there's no open paragraph to continue.
      const lastNL = text.lastIndexOf('\n')
      const lastLine = lastNL < 0 ? text : text.slice(lastNL + 1)
      if (lastLine.length === 0) return false
      if (/^(?:    |\t)/.test(lastLine)) return false
      return true
    },

    // Indented line inside a blockquote without strict `>` prefix: a
    // lazy continuation of the blockquote's open paragraph (spec
    // example 238). Re-indent the stripped content with four spaces
    // so the blockquote's sub-parse treats it as lazy paragraph text
    // rather than as a block start (list, heading, etc.).
    '@quote-lazy-mic': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      ctx.u.mdCurrent.text += '\n    ' + v
    },

    // Lazy continuation of a blockquote's paragraph is only allowed when
    // the blockquote's accumulated content does not already end with an
    // empty line (i.e. a prior `>` with no content ended the paragraph).
    '@quote-lazy-ok': (_r: Rule, ctx: Context) => {
      const text = (ctx.u.mdCurrent?.text ?? '') as string
      if (/\n$/.test(text)) return false
      // Lazy continuation only applies to an open paragraph. If the
      // blockquote's last line itself opens a non-paragraph block
      // (fenced code, ATX heading, thematic break, HTML block, etc.)
      // then a following line without `>` must terminate the quote
      // instead of lazily joining (spec example 237).
      const lastNL = text.lastIndexOf('\n')
      const lastLine = lastNL < 0 ? text : text.slice(lastNL + 1)
      if (/^ {0,3}(?:`{3,}|~{3,})/.test(lastLine)) return false
      return true
    },

    '@para-start': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block: any = {
        type: 'paragraph',
        text: options.trim ? v.trim() : v,
      }
      markPrecededByBlank(block, ctx)
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
      ctx.u.mdCurrent = block
    },

    '@para-append': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      ctx.u.mdCurrent.text += '\n' + (options.trim ? v.trim() : v)
      if (emitHtml) ctx.u.mdCurrent.html = renderHtml(ctx.u.mdCurrent)
    },

    // Guard: the current paragraph has promotable content (something
    // beyond leading link-reference definitions). Only when this holds
    // does the setext underline actually produce a heading.
    '@setext-has-content': (_r: Rule, ctx: Context) => {
      const block = ctx.u.mdCurrent
      let text = block?.text ?? ''
      while (true) {
        const def = parseLinkRefDef(text)
        if (!def) break
        text = text.slice(def.length)
      }
      return text.trim().length > 0
    },

    // Setext underline encountered while accumulating a paragraph: rewrite
    // the current block in place as a heading. The grammar drops out of
    // para-tail after this action, so the underline token is consumed.
    '@setext-promote': (_r: Rule, ctx: Context) => {
      const { level } = r_o0_val(_r) as { level: number }
      const block = ctx.u.mdCurrent
      // Strip leading link reference definitions. Definitions that
      // were consumed are stashed on a non-enumerable `_implicitRefDefs`
      // property of the block so the document-level ref gathering picks
      // them up even though the enclosing paragraph became a heading.
      let text = block.text
      const defs: { label: string; url: string; title: string }[] = []
      while (true) {
        const def = parseLinkRefDef(text)
        if (!def) break
        defs.push({ label: def.label, url: def.url, title: def.title })
        text = text.slice(def.length)
      }
      if (defs.length > 0) {
        Object.defineProperty(block, '_implicitRefDefs', {
          value: defs,
          enumerable: false,
          writable: true,
        })
      }
      text = text.trim()
      block.type = 'heading'
      block.level = level
      block.text = text
    },

    // Setext underline encountered when the paragraph is entirely link
    // reference definitions — the underline cannot promote to a heading
    // (nothing to promote), so fold the underline's raw text onto the
    // paragraph as its next line and keep accumulating paragraph content.
    '@setext-as-para': (r: Rule, ctx: Context) => {
      const v = r.o0.val as { level: number; raw: string }
      const block = ctx.u.mdCurrent
      const line = options.trim ? v.raw.trim() : v.raw
      block.text = block.text.length === 0 ? line : block.text + '\n' + line
      if (emitHtml) block.html = renderHtml(block)
    },

    '@icode-start': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block: any = { type: 'code', lang: '', text: v }
      markPrecededByBlank(block, ctx)
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
      ctx.u.mdCurrent = block
      ctx.u.icodePendingBlanks = 0
    },

    '@icode-append': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const pending = (ctx.u.icodePendingBlanks as number) || 0
      ctx.u.mdCurrent.text += '\n'.repeat(pending + 1) + v
      ctx.u.icodePendingBlanks = 0
      if (emitHtml) ctx.u.mdCurrent.html = renderHtml(ctx.u.mdCurrent)
    },

    '@icode-blank': (_r: Rule, ctx: Context) => {
      ctx.u.icodePendingBlanks = ((ctx.u.icodePendingBlanks as number) || 0) + 1
    },
  }

  // Helper used by @setext-promote so the body reads naturally.
  function r_o0_val(r: Rule): any {
    return r.o0.val
  }

  // If the `block` rule just consumed a `#MB`, stamp the newly-opened
  // block as `_precededByBlank` (non-enumerable) and clear the flag.
  // Used by list rendering to decide loose-vs-tight dynamically: a list
  // item whose sub-parse has top-level blocks separated by blanks
  // forces the enclosing list to render loose, whereas blanks that
  // belong to nested content stay inside the nested block.
  function markPrecededByBlank(block: any, ctx: Context): void {
    if (ctx.u.blockBlankSeen) {
      Object.defineProperty(block, '_precededByBlank', {
        value: true,
        enumerable: false,
        writable: true,
      })
      ctx.u.blockBlankSeen = false
    }
  }


  // Parse embedded grammar definition using a separate standard Jsonic instance,
  // attach the refs map, and register with the current instance.
  const grammarDef = Jsonic.make()(grammarText)
  grammarDef.ref = refs
  jsonic.grammar(grammarDef)
}


// computeListItem determines the effective content column and first-line
// text for a list item. Per CommonMark § 5.2, if the marker is followed by
// 1-4 spaces the content column is (marker_end + spaces); if 5+ spaces,
// the content column is (marker_end + 1) and the rest of the whitespace
// becomes part of the item's content (forming an indented code block).
// If the content after the marker is empty/whitespace-only, the content
// column is (marker_end + 1) regardless of trailing space count.
function computeListItem(
  markerEnd: number,
  spacesAfter: number,
  rest: string,
): { contentCol: number; text: string } {
  if (rest.length === 0) {
    return { contentCol: markerEnd + 1, text: '' }
  }
  if (spacesAfter >= 5) {
    return {
      contentCol: markerEnd + 1,
      text: ' '.repeat(spacesAfter - 1) + rest,
    }
  }
  return { contentCol: markerEnd + spacesAfter, text: rest }
}

// stripIndent removes up to 3 leading spaces from a line. CommonMark allows
// that much indentation on most block-level constructs before they count as
// indented code.
function stripIndent(s: string): string {
  let i = 0
  while (i < 3 && i < s.length && s[i] === ' ') i++
  return s.slice(i)
}

// expandMarkerTabs normalises the spaces/tabs that follow a list marker.
// Per CommonMark § 2.2 and spec example 7, tabs in the whitespace that
// separates the marker from content expand to virtual spaces (up to the
// next 4-column tab stop). The "indented code in item" rule (spec § 5.2
// rule 2) says that if this virtual width is >= 5 or empty/blank, the
// marker's effective padding is 1 and the REMAINING virtual spaces stay
// in the content — this is how `-\t\tfoo` produces a `<pre><code>  foo`
// indented code block inside the item.
function expandMarkerTabs(
  padding: string,
  rest: string,
  markerEndCol: number,
): { spaces: number; rest: string } {
  let col = markerEndCol
  let virtual = 0
  for (let i = 0; i < padding.length; i++) {
    const ch = padding[i]
    if (ch === ' ') {
      virtual++
      col++
    } else if (ch === '\t') {
      const fill = 4 - (col % 4)
      virtual += fill
      col += fill
    }
  }
  if (virtual >= 5 || rest.length === 0) {
    // Padding becomes exactly one column; the surplus spills into the
    // content so its further tab expansion starts at the right column.
    return { spaces: 1, rest: ' '.repeat(virtual - 1) + rest }
  }
  return { spaces: virtual, rest }
}

// expandLeadingTabs replaces tabs in the line's leading whitespace with
// spaces that advance to the next tab stop at multiples of 4. Non-leading
// tabs are preserved, matching CommonMark's rule that tabs behave as
// spaces for the purposes of block-structure classification but keep
// their literal value inside code content.
function expandLeadingTabs(s: string): string {
  let out = ''
  let col = 0
  let i = 0
  while (i < s.length) {
    const c = s[i]
    if (c === ' ') {
      out += ' '
      col++
      i++
    } else if (c === '\t') {
      const spaces = 4 - (col % 4)
      out += ' '.repeat(spaces)
      col += spaces
      i++
    } else {
      break
    }
  }
  return out + s.slice(i)
}

// HTML block recognition tables (CommonMark § 4.6). We implement types 1-5
// (distinct end markers), type 6 (block-level tag names, terminated by a
// blank line), and type 7 (any well-formed tag on a line by itself,
// terminated by a blank line).
const HTML_BLOCK_T1_OPEN =
  /^ {0,3}<(?:script|pre|style|textarea)(?:[\s>]|$)/i
const HTML_BLOCK_T1_CLOSE = /<\/(?:script|pre|style|textarea)>/i
const HTML_BLOCK_T2_OPEN = /^ {0,3}<!--/
const HTML_BLOCK_T2_CLOSE = /-->/
const HTML_BLOCK_T3_OPEN = /^ {0,3}<\?/
const HTML_BLOCK_T3_CLOSE = /\?>/
const HTML_BLOCK_T4_OPEN = /^ {0,3}<![A-Za-z]/
const HTML_BLOCK_T4_CLOSE = />/
const HTML_BLOCK_T5_OPEN = /^ {0,3}<!\[CDATA\[/
const HTML_BLOCK_T5_CLOSE = /\]\]>/
// Type 6: specific block-level tag names. The tag may be followed by any
// content on the same line; the block terminates on a blank line.
const HTML_BLOCK_T6_OPEN =
  /^ {0,3}<\/?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?:\s|\/?>|$)/i
// Type 7: any open or close tag on a line by itself (no trailing content).
const HTML_BLOCK_T7_OPEN =
  /^ {0,3}(?:<[a-zA-Z][a-zA-Z0-9-]*(?:\s+[a-zA-Z_:][a-zA-Z0-9_.:-]*(?:\s*=\s*(?:[^\s"'=<>`]+|'[^']*'|"[^"]*"))?)*\s*\/?>|<\/[a-zA-Z][a-zA-Z0-9-]*\s*>)[ \t]*$/

// Build the line-level lexer matcher. It scans one markdown line at a time
// (including the trailing newline) and emits a token classified by block kind.
// For fenced code blocks, it also consumes content lines through to the closing
// fence and emits a single #MC token containing the language and body text.
function buildMarkdownLineMatcher(options: MarkdownOptions) {
  const fence = options.codeFence

  return function makeMarkdownLineMatcher(_cfg: Config, _opts: Options) {
    return function markdownLineMatcher(lex: Lex) {
      const { pnt, src } = lex
      let sI = pnt.sI
      let rI = pnt.rI
      let cI = pnt.cI
      const srclen = src.length

      // No input left: let Jsonic emit #ZZ.
      if (sI >= srclen) return undefined

      // Per-parse state is attached to the lex instance so it resets on
      // each new parse. mdLast tracks the last emitted token kind for
      // context-sensitive classification (indented code vs. paragraph
      // continuation, setext underline recognition, list continuation).
      const lexAny = lex as any
      const state: {
        last: string
        listContentCol: number
        listMarkerId?: string
        listItemEmpty?: boolean
      } =
        lexAny.__md ??
        (lexAny.__md = { last: 'start', listContentCol: 0 })

      // Read the current line (exclusive of trailing \n).
      let lineEnd = sI
      while (lineEnd < srclen && src[lineEnd] !== '\n') lineEnd++
      let line = src.substring(sI, lineEnd)
      let consumeEnd = lineEnd < srclen ? lineEnd + 1 : lineEnd

      // Handle CRLF by stripping the \r from the captured line content.
      let lineContent = line
      if (lineContent.endsWith('\r')) {
        lineContent = lineContent.slice(0, -1)
      }
      // Expand leading tabs so block-structure classification sees
      // effective column positions.
      lineContent = expandLeadingTabs(lineContent)

      let tkn
      let srcPart: string
      let kind = 'text'

      // Classify the line.

      // Blank line. If we're currently inside an indented-code block
      // and this "blank" line actually has at least 4 leading
      // whitespace chars, preserve the stripped content as a code
      // line instead of as a blank.
      if (/^[ \t]*$/.test(lineContent)) {
        if (
          state.last === 'icode' &&
          (lineContent.startsWith('    ') || lineContent.startsWith('\t'))
        ) {
          const stripped = lineContent.startsWith('\t')
            ? lineContent.slice(1)
            : lineContent.slice(4)
          srcPart = src.substring(sI, consumeEnd)
          tkn = lex.token('#MIC', stripped, srcPart, pnt)
          kind = 'icode'
        } else {
          srcPart = src.substring(sI, consumeEnd)
          tkn = lex.token('#MB', null, srcPart, pnt)
          kind = 'blank'
        }
      }

      // HTML block: consume contiguous lines through the type's end marker
      // (types 1-5) or through the next blank line (types 6 and 7).
      else if (
        HTML_BLOCK_T1_OPEN.test(lineContent) ||
        HTML_BLOCK_T2_OPEN.test(lineContent) ||
        HTML_BLOCK_T3_OPEN.test(lineContent) ||
        HTML_BLOCK_T5_OPEN.test(lineContent) ||
        HTML_BLOCK_T4_OPEN.test(lineContent) ||
        HTML_BLOCK_T6_OPEN.test(lineContent) ||
        (state.last !== 'text' && HTML_BLOCK_T7_OPEN.test(lineContent))
      ) {
        const type1 = HTML_BLOCK_T1_OPEN.test(lineContent)
        const type2 = HTML_BLOCK_T2_OPEN.test(lineContent)
        const type3 = HTML_BLOCK_T3_OPEN.test(lineContent)
        const type5 = HTML_BLOCK_T5_OPEN.test(lineContent)
        const type4 =
          !type1 && !type2 && !type3 && !type5 && HTML_BLOCK_T4_OPEN.test(lineContent)

        let closeRe: RegExp | null
        if (type1) closeRe = HTML_BLOCK_T1_CLOSE
        else if (type2) closeRe = HTML_BLOCK_T2_CLOSE
        else if (type3) closeRe = HTML_BLOCK_T3_CLOSE
        else if (type4) closeRe = HTML_BLOCK_T4_CLOSE
        else if (type5) closeRe = HTML_BLOCK_T5_CLOSE
        else closeRe = null // types 6 and 7 terminate on blank line

        const htmlLines: string[] = []
        let htmlEnd = sI
        let done = false
        while (htmlEnd < srclen && !done) {
          let le = htmlEnd
          while (le < srclen && src[le] !== '\n') le++
          let innerLine = src.substring(htmlEnd, le)
          if (innerLine.endsWith('\r')) innerLine = innerLine.slice(0, -1)
          const nextEnd = le < srclen ? le + 1 : le

          if (!closeRe && /^[ \t]*$/.test(innerLine)) {
            done = true
            // Blank line is NOT included in the block.
            break
          }
          htmlLines.push(innerLine)
          htmlEnd = nextEnd
          if (closeRe && closeRe.test(innerLine)) {
            done = true
            break
          }
        }

        const htmlText = htmlLines.join('\n')
        consumeEnd = htmlEnd
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MHB', htmlText, srcPart, pnt)
        kind = 'htmlblock'
      }

      // List item continuation at the active list's content column.
      // When we're inside a list, a line indented to at least the item's
      // content column is always a continuation, even if its stripped
      // form would otherwise be a new block (HR, ATX heading, list
      // marker, etc.). The sub-parse of the item's accumulated text
      // applies those block classifications on the stripped content.
      else if (
        state.listContentCol > 0 &&
        lineContent.length >= state.listContentCol &&
        lineContent.slice(0, state.listContentCol).trim() === ''
      ) {
        const stripped = lineContent.slice(state.listContentCol)
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MLC', stripped, srcPart, pnt)
        kind = 'listcont'
      }

      // Fenced code block start: 3+ backticks or 3+ tildes with up to 3
      // leading spaces. The closing fence must use the same character and
      // be at least as long. The opening fence's indentation is stripped
      // from content lines where present.
      else if (/^ {0,3}(?:`{3,}|~{3,})/.test(lineContent)) {
        const open = lineContent.match(
          /^( {0,3})(`{3,}|~{3,})(.*)$/,
        )
        if (!open) {
          // Fall through to text.
          srcPart = src.substring(sI, consumeEnd)
          tkn = lex.token('#MT', lineContent, srcPart, pnt)
          kind = 'text'
        } else {
          const openIndent = open[1].length
          const fenceRun = open[2]
          const fenceChar = fenceRun[0]
          const fenceLen = fenceRun.length
          const infoRaw = open[3]
          // For backtick fences, the info string must not contain backticks.
          if (fenceChar === '`' && infoRaw.indexOf('`') >= 0) {
            srcPart = src.substring(sI, consumeEnd)
            tkn = lex.token('#MT', lineContent, srcPart, pnt)
            kind = 'text'
          } else {
            const lang = decodeLinkText(infoRaw.trim().split(/\s+/)[0] || '')
            const codeLines: string[] = []
            let codeEnd = consumeEnd

            while (codeEnd < srclen) {
              let innerEnd = codeEnd
              while (innerEnd < srclen && src[innerEnd] !== '\n') innerEnd++
              let innerLine = src.substring(codeEnd, innerEnd)
              if (innerLine.endsWith('\r')) innerLine = innerLine.slice(0, -1)

              const nextEnd = innerEnd < srclen ? innerEnd + 1 : innerEnd

              const closeMatch = innerLine.match(
                fenceChar === '`'
                  ? /^ {0,3}(`{3,})[ \t]*$/
                  : /^ {0,3}(~{3,})[ \t]*$/,
              )
              if (closeMatch && closeMatch[1].length >= fenceLen) {
                codeEnd = nextEnd
                break
              }

              // Strip up to openIndent leading spaces.
              let strip = 0
              while (
                strip < openIndent &&
                strip < innerLine.length &&
                innerLine[strip] === ' '
              ) {
                strip++
              }
              codeLines.push(innerLine.slice(strip))
              codeEnd = nextEnd
            }

            // Each content line contributes `<line>\n`, so the joined
            // text needs a terminal newline too (matters when the final
            // line is blank — lines.join drops the trailing separator).
            const codeText =
              codeLines.length > 0 ? codeLines.join('\n') + '\n' : ''
            consumeEnd = codeEnd
            srcPart = src.substring(sI, consumeEnd)
            tkn = lex.token('#MC', { lang, text: codeText }, srcPart, pnt)
            kind = 'code'
          }
        }
      }

      // ATX heading: up to 3 leading spaces, 1-6 `#`, optional space+text.
      // Trailing `#` closure (preceded by space, or the whole content
      // consisting only of `#`s) is stripped.
      else if (/^ {0,3}(#{1,6})(?:[ \t]+.*?)?[ \t]*$/.test(lineContent)) {
        const m = lineContent.match(
          /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$/,
        )!
        let textRaw = m[2] ?? ''
        // Strip trailing `<whitespace>#+<whitespace>` closure sequence.
        textRaw = textRaw.replace(/[ \t]+#+[ \t]*$/, '')
        // Collapse an all-hash content to empty (e.g. `### ###`).
        if (/^#+$/.test(textRaw)) textRaw = ''
        textRaw = textRaw.trim()
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#MH',
          { level: m[1].length, text: textRaw },
          srcPart,
          pnt,
        )
        kind = 'heading'
      }

      // Setext underline: only valid immediately after a paragraph line.
      // When recognized, it closes the current paragraph as a heading.
      else if (
        state.last === 'text' &&
        /^ {0,3}(=+|-+)[ \t]*$/.test(lineContent)
      ) {
        const level = lineContent.trimStart()[0] === '=' ? 1 : 2
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#MSX',
          { level, raw: lineContent },
          srcPart,
          pnt,
        )
        kind = 'setext'
      }

      // Horizontal rule: line of 3+ `-`, `*`, or `_`, optionally spaced.
      // (Setext underline takes precedence above when a paragraph precedes.)
      else if (
        /^[ \t]{0,3}([-*_])[ \t]*\1[ \t]*\1([ \t]*\1)*[ \t]*$/.test(lineContent)
      ) {
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MR', null, srcPart, pnt)
        kind = 'hr'
      }

      // Ordered list item. CommonMark limits the marker to 1-9 digits, and
      // an ordered list can only interrupt a paragraph when its first
      // item has start == 1.
      else if (
        /^ {0,3}\d{1,9}[.)][ \t]/.test(lineContent) &&
        (state.last !== 'text' ||
          /^ {0,3}1[.)][ \t]/.test(lineContent))
      ) {
        const m = lineContent.match(/^( {0,3})(\d{1,9})([.)])([ \t]+)(.*)$/)!
        const after = expandMarkerTabs(
          m[4],
          m[5],
          m[1].length + m[2].length + 1,
        )
        const result = computeListItem(
          m[1].length + m[2].length + 1,
          after.spaces,
          after.rest,
        )
        state.listContentCol = result.contentCol
        const start = parseInt(m[2], 10)
        const markerId = 'o' + m[3]
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#ML',
          { ordered: true, text: result.text, start, markerId },
          srcPart,
          pnt,
        )
        kind = 'list'
      }

      // Unordered list item.
      else if (/^ {0,3}[-*+][ \t]/.test(lineContent)) {
        const m = lineContent.match(/^( {0,3})([-*+])([ \t]+)(.*)$/)!
        const after = expandMarkerTabs(
          m[3],
          m[4],
          m[1].length + m[2].length,
        )
        const result = computeListItem(
          m[1].length + m[2].length,
          after.spaces,
          after.rest,
        )
        state.listContentCol = result.contentCol
        const markerId = 'u' + m[2]
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#ML',
          { ordered: false, text: result.text, markerId },
          srcPart,
          pnt,
        )
        kind = 'list'
      }

      // Bare list marker with no content on the same line. Per CommonMark,
      // an empty-content list marker cannot interrupt a paragraph, so
      // fall through to plain text when we're currently in one.
      else if (
        state.last !== 'text' &&
        /^ {0,3}[-*+][ \t]*$/.test(lineContent)
      ) {
        const m = lineContent.match(/^( {0,3})([-*+])/)!
        state.listContentCol = m[1].length + m[2].length + 1
        const markerId = 'u' + m[2]
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#ML',
          { ordered: false, text: '', markerId },
          srcPart,
          pnt,
        )
        kind = 'list'
      }

      else if (
        state.last !== 'text' &&
        /^ {0,3}\d{1,9}[.)][ \t]*$/.test(lineContent)
      ) {
        const m = lineContent.match(/^( {0,3})(\d{1,9})([.)])/)!
        state.listContentCol = m[1].length + m[2].length + 1 + 1
        const start = parseInt(m[2], 10)
        const markerId = 'o' + m[3]
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#ML',
          { ordered: true, text: '', start, markerId },
          srcPart,
          pnt,
        )
        kind = 'list'
      }

      // Blockquote line. CommonMark allows up to 3 leading spaces before
      // the `>`, and optionally one space after it (which is not included
      // in the content). Tabs in the content's leading whitespace are
      // expanded relative to the column AFTER the marker so the blockquote
      // sees proper virtual-column indentation (spec example 6).
      else if (/^ {0,3}>/.test(lineContent)) {
        const prefix = lineContent.match(/^ {0,3}>/)![0]
        const rest = lineContent.slice(prefix.length)
        let col = prefix.length
        let j = 0
        let expanded = ''
        while (j < rest.length) {
          const ch = rest[j]
          if (ch === ' ') {
            expanded += ' '
            col++
            j++
          } else if (ch === '\t') {
            const spaces = 4 - (col % 4)
            expanded += ' '.repeat(spaces)
            col += spaces
            j++
          } else {
            break
          }
        }
        expanded += rest.slice(j)
        const content = expanded.length > 0 && expanded[0] === ' '
          ? expanded.slice(1)
          : expanded
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MQ', content, srcPart, pnt)
        kind = 'quote'
      }

      // Indented line (4+ spaces or leading tab): indented code block,
      // unless we're currently inside a paragraph or inside a list item
      // still accumulating a paragraph — in which case this is a lazy
      // continuation line.
      else if (/^(?:    |\t)/.test(lineContent)) {
        if (
          state.last === 'text' ||
          (state.listContentCol > 0 &&
            (state.last === 'list' || state.last === 'listcont'))
        ) {
          const stripped = lineContent.replace(/^[ \t]+/, '')
          srcPart = src.substring(sI, consumeEnd)
          tkn = lex.token('#MT', stripped, srcPart, pnt)
          kind = 'text'
        } else {
          const stripped = lineContent.startsWith('\t')
            ? lineContent.slice(1)
            : lineContent.slice(4)
          srcPart = src.substring(sI, consumeEnd)
          tkn = lex.token('#MIC', stripped, srcPart, pnt)
          kind = 'icode'
        }
      }

      // Plain text line. Leading whitespace on paragraph lines is not
      // significant — strip it so both the first-line spec slack (up to
      // 3 spaces) and any continuation-line indentation are normalized.
      else {
        const stripped = lineContent.replace(/^[ \t]+/, '')
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MT', stripped, srcPart, pnt)
        kind = 'text'
      }

      state.last = kind
      // When we leave list context (non-list, non-continuation, non-blank
      // following a list) reset the saved content column. Blank lines
      // preserve it so item continuation can resume after them, and a
      // bare paragraph-like `text` line can still be a lazy continuation
      // of the list item (spec example 290), so preserve across those too.
      if (
        kind !== 'list' &&
        kind !== 'listcont' &&
        kind !== 'blank' &&
        kind !== 'text'
      ) {
        state.listContentCol = 0
        state.listMarkerId = undefined
        state.listItemEmpty = false
      }

      // Track whether the most recently opened list item is still empty.
      // A blank line following an empty item ends the list per CommonMark
      // (§ 5.2: "A list item can begin with at most one blank line").
      if (kind === 'list') {
        const v = (tkn as any).val
        state.listItemEmpty = !v || v.text === ''
      } else if (kind === 'listcont') {
        state.listItemEmpty = false
      } else if (kind === 'blank' && state.listItemEmpty) {
        state.listContentCol = 0
        state.listMarkerId = undefined
        state.listItemEmpty = false
      }

      // Advance the lex point past the consumed span, tracking row/column.
      for (let i = sI; i < consumeEnd; i++) {
        if (src[i] === '\n') {
          rI++
          cI = 1
        } else {
          cI++
        }
      }
      pnt.sI = consumeEnd
      pnt.rI = rI
      pnt.cI = cI

      return tkn
    }
  }
}


// HTML rendering of a single block. Matches CommonMark-style output for the
// block-level constructs this parser supports. Block text is routed through
// renderInline so inline constructs (escapes, entities, code spans, links,
// emphasis, autolinks, raw HTML) are handled. When a refs map is supplied,
// reference-style links resolve through it.
function renderHtml(block: any, refs: LinkRefMap = NO_REFS): string {
  switch (block.type) {
    case 'heading':
      return `<h${block.level}>${renderInline(
        block.text,
        refs,
      )}</h${block.level}>`
    case 'paragraph':
      // Trailing whitespace on the final line of a paragraph is not
      // significant (hard-break spaces are always followed by a \n and
      // therefore internal); strip it before inline processing.
      return `<p>${renderInline(block.text.replace(/[ \t]+$/, ''), refs)}</p>`
    case 'hr':
      return `<hr />`
    case 'code': {
      const cls = block.lang
        ? ` class="language-${escapeHtml(block.lang)}"`
        : ''
      return (
        `<pre><code${cls}>` +
        escapeHtml(block.text) +
        (block.text.length === 0 || block.text.endsWith('\n') ? '' : '\n') +
        `</code></pre>`
      )
    }
    case 'list': {
      const tag = block.ordered ? 'ol' : 'ul'
      let loose = !!block.loose
      const startAttr =
        block.ordered && block.start !== undefined && block.start !== 1
          ? ` start="${block.start}"`
          : ''
      // Pre-parse each item so we can (a) inspect top-level blocks for
      // `_precededByBlank` to decide dynamic looseness and (b) reuse the
      // parse result when rendering.
      const parsedItems = block.items.map((it: any) => {
        if ((it.text as string).length === 0) {
          return {
            ex: { blocks: [] as MdBlock[], refs: {} as LinkRefMap },
            hasBlank: false,
          }
        }
        const nested = parseNested(it.text) as MdBlock[]
        // Detect blank-separated top-level blocks BEFORE extracting
        // link-reference definitions — ref defs sometimes occupy the
        // blank-preceded slot and must still be counted toward looseness.
        let hasBlank = false
        for (let i = 1; i < nested.length; i++) {
          if ((nested[i] as any)._precededByBlank) {
            hasBlank = true
            break
          }
        }
        return { ex: extractLinkRefsAndClean(nested), hasBlank }
      })
      if (!loose) {
        for (const p of parsedItems) {
          if (p.hasBlank) {
            loose = true
            break
          }
        }
      }
      const items = parsedItems
        .map(
          (p: {
            ex: { blocks: MdBlock[]; refs: LinkRefMap }
            hasBlank: boolean
          }) => renderListItemFromBlocks(p.ex, loose, refs),
        )
        .join('\n')
      return `<${tag}${startAttr}>\n${items}\n</${tag}>`
    }
    case 'blockquote': {
      // Re-parse the blockquote's raw content as markdown so nested
      // headings, lists, code blocks, and further blockquotes render
      // correctly. Refs defined inside a blockquote are merged with the
      // outer refs for resolution within the nested blocks.
      const nestedBlocks = parseNested(block.text) as MdBlock[]
      const nestedExtract = extractLinkRefsAndClean(nestedBlocks)
      const merged: LinkRefMap = { ...refs }
      for (const k of Object.keys(nestedExtract.refs)) {
        if (!(k in merged)) merged[k] = nestedExtract.refs[k]
      }
      let inner = ''
      for (const nb of nestedExtract.blocks) {
        const h = renderHtml(nb, merged)
        if (h.length > 0) inner += h + '\n'
      }
      return `<blockquote>\n${inner}</blockquote>`
    }
    case 'html':
      return block.text
  }
  return ''
}

// renderListItemFromBlocks renders a list item from its already-parsed
// sub-blocks. In tight lists a singleton paragraph is rendered without
// its `<p>` wrapper (matching CommonMark), while other block types
// always render in full.
function renderListItemFromBlocks(
  ex: { blocks: MdBlock[]; refs: LinkRefMap },
  loose: boolean,
  refs: LinkRefMap,
): string {
  if (ex.blocks.length === 0) return `<li></li>`
  const merged: LinkRefMap = { ...refs }
  for (const k of Object.keys(ex.refs)) {
    if (!(k in merged)) merged[k] = ex.refs[k]
  }
  const parts: string[] = []
  for (const nb of ex.blocks) {
    if (!loose && nb.type === 'paragraph') {
      parts.push(renderInline((nb as any).text, merged))
    } else {
      parts.push(renderHtml(nb, merged))
    }
  }
  if (parts.length === 0) return `<li></li>`
  if (!loose && parts.length === 1 && !parts[0].startsWith('<')) {
    return `<li>${parts[0]}</li>`
  }
  // Tight-mode inline text at the first or last slot sits flush against
  // the `<li>` boundary (no leading/trailing newline). This matches
  // CommonMark's `<li>a\n<ul>...</ul>\n</li>` and
  // `<li>\n<h2>Bar</h2>\nbaz</li>` shapes.
  const firstInline = !loose && parts[0] && !parts[0].startsWith('<')
  const lastInline =
    !loose &&
    parts.length > 0 &&
    !parts[parts.length - 1].startsWith('<')
  const open = firstInline ? `<li>${parts[0]}\n` : `<li>\n`
  const mid = (firstInline ? parts.slice(1) : parts).join('\n')
  const close = lastInline ? `</li>` : `\n</li>`
  return `${open}${mid}${close}`
}

// parseNested runs the markdown parser on a substring for use inside a
// blockquote or other nested block. The parser instance is cached so
// repeated nested parses don't rebuild the grammar on each call.
let _nestedParser: ((src: string) => any) | null = null
function parseNested(src: string): MdBlock[] {
  if (_nestedParser === null) {
    const j = Jsonic.make()
    j.use(Markdown)
    _nestedParser = j
  }
  const result = _nestedParser(src)
  return Array.isArray(result) ? (result as MdBlock[]) : []
}

// escapeHtml performs minimal HTML escaping: `& < > "`. Used for code content
// where no inline markdown processing is applied.
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

// ASCII punctuation set recognized as a backslash escape target per
// CommonMark (§ 6.1). Matches !"#$%&'()*+,-./:;<=>?@[\]^_`{|}~.
const BACKSLASH_ESCAPABLE =
  '!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~'

// Named HTML entities recognized by the inline parser. A broad subset
// covering ASCII punctuation, common Latin-1 accents, arrows, currency,
// mathematical operators, Greek letters, and a set of less-common names
// that appear in the CommonMark conformance spec.
const NAMED_ENTITIES: Record<string, string> = {
  amp: '&', lt: '<', gt: '>', quot: '"', apos: "'",
  nbsp: '\u00A0', iexcl: '\u00A1', cent: '\u00A2', pound: '\u00A3',
  curren: '\u00A4', yen: '\u00A5', brvbar: '\u00A6', sect: '\u00A7',
  uml: '\u00A8', copy: '\u00A9', ordf: '\u00AA', laquo: '\u00AB',
  not: '\u00AC', shy: '\u00AD', reg: '\u00AE', macr: '\u00AF',
  deg: '\u00B0', plusmn: '\u00B1', sup2: '\u00B2', sup3: '\u00B3',
  acute: '\u00B4', micro: '\u00B5', para: '\u00B6', middot: '\u00B7',
  cedil: '\u00B8', sup1: '\u00B9', ordm: '\u00BA', raquo: '\u00BB',
  frac14: '\u00BC', frac12: '\u00BD', frac34: '\u00BE', iquest: '\u00BF',
  Agrave: '\u00C0', Aacute: '\u00C1', Acirc: '\u00C2', Atilde: '\u00C3',
  Auml: '\u00C4', Aring: '\u00C5', AElig: '\u00C6', Ccedil: '\u00C7',
  Egrave: '\u00C8', Eacute: '\u00C9', Ecirc: '\u00CA', Euml: '\u00CB',
  Igrave: '\u00CC', Iacute: '\u00CD', Icirc: '\u00CE', Iuml: '\u00CF',
  ETH: '\u00D0', Ntilde: '\u00D1', Ograve: '\u00D2', Oacute: '\u00D3',
  Ocirc: '\u00D4', Otilde: '\u00D5', Ouml: '\u00D6', times: '\u00D7',
  Oslash: '\u00D8', Ugrave: '\u00D9', Uacute: '\u00DA', Ucirc: '\u00DB',
  Uuml: '\u00DC', Yacute: '\u00DD', THORN: '\u00DE', szlig: '\u00DF',
  agrave: '\u00E0', aacute: '\u00E1', acirc: '\u00E2', atilde: '\u00E3',
  auml: '\u00E4', aring: '\u00E5', aelig: '\u00E6', ccedil: '\u00E7',
  egrave: '\u00E8', eacute: '\u00E9', ecirc: '\u00EA', euml: '\u00EB',
  igrave: '\u00EC', iacute: '\u00ED', icirc: '\u00EE', iuml: '\u00EF',
  eth: '\u00F0', ntilde: '\u00F1', ograve: '\u00F2', oacute: '\u00F3',
  ocirc: '\u00F4', otilde: '\u00F5', ouml: '\u00F6', divide: '\u00F7',
  oslash: '\u00F8', ugrave: '\u00F9', uacute: '\u00FA', ucirc: '\u00FB',
  uuml: '\u00FC', yacute: '\u00FD', thorn: '\u00FE', yuml: '\u00FF',
  OElig: '\u0152', oelig: '\u0153', Scaron: '\u0160', scaron: '\u0161',
  Dcaron: '\u010E', dcaron: '\u010F',
  Yuml: '\u0178', fnof: '\u0192',
  ensp: '\u2002', emsp: '\u2003', thinsp: '\u2009',
  zwnj: '\u200C', zwj: '\u200D', lrm: '\u200E', rlm: '\u200F',
  ndash: '\u2013', mdash: '\u2014', lsquo: '\u2018', rsquo: '\u2019',
  sbquo: '\u201A', ldquo: '\u201C', rdquo: '\u201D', bdquo: '\u201E',
  dagger: '\u2020', Dagger: '\u2021', bull: '\u2022', hellip: '\u2026',
  permil: '\u2030', prime: '\u2032', Prime: '\u2033', lsaquo: '\u2039',
  rsaquo: '\u203A', oline: '\u203E', euro: '\u20AC', trade: '\u2122',
  HilbertSpace: '\u210B', DifferentialD: '\u2146',
  ClockwiseContourIntegral: '\u2232',
  larr: '\u2190', uarr: '\u2191', rarr: '\u2192', darr: '\u2193',
  harr: '\u2194', crarr: '\u21B5',
  forall: '\u2200', part: '\u2202', exist: '\u2203', empty: '\u2205',
  nabla: '\u2207', isin: '\u2208', notin: '\u2209', ni: '\u220B',
  prod: '\u220F', sum: '\u2211', minus: '\u2212', lowast: '\u2217',
  radic: '\u221A', prop: '\u221D', infin: '\u221E', ang: '\u2220',
  and: '\u2227', or: '\u2228', cap: '\u2229', cup: '\u222A',
  int: '\u222B', there4: '\u2234', sim: '\u223C', cong: '\u2245',
  asymp: '\u2248', ne: '\u2260', equiv: '\u2261', le: '\u2264',
  ge: '\u2265', sub: '\u2282', sup: '\u2283', nsub: '\u2284',
  sube: '\u2286', supe: '\u2287', oplus: '\u2295', otimes: '\u2297',
  perp: '\u22A5', sdot: '\u22C5',
  ngE: '\u2267\u0338',
  lceil: '\u2308', rceil: '\u2309', lfloor: '\u230A', rfloor: '\u230B',
  lang: '\u27E8', rang: '\u27E9',
  loz: '\u25CA', spades: '\u2660', clubs: '\u2663',
  hearts: '\u2665', diams: '\u2666',
  circ: '\u02C6', tilde: '\u02DC',
  Alpha: '\u0391', Beta: '\u0392', Gamma: '\u0393', Delta: '\u0394',
  Epsilon: '\u0395', Zeta: '\u0396', Eta: '\u0397', Theta: '\u0398',
  Iota: '\u0399', Kappa: '\u039A', Lambda: '\u039B', Mu: '\u039C',
  Nu: '\u039D', Xi: '\u039E', Omicron: '\u039F', Pi: '\u03A0',
  Rho: '\u03A1', Sigma: '\u03A3', Tau: '\u03A4', Upsilon: '\u03A5',
  Phi: '\u03A6', Chi: '\u03A7', Psi: '\u03A8', Omega: '\u03A9',
  alpha: '\u03B1', beta: '\u03B2', gamma: '\u03B3', delta: '\u03B4',
  epsilon: '\u03B5', zeta: '\u03B6', eta: '\u03B7', theta: '\u03B8',
  iota: '\u03B9', kappa: '\u03BA', lambda: '\u03BB', mu: '\u03BC',
  nu: '\u03BD', xi: '\u03BE', omicron: '\u03BF', pi: '\u03C0',
  rho: '\u03C1', sigmaf: '\u03C2', sigma: '\u03C3', tau: '\u03C4',
  upsilon: '\u03C5', phi: '\u03C6', chi: '\u03C7', psi: '\u03C8',
  omega: '\u03C9',
}

// decodeEntity returns the Unicode string for a full entity reference
// (including the leading `&` and trailing `;`), or null if the reference is
// not recognized. Numeric references are always decoded; invalid or
// zero-code-point references map to the Unicode replacement character.
function decodeEntity(ref: string): string | null {
  if (ref.length < 3 || ref[0] !== '&' || ref[ref.length - 1] !== ';') {
    return null
  }
  const body = ref.slice(1, -1)
  if (body.startsWith('#x') || body.startsWith('#X')) {
    const n = parseInt(body.slice(2), 16)
    if (!Number.isFinite(n) || n === 0 || n > 0x10ffff) return '\uFFFD'
    try {
      return String.fromCodePoint(n)
    } catch {
      return '\uFFFD'
    }
  }
  if (body.startsWith('#')) {
    const n = parseInt(body.slice(1), 10)
    if (!Number.isFinite(n) || n === 0 || n > 0x10ffff) return '\uFFFD'
    try {
      return String.fromCodePoint(n)
    } catch {
      return '\uFFFD'
    }
  }
  const named = NAMED_ENTITIES[body]
  return named === undefined ? null : named
}

// InlineSeg is a segment produced by the inline tokenizer. Text segments
// need HTML-escaping at render time; html segments are already-safe HTML
// atoms (code span output, decoded entities, escape output, hard breaks);
// delim segments are `*`/`_` runs that the emphasis pass may consume;
// bracket segments are `[`, `![`, or `]` markers consumed by the link pass.
// A closing bracket may carry either inline target info (url/title) or
// reference info (refLabel filled = full, refCollapsed = collapsed `[]`,
// refShortcut = bare `[text]`).
type InlineSeg =
  | { kind: 'text'; value: string }
  | { kind: 'html'; value: string }
  | {
      kind: 'delim'
      char: '*' | '_'
      length: number
      canOpen: boolean
      canClose: boolean
    }
  | {
      kind: 'bracket'
      open: boolean
      image: boolean
      active: boolean
      url?: string
      title?: string
      refLabel?: string
      refCollapsed?: boolean
      refShortcut?: boolean
      // Original source text consumed for this segment (e.g. `[`, `![`,
      // `]`, `](/url)`, `][label]`). Rendered verbatim when the segment
      // ends up unmatched.
      srcText: string
      // Position (in the enclosing inline source string) of the first
      // character AFTER the bracket marker (openers) or of the closing
      // `]` (closers). Used to reconstruct raw label text for shortcut
      // links, whose label must be matched with backslash escapes kept
      // intact per the CommonMark label equality rule.
      srcPos?: number
      // Opener removed from the "delimiter stack" by a preceding closer
      // that found it but failed to form a link. Subsequent closers skip
      // it entirely (per CommonMark's pop-on-fail rule), so no later
      // closer can match across a removed opener to an even earlier one.
      removed?: boolean
    }

// Link reference definition — produced by extracting `[label]: url "title"`
// paragraphs from the parsed block list.
type LinkRef = { url: string; title: string }
type LinkRefMap = Record<string, LinkRef>

const NO_REFS: LinkRefMap = {}

// Punctuation set for CommonMark emphasis flanking classification. Matches
// ASCII punctuation plus any Unicode character in the P (punctuation) or S
// (symbol) general category — the latter covers currency symbols, math
// operators, etc., which CommonMark treats like punctuation for flanking.
const ASCII_PUNCT = /[!-/:-@\[-`{-~]|\p{P}|\p{S}/u

// Unicode whitespace per CommonMark: ASCII space/tab/LF/CR/FF plus any
// character in the Unicode Zs (Space Separator) general category, e.g.
// non-breaking space (U+00A0), figure space, etc.
const UNICODE_WS = /^\p{Zs}$/u
function isWhitespaceChar(c: string): boolean {
  if (c === '' || c === ' ' || c === '\t' || c === '\n' || c === '\r' || c === '\f') {
    return true
  }
  return UNICODE_WS.test(c)
}

// parseLinkTarget parses `(URL[ "TITLE"])` starting at position i in s and
// returns { url, title, end } on success, or null if the string at i is not
// a valid inline link target.
function parseLinkTarget(
  s: string,
  i: number,
): { url: string; title: string; end: number } | null {
  if (s[i] !== '(') return null
  let j = i + 1

  // Optional whitespace (including one newline).
  while (j < s.length && /[ \t\r\n]/.test(s[j])) j++

  let url = ''
  if (s[j] === '<') {
    let k = j + 1
    while (
      k < s.length &&
      s[k] !== '>' &&
      s[k] !== '<' &&
      s[k] !== '\n'
    ) {
      if (s[k] === '\\' && k + 1 < s.length) {
        url += s[k] + s[k + 1]
        k += 2
        continue
      }
      url += s[k]
      k++
    }
    if (s[k] !== '>') return null
    j = k + 1
  } else {
    let depth = 0
    while (j < s.length) {
      const c = s[j]
      if (c === '\\' && j + 1 < s.length) {
        url += s[j] + s[j + 1]
        j += 2
        continue
      }
      if (c === ' ' || c === '\t' || c === '\n' || c === '\r') break
      if (c === '(') {
        depth++
      } else if (c === ')') {
        if (depth === 0) break
        depth--
      } else if (c.charCodeAt(0) < 0x20 || c === '\x7f') {
        break
      }
      url += c
      j++
    }
    if (url.length === 0 && s[j] !== ')') return null
  }

  while (j < s.length && /[ \t\r\n]/.test(s[j])) j++

  let title = ''
  if (s[j] === '"' || s[j] === "'" || s[j] === '(') {
    const openQ = s[j]
    const closeQ = openQ === '(' ? ')' : openQ
    let k = j + 1
    while (k < s.length && s[k] !== closeQ) {
      if (s[k] === '\\' && k + 1 < s.length) {
        title += s[k] + s[k + 1]
        k += 2
        continue
      }
      title += s[k]
      k++
    }
    if (s[k] !== closeQ) return null
    j = k + 1
  }

  while (j < s.length && /[ \t\r\n]/.test(s[j])) j++

  if (s[j] !== ')') return null
  return { url, title, end: j + 1 }
}

// parseReferenceLabel parses `[label]` or `[]` starting at position i in s
// and returns { label, collapsed, end } on success, else null. Collapsed
// references are bracket pairs containing only optional whitespace.
function parseReferenceLabel(
  s: string,
  i: number,
): { label: string; collapsed: boolean; end: number } | null {
  if (s[i] !== '[') return null
  let j = i + 1
  let label = ''
  while (j < s.length && s[j] !== ']') {
    if (s[j] === '\\' && j + 1 < s.length) {
      label += s[j] + s[j + 1]
      j += 2
      continue
    }
    if (s[j] === '[') return null
    label += s[j]
    j++
  }
  if (s[j] !== ']') return null
  const collapsed = /^[ \t\r\n]*$/.test(label)
  return { label, collapsed, end: j + 1 }
}

// normalizeLinkLabel applies the CommonMark label equality rule: strip
// leading/trailing whitespace, collapse interior whitespace runs to a
// single space, and case-fold. Backslash escapes are preserved (so
// `foo\!` ≠ `foo!`). The German eszett (`ß`/`ẞ`) folds to `ss` via an
// explicit replacement since JS's toLowerCase does not do full Unicode
// case-folding.
function normalizeLinkLabel(s: string): string {
  let out = s.replace(/ẞ/g, 'ss').replace(/ß/g, 'ss')
  out = out.trim().toLowerCase().replace(/[ \t\r\n]+/g, ' ')
  return out
}

// parseLinkRefDef attempts to match a `[label]: destination[ "title"]`
// link reference definition at the start of text. Returns { label, url,
// title, length } on success, or null.
function parseLinkRefDef(
  text: string,
): { label: string; url: string; title: string; length: number } | null {
  const lead = text.match(/^[ ]{0,3}/)!
  let i = lead[0].length
  if (text[i] !== '[') return null
  i++
  let label = ''
  let labelEnd = -1
  while (i < text.length) {
    const c = text[i]
    if (c === '\n') {
      label += c
      i++
      // A link label cannot contain a blank line (two line-endings
      // separated only by whitespace). Multi-line labels are otherwise
      // fine — the `[` ... `]` span can wrap over several lines.
      if (/\n[ \t]*\n/.test(label)) return null
      continue
    }
    if (c === ']') {
      labelEnd = i
      break
    }
    if (c === '\\' && i + 1 < text.length) {
      // Preserve the backslash so the stored label matches CommonMark's
      // label equality rule, which treats `foo\!` and `foo!` as distinct
      // labels (see spec example 545).
      label += text[i] + text[i + 1]
      i += 2
      continue
    }
    if (c === '[') return null
    label += c
    i++
  }
  if (labelEnd < 0) return null
  if (!/\S/.test(label)) return null
  i = labelEnd + 1
  if (text[i] !== ':') return null
  i++
  // Optional whitespace (at most one newline).
  let nls = 0
  while (i < text.length && /[ \t\r\n]/.test(text[i])) {
    if (text[i] === '\n') {
      nls++
      if (nls > 1) return null
    }
    i++
  }

  // URL: angle-bracket or plain. Backslashes are kept verbatim in the
  // stored URL — encodeLinkUrl will decode them at render time. The
  // backslash still protects the next character from being treated as a
  // delimiter during parsing.
  let url = ''
  if (text[i] === '<') {
    let k = i + 1
    while (k < text.length && text[k] !== '>' && text[k] !== '\n' && text[k] !== '<') {
      if (text[k] === '\\' && k + 1 < text.length) {
        url += text[k] + text[k + 1]
        k += 2
        continue
      }
      url += text[k]
      k++
    }
    if (text[k] !== '>') return null
    i = k + 1
  } else {
    while (i < text.length) {
      const c = text[i]
      if (c === ' ' || c === '\t' || c === '\n' || c === '\r') break
      if (c.charCodeAt(0) < 0x20 || c === '\x7f') break
      if (c === '\\' && i + 1 < text.length) {
        url += text[i] + text[i + 1]
        i += 2
        continue
      }
      url += c
      i++
    }
    if (url.length === 0) return null
  }

  // Optional title on same line, or on the next line after whitespace.
  // We require AT LEAST one whitespace character between the URL and
  // the title's opening quote; otherwise what looks like a title is
  // actually part of the URL or invalid.
  const urlEnd = i
  let titleEnd = i
  let title = ''
  let j = i
  let titleNewlines = 0
  while (j < text.length && /[ \t]/.test(text[j])) j++
  if (j < text.length && text[j] === '\n') {
    j++
    while (j < text.length && /[ \t]/.test(text[j])) j++
  }
  const hadWhitespaceAfterUrl = j > urlEnd
  if (
    hadWhitespaceAfterUrl &&
    j < text.length &&
    (text[j] === '"' || text[j] === "'" || text[j] === '(')
  ) {
    const openQ = text[j]
    const closeQ = openQ === '(' ? ')' : openQ
    let k = j + 1
    let ok = false
    const tbuf: string[] = []
    while (k < text.length) {
      const c = text[k]
      if (c === '\\' && k + 1 < text.length) {
        tbuf.push(text[k] + text[k + 1])
        k += 2
        continue
      }
      if (c === closeQ) {
        ok = true
        break
      }
      if (c === '\n') {
        titleNewlines++
        // A link title may span multiple lines but cannot contain a
        // blank line. Detect by peeking at the next non-(space|tab)
        // character — if it's another newline, this is a blank line.
        let p = k + 1
        while (p < text.length && (text[p] === ' ' || text[p] === '\t')) {
          p++
        }
        if (p < text.length && text[p] === '\n') break
      }
      if (openQ === '(' && c === '(') break
      tbuf.push(c)
      k++
    }
    if (ok) {
      title = tbuf.join('')
      j = k + 1
      // The rest of this line must be only whitespace.
      let m = j
      while (m < text.length && text[m] !== '\n') {
        if (!/[ \t]/.test(text[m])) {
          // Title invalid in this position — revert.
          title = ''
          j = titleEnd
          break
        }
        m++
      }
      if (title !== '') titleEnd = j
    }
  }

  // Ensure the definition ends at end of line (optionally whitespace).
  let endPos = titleEnd
  while (endPos < text.length && /[ \t]/.test(text[endPos])) endPos++
  if (endPos < text.length) {
    if (text[endPos] !== '\n') {
      // If title was parsed but followed by non-newline garbage, and title
      // was on the *same* line as the URL, the whole definition is invalid.
      // If title was on the following line, we still accept with no title.
      if (title !== '') {
        title = ''
        titleEnd = i
        endPos = titleEnd
        while (endPos < text.length && /[ \t]/.test(text[endPos])) endPos++
        if (endPos < text.length && text[endPos] !== '\n') return null
      } else {
        return null
      }
    }
  }
  if (endPos < text.length && text[endPos] === '\n') endPos++

  return { label, url, title, length: endPos }
}

// extractLinkRefsAndClean walks the block list, pulls `[label]: url "title"`
// link reference definitions out of the leading portion of paragraph
// blocks, returns the accumulated ref map, and filters blocks that have
// been fully consumed by definitions.
function extractLinkRefsAndClean(
  blocks: MdBlock[],
): { refs: LinkRefMap; blocks: MdBlock[] } {
  const refs: LinkRefMap = {}
  const out: MdBlock[] = []
  for (const b of blocks) {
    if (b.type !== 'paragraph') {
      out.push(b)
      continue
    }
    let text = b.text
    while (true) {
      const def = parseLinkRefDef(text)
      if (!def) break
      const norm = normalizeLinkLabel(def.label)
      if (norm.length > 0 && !(norm in refs)) {
        refs[norm] = { url: def.url, title: def.title }
      }
      text = text.slice(def.length)
    }
    if (text.length > 0) {
      if (text === b.text) {
        out.push(b)
      } else {
        const cloned: any = { ...b, text }
        if ((b as any)._precededByBlank) {
          Object.defineProperty(cloned, '_precededByBlank', {
            value: true,
            enumerable: false,
            writable: true,
          })
        }
        out.push(cloned)
      }
    }
  }
  return { refs, blocks: out }
}

// The "unreserved + reserved-safe" set preserved by CommonMark's URL
// normalization. Characters outside this set are percent-encoded as UTF-8.
const URL_SAFE = /[A-Za-z0-9\-._~!$&'()*+,;=:@/?#]/

// decodeLinkText decodes backslash escapes and well-formed entity
// references in link/title text. Invalid sequences are passed through.
function decodeLinkText(s: string): string {
  let out = ''
  let i = 0
  const n = s.length
  while (i < n) {
    const c = s[i]
    if (c === '\\' && i + 1 < n && BACKSLASH_ESCAPABLE.indexOf(s[i + 1]) >= 0) {
      out += s[i + 1]
      i += 2
      continue
    }
    if (c === '&') {
      const m = s
        .slice(i)
        .match(
          /^&(#[xX][0-9a-fA-F]{1,6};|#[0-9]{1,7};|[a-zA-Z][a-zA-Z0-9]{1,31};)/,
        )
      if (m) {
        const decoded = decodeEntity('&' + m[1])
        if (decoded !== null) {
          out += decoded
          i += m[0].length
          continue
        }
      }
    }
    out += c
    i++
  }
  return out
}

// encodeLinkUrl performs CommonMark-style URL normalization: backslash
// escapes and entities are decoded (unless decodeEscapes === false, as for
// autolinks which are literal), existing percent-encoded sequences are
// preserved (case-preserving), and any other character outside the safe
// set is percent-encoded as its UTF-8 byte sequence.
function encodeLinkUrl(url: string, decodeEscapes = true): string {
  const src = decodeEscapes ? decodeLinkText(url) : url
  let out = ''
  let i = 0
  while (i < src.length) {
    const c = src[i]
    // Preserve a well-formed `%XX` percent-encoded byte.
    if (
      c === '%' &&
      i + 2 < src.length &&
      /[0-9a-fA-F]/.test(src[i + 1]) &&
      /[0-9a-fA-F]/.test(src[i + 2])
    ) {
      out += '%' + src[i + 1].toUpperCase() + src[i + 2].toUpperCase()
      i += 3
      continue
    }
    if (c === '&') {
      out += '&amp;'
      i++
      continue
    }
    if (URL_SAFE.test(c)) {
      out += c
      i++
      continue
    }
    // Percent-encode this character's UTF-8 bytes.
    const bytes = utf8Bytes(c)
    for (const b of bytes) {
      out += '%' + b.toString(16).toUpperCase().padStart(2, '0')
    }
    i++
  }
  return out
}

// utf8Bytes returns the UTF-8 byte representation of a single character.
// Handles surrogate pairs by delegating to TextEncoder for code points
// beyond the BMP.
function utf8Bytes(c: string): number[] {
  const enc = new TextEncoder()
  const bytes = enc.encode(c)
  const out: number[] = []
  for (let i = 0; i < bytes.length; i++) out.push(bytes[i])
  return out
}

// scanFollowingBracketPair checks whether the segments at `start` form a
// `[label]` or `[]` bracket pair and, if so, returns its inner-text label
// (empty for `[]`) and the index of the closing bracket segment.
function scanFollowingBracketPair(
  segs: InlineSeg[],
  start: number,
): { label: string; endIdx: number } | null {
  if (start >= segs.length) return null
  const open = segs[start]
  if (open.kind !== 'bracket' || !open.open || open.image) return null
  let depth = 1
  for (let j = start + 1; j < segs.length; j++) {
    const s = segs[j]
    if (s.kind === 'bracket') {
      if (s.open) {
        depth++
      } else {
        depth--
        if (depth === 0) {
          const inner = segs.slice(start + 1, j)
          return { label: innerText(inner), endIdx: j }
        }
      }
    }
  }
  return null
}

// innerText extracts a best-effort plain-text rendering of a segment list,
// used as alt text for images. Nested markup is stripped; a nested `<img>`
// contributes its alt attribute.
function innerText(segs: InlineSeg[]): string {
  let out = ''
  for (const seg of segs) {
    if (seg.kind === 'text') out += seg.value
    else if (seg.kind === 'delim') out += seg.char.repeat(seg.length)
    else if (seg.kind === 'bracket') {
      // Use the full source text for closers so unmatched inline /
      // ref-style suffixes (e.g. `](uri2)`) remain visible in the
      // outer alt string (spec example 520).
      if (!seg.open && seg.srcText && seg.srcText.length > 1) {
        out += seg.srcText
      } else {
        out += seg.open ? (seg.image ? '![' : '[') : ']'
      }
    } else {
      // html segment — strip tags but pull `alt="..."` from `<img>` so
      // nested images contribute their alt text to the outer alt.
      let v = seg.value
      v = v.replace(/<img\s[^>]*\balt="([^"]*)"[^>]*>/g, '$1')
      out += v.replace(/<[^>]*>/g, '')
    }
  }
  return out
}

// tokenizeInline walks the input and produces a flat segment list. Code
// spans, entity refs, backslash escapes, and hard breaks are resolved into
// `html` segments; plain text accumulates into `text` segments; runs of
// `*`/`_` are classified and emitted as `delim` segments for the emphasis
// pass to consume; `[`, `![`, and `]` become `bracket` segments for the
// link pass.
function tokenizeInline(s: string): InlineSeg[] {
  const segs: InlineSeg[] = []
  const appendText = (t: string) => {
    const last = segs[segs.length - 1]
    if (last && last.kind === 'text') last.value += t
    else segs.push({ kind: 'text', value: t })
  }

  let i = 0
  const n = s.length

  while (i < n) {
    const c = s[i]

    // Backslash escape or hard line break via backslash.
    if (c === '\\' && i + 1 < n) {
      const next = s[i + 1]
      if (next === '\n') {
        segs.push({ kind: 'html', value: '<br />\n' })
        i += 2
        continue
      }
      if (BACKSLASH_ESCAPABLE.indexOf(next) >= 0) {
        segs.push({ kind: 'html', value: escapeHtmlChar(next) })
        i += 2
        continue
      }
    }

    // Code span.
    if (c === '`') {
      let openLen = 1
      while (i + openLen < n && s[i + openLen] === '`') openLen++

      let j = i + openLen
      let found = -1
      while (j < n) {
        if (s[j] === '`') {
          let closeLen = 1
          while (j + closeLen < n && s[j + closeLen] === '`') closeLen++
          if (closeLen === openLen) {
            found = j
            break
          }
          j += closeLen
        } else {
          j++
        }
      }

      if (found >= 0) {
        let content = s.slice(i + openLen, found).replace(/[\r\n]+/g, ' ')
        if (
          content.length >= 2 &&
          content.startsWith(' ') &&
          content.endsWith(' ') &&
          /[^ ]/.test(content)
        ) {
          content = content.slice(1, -1)
        }
        segs.push({
          kind: 'html',
          value: '<code>' + escapeHtmlString(content) + '</code>',
        })
        i = found + openLen
        continue
      }

      appendText('`'.repeat(openLen))
      i += openLen
      continue
    }

    // Entity or numeric character reference.
    if (c === '&') {
      const m = s
        .slice(i)
        .match(
          /^&(#[xX][0-9a-fA-F]{1,6};|#[0-9]{1,7};|[a-zA-Z][a-zA-Z0-9]{1,31};)/,
        )
      if (m) {
        const decoded = decodeEntity('&' + m[1])
        if (decoded !== null) {
          segs.push({ kind: 'html', value: escapeHtmlString(decoded) })
          i += m[0].length
          continue
        }
      }
    }

    // Autolink / raw HTML. When `<` starts one of these patterns it's
    // emitted as a pre-rendered html segment; otherwise it falls through
    // to be escaped as `&lt;`.
    if (c === '<') {
      const rest = s.slice(i)

      // Autolink URI: <scheme:path>. Autolink URL content is literal —
      // no backslash-escape decoding.
      let m = rest.match(
        /^<([a-zA-Z][a-zA-Z0-9.+-]{1,31}:[^\s<>\x00-\x1f\x7f]*)>/,
      )
      if (m) {
        segs.push({
          kind: 'html',
          value:
            '<a href="' +
            encodeLinkUrl(m[1], false) +
            '">' +
            escapeHtmlString(m[1]) +
            '</a>',
        })
        i += m[0].length
        continue
      }

      // Autolink email: <user@domain>
      m = rest.match(
        /^<([a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*)>/,
      )
      if (m) {
        segs.push({
          kind: 'html',
          value:
            '<a href="mailto:' +
            encodeLinkUrl(m[1], false) +
            '">' +
            escapeHtmlString(m[1]) +
            '</a>',
        })
        i += m[0].length
        continue
      }

      // Raw HTML: open tag, close tag, comment, PI, declaration, CDATA.
      m = rest.match(/^<!--(?:-?>|(?:[^-]|-[^-]|--[^>])*?-->)/)
      if (m) {
        segs.push({ kind: 'html', value: m[0] })
        i += m[0].length
        continue
      }
      m = rest.match(/^<\?[\s\S]*?\?>/)
      if (m) {
        segs.push({ kind: 'html', value: m[0] })
        i += m[0].length
        continue
      }
      m = rest.match(/^<!\[CDATA\[[\s\S]*?\]\]>/)
      if (m) {
        segs.push({ kind: 'html', value: m[0] })
        i += m[0].length
        continue
      }
      m = rest.match(/^<![A-Z][^>]*>/)
      if (m) {
        segs.push({ kind: 'html', value: m[0] })
        i += m[0].length
        continue
      }
      // Open tag with optional attributes.
      m = rest.match(
        /^<[a-zA-Z][a-zA-Z0-9-]*(?:\s+[a-zA-Z_:][a-zA-Z0-9_.:-]*(?:\s*=\s*(?:[^\s"'=<>`]+|'[^']*'|"[^"]*"))?)*\s*\/?>/,
      )
      if (m) {
        segs.push({ kind: 'html', value: m[0] })
        i += m[0].length
        continue
      }
      // Close tag.
      m = rest.match(/^<\/[a-zA-Z][a-zA-Z0-9-]*\s*>/)
      if (m) {
        segs.push({ kind: 'html', value: m[0] })
        i += m[0].length
        continue
      }
      // Fall through: literal `<`.
    }

    // Image open `![`.
    if (c === '!' && s[i + 1] === '[') {
      segs.push({
        kind: 'bracket',
        open: true,
        image: true,
        active: true,
        srcText: '![',
        srcPos: i + 2,
      })
      i += 2
      continue
    }

    // Link open `[`.
    if (c === '[') {
      segs.push({
        kind: 'bracket',
        open: true,
        image: false,
        active: true,
        srcText: '[',
        srcPos: i + 1,
      })
      i++
      continue
    }

    // Link close `]` — if followed by `(url[ "title"])`, stash the inline
    // target. Otherwise, if followed by `[label]` or `[]`, stash reference
    // info. Otherwise mark as a shortcut reference (label = bracket text).
    if (c === ']') {
      const lt = parseLinkTarget(s, i + 1)
      if (lt) {
        segs.push({
          kind: 'bracket',
          open: false,
          image: false,
          active: true,
          url: lt.url,
          title: lt.title,
          srcText: s.slice(i, lt.end),
          srcPos: i,
        })
        i = lt.end
        continue
      }
      const rl = parseReferenceLabel(s, i + 1)
      if (rl) {
        segs.push({
          kind: 'bracket',
          open: false,
          image: false,
          active: true,
          refLabel: rl.collapsed ? undefined : rl.label,
          refCollapsed: rl.collapsed,
          srcText: s.slice(i, rl.end),
          srcPos: i,
        })
        i = rl.end
        continue
      }
      segs.push({
        kind: 'bracket',
        open: false,
        image: false,
        active: true,
        refShortcut: true,
        srcText: ']',
        srcPos: i,
      })
      i++
      continue
    }

    // Emphasis delimiter run.
    if (c === '*' || c === '_') {
      let length = 1
      while (i + length < n && s[i + length] === c) length++

      const before = i === 0 ? ' ' : s[i - 1]
      const after = i + length >= n ? ' ' : s[i + length]
      const beforeWs = isWhitespaceChar(before)
      const afterWs = isWhitespaceChar(after)
      const beforePunct = ASCII_PUNCT.test(before)
      const afterPunct = ASCII_PUNCT.test(after)

      const leftFlanking =
        !afterWs && (!afterPunct || beforeWs || beforePunct)
      const rightFlanking =
        !beforeWs && (!beforePunct || afterWs || afterPunct)

      let canOpen = leftFlanking
      let canClose = rightFlanking
      if (c === '_') {
        canOpen = leftFlanking && (!rightFlanking || beforePunct)
        canClose = rightFlanking && (!leftFlanking || afterPunct)
      }

      segs.push({ kind: 'delim', char: c, length, canOpen, canClose })
      i += length
      continue
    }

    // Hard line break via 2+ trailing spaces, or plain newline. A single
    // trailing space (or trailing tabs) before a newline is insignificant
    // per CommonMark — strip it before emitting the newline.
    if (c === '\n') {
      const last = segs[segs.length - 1]
      if (last && last.kind === 'text' && /  $/.test(last.value)) {
        last.value = last.value.replace(/ +$/, '')
        if (last.value.length === 0) segs.pop()
        segs.push({ kind: 'html', value: '<br />\n' })
        i++
        continue
      }
      if (last && last.kind === 'text' && /[ \t]$/.test(last.value)) {
        last.value = last.value.replace(/[ \t]+$/, '')
        if (last.value.length === 0) segs.pop()
      }
      appendText('\n')
      i++
      continue
    }

    appendText(c)
    i++
  }

  return segs
}

// processLinks walks the segment list forward, matching each link/image
// closing bracket with the most recent active opener. For inline links
// (closer carries a parsed url), the enclosed segments are processed
// recursively (emphasis + nested links) and the whole `[...]( )` span is
// replaced by a single html segment wrapping the rendered inner text in an
// `<a>` or `<img>` element. For reference-style links (full, collapsed,
// or shortcut), the label is resolved against the supplied refs map.
function processLinks(
  segs: InlineSeg[],
  refs: LinkRefMap = NO_REFS,
  src: string = '',
): InlineSeg[] {
  // rawLabel extracts the original source text between an opener
  // bracket and its matching closer `]`, used for shortcut reference
  // matching where CommonMark preserves backslash escapes literally
  // (so `[foo\!]` ≠ `[foo!]`).
  const rawLabel = (op: Extract<InlineSeg, { kind: 'bracket' }>,
                    cl: Extract<InlineSeg, { kind: 'bracket' }>): string => {
    if (src && op.srcPos !== undefined && cl.srcPos !== undefined) {
      return src.slice(op.srcPos, cl.srcPos)
    }
    return innerText(segs.slice(segs.indexOf(op) + 1, segs.indexOf(cl)))
  }
  let i = 0
  while (i < segs.length) {
    const close = segs[i]
    if (close.kind !== 'bracket' || close.open) {
      i++
      continue
    }

    let j = i - 1
    // Per CM § 6.4: scan back and pop the topmost (nearest) opener
    // that is still on the stack (`!removed`). If it's inactive,
    // discard it and render this `]` as literal; further closers then
    // cannot reach any still-active opener that sat behind it.
    let openIdx = -1
    let nearestIdx = -1
    while (j >= 0) {
      const op = segs[j]
      if (op.kind === 'bracket' && op.open && !op.removed) {
        nearestIdx = j
        if (op.active) openIdx = j
        break
      }
      j--
    }
    if (openIdx < 0 && nearestIdx >= 0) {
      const staleOpen = segs[nearestIdx] as Extract<
        InlineSeg,
        { kind: 'bracket' }
      >
      staleOpen.removed = true
    }

    const open =
      openIdx >= 0
        ? (segs[openIdx] as Extract<InlineSeg, { kind: 'bracket' }>)
        : null
    const inner = openIdx >= 0 ? segs.slice(openIdx + 1, i) : []

    // Resolve the target: inline url, or lookup via refs. Only attempted
    // if we have a matching opener.
    let url: string | undefined
    let title: string | undefined
    let consumeThrough = i // last segment index to remove when match forms
    if (openIdx >= 0) {
      if (close.url !== undefined) {
        url = close.url
        title = close.title
      } else if (close.refLabel) {
        // Full ref consumed by tokenizer. If it doesn't resolve, we
        // re-split the suffix below so the `[label]` can still form its
        // own link later.
        const ref = refs[normalizeLinkLabel(close.refLabel)]
        if (ref) {
          url = ref.url
          title = ref.title
        }
      } else if (close.refCollapsed) {
        // Collapsed `[]` consumed by tokenizer; label is the raw text
        // of the opener's `[...]` span.
        const ref = refs[normalizeLinkLabel(rawLabel(open!, close))]
        if (ref) {
          url = ref.url
          title = ref.title
        }
      } else {
        // Shortcut candidate. Per CommonMark, if this `]` is followed by
        // a `[label]` or `[]` bracket pair, that's a full/collapsed ref
        // attempt which WINS over a shortcut attempt — even if the
        // label doesn't resolve, the shortcut is NOT tried.
        const la = scanFollowingBracketPair(segs, i + 1)
        if (la) {
          const label = la.label !== '' ? la.label : rawLabel(open!, close)
          const ref = refs[normalizeLinkLabel(label)]
          if (ref) {
            url = ref.url
            title = ref.title
            consumeThrough = la.endIdx
          }
          // If the full/collapsed ref attempt didn't resolve, leave
          // url undefined — the outer `[text]` link fails and the
          // enclosed `[label]` segments remain for their own pass.
        } else {
          // No follow-up: try shortcut with raw label source text.
          const ref = refs[normalizeLinkLabel(rawLabel(open!, close))]
          if (ref) {
            url = ref.url
            title = ref.title
          }
        }
      }
    }

    if (url === undefined) {
      // No match. If this close bracket had consumed a `[label]` or `[]`
      // reference suffix, break the suffix back out into separate bracket
      // segments so the enclosed `[label]` can still form its own link
      // in a later iteration (CommonMark does left-to-right matching;
      // a failed full/collapsed ref releases its label for re-processing).
      if (close.refLabel !== undefined || close.refCollapsed) {
        const suffix = close.srcText.slice(1) // drop the leading `]`
        // Remember the original closer position in `src` so we can
        // shift the re-tokenized segments' `srcPos` fields to still
        // refer into the outer source string (rawLabel slicing relies
        // on that invariant).
        const suffixStart =
          close.srcPos !== undefined ? close.srcPos + 1 : -1
        close.refLabel = undefined
        close.refCollapsed = false
        close.refShortcut = true
        close.srcText = ']'
        if (suffix.length > 0) {
          const extra = tokenizeInline(suffix)
          if (suffixStart >= 0) {
            for (const s2 of extra) {
              if (s2.kind === 'bracket' && s2.srcPos !== undefined) {
                s2.srcPos += suffixStart
              }
            }
          }
          segs.splice(i + 1, 0, ...extra)
        }
      }
      if (open) {
        // Pop this opener off the delimiter stack so a later `]` can't
        // sneak back to an even earlier active opener (spec example 520).
        open.active = false
        open.removed = true
      }
      i++
      continue
    }

    // Recursively process nested links then emphasis within link text.
    const nested = processLinks(inner, refs, src)
    processEmphasis(nested)

    // At this point open must be non-null because url was resolved only
    // when openIdx >= 0.
    const openNN = open!
    let html: string
    if (openNN.image) {
      const alt = innerText(nested)
      const titleAttr = title
        ? ` title="${escapeHtmlString(decodeLinkText(title))}"`
        : ''
      html = `<img src="${encodeLinkUrl(url)}" alt="${escapeHtmlString(
        alt,
      )}"${titleAttr} />`
    } else {
      const inside = renderSegments(nested)
      const titleAttr = title
        ? ` title="${escapeHtmlString(decodeLinkText(title))}"`
        : ''
      html = `<a href="${encodeLinkUrl(url)}"${titleAttr}>${inside}</a>`
    }

    segs.splice(openIdx, consumeThrough - openIdx + 1, {
      kind: 'html',
      value: html,
    })

    // Per CommonMark, matching a link deactivates all earlier `[` openers
    // to prevent nested <a> elements. (Images may still be nested.)
    if (!openNN.image) {
      for (let k = 0; k < openIdx; k++) {
        const s2 = segs[k]
        if (s2.kind === 'bracket' && s2.open && !s2.image) {
          s2.active = false
        }
      }
    }

    i = openIdx + 1
  }
  return segs
}

// processEmphasis walks the segment list, matching close delimiters with
// prior opening delimiters of the same character to wrap the enclosed
// content in <em> / <strong> tags. Follows CommonMark's delimiter-stack
// algorithm (§ 6.4) with an ASCII punctuation approximation.
function processEmphasis(segs: InlineSeg[]): InlineSeg[] {
  let i = 0
  while (i < segs.length) {
    const closer = segs[i]
    if (closer.kind !== 'delim' || !closer.canClose) {
      i++
      continue
    }

    // Scan back for a matching opener.
    let j = i - 1
    let matched = -1
    while (j >= 0) {
      const op = segs[j]
      if (
        op.kind === 'delim' &&
        op.canOpen &&
        op.char === closer.char
      ) {
        // Rule 9/10: avoid "odd match" where the sum of the two lengths
        // is not a multiple of 3 but one of them is.
        const bothCanOpenClose =
          (op.canOpen && op.canClose) || (closer.canOpen && closer.canClose)
        if (
          !bothCanOpenClose ||
          (op.length + closer.length) % 3 !== 0 ||
          (op.length % 3 === 0 && closer.length % 3 === 0)
        ) {
          matched = j
          break
        }
      }
      j--
    }

    if (matched < 0) {
      i++
      continue
    }

    const op = segs[matched] as Extract<InlineSeg, { kind: 'delim' }>
    const useStrong = op.length >= 2 && closer.length >= 2
    const consume = useStrong ? 2 : 1
    const tag = useStrong ? 'strong' : 'em'

    const inner = segs.slice(matched + 1, i)
    // Per CM rule 15, any delimiter segments that ended up inside this
    // matched pair are removed from the delimiter stack — they can no
    // longer match further closers. Their characters still render as
    // literals via renderSegments.
    for (const s2 of inner) {
      if (s2.kind === 'delim') {
        s2.canOpen = false
        s2.canClose = false
      }
    }
    const replacement: InlineSeg[] = []
    op.length -= consume
    closer.length -= consume
    if (op.length > 0) replacement.push(op)
    replacement.push({ kind: 'html', value: `<${tag}>` })
    replacement.push(...inner)
    replacement.push({ kind: 'html', value: `</${tag}>` })
    if (closer.length > 0) replacement.push(closer)

    segs.splice(matched, i - matched + 1, ...replacement)
    // Restart scan just after the opener to allow further matches inside.
    i = matched + (op.length > 0 ? 1 : 0) + 1
  }
  return segs
}

// renderSegments produces the final HTML string. Leftover delim and
// bracket segments (unmatched) are rendered as their literal characters.
function renderSegments(segs: InlineSeg[]): string {
  let out = ''
  for (const seg of segs) {
    if (seg.kind === 'text') {
      out += escapeHtmlString(seg.value)
    } else if (seg.kind === 'html') {
      out += seg.value
    } else if (seg.kind === 'delim') {
      out += escapeHtmlString(seg.char.repeat(seg.length))
    } else {
      // bracket — render the original source verbatim (escaped).
      out += escapeHtmlString(seg.srcText)
    }
  }
  return out
}

// renderInline processes block-level text as inline markdown. It handles:
//   - backslash escapes (\<punct> or \<newline>)
//   - entity / numeric character references
//   - hard line breaks (2+ trailing spaces before \n, or backslash before \n)
//   - code spans (`...`)
//   - inline, reference, collapsed, and shortcut links + images
//   - autolinks `<uri>` and `<email>`
//   - raw HTML (open/close tags, comments, PI, CDATA, declarations)
//   - emphasis and strong (`*` / `_` delimiter runs)
function renderInline(s: string, refs: LinkRefMap = NO_REFS): string {
  const segs = tokenizeInline(s)
  processLinks(segs, refs, s)
  processEmphasis(segs)
  return renderSegments(segs)
}

function escapeHtmlChar(c: string): string {
  if (c === '&') return '&amp;'
  if (c === '<') return '&lt;'
  if (c === '>') return '&gt;'
  if (c === '"') return '&quot;'
  return c
}

function escapeHtmlString(s: string): string {
  let out = ''
  for (const c of s) out += escapeHtmlChar(c)
  return out
}

// gatherAllLinkRefs walks the top-level blocks (and recursively through
// blockquote content and list item content) and collects every link
// reference definition it finds. Refs defined inside a blockquote or list
// item are visible to the document as a whole.
function gatherAllLinkRefs(blocks: MdBlock[]): LinkRefMap {
  const refs: LinkRefMap = {}
  const absorb = (defs: any[] | undefined) => {
    if (!defs) return
    for (const d of defs) {
      const norm = normalizeLinkLabel(d.label)
      if (norm.length > 0 && !(norm in refs)) {
        refs[norm] = { url: d.url, title: d.title }
      }
    }
  }
  const visit = (bs: MdBlock[]) => {
    for (const b of bs) {
      // Implicit defs stashed on the block by @setext-promote for
      // paragraphs that were promoted to headings after leading ref
      // defs were stripped.
      absorb((b as any)._implicitRefDefs)
      if (b.type === 'paragraph') {
        let text = (b as any).text
        while (true) {
          const def = parseLinkRefDef(text)
          if (!def) break
          const norm = normalizeLinkLabel(def.label)
          if (norm.length > 0 && !(norm in refs)) {
            refs[norm] = { url: def.url, title: def.title }
          }
          text = text.slice(def.length)
        }
      } else if (b.type === 'blockquote') {
        try {
          visit(parseNested((b as any).text) as MdBlock[])
        } catch {}
      } else if (b.type === 'list') {
        for (const item of (b as any).items) {
          try {
            visit(parseNested(item.text) as MdBlock[])
          } catch {}
        }
      }
    }
  }
  visit(blocks)
  return refs
}

// Concatenate per-block html into a full document string. Gathers link
// reference definitions from the full block tree (including blockquote
// and list item content), then extracts top-level defs to drop
// defs-only paragraphs, and renders each surviving block against the
// merged ref map.
function toHtml(blocks: MdBlock[]): string {
  const allRefs = gatherAllLinkRefs(blocks)
  const { refs: topRefs, blocks: cleaned } = extractLinkRefsAndClean(blocks)
  const merged: LinkRefMap = { ...allRefs }
  for (const k of Object.keys(topRefs)) merged[k] = topRefs[k]
  let out = ''
  for (const b of cleaned) {
    const h = renderHtml(b, merged)
    if (h.length > 0) out += h + '\n'
  }
  return out
}


// Default option values.
Markdown.defaults = {
  codeFence: '```',
  trim: false,
  html: false,
} as MarkdownOptions

export { Markdown, buildMarkdownLineMatcher, renderHtml, toHtml }

export type { MarkdownOptions, MdBlock }

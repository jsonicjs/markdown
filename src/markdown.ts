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
    { s: '#MB'  g: 'md,blank' }
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


    // === Alt actions ===

    '@heading': (r: Rule) => {
      const v = r.o0.val as { level: number; text: string }
      const block: any = { type: 'heading', level: v.level, text: v.text }
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
    },

    '@hr': (r: Rule) => {
      const block: any = { type: 'hr' }
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
    },

    '@code': (r: Rule) => {
      const v = r.o0.val as { lang: string; text: string }
      const block: any = { type: 'code', lang: v.lang, text: v.text }
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
    },

    '@htmlblock': (r: Rule) => {
      const v = r.o0.val as string
      const block: any = { type: 'html', text: v }
      if (emitHtml) block.html = v
      r.node.push(block)
    },

    '@list-start': (r: Rule, ctx: Context) => {
      const v = r.o0.val as { ordered: boolean; text: string }
      const block: any = {
        type: 'list',
        ordered: v.ordered,
        items: [{ text: v.text }],
      }
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
      ctx.u.mdCurrent = block
    },

    '@list-append': (r: Rule, ctx: Context) => {
      const v = r.o0.val as { ordered: boolean; text: string }
      ctx.u.mdCurrent.items.push({ text: v.text })
      if (emitHtml) ctx.u.mdCurrent.html = renderHtml(ctx.u.mdCurrent)
    },

    '@quote-start': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block: any = { type: 'blockquote', text: v }
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
      ctx.u.mdCurrent = block
    },

    '@quote-append': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      ctx.u.mdCurrent.text += '\n' + v
      if (emitHtml) ctx.u.mdCurrent.html = renderHtml(ctx.u.mdCurrent)
    },

    '@para-start': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block: any = {
        type: 'paragraph',
        text: options.trim ? v.trim() : v,
      }
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
      ctx.u.mdCurrent = block
    },

    '@para-append': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      ctx.u.mdCurrent.text += '\n' + (options.trim ? v.trim() : v)
      if (emitHtml) ctx.u.mdCurrent.html = renderHtml(ctx.u.mdCurrent)
    },

    // Setext underline encountered while accumulating a paragraph: rewrite
    // the current block in place as a heading. The grammar drops out of
    // para-tail after this action, so the underline token is consumed.
    '@setext-promote': (_r: Rule, ctx: Context) => {
      const { level } = r_o0_val(_r) as { level: number }
      const block = ctx.u.mdCurrent
      block.type = 'heading'
      block.level = level
      block.text = block.text.trim()
      if (emitHtml) block.html = renderHtml(block)
    },

    '@icode-start': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      const block: any = { type: 'code', lang: '', text: v }
      if (emitHtml) block.html = renderHtml(block)
      r.node.push(block)
      ctx.u.mdCurrent = block
    },

    '@icode-append': (r: Rule, ctx: Context) => {
      const v = r.o0.val as string
      ctx.u.mdCurrent.text += '\n' + v
      if (emitHtml) ctx.u.mdCurrent.html = renderHtml(ctx.u.mdCurrent)
    },
  }

  // Helper used by @setext-promote so the body reads naturally.
  function r_o0_val(r: Rule): any {
    return r.o0.val
  }


  // Parse embedded grammar definition using a separate standard Jsonic instance,
  // attach the refs map, and register with the current instance.
  const grammarDef = Jsonic.make()(grammarText)
  grammarDef.ref = refs
  jsonic.grammar(grammarDef)
}


// stripIndent removes up to 3 leading spaces from a line. CommonMark allows
// that much indentation on most block-level constructs before they count as
// indented code.
function stripIndent(s: string): string {
  let i = 0
  while (i < 3 && i < s.length && s[i] === ' ') i++
  return s.slice(i)
}

// HTML block recognition tables (CommonMark § 4.6). We implement types 1-5
// (distinct end markers) and type 7 (any well-formed tag on a line by
// itself, terminated by a blank line). Type 6 (the long list of
// block-level tag names) is folded into the type-7 fallthrough for
// simplicity.
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
      // continuation, setext underline recognition, etc.).
      const lexAny = lex as any
      const state: { last: string } =
        lexAny.__md ?? (lexAny.__md = { last: 'start' })

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

      let tkn
      let srcPart: string
      let kind = 'text'

      // Classify the line.

      // Blank line.
      if (/^[ \t]*$/.test(lineContent)) {
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MB', null, srcPart, pnt)
        kind = 'blank'
      }

      // HTML block: consume contiguous lines through the type's end marker
      // (types 1-5) or through the next blank line (type 7).
      else if (
        HTML_BLOCK_T1_OPEN.test(lineContent) ||
        HTML_BLOCK_T2_OPEN.test(lineContent) ||
        HTML_BLOCK_T3_OPEN.test(lineContent) ||
        HTML_BLOCK_T5_OPEN.test(lineContent) ||
        HTML_BLOCK_T4_OPEN.test(lineContent) ||
        (state.last !== 'text' && HTML_BLOCK_T7_OPEN.test(lineContent))
      ) {
        const type1 = HTML_BLOCK_T1_OPEN.test(lineContent)
        const type2 = HTML_BLOCK_T2_OPEN.test(lineContent)
        const type3 = HTML_BLOCK_T3_OPEN.test(lineContent)
        const type5 = HTML_BLOCK_T5_OPEN.test(lineContent)
        const type4 =
          !type1 && !type2 && !type3 && !type5 && HTML_BLOCK_T4_OPEN.test(lineContent)

        let closeRe: RegExp | null
        let terminateOnBlank = false
        if (type1) closeRe = HTML_BLOCK_T1_CLOSE
        else if (type2) closeRe = HTML_BLOCK_T2_CLOSE
        else if (type3) closeRe = HTML_BLOCK_T3_CLOSE
        else if (type4) closeRe = HTML_BLOCK_T4_CLOSE
        else if (type5) closeRe = HTML_BLOCK_T5_CLOSE
        else {
          closeRe = null
          terminateOnBlank = true
        }

        const htmlLines: string[] = []
        let htmlEnd = sI
        let done = false
        while (htmlEnd < srclen && !done) {
          let le = htmlEnd
          while (le < srclen && src[le] !== '\n') le++
          let innerLine = src.substring(htmlEnd, le)
          if (innerLine.endsWith('\r')) innerLine = innerLine.slice(0, -1)
          const nextEnd = le < srclen ? le + 1 : le

          if (terminateOnBlank && /^[ \t]*$/.test(innerLine)) {
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
            const lang = infoRaw.trim().split(/\s+/)[0] || ''
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

            const codeText = codeLines.join('\n')
            consumeEnd = codeEnd
            srcPart = src.substring(sI, consumeEnd)
            tkn = lex.token('#MC', { lang, text: codeText }, srcPart, pnt)
            kind = 'code'
          }
        }
      }

      // ATX heading: up to 3 leading spaces, 1-6 `#`, then space+text or end
      // of line. Trailing `#` sequences preceded by whitespace are stripped.
      else if (
        /^ {0,3}(#{1,6})(?:[ \t]+.*?)?(?:[ \t]+#+)?[ \t]*$/.test(lineContent)
      ) {
        const m = lineContent.match(
          /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$/,
        )!
        const textRaw = (m[2] ?? '').trim()
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
        tkn = lex.token('#MSX', { level }, srcPart, pnt)
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

      // Ordered list item.
      else if (/^\s*(\d+)[.)]\s+/.test(lineContent)) {
        const m = lineContent.match(/^\s*(\d+)[.)]\s+(.*)$/)!
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#ML', { ordered: true, text: m[2] }, srcPart, pnt)
        kind = 'list'
      }

      // Unordered list item.
      else if (/^\s*[-*+]\s+/.test(lineContent)) {
        const m = lineContent.match(/^\s*[-*+]\s+(.*)$/)!
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#ML', { ordered: false, text: m[1] }, srcPart, pnt)
        kind = 'list'
      }

      // Blockquote line.
      else if (/^\s*>\s?/.test(lineContent)) {
        const m = lineContent.match(/^\s*>\s?(.*)$/)!
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MQ', m[1], srcPart, pnt)
        kind = 'quote'
      }

      // Indented line (4+ spaces or leading tab): indented code block,
      // unless we're currently inside a paragraph (in which case it's
      // paragraph continuation).
      else if (/^(?:    |\t)/.test(lineContent)) {
        if (state.last === 'text') {
          srcPart = src.substring(sI, consumeEnd)
          tkn = lex.token('#MT', lineContent, srcPart, pnt)
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

      // Plain text line.
      else {
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MT', lineContent, srcPart, pnt)
        kind = 'text'
      }

      state.last = kind

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
      return `<p>${renderInline(block.text, refs)}</p>`
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
      const items = block.items
        .map((it: any) => `<li>${renderInline(it.text, refs)}</li>`)
        .join('\n')
      return `<${tag}>\n${items}\n</${tag}>`
    }
    case 'blockquote':
      return (
        `<blockquote>\n<p>` +
        renderInline(block.text, refs) +
        `</p>\n</blockquote>`
      )
    case 'html':
      return block.text
  }
  return ''
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

// Named HTML entities recognized by the inline parser. This is a small
// common subset rather than the full HTML5 named entity table; numeric
// references (&#dec; and &#xhex;) are always decoded.
const NAMED_ENTITIES: Record<string, string> = {
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
  nbsp: '\u00A0',
  copy: '\u00A9',
  reg: '\u00AE',
  trade: '\u2122',
  hellip: '\u2026',
  mdash: '\u2014',
  ndash: '\u2013',
  lsquo: '\u2018',
  rsquo: '\u2019',
  ldquo: '\u201C',
  rdquo: '\u201D',
  laquo: '\u00AB',
  raquo: '\u00BB',
  para: '\u00B6',
  sect: '\u00A7',
  middot: '\u00B7',
  bull: '\u2022',
  deg: '\u00B0',
  plusmn: '\u00B1',
  times: '\u00D7',
  divide: '\u00F7',
  pound: '\u00A3',
  euro: '\u20AC',
  yen: '\u00A5',
  cent: '\u00A2',
  Auml: '\u00C4',
  Ouml: '\u00D6',
  Uuml: '\u00DC',
  auml: '\u00E4',
  ouml: '\u00F6',
  uuml: '\u00FC',
  szlig: '\u00DF',
  agrave: '\u00E0',
  eacute: '\u00E9',
  egrave: '\u00E8',
  aring: '\u00E5',
  oslash: '\u00F8',
  AElig: '\u00C6',
  aelig: '\u00E6',
  frac12: '\u00BD',
  frac14: '\u00BC',
  frac34: '\u00BE',
  iexcl: '\u00A1',
  iquest: '\u00BF',
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
    }

// Link reference definition — produced by extracting `[label]: url "title"`
// paragraphs from the parsed block list.
type LinkRef = { url: string; title: string }
type LinkRefMap = Record<string, LinkRef>

const NO_REFS: LinkRefMap = {}

// ASCII punctuation used for CommonMark flanking classification. The full
// spec uses Unicode punctuation; this is a close ASCII approximation.
const ASCII_PUNCT = /[!-/:-@\[-`{-~]/

function isWhitespaceChar(c: string): boolean {
  return c === '' || c === ' ' || c === '\t' || c === '\n' || c === '\r'
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
        url += s[k + 1]
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
        url += s[j + 1]
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
        title += s[k + 1]
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
// leading/trailing whitespace, collapse interior whitespace to single
// spaces, case-fold via toLowerCase.
function normalizeLinkLabel(s: string): string {
  return s.trim().toLowerCase().replace(/[ \t\r\n]+/g, ' ')
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
      if (label.split('\n').length > 2) return null
      continue
    }
    if (c === ']') {
      labelEnd = i
      break
    }
    if (c === '\\' && i + 1 < text.length) {
      label += text[i + 1]
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

  // URL: angle-bracket or plain.
  let url = ''
  if (text[i] === '<') {
    let k = i + 1
    while (k < text.length && text[k] !== '>' && text[k] !== '\n' && text[k] !== '<') {
      if (text[k] === '\\' && k + 1 < text.length) {
        url += text[k + 1]
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
        url += text[i + 1]
        i += 2
        continue
      }
      url += c
      i++
    }
    if (url.length === 0) return null
  }

  // Optional title on same line, or on the next line after whitespace.
  let titleEnd = i
  let title = ''
  let j = i
  let titleNewlines = 0
  // Save position in case we need to back out (title on following line
  // requires whitespace including newline before the title).
  while (j < text.length && /[ \t]/.test(text[j])) j++
  let hadNewlineBeforeTitle = false
  if (j < text.length && text[j] === '\n') {
    j++
    hadNewlineBeforeTitle = true
    while (j < text.length && /[ \t]/.test(text[j])) j++
  }
  if (j < text.length && (text[j] === '"' || text[j] === "'" || text[j] === '(')) {
    const openQ = text[j]
    const closeQ = openQ === '(' ? ')' : openQ
    let k = j + 1
    let ok = false
    const tbuf: string[] = []
    while (k < text.length) {
      const c = text[k]
      if (c === '\\' && k + 1 < text.length) {
        tbuf.push(text[k + 1])
        k += 2
        continue
      }
      if (c === closeQ) {
        ok = true
        break
      }
      if (c === '\n') {
        titleNewlines++
        if (titleNewlines > 1) break
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
    void hadNewlineBeforeTitle
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
      out.push({ ...b, text })
    }
  }
  return { refs, blocks: out }
}

// encodeLinkUrl applies a minimal URL normalization to the destination URL:
// only `&`, `<`, `>`, `"`, and ` ` are replaced, matching the subset of
// CommonMark's full percent-encoding that most spec tests exercise.
function encodeLinkUrl(url: string): string {
  return url
    .replace(/&(?!#x?[0-9a-fA-F]+;|[a-zA-Z][a-zA-Z0-9]+;)/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '%22')
    .replace(/ /g, '%20')
}

// innerText extracts a best-effort plain-text rendering of a segment list,
// used as alt text for images. Nested markup is stripped.
function innerText(segs: InlineSeg[]): string {
  let out = ''
  for (const seg of segs) {
    if (seg.kind === 'text') out += seg.value
    else if (seg.kind === 'delim') out += seg.char.repeat(seg.length)
    else if (seg.kind === 'bracket') {
      out += seg.open ? (seg.image ? '![' : '[') : ']'
    } else {
      // html segment: strip tags, keep textual content
      out += seg.value.replace(/<[^>]*>/g, '')
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

      // Autolink URI: <scheme:path>
      let m = rest.match(
        /^<([a-zA-Z][a-zA-Z0-9.+-]{1,31}:[^\s<>\x00-\x1f\x7f]*)>/,
      )
      if (m) {
        segs.push({
          kind: 'html',
          value:
            '<a href="' +
            encodeLinkUrl(m[1]) +
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
            encodeLinkUrl(m[1]) +
            '">' +
            escapeHtmlString(m[1]) +
            '</a>',
        })
        i += m[0].length
        continue
      }

      // Raw HTML: open tag, close tag, comment, PI, declaration, CDATA.
      m = rest.match(/^<!--(?:[^-]|-[^-]|--[^>])*?-->/)
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

    // Hard line break via 2+ trailing spaces.
    if (c === '\n') {
      const last = segs[segs.length - 1]
      if (last && last.kind === 'text' && /  $/.test(last.value)) {
        last.value = last.value.replace(/ +$/, '')
        if (last.value.length === 0) segs.pop()
        segs.push({ kind: 'html', value: '<br />\n' })
        i++
        continue
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
): InlineSeg[] {
  let i = 0
  while (i < segs.length) {
    const close = segs[i]
    if (close.kind !== 'bracket' || close.open) {
      i++
      continue
    }

    let j = i - 1
    let openIdx = -1
    while (j >= 0) {
      const op = segs[j]
      if (op.kind === 'bracket' && op.open && op.active) {
        openIdx = j
        break
      }
      j--
    }

    if (openIdx < 0) {
      // Unmatched close bracket — deactivate and move on.
      i++
      continue
    }

    const open = segs[openIdx] as Extract<InlineSeg, { kind: 'bracket' }>
    const inner = segs.slice(openIdx + 1, i)

    // Resolve the target: inline url, or lookup via refs.
    let url: string | undefined
    let title: string | undefined
    if (close.url !== undefined) {
      url = close.url
      title = close.title
    } else {
      let label: string | undefined
      if (close.refLabel) {
        label = close.refLabel
      } else if (close.refCollapsed || close.refShortcut) {
        label = innerText(inner)
      }
      if (label !== undefined) {
        const ref = refs[normalizeLinkLabel(label)]
        if (ref) {
          url = ref.url
          title = ref.title
        }
      }
    }

    if (url === undefined) {
      // No match. Deactivate the open bracket (for shortcut only) and
      // continue past this close bracket.
      open.active = false
      i++
      continue
    }

    // Recursively process nested links then emphasis within link text.
    const nested = processLinks(inner, refs)
    processEmphasis(nested)

    let html: string
    if (open.image) {
      const alt = innerText(nested)
      const titleAttr = title
        ? ` title="${escapeHtmlString(title)}"`
        : ''
      html = `<img src="${encodeLinkUrl(url)}" alt="${escapeHtmlString(
        alt,
      )}"${titleAttr} />`
    } else {
      const inside = renderSegments(nested)
      const titleAttr = title
        ? ` title="${escapeHtmlString(title)}"`
        : ''
      html = `<a href="${encodeLinkUrl(url)}"${titleAttr}>${inside}</a>`
    }

    segs.splice(openIdx, i - openIdx + 1, {
      kind: 'html',
      value: html,
    })

    // Per CommonMark, matching a link deactivates all earlier `[` openers
    // to prevent nested <a> elements. (Images may still be nested.)
    if (!open.image) {
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
      // bracket
      out += escapeHtmlString(
        seg.open ? (seg.image ? '![' : '[') : ']',
      )
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
  processLinks(segs, refs)
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

// Concatenate per-block html into a full document string. First extracts
// link reference definitions from paragraph blocks and re-renders each
// remaining block against the resulting refs map so reference-style links
// resolve. Each rendered block contributes its html followed by a newline,
// matching the shape CommonMark spec tests expect.
function toHtml(blocks: MdBlock[]): string {
  const { refs, blocks: cleaned } = extractLinkRefsAndClean(blocks)
  let out = ''
  for (const b of cleaned) {
    const h = renderHtml(b, refs)
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

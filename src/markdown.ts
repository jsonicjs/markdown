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

      // Fenced code block start.
      else if (stripIndent(lineContent).startsWith(fence)) {
        const stripped = stripIndent(lineContent)
        const lang = stripped.substring(fence.length).trim()
        const codeLines: string[] = []
        let codeEnd = consumeEnd
        let closed = false

        while (codeEnd < srclen) {
          let innerEnd = codeEnd
          while (innerEnd < srclen && src[innerEnd] !== '\n') innerEnd++
          let innerLine = src.substring(codeEnd, innerEnd)
          if (innerLine.endsWith('\r')) innerLine = innerLine.slice(0, -1)

          const nextEnd = innerEnd < srclen ? innerEnd + 1 : innerEnd

          if (stripIndent(innerLine).startsWith(fence)) {
            codeEnd = nextEnd
            closed = true
            break
          }
          codeLines.push(innerLine)
          codeEnd = nextEnd
        }

        // If the fence was never closed, still produce a code block from what
        // we captured rather than erroring.
        void closed

        const codeText = codeLines.join('\n')
        consumeEnd = codeEnd
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MC', { lang, text: codeText }, srcPart, pnt)
        kind = 'code'
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
// renderInline so backslash escapes, entity references, and hard line breaks
// are handled. Inline emphasis, links, code spans, and autolinks are not
// yet implemented.
function renderHtml(block: any): string {
  switch (block.type) {
    case 'heading':
      return `<h${block.level}>${renderInline(block.text)}</h${block.level}>`
    case 'paragraph':
      return `<p>${renderInline(block.text)}</p>`
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
        .map((it: any) => `<li>${renderInline(it.text)}</li>`)
        .join('\n')
      return `<${tag}>\n${items}\n</${tag}>`
    }
    case 'blockquote':
      return (
        `<blockquote>\n<p>` +
        renderInline(block.text) +
        `</p>\n</blockquote>`
      )
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

// renderInline processes block-level text as inline markdown. It currently
// handles:
//   - backslash escapes (\<punct> or \<newline>)
//   - entity / numeric character references
//   - hard line breaks (2+ trailing spaces before \n, or backslash before \n)
//   - code spans (`...`, with matching-length backtick runs)
// All other characters are HTML-escaped as needed. Inline emphasis, links,
// and autolinks are not yet implemented.
function renderInline(s: string): string {
  let out = ''
  let i = 0
  const n = s.length

  while (i < n) {
    const c = s[i]

    // Backslash escape or hard line break via backslash.
    if (c === '\\' && i + 1 < n) {
      const next = s[i + 1]
      if (next === '\n') {
        out += '<br />\n'
        i += 2
        continue
      }
      if (BACKSLASH_ESCAPABLE.indexOf(next) >= 0) {
        out += escapeHtmlChar(next)
        i += 2
        continue
      }
    }

    // Code span: a run of N backticks is closed by the next run of exactly
    // N backticks. Content is literal (no inline processing), newlines
    // collapse to spaces, and a single matching leading+trailing space is
    // stripped when both exist and the content is not all spaces.
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
        out += '<code>' + escapeHtmlString(content) + '</code>'
        i = found + openLen
        continue
      }

      // No matching close: emit the backticks literally and continue.
      out += '`'.repeat(openLen)
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
          out += escapeHtmlString(decoded)
          i += m[0].length
          continue
        }
      }
    }

    // Hard line break via 2+ trailing spaces.
    if (c === '\n') {
      let trailing = 0
      while (trailing < out.length && out[out.length - 1 - trailing] === ' ') {
        trailing++
      }
      if (trailing >= 2) {
        out = out.slice(0, out.length - trailing) + '<br />\n'
        i++
        continue
      }
      out += '\n'
      i++
      continue
    }

    out += escapeHtmlChar(c)
    i++
  }

  return out
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

// Concatenate per-block html fields into a full document string. Each block
// contributes its html followed by a trailing newline, matching the shape
// CommonMark spec tests expect.
function toHtml(blocks: MdBlock[]): string {
  let out = ''
  for (const b of blocks) {
    if (b.html !== undefined) out += b.html + '\n'
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

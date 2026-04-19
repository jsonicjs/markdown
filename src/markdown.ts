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
  }


  // Parse embedded grammar definition using a separate standard Jsonic instance,
  // attach the refs map, and register with the current instance.
  const grammarDef = Jsonic.make()(grammarText)
  grammarDef.ref = refs
  jsonic.grammar(grammarDef)
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

      // Classify the line.

      // Blank line.
      if (/^\s*$/.test(lineContent)) {
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MB', null, srcPart, pnt)
      }

      // Fenced code block start.
      else if (lineContent.startsWith(fence)) {
        const lang = lineContent.substring(fence.length).trim()
        const codeLines: string[] = []
        let codeEnd = consumeEnd
        let closed = false

        while (codeEnd < srclen) {
          let innerEnd = codeEnd
          while (innerEnd < srclen && src[innerEnd] !== '\n') innerEnd++
          let innerLine = src.substring(codeEnd, innerEnd)
          if (innerLine.endsWith('\r')) innerLine = innerLine.slice(0, -1)

          const nextEnd = innerEnd < srclen ? innerEnd + 1 : innerEnd

          if (innerLine.startsWith(fence)) {
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
      }

      // Heading: 1-6 leading # followed by a space.
      else if (/^#{1,6}\s+/.test(lineContent)) {
        const m = lineContent.match(/^(#{1,6})\s+(.*)$/)!
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#MH',
          { level: m[1].length, text: m[2].replace(/\s+#+\s*$/, '').trim() },
          srcPart,
          pnt,
        )
      }

      // Horizontal rule: line of 3+ `-`, `*`, or `_`, optionally spaced.
      else if (/^[ \t]{0,3}([-*_])[ \t]*\1[ \t]*\1([ \t]*\1)*[ \t]*$/.test(lineContent)) {
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MR', null, srcPart, pnt)
      }

      // Ordered list item.
      else if (/^\s*(\d+)[.)]\s+/.test(lineContent)) {
        const m = lineContent.match(/^\s*(\d+)[.)]\s+(.*)$/)!
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#ML',
          { ordered: true, text: m[2] },
          srcPart,
          pnt,
        )
      }

      // Unordered list item.
      else if (/^\s*[-*+]\s+/.test(lineContent)) {
        const m = lineContent.match(/^\s*[-*+]\s+(.*)$/)!
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token(
          '#ML',
          { ordered: false, text: m[1] },
          srcPart,
          pnt,
        )
      }

      // Blockquote line.
      else if (/^\s*>\s?/.test(lineContent)) {
        const m = lineContent.match(/^\s*>\s?(.*)$/)!
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MQ', m[1], srcPart, pnt)
      }

      // Plain text line.
      else {
        srcPart = src.substring(sI, consumeEnd)
        tkn = lex.token('#MT', lineContent, srcPart, pnt)
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
// block-level constructs this parser supports. Inline emphasis, links, code
// spans, entity references, and similar are not implemented.
function renderHtml(block: any): string {
  switch (block.type) {
    case 'heading':
      return `<h${block.level}>${escapeHtml(block.text)}</h${block.level}>`
    case 'paragraph':
      return `<p>${escapeHtml(block.text)}</p>`
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
        .map((it: any) => `<li>${escapeHtml(it.text)}</li>`)
        .join('\n')
      return `<${tag}>\n${items}\n</${tag}>`
    }
    case 'blockquote':
      return (
        `<blockquote>\n<p>` +
        escapeHtml(block.text) +
        `</p>\n</blockquote>`
      )
  }
  return ''
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
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

/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

import { describe, test } from 'node:test'
import assert from 'node:assert'

import { Jsonic } from 'jsonic'
import { Markdown } from '../dist/markdown'

describe('markdown', () => {
  test('empty', () => {
    const md = Jsonic.make().use(Markdown)
    assert.deepEqual(md(''), [])
    assert.deepEqual(md('\n'), [])
    assert.deepEqual(md('\n\n\n'), [])
  })

  test('heading', () => {
    const md = Jsonic.make().use(Markdown)
    assert.deepEqual(md('# Title'), [
      { type: 'heading', level: 1, text: 'Title' },
    ])
    assert.deepEqual(md('## Subtitle'), [
      { type: 'heading', level: 2, text: 'Subtitle' },
    ])
    assert.deepEqual(md('###### Deep'), [
      { type: 'heading', level: 6, text: 'Deep' },
    ])
    assert.deepEqual(md('# One\n## Two\n### Three'), [
      { type: 'heading', level: 1, text: 'One' },
      { type: 'heading', level: 2, text: 'Two' },
      { type: 'heading', level: 3, text: 'Three' },
    ])
  })

  test('paragraph', () => {
    const md = Jsonic.make().use(Markdown)
    assert.deepEqual(md('Hello world.'), [
      { type: 'paragraph', text: 'Hello world.' },
    ])
    assert.deepEqual(md('Line one.\nLine two.'), [
      { type: 'paragraph', text: 'Line one.\nLine two.' },
    ])
    assert.deepEqual(md('P1\n\nP2'), [
      { type: 'paragraph', text: 'P1' },
      { type: 'paragraph', text: 'P2' },
    ])
  })

  test('horizontal-rule', () => {
    const md = Jsonic.make().use(Markdown)
    assert.deepEqual(md('---'), [{ type: 'hr' }])
    assert.deepEqual(md('***'), [{ type: 'hr' }])
    assert.deepEqual(md('___'), [{ type: 'hr' }])
    assert.deepEqual(md('- - -'), [{ type: 'hr' }])
  })

  test('unordered-list', () => {
    const md = Jsonic.make().use(Markdown)
    assert.deepEqual(md('- a\n- b\n- c'), [
      {
        type: 'list',
        ordered: false,
        items: [{ text: 'a' }, { text: 'b' }, { text: 'c' }],
      },
    ])
    assert.deepEqual(md('* x\n* y'), [
      {
        type: 'list',
        ordered: false,
        items: [{ text: 'x' }, { text: 'y' }],
      },
    ])
  })

  test('ordered-list', () => {
    const md = Jsonic.make().use(Markdown)
    assert.deepEqual(md('1. first\n2. second\n3. third'), [
      {
        type: 'list',
        ordered: true,
        items: [
          { text: 'first' },
          { text: 'second' },
          { text: 'third' },
        ],
      },
    ])
  })

  test('blockquote', () => {
    const md = Jsonic.make().use(Markdown)
    assert.deepEqual(md('> quoted'), [
      { type: 'blockquote', text: 'quoted' },
    ])
    assert.deepEqual(md('> line 1\n> line 2'), [
      { type: 'blockquote', text: 'line 1\nline 2' },
    ])
  })

  test('code-block', () => {
    const md = Jsonic.make().use(Markdown)
    // Fenced code text retains the terminal newline of the last content
    // line so a trailing blank line (e.g. "foo\n\n") is not lost.
    assert.deepEqual(md('```\nplain code\n```'), [
      { type: 'code', lang: '', text: 'plain code\n' },
    ])
    assert.deepEqual(md('```js\nconst x = 1\nconst y = 2\n```'), [
      { type: 'code', lang: 'js', text: 'const x = 1\nconst y = 2\n' },
    ])
    // Unclosed fence captures to end.
    assert.deepEqual(md('```py\nprint(1)\n'), [
      { type: 'code', lang: 'py', text: 'print(1)\n' },
    ])
  })

  test('mixed-document', () => {
    const md = Jsonic.make().use(Markdown)
    const src = `# Title

Intro paragraph
spanning two lines.

## Subsection

- one
- two

> quote line

\`\`\`ts
let a = 1
\`\`\`

---

End.
`
    assert.deepEqual(md(src), [
      { type: 'heading', level: 1, text: 'Title' },
      {
        type: 'paragraph',
        text: 'Intro paragraph\nspanning two lines.',
      },
      { type: 'heading', level: 2, text: 'Subsection' },
      {
        type: 'list',
        ordered: false,
        items: [{ text: 'one' }, { text: 'two' }],
      },
      { type: 'blockquote', text: 'quote line' },
      { type: 'code', lang: 'ts', text: 'let a = 1\n' },
      { type: 'hr' },
      { type: 'paragraph', text: 'End.' },
    ])
  })

  test('crlf', () => {
    const md = Jsonic.make().use(Markdown)
    assert.deepEqual(md('# Title\r\n\r\nA line.\r\nAnother.\r\n'), [
      { type: 'heading', level: 1, text: 'Title' },
      { type: 'paragraph', text: 'A line.\nAnother.' },
    ])
  })

  test('trim-option', () => {
    const md = Jsonic.make().use(Markdown, { trim: true })
    assert.deepEqual(md('  hello  \n  world  '), [
      { type: 'paragraph', text: 'hello\nworld' },
    ])
  })
})

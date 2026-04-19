/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

// CommonMark spec validation.
//
// Loads the CommonMark 0.31.2 spec.json (vendored at
// test/fixtures/commonmark-spec.json) and runs every example through the
// parser with { html: true }, then compares the rendered HTML against the
// expected HTML from the spec. Results are grouped by spec section and
// printed as a conformance summary, plus an assertion that the overall pass
// count does not regress below a recorded floor.
//
// This plugin targets block-level markdown only and does NOT implement
// inline constructs (emphasis, links, code spans, entity references, hard
// breaks), setext headings, indented code blocks, link reference
// definitions, or HTML blocks. The vast majority of spec examples exercise
// those features, so the overall pass rate is expected to be modest — the
// point of this test is to track conformance on the block-level subset we
// do support and catch regressions.

import { describe, test } from 'node:test'
import assert from 'node:assert'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { Jsonic } from 'jsonic'
import { Markdown, toHtml } from '../dist/markdown'

type SpecCase = {
  markdown: string
  html: string
  example: number
  section: string
}

const specPath = join(
  __dirname,
  '..',
  'test',
  'fixtures',
  'commonmark-spec.json',
)
const spec: SpecCase[] = JSON.parse(readFileSync(specPath, 'utf8'))

// Minimum expected pass count — acts as a regression floor. Bump upward as
// the parser improves. Never decrease without an explanation in the commit.
// Current baseline: 575/652 (step 16: link URL/title normalization).
const MIN_PASSES = 570

describe('commonmark-spec', () => {
  test('conformance-summary', () => {
    const md = Jsonic.make().use(Markdown, { html: true })

    const bySection: Record<string, { pass: number; fail: number }> = {}
    let totalPass = 0
    let totalFail = 0
    const failures: { example: number; section: string }[] = []

    for (const ex of spec) {
      const bucket = (bySection[ex.section] ??= { pass: 0, fail: 0 })
      let got = ''
      try {
        const blocks = md(ex.markdown)
        got = toHtml(blocks)
      } catch {
        got = ''
      }
      if (got === ex.html) {
        bucket.pass++
        totalPass++
      } else {
        bucket.fail++
        totalFail++
        if (failures.length < 10) {
          failures.push({ example: ex.example, section: ex.section })
        }
      }
    }

    const sections = Object.keys(bySection).sort()
    const lines: string[] = []
    lines.push(`CommonMark 0.31.2 conformance: ${totalPass}/${spec.length}`)
    for (const name of sections) {
      const s = bySection[name]
      const total = s.pass + s.fail
      lines.push(`  ${name.padEnd(42)} ${s.pass}/${total}`)
    }
    console.log(lines.join('\n'))

    assert.ok(
      totalPass >= MIN_PASSES,
      `CommonMark conformance regressed: ${totalPass} passes < floor ${MIN_PASSES}. First failures: ${JSON.stringify(failures)}`,
    )
    assert.strictEqual(
      totalPass + totalFail,
      spec.length,
      'every spec example should be counted',
    )
  })

  // Per-section subset tests that MUST pass on the block-level features the
  // parser implements. These are curated block-only cases (no inline
  // constructs, entities, or setext/indented variants) drawn from the spec.
  test('atx-headings-block-subset', () => {
    const md = Jsonic.make().use(Markdown, { html: true })

    const cases: [string, string][] = [
      ['# foo\n', '<h1>foo</h1>\n'],
      ['## foo\n', '<h2>foo</h2>\n'],
      ['### foo\n', '<h3>foo</h3>\n'],
      ['#### foo\n', '<h4>foo</h4>\n'],
      ['##### foo\n', '<h5>foo</h5>\n'],
      ['###### foo\n', '<h6>foo</h6>\n'],
    ]

    for (const [src, want] of cases) {
      const got = toHtml(md(src))
      assert.strictEqual(got, want, `heading: ${JSON.stringify(src)}`)
    }
  })

  test('thematic-breaks-block-subset', () => {
    const md = Jsonic.make().use(Markdown, { html: true })

    const cases: [string, string][] = [
      ['***\n', '<hr />\n'],
      ['---\n', '<hr />\n'],
      ['___\n', '<hr />\n'],
      [' - - -\n', '<hr />\n'],
      [' **  * ** * ** * **\n', '<hr />\n'],
    ]

    for (const [src, want] of cases) {
      const got = toHtml(md(src))
      assert.strictEqual(got, want, `hr: ${JSON.stringify(src)}`)
    }
  })

  test('fenced-code-block-subset', () => {
    const md = Jsonic.make().use(Markdown, { html: true })

    const cases: [string, string][] = [
      ['```\n<\n >\n```\n', '<pre><code>&lt;\n &gt;\n</code></pre>\n'],
      [
        '```ruby\ndef foo(x)\n  return 3\nend\n```\n',
        '<pre><code class="language-ruby">def foo(x)\n  return 3\nend\n</code></pre>\n',
      ],
    ]

    for (const [src, want] of cases) {
      const got = toHtml(md(src))
      assert.strictEqual(got, want, `code: ${JSON.stringify(src)}`)
    }
  })
})

# Markdown plugin for Jsonic (TypeScript)

A Jsonic syntax plugin that parses CommonMark markdown into a
structured array of block nodes, and (optionally) renders it back to
HTML. Passes **all 652 examples** of the CommonMark 0.31.2 spec.

```bash
npm install @jsonic/csv
```

The package `@jsonic/csv` ships the `Markdown` plugin as an additional
named export — the csv and markdown plugins live in the same repo and
npm package.

Requires `jsonic` >= 2 as a peer dependency.

This documentation follows the [Diataxis](https://diataxis.fr)
framework: a **tutorial** for first-time users, **how-to guides**
for specific tasks, **explanation** of key concepts, and a complete
**reference**.


## Tutorial: parse a README and render it as HTML

This tutorial walks you through the plugin end-to-end. You'll parse
a short README, inspect the block tree, render it to HTML, and
finally extract the heading outline.

Create a new project:

```bash
mkdir md-tutorial && cd md-tutorial
npm init -y
npm install jsonic @jsonic/csv
```

### Step 1 — parse a document to blocks

Create `tutorial.ts`:

```typescript
import { Jsonic } from 'jsonic'
import { Markdown } from '@jsonic/csv/dist/markdown'

const parseMd = Jsonic.make().use(Markdown)

const source = `# Hello

A short *readme* with a [link](https://example.com).

- one
- two
- three
`

console.dir(parseMd(source), { depth: null })
```

You get an array of typed block objects — headings, paragraphs,
lists, etc. Each element has a `type` discriminator and the fields
relevant to that block.

```text
[
  { type: 'heading', level: 1, text: 'Hello' },
  { type: 'paragraph', text: 'A short *readme* with a [link](https://example.com).' },
  {
    type: 'list',
    ordered: false,
    items: [ { text: 'one' }, { text: 'two' }, { text: 'three' } ]
  }
]
```

Notice that the paragraph's `text` still contains the raw markdown
markup (`*readme*`, `[link](...)`): block parsing and inline
rendering are separate stages.

### Step 2 — render it as HTML

Enable inline rendering by turning on `html` and then using `toHtml`
to walk the blocks. `toHtml` handles cross-block concerns like link
reference definitions:

```typescript
import { Markdown, toHtml } from '@jsonic/csv/dist/markdown'

const parseMd = Jsonic.make().use(Markdown, { html: true })
console.log(toHtml(parseMd(source)))
```

Output:

```html
<h1>Hello</h1>
<p>A short <em>readme</em> with a <a href="https://example.com">link</a>.</p>
<ul>
<li>one</li>
<li>two</li>
<li>three</li>
</ul>
```

### Step 3 — build a heading outline

Because `parseMd` returns plain data, building secondary structures
is a one-liner:

```typescript
const outline = parseMd(source)
  .filter(b => b.type === 'heading')
  .map(h => ({ level: (h as any).level, text: (h as any).text }))

console.log(outline)
// [ { level: 1, text: 'Hello' } ]
```

### Step 4 — resolve link references across a document

`toHtml` understands link reference definitions that appear anywhere
in the document:

```typescript
const withRefs = `
See the [Jsonic docs][j] for details.

[j]: https://jsonic.senecajs.org "Jsonic"
`

console.log(toHtml(parseMd(withRefs)))
// <p>See the <a href="https://jsonic.senecajs.org" title="Jsonic">Jsonic docs</a> for details.</p>
```

That's it — you've parsed a document, rendered it, and built a
derived view from the same block array.


## How-to guides

Each recipe assumes the plugin is loaded; drop these in where you
need them.

### Turn on HTML rendering

```typescript
const parseMd = Jsonic.make().use(Markdown, { html: true })

// Every block now also has an `html` field:
parseMd('# Title')[0]
// { type: 'heading', level: 1, text: 'Title', html: '<h1>Title</h1>' }
```

### Render blocks to a complete HTML string

```typescript
import { Markdown, toHtml } from '@jsonic/csv/dist/markdown'

const parseMd = Jsonic.make().use(Markdown, { html: true })
const html = toHtml(parseMd('# A\n\n- one\n- two\n'))
```

### Emit blocks only (no HTML)

Leave `html` off if you just need the structured tree; you'll skip
all inline-render work:

```typescript
const parseMd = Jsonic.make().use(Markdown)
const blocks = parseMd('# Title\n\ntext')
// [{ type: 'heading', level: 1, text: 'Title' },
//  { type: 'paragraph', text: 'text' }]
```

### Trim whitespace from paragraph lines

```typescript
const parseMd = Jsonic.make().use(Markdown, { trim: true })

parseMd('  hello  ')
// [{ type: 'paragraph', text: 'hello' }]
```

### Use a custom fenced-code marker

The default is three backticks; change it to three tildes (or any
other marker) with `codeFence`:

```typescript
const parseMd = Jsonic.make().use(Markdown, { codeFence: '~~~' })

parseMd('~~~js\nconst x=1\n~~~')
// [{ type: 'code', lang: 'js', text: 'const x=1\n' }]
```

### Extract a heading outline

```typescript
const outline = parseMd(source)
  .filter(b => b.type === 'heading')
  .map(h => ({ level: (h as any).level, text: (h as any).text }))
```

### Walk nested blockquotes and lists

Blockquotes and list items store their inner markdown as a raw
`text` field. Render them with `toHtml`, or re-parse them explicitly:

```typescript
import { Markdown, toHtml } from '@jsonic/csv/dist/markdown'

const parseMd = Jsonic.make().use(Markdown, { html: true })
const [quote] = parseMd('> ## nested\n> \n> some **bold** text')

console.log(quote.html)
// <blockquote>
// <h2>nested</h2>
// <p>some <strong>bold</strong> text</p>
// </blockquote>
```


## Explanation

### Block parsing is separate from inline rendering

The plugin runs in two distinct stages:

1. **Block parsing** produces an array of `MdBlock` objects. This is
   done by a custom line-scanning lexer plus a declarative Jsonic
   grammar.
2. **Inline rendering** converts the `text` of each block into HTML
   (handling emphasis, code spans, links, autolinks, raw HTML, etc).
   This only runs when `html: true` or when you call `toHtml`.

Keeping the two stages separate lets the plugin also work as a
structured parser — you can read, filter, or transform the block
tree without ever touching inline syntax.

### CommonMark conformance

The parser tracks the CommonMark 0.31.2 specification and passes the
full 652-example conformance test suite. That includes: ATX and
setext headings, paragraphs, fenced and indented code blocks,
thematic breaks, HTML blocks, blockquotes with lazy continuation,
ordered and unordered lists (loose and tight, nested),
emphasis/strong, inline links and images (inline, full-ref,
collapsed, shortcut), autolinks, entity references, hard line breaks,
backslash escapes, and link reference definitions (including
multi-line titles/labels).

### Link reference definitions

CommonMark separates the declaration of a link from its use:

```markdown
See [the docs][d] for details.

[d]: https://example.com "Home"
```

`toHtml` handles this by first scanning the whole block tree for
`[label]: url "title"` entries (including those nested in
blockquotes and list items, and those promoted by setext headings),
building a `LinkRefMap`, then rendering inline text against the
merged map. Label matching follows CommonMark equality rules:
case-fold + Unicode Zs / Latin eszett fold, but **no backslash
decoding** — so `[foo\!]` and `[foo!]` are distinct labels.

### Lists: loose vs tight

A list is "loose" if any of its items contain blocks separated by
blank lines; otherwise it's "tight". Loose items render as
`<li><p>...</p></li>`, tight items as `<li>...</li>`. The plugin
detects looseness *dynamically* when rendering, by sub-parsing each
item's raw content and checking whether any top-level block other
than the first was preceded by a blank line. Blanks that belong to
nested children (a child list, a nested code fence) do not promote
the outer list.


## Reference

### `Markdown` (Plugin)

The plugin function. Register with
`Jsonic.make().use(Markdown, options)`.

### `MarkdownOptions`

```typescript
type MarkdownOptions = {
  // Fenced-code marker sequence. Default: '```'
  codeFence: string

  // Trim leading/trailing whitespace from paragraph lines. Default: false
  trim: boolean

  // Attach an `html` property to each block with its rendered HTML.
  // Default: false
  html: boolean
}
```

### `MdBlock`

```typescript
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
```

### `toHtml` (Function)

```typescript
function toHtml(blocks: MdBlock[]): string
```

Render an array of `MdBlock`s to a complete HTML string, resolving
link reference definitions that appear anywhere in the tree. Blocks
that normalise to empty output (ref-def-only paragraphs, etc.) are
omitted.

### `renderHtml` (Function)

```typescript
function renderHtml(block: any, refs?: LinkRefMap): string
```

Render a single block. Advanced use — most code should prefer
`toHtml`.

### `buildMarkdownLineMatcher` (Function)

Exported for advanced use. Creates the custom line-scanning lexer
matcher used internally by the plugin to emit one token per
markdown line.

# Markdown plugin for Jsonic (Go)

A Jsonic syntax plugin that parses CommonMark markdown into
structured Go values, and (optionally) renders it back to HTML.
Passes **all 652 examples** of the CommonMark 0.31.2 spec.

```bash
go get github.com/jsonicjs/csv/go/markdown@latest
```

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
go mod init md-tutorial
go get github.com/jsonicjs/csv/go/markdown@latest
```

### Step 1 — parse a document to blocks

Create `main.go`:

```go
package main

import (
    "fmt"

    jsonic "github.com/jsonicjs/jsonic/go"
    markdown "github.com/jsonicjs/csv/go/markdown"
)

func main() {
    j := jsonic.Make()
    if err := j.UseDefaults(markdown.Markdown, markdown.Defaults); err != nil {
        panic(err)
    }

    const source = `# Hello

A short *readme* with a [link](https://example.com).

- one
- two
- three
`

    result, _ := j.Parse(source)
    fmt.Printf("%#v\n", result)
}
```

You get a `[]any` where each element is a `map[string]any` with a
`"type"` discriminator and the fields relevant to that block:

```text
[]interface {}{
  map[string]interface {}{"type":"heading", "level":1, "text":"Hello"},
  map[string]interface {}{"type":"paragraph",
    "text":"A short *readme* with a [link](https://example.com)."},
  map[string]interface {}{"type":"list", "ordered":false,
    "items":[]interface {}{...}},
}
```

Notice the paragraph's `text` still contains the raw markdown
markup — block parsing and inline rendering are separate stages.

### Step 2 — render it as HTML

Enable inline rendering by turning `html` on, then call `ToHTML`:

```go
j := jsonic.Make()
j.UseDefaults(markdown.Markdown, markdown.Defaults, map[string]any{
    "html": true,
})

result, _ := j.Parse(source)
blocks := result.([]any)
fmt.Println(markdown.ToHTML(blocks))
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

Because each block is just a map, secondary structures come cheap:

```go
type Heading struct {
    Level int
    Text  string
}

var outline []Heading
for _, b := range blocks {
    m := b.(map[string]any)
    if m["type"] == "heading" {
        outline = append(outline, Heading{
            Level: m["level"].(int),
            Text:  m["text"].(string),
        })
    }
}

fmt.Printf("%+v\n", outline)
// [{Level:1 Text:Hello}]
```

### Step 4 — resolve link references across a document

`ToHTML` handles link reference definitions that appear anywhere in
the document:

```go
const withRefs = `
See the [Jsonic docs][j] for details.

[j]: https://jsonic.senecajs.org "Jsonic"
`

result, _ = j.Parse(withRefs)
fmt.Println(markdown.ToHTML(result.([]any)))
// <p>See the <a href="https://jsonic.senecajs.org" title="Jsonic">Jsonic docs</a> for details.</p>
```

That's it — you've parsed a document, rendered it, and built a
derived view from the same block slice.


## How-to guides

Each recipe assumes you already have a configured Jsonic parser (see
step 1 of the tutorial).

### Turn on HTML rendering

Pass the `html` option via `UseDefaults`:

```go
j.UseDefaults(markdown.Markdown, markdown.Defaults, map[string]any{
    "html": true,
})

result, _ := j.Parse("# Title")
// blocks have an additional "html" key:
//   map[type:heading level:1 text:Title html:<h1>Title</h1>]
```

### Render blocks to a complete HTML string

```go
result, _ := j.Parse(source)
html := markdown.ToHTML(result.([]any))
```

`ToHTML` walks the tree, collects link reference definitions from
every nested block, and renders each surviving block against the
merged ref map.

### Emit blocks only (no HTML)

Skip the `html` option if you just want the structured tree; you'll
skip all inline-render work:

```go
j.UseDefaults(markdown.Markdown, markdown.Defaults)

result, _ := j.Parse("# Title\n\ntext")
// []any{ {type:heading, level:1, text:Title}, {type:paragraph, text:text} }
```

### Trim whitespace from paragraph lines

```go
j.UseDefaults(markdown.Markdown, markdown.Defaults, map[string]any{
    "trim": true,
})
```

### Use a custom fenced-code marker

The default is three backticks; change it to three tildes (or any
other marker) with `codeFence`:

```go
j.UseDefaults(markdown.Markdown, markdown.Defaults, map[string]any{
    "codeFence": "~~~",
})

result, _ := j.Parse("~~~js\nconst x=1\n~~~")
```

### Build a reusable parser

`Jsonic.Make()` plus `UseDefaults` gives you a configured parser you
can call repeatedly:

```go
j := jsonic.Make()
j.UseDefaults(markdown.Markdown, markdown.Defaults, map[string]any{
    "html": true,
})

r1, _ := j.Parse(firstDoc)
r2, _ := j.Parse(secondDoc)
```


## Explanation

### Block parsing is separate from inline rendering

The plugin runs in two distinct stages:

1. **Block parsing** produces a slice of block maps. This is done by
   a custom line-scanning lexer plus a declarative Jsonic grammar
   (shared with the TypeScript implementation).
2. **Inline rendering** converts the `text` of each block into HTML
   (handling emphasis, code spans, links, autolinks, raw HTML, etc).
   This only runs when `html: true` or when you call `ToHTML`.

Keeping the two stages separate lets the plugin also work as a
structured parser — you can read, filter, or transform the block
tree without ever touching inline syntax.

### CommonMark conformance

The parser tracks the CommonMark 0.31.2 specification and passes the
full 652-example conformance test suite. Both the Go and TypeScript
implementations share the same grammar file and produce the same
block tree.

### Why `map[string]any` instead of structs?

Jsonic's plugin API is dynamically typed — grammars work on a
generic node tree, so every block arrives as `map[string]any`. This
makes it trivial to add new block types without API breakage, and
mirrors the TypeScript output shape closely. If you want statically
typed access, wrap the output in your own structs at the boundary:

```go
type Heading struct { Level int; Text string }

func asHeading(b any) (Heading, bool) {
    m, ok := b.(map[string]any)
    if !ok || m["type"] != "heading" {
        return Heading{}, false
    }
    return Heading{
        Level: m["level"].(int),
        Text:  m["text"].(string),
    }, true
}
```

### Link reference definitions

CommonMark separates the declaration of a link from its use. `ToHTML`
handles this by first calling `GatherAllLinkRefs` on the whole tree
(including blocks nested in blockquotes and list items, plus ref
defs promoted by setext headings), building a `LinkRefMap`, then
rendering inline text against the merged map. Label matching follows
CommonMark equality rules: case-fold + Unicode Zs / Latin eszett
fold, but **no backslash decoding** — so `[foo\!]` and `[foo!]` are
distinct labels.

### Lists: loose vs tight

A list is "loose" if any of its items contain blocks separated by
blank lines; otherwise it's "tight". Loose items render as
`<li><p>...</p></li>`, tight items as `<li>...</li>`. The plugin
detects looseness *dynamically* during rendering, by sub-parsing
each item's raw content and checking whether any top-level block
other than the first was preceded by a blank line. Blanks that
belong to nested children (a child list, a nested code fence) do
not promote the outer list.


## Reference

### `Markdown` (Plugin)

```go
func Markdown(j *jsonic.Jsonic, options map[string]any) error
```

Register via `jsonic.Make().UseDefaults(Markdown, Defaults, opts)`.
`opts` is any subset of the default option keys.

### `Defaults`

```go
var Defaults = map[string]any{
    "codeFence": "```",
    "trim":      false,
    "html":      false,
}
```

Pass with `UseDefaults` and override keys as needed:

```go
j.UseDefaults(markdown.Markdown, markdown.Defaults, map[string]any{
    "html": true,
    "trim": true,
})
```

### Block shape

Every parsed block is `map[string]any` with a `"type"` key and the
following per-type fields:

| type         | fields                                                                   |
| ------------ | ------------------------------------------------------------------------ |
| `heading`    | `level` (int), `text` (string), optional `html` (string)                 |
| `paragraph`  | `text` (string), optional `html` (string)                                |
| `code`       | `lang` (string), `text` (string), optional `html` (string)               |
| `list`       | `ordered` (bool), `items` ([]any of `{text}`), optional `start` (int), optional `html` |
| `blockquote` | `text` (string), optional `html` (string)                                |
| `hr`         | optional `html` (string)                                                 |
| `html`       | `text` (string), optional `html` (string)                                |

### `ToHTML` (Function)

```go
func ToHTML(blocks []any) string
```

Render a block slice to a complete HTML string, resolving link
reference definitions anywhere in the tree. Blocks that normalise to
empty output (ref-def-only paragraphs, etc.) are omitted.

### `RenderHTML` (Function)

```go
func RenderHTML(block map[string]any) string
```

Render a single block. Advanced use — most code should prefer
`ToHTML`.

### `ExtractLinkRefsAndClean` (Function)

```go
func ExtractLinkRefsAndClean(blocks []any) (LinkRefMap, []any)
```

Strip leading `[label]: url "title"` definitions from paragraph
blocks, returning the ref map plus the surviving blocks.

### `GatherAllLinkRefs` (Function)

```go
func GatherAllLinkRefs(blocks []any) LinkRefMap
```

Walk the block tree (including nested blockquotes and list-item
content) and collect every link reference definition found.

### `LinkRef` / `LinkRefMap`

```go
type LinkRef struct {
    URL   string
    Title string
}

type LinkRefMap = map[string]LinkRef
```

### `Version`

```go
const Version = "0.1.0"
```

Current plugin version, bumped by `make publish-go-markdown`.

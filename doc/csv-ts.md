# CSV plugin for Jsonic (TypeScript)

A Jsonic syntax plugin that parses CSV text into JavaScript arrays
of objects or arrays, with support for headers, quoted fields,
custom delimiters, streaming, and strict/non-strict modes.

```bash
npm install @jsonic/csv
```

Requires `jsonic` >= 2 as a peer dependency.

This documentation follows the [Diataxis](https://diataxis.fr)
framework: a **tutorial** for first-time users, **how-to guides**
for specific tasks, **explanation** of key concepts, and a complete
**reference**.


## Tutorial: load, filter and stream a sales CSV

This tutorial walks you through using `@jsonic/csv` end-to-end: you
will parse a small sales file, enable strict typing for the numeric
columns, reshape the data, and finally re-parse it as a stream so
that nothing is held in memory.

Create a new project:

```bash
mkdir csv-tutorial && cd csv-tutorial
npm init -y
npm install jsonic @jsonic/csv
```

### Step 1 — parse a file with a header row

Create `sales.csv`:

```
region,product,units,revenue
EU,widget,12,240.00
US,gadget, 7,189.50
US,widget,20,400.00
```

Create `tutorial.ts`:

```typescript
import { readFileSync } from 'node:fs'
import { Jsonic } from 'jsonic'
import { Csv } from '@jsonic/csv'

const parseCsv = Jsonic.make().use(Csv)
const text = readFileSync('sales.csv', 'utf8')
const rows = parseCsv(text)

console.log(rows[0])
// { region: 'EU', product: 'widget', units: '12', revenue: '240.00' }
```

Every field is a string. That is the default: CSV parsers should not
silently turn `"12"` into a number, because a column of US ZIP codes
would lose its leading zeroes.

### Step 2 — turn numbers on for real

For this data set we DO want numbers. Opt in with `number` and tidy
up padding with `trim`:

```typescript
const parseCsv = Jsonic.make().use(Csv, {
  number: true,
  trim: true,
})

const rows = parseCsv(text)
console.log(rows[1])
// { region: 'US', product: 'gadget', units: 7, revenue: 189.5 }
```

`units` and `revenue` are now numbers; `region` and `product` stay
strings because they don't look numeric.

### Step 3 — aggregate the rows

At this point `rows` is a plain array of objects, so any standard
JavaScript works:

```typescript
const totalByRegion = rows.reduce<Record<string, number>>((acc, r) => {
  acc[r.region] = (acc[r.region] ?? 0) + r.revenue
  return acc
}, {})

console.log(totalByRegion)  // { EU: 240, US: 589.5 }
```

### Step 4 — stream instead of materialise

For a million-row file you don't want the whole array in memory.
Pass a `stream` callback; the plugin invokes it once per record and
returns an empty array at the end:

```typescript
let total = 0

Jsonic.make().use(Csv, {
  number: true,
  trim: true,
  stream: (what, record) => {
    if (what === 'record' && record && !(record instanceof Error)) {
      total += (record as any).revenue
    }
  },
})(text)

console.log(total)  // 829.5
```

That's it — you've parsed, typed, aggregated, and streamed a CSV
file. The rest of this document is organised so you can drop in to
answer a specific question without re-reading the whole tutorial.


## How-to guides

Short, task-focused recipes. Each one assumes you already know how
to load the plugin (see the tutorial).

### Use a custom field delimiter

Set `field.separation` to use a delimiter other than comma:

```typescript
const parse = Jsonic.make().use(Csv, {
  field: { separation: '\t' }
})

parse("name\tage\nAlice\t30")
// [{ name: 'Alice', age: '30' }]
```

### Enable number and value parsing

By default (strict mode), all values are strings. Enable `number`
and `value` to parse numeric and boolean values:

```typescript
const parse = Jsonic.make().use(Csv, {
  number: true,
  value: true,
})

parse("a,b,c\n1,true,null")
// [{ a: 1, b: true, c: null }]
```

### Trim whitespace from fields

```typescript
const parse = Jsonic.make().use(Csv, { trim: true })

parse("a , b \n 1 , 2 ")
// [{ a: '1', b: '2' }]
```

### Parse CSV without headers

Return rows as arrays instead of objects, with no header row:

```typescript
const parse = Jsonic.make().use(Csv, {
  header: false,
  object: false,
})

parse("a,b,c\n1,2,3")
// [['a', 'b', 'c'], ['1', '2', '3']]
```

### Provide explicit field names

Useful when the CSV has no header row but you want object output
with named fields:

```typescript
const parse = Jsonic.make().use(Csv, {
  header: false,
  field: { names: ['x', 'y', 'z'] },
})

parse("1,2,3\n4,5,6")
// [{ x: '1', y: '2', z: '3' }, { x: '4', y: '5', z: '6' }]
```

### Enforce exact field counts

Set `field.exact` to throw when a row has more or fewer fields than
the header:

```typescript
const parse = Jsonic.make().use(Csv, {
  field: { exact: true },
})

// parse("a,b\n1,2,3")  // throws: unexpected extra field value
// parse("a,b\n1")      // throws: missing field
```

### Stream records as they are parsed

```typescript
const records: any[] = []

Jsonic.make().use(Csv, {
  stream: (what, record) => {
    if (what === 'record') records.push(record)
  },
})("a,b\n1,2\n3,4")

// records === [{ a: '1', b: '2' }, { a: '3', b: '4' }]
```

### Use non-strict mode for embedded JSON

Disable `strict` to allow Jsonic syntax inside CSV fields, including
JSON objects, arrays, and expressions:

```typescript
const parse = Jsonic.make().use(Csv, { strict: false })

parse("a,b\ntrue,[1,2]")
// [{ a: true, b: [1, 2] }]
```

### Enable `#` comment lines

```typescript
const parse = Jsonic.make().use(Csv, { comment: true })

parse("a,b\n# skip this\n1,2")
// [{ a: '1', b: '2' }]
```

### Preserve empty records

By default, blank lines are skipped. Set `record.empty` to preserve
them as empty-field records:

```typescript
const parse = Jsonic.make().use(Csv, { record: { empty: true } })

parse("a\n1\n\n2")
// [{ a: '1' }, { a: '' }, { a: '2' }]
```


## Explanation

Context and design notes — read these when you want to understand
*why* the plugin behaves the way it does.

### Strict vs non-strict mode

CSV is not a single format. Real-world files range from strict
RFC 4180 comma-separated strings to ad-hoc files with comments,
type coercion, trimmed whitespace, and embedded JSON blobs. The
plugin handles both ends of the spectrum with a single switch:

- In **strict mode** (default), Jsonic's built-in JSON parsing is
  disabled. All field values are raw strings unless you opt in with
  `number` or `value`. This matches standard CSV libraries.
- In **non-strict mode** (`strict: false`), Jsonic syntax is
  preserved. Fields can be objects (`{x:1}`), arrays, booleans,
  numbers, or quoted strings. Non-strict mode also turns `trim`,
  `comment`, and `number` on by default because those features
  almost always go together when you're writing CSV "by hand".

### How quoted fields work

The plugin includes a custom CSV string matcher that handles the
RFC 4180 double-quote escaping convention:

- A field wrapped in `"..."` can contain commas, newlines, and
  quotes.
- A literal quote inside a quoted field is written `""`.
- `"a""b"` parses to `a"b`.

The matcher runs before Jsonic's default single/double-quoted-string
matchers so it wins on first-character-`"`, which is what lets the
plugin handle embedded commas and newlines correctly.

### The streaming model

Jsonic parsing is inherently synchronous, so "streaming" here means
*don't retain records in the returned array*. When a `stream`
callback is supplied, the plugin hands each parsed record to the
callback as soon as it's complete, then discards it. The returned
value is an empty array — use the callback for all record handling.
The callback is invoked with one of: `"start"`, `"record"`, `"end"`,
or `"error"`.


## Reference

Authoritative description of every exported symbol.

### `Csv` (Plugin)

The plugin function. Register with `Jsonic.make().use(Csv, options)`.

### `CsvOptions`

```typescript
type CsvOptions = {
  // Trim surrounding whitespace.
  // Default: null (false in strict, true in non-strict)
  trim: boolean | null

  // Enable `#` line comments.
  // Default: null (false in strict, true in non-strict)
  comment: boolean | null

  // Parse numeric values.
  // Default: null (false in strict, true in non-strict)
  number: boolean | null

  // Parse value keywords (true/false/null).
  // Default: null (false in strict, false in non-strict)
  value: boolean | null

  // First row is a header row. Default: true
  header: boolean

  // Return records as objects (true) or arrays (false). Default: true
  object: boolean

  // Stream callback. Default: null
  stream: null | ((what: string, record?: Record<string, any> | Error) => void)

  // Strict CSV mode (disables Jsonic syntax). Default: true
  strict: boolean

  field: {
    // Field separator string. Default: null (uses comma)
    separation: null | string

    // Prefix for unnamed extra fields. Default: 'field~'
    nonameprefix: string

    // Value for empty fields. Default: ''
    empty: any

    // Explicit field names (overrides header). Default: undefined
    names: undefined | string[]

    // Error on field count mismatch. Default: false
    exact: boolean
  }

  record: {
    // Custom record separator characters. Default: null
    separators: null | string

    // Preserve empty lines as records. Default: false
    empty: boolean
  }

  string: {
    // Quote character. Default: '"'
    quote: string

    // Force CSV string mode (null=auto). Default: null
    csv: null | boolean
  }
}
```

### `buildCsvStringMatcher` (Function)

Exported for advanced use. Creates the custom CSV double-quote
string matcher used internally by the plugin.

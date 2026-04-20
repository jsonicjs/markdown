# CSV plugin for Jsonic (Go)

A Jsonic syntax plugin that parses CSV text into Go slices of maps
or slices, with support for headers, quoted fields, custom
delimiters, streaming, and strict/non-strict modes.

```bash
go get github.com/jsonicjs/csv/go@latest
```

This documentation follows the [Diataxis](https://diataxis.fr)
framework: a **tutorial** for first-time users, **how-to guides**
for specific tasks, **explanation** of key concepts, and a complete
**reference**.


## Tutorial: load, filter and stream a sales CSV

This tutorial walks you through using `jsonicjs/csv/go` end-to-end:
you will parse a small sales file, enable strict typing for the
numeric columns, reshape the data, and finally re-parse it as a
stream so that nothing is held in memory.

Create a new project:

```bash
mkdir csv-tutorial && cd csv-tutorial
go mod init csv-tutorial
go get github.com/jsonicjs/csv/go@latest
```

### Step 1 — parse a file with a header row

Create `sales.csv`:

```
region,product,units,revenue
EU,widget,12,240.00
US,gadget, 7,189.50
US,widget,20,400.00
```

Create `main.go`:

```go
package main

import (
    "fmt"
    "os"

    csv "github.com/jsonicjs/csv/go"
)

func main() {
    data, _ := os.ReadFile("sales.csv")
    rows, _ := csv.Parse(string(data))
    fmt.Printf("%+v\n", rows[0])
    // map[region:EU product:widget units:12 revenue:240.00]
}
```

Every field is a string. That's the default: a CSV parser should
not silently turn `"12"` into a number, because a column of US ZIP
codes would lose its leading zeroes.

### Step 2 — turn numbers on for real

For this data set we DO want numbers. Opt in with `Number` and tidy
up padding with `Trim`:

```go
rows, _ := csv.Parse(string(data), csv.CsvOptions{
    Number: boolPtr(true),
    Trim:   boolPtr(true),
})

fmt.Printf("%+v\n", rows[1])
// map[region:US product:gadget units:7 revenue:189.5]

// Option-pointer helper used throughout the API.
func boolPtr(b bool) *bool { return &b }
```

`units` and `revenue` are now Go numeric values; `region` and
`product` stay strings because they don't look numeric.

### Step 3 — aggregate the rows

At this point `rows` is a plain `[]any` of `map[string]any`, so any
regular Go works:

```go
totals := map[string]float64{}
for _, row := range rows {
    r := row.(map[string]any)
    totals[r["region"].(string)] += r["revenue"].(float64)
}
fmt.Println(totals) // map[EU:240 US:589.5]
```

### Step 4 — stream instead of materialise

For a million-row file you don't want the whole slice in memory.
Supply a `Stream` callback; the plugin invokes it once per record
and returns an empty slice at the end:

```go
var total float64

csv.Parse(string(data), csv.CsvOptions{
    Number: boolPtr(true),
    Trim:   boolPtr(true),
    Stream: func(what string, record any) {
        if what == "record" {
            if r, ok := record.(map[string]any); ok {
                total += r["revenue"].(float64)
            }
        }
    },
})

fmt.Println(total) // 829.5
```

That's it — you've parsed, typed, aggregated, and streamed a CSV
file. The rest of this document is organised so you can drop in to
answer a specific question without re-reading the whole tutorial.


## How-to guides

Short, task-focused recipes.

### Use a custom field delimiter

```go
result, _ := csv.Parse("name\tage\nAlice\t30", csv.CsvOptions{
    Field: &csv.FieldOptions{Separation: "\t"},
})
// [{name:Alice age:30}]
```

### Enable number and value parsing

```go
result, _ := csv.Parse("a,b,c\n1,true,null", csv.CsvOptions{
    Number: boolPtr(true),
    Value:  boolPtr(true),
})
// [{a:1 b:true c:<nil>}]
```

### Trim whitespace from fields

```go
result, _ := csv.Parse("a , b \n 1 , 2 ", csv.CsvOptions{
    Trim: boolPtr(true),
})
// [{a:1 b:2}]
```

### Parse CSV without headers

```go
result, _ := csv.Parse("a,b,c\n1,2,3", csv.CsvOptions{
    Header: boolPtr(false),
    Object: boolPtr(false),
})
// [[a b c] [1 2 3]]
```

### Provide explicit field names

```go
result, _ := csv.Parse("1,2,3\n4,5,6", csv.CsvOptions{
    Header: boolPtr(false),
    Field:  &csv.FieldOptions{Names: []string{"x", "y", "z"}},
})
// [{x:1 y:2 z:3} {x:4 y:5 z:6}]
```

### Enforce exact field counts

```go
_, err := csv.Parse("a,b\n1,2,3", csv.CsvOptions{
    Field: &csv.FieldOptions{Exact: true},
})
// err: unexpected extra field value
```

### Stream records as they are parsed

```go
var records []any

csv.Parse("a,b\n1,2\n3,4", csv.CsvOptions{
    Stream: func(what string, record any) {
        if what == "record" {
            records = append(records, record)
        }
    },
})
// records contains [{a:1 b:2}, {a:3 b:4}]
```

### Create a reusable parser

Use `MakeJsonic` to create a configured Jsonic instance you can
call repeatedly — this avoids re-running plugin setup for each
document:

```go
j := csv.MakeJsonic(csv.CsvOptions{
    Number: boolPtr(true),
})

r1, _ := j.Parse("a,b\n1,2")
r2, _ := j.Parse("x,y\n3,4")
```

### Enable `#` comment lines

```go
result, _ := csv.Parse("a,b\n# skip\n1,2", csv.CsvOptions{
    Comment: boolPtr(true),
})
// [{a:1 b:2}]
```


## Explanation

Context and design notes.

### Strict vs non-strict mode

CSV is not a single format. Real-world files range from strict
RFC 4180 comma-separated strings to ad-hoc files with comments,
type coercion, trimmed whitespace, and embedded JSON blobs. The
plugin handles both ends of the spectrum with a single switch:

- In **strict mode** (default), Jsonic's built-in JSON parsing is
  disabled. All field values are raw strings unless you opt in with
  `Number` or `Value`. This matches standard CSV libraries.
- In **non-strict mode** (`Strict: boolPtr(false)`), Jsonic syntax
  is preserved. Fields can be objects, arrays, booleans, numbers,
  or quoted strings. Non-strict mode also turns `Trim`, `Comment`,
  and `Number` on by default because those features usually go
  together when you're writing CSV "by hand".

### Why the options are pointers

Most `CsvOptions` fields are `*bool` because the plugin needs to
distinguish "caller didn't set this" (use the strict-mode default)
from "caller explicitly set it to false". A plain `bool` cannot
represent three states — only `nil`, `*true`, and `*false` can.
The `boolPtr` helper in the tutorial keeps call sites readable.

### How quoted fields work

The plugin includes a custom CSV string matcher that handles the
RFC 4180 double-quote escaping convention:

- A field wrapped in `"..."` can contain commas, newlines, and
  quotes.
- A literal quote inside a quoted field is written `""`.
- `"a""b"` parses to `a"b`.

### The streaming model

Jsonic parsing is synchronous, so "streaming" here means *don't
retain records in the returned slice*. When a `Stream` callback is
supplied, the plugin hands each parsed record to it as soon as it's
complete, then discards it. The returned slice is empty — use the
callback for all record handling. The callback is invoked with one
of: `"start"`, `"record"`, `"end"`, or `"error"`.


## Reference

Authoritative description of every exported symbol.

### `Parse` (Function)

```go
func Parse(src string, opts ...CsvOptions) ([]any, error)
```

Parse CSV text with the given options. Returns a slice of records.

### `MakeJsonic` (Function)

```go
func MakeJsonic(opts ...CsvOptions) *jsonic.Jsonic
```

Create a reusable Jsonic instance configured for CSV parsing.

### `CsvOptions`

```go
type CsvOptions struct {
    Object  *bool          // Return maps (true) or slices (false). Default: true
    Header  *bool          // First row is header. Default: true
    Trim    *bool          // Trim whitespace. Default: nil (false strict, true non-strict)
    Comment *bool          // Enable # comments. Default: nil (false strict, true non-strict)
    Number  *bool          // Parse numbers. Default: nil (false strict, true non-strict)
    Value   *bool          // Parse true/false/null. Default: nil
    Strict  *bool          // Strict CSV mode. Default: true
    Field   *FieldOptions
    Record  *RecordOptions
    String  *StringOptions
    Stream  StreamFunc
}
```

### `FieldOptions`

```go
type FieldOptions struct {
    Separation   string   // Field separator. Default: ","
    NonamePrefix string   // Prefix for unnamed extra fields. Default: "field~"
    Empty        string   // Value for empty fields. Default: ""
    Names        []string // Explicit field names.
    Exact        bool     // Error on field count mismatch. Default: false
}
```

### `RecordOptions`

```go
type RecordOptions struct {
    Separators string // Custom record separator characters.
    Empty      bool   // Preserve empty lines as records. Default: false
}
```

### `StringOptions`

```go
type StringOptions struct {
    Quote string // Quote character. Default: `"`
    Csv   *bool  // Force CSV string mode (nil=auto).
}
```

### `StreamFunc`

```go
type StreamFunc func(what string, record any)
```

Callback for streaming CSV parsing. Called with `"start"`,
`"record"`, `"end"`, or `"error"`.

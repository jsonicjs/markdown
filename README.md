# @jsonic/csv

A collection of [Jsonic](https://jsonic.senecajs.org) syntax plugins
that parse **CSV** and **CommonMark markdown** into structured data.
Available for both TypeScript and Go, and passes the full CommonMark
0.31.2 conformance suite (652/652 examples).


[![npm version](https://img.shields.io/npm/v/@jsonic/csv.svg)](https://npmjs.com/package/@jsonic/csv)
[![build](https://github.com/jsonicjs/csv/actions/workflows/build.yml/badge.svg)](https://github.com/jsonicjs/csv/actions/workflows/build.yml)
[![Coverage Status](https://coveralls.io/repos/github/jsonicjs/csv/badge.svg?branch=main)](https://coveralls.io/github/jsonicjs/csv?branch=main)
[![Known Vulnerabilities](https://snyk.io/test/github/jsonicjs/csv/badge.svg)](https://snyk.io/test/github/jsonicjs/csv)
[![DeepScan grade](https://deepscan.io/api/teams/5016/projects/22466/branches/663906/badge/grade.svg)](https://deepscan.io/dashboard#view=project&tid=5016&pid=22466&bid=663906)
[![Maintainability](https://api.codeclimate.com/v1/badges/10e9bede600896c77ce8/maintainability)](https://codeclimate.com/github/jsonicjs/csv/maintainability)

| ![Voxgig](https://www.voxgig.com/res/img/vgt01r.png) | This open source module is sponsored and supported by [Voxgig](https://www.voxgig.com). |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------- |


## Quick example — CSV

**TypeScript**

```typescript
import { Jsonic } from 'jsonic'
import { Csv } from '@jsonic/csv'

const parse = Jsonic.make().use(Csv)

parse("name,age\nAlice,30\nBob,25")
// [{ name: 'Alice', age: '30' }, { name: 'Bob', age: '25' }]
```

**Go**

```go
import csv "github.com/jsonicjs/csv/go"

result, _ := csv.Parse("name,age\nAlice,30\nBob,25")
// [{name:Alice age:30} {name:Bob age:25}]
```


## Quick example — Markdown

**TypeScript**

```typescript
import { Jsonic } from 'jsonic'
import { Markdown, toHtml } from '@jsonic/csv/dist/markdown'

const parseMd = Jsonic.make().use(Markdown, { html: true })
console.log(toHtml(parseMd('# Hello\n\nworld')))
// <h1>Hello</h1>
// <p>world</p>
```

**Go**

```go
import (
  jsonic "github.com/jsonicjs/jsonic/go"
  markdown "github.com/jsonicjs/csv/go/markdown"
)

j := jsonic.Make()
j.UseDefaults(markdown.Markdown, markdown.Defaults, map[string]any{"html": true})
r, _ := j.Parse("# Hello\n\nworld")
fmt.Println(markdown.ToHTML(r.([]any)))
```


## Documentation

Full documentation following the [Diataxis](https://diataxis.fr)
framework (tutorials, how-to guides, explanation, reference):

- CSV plugin — [TypeScript](doc/csv-ts.md) · [Go](doc/csv-go.md)
- Markdown plugin — [TypeScript](doc/markdown-ts.md) · [Go](doc/markdown-go.md)


## License

Copyright (c) 2021-2025 Richard Rodger and other contributors,
[MIT License](LICENSE).

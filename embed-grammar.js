#!/usr/bin/env node

// Embed grammar definitions into TypeScript and Go source files.
// Run via: npm run embed  (or:  node embed-grammar.js)
//
// Each entry in TARGETS points a grammar source file at one or more output
// files. Each output declares the marker pair that brackets the embedded
// grammar block so that this script can replace it in place.

const fs = require('fs')
const path = require('path')

const TARGETS = [
  {
    grammar: path.join(__dirname, 'csv-grammar.jsonic'),
    outputs: [
      {
        file: path.join(__dirname, 'src', 'csv.ts'),
        kind: 'ts',
        begin: '// --- BEGIN EMBEDDED csv-grammar.jsonic ---',
        end: '// --- END EMBEDDED csv-grammar.jsonic ---',
      },
      {
        file: path.join(__dirname, 'go', 'csv.go'),
        kind: 'go',
        begin: '// --- BEGIN EMBEDDED csv-grammar.jsonic ---',
        end: '// --- END EMBEDDED csv-grammar.jsonic ---',
      },
    ],
  },
  {
    grammar: path.join(__dirname, 'markdown-grammar.jsonic'),
    outputs: [
      {
        file: path.join(__dirname, 'src', 'markdown.ts'),
        kind: 'ts',
        begin: '// --- BEGIN EMBEDDED markdown-grammar.jsonic ---',
        end: '// --- END EMBEDDED markdown-grammar.jsonic ---',
      },
      {
        file: path.join(__dirname, 'go', 'markdown', 'markdown.go'),
        kind: 'go',
        begin: '// --- BEGIN EMBEDDED markdown-grammar.jsonic ---',
        end: '// --- END EMBEDDED markdown-grammar.jsonic ---',
      },
    ],
  },
]

function embedTS(grammar, output) {
  let src = fs.readFileSync(output.file, 'utf8')
  const startIdx = src.indexOf(output.begin)
  const endIdx = src.indexOf(output.end)
  if (startIdx === -1 || endIdx === -1) {
    console.error('TS markers not found in', output.file)
    process.exit(1)
  }

  // Escape backticks and template expressions for a JS template literal.
  const escaped = grammar
    .replace(/\\/g, '\\\\')
    .replace(/`/g, '\\`')
    .replace(/\$\{/g, '\\${')

  const replacement =
    output.begin +
    '\nconst grammarText = `\n' +
    escaped +
    '`\n' +
    output.end

  src = src.substring(0, startIdx) + replacement + src.substring(endIdx + output.end.length)
  fs.writeFileSync(output.file, src)
  console.log('Embedded grammar into', output.file)
}

function embedGo(grammar, output) {
  let src = fs.readFileSync(output.file, 'utf8')
  const startIdx = src.indexOf(output.begin)
  const endIdx = src.indexOf(output.end)
  if (startIdx === -1 || endIdx === -1) {
    console.error('Go markers not found in', output.file)
    process.exit(1)
  }

  if (grammar.includes('`')) {
    console.error('Grammar contains backticks, incompatible with Go raw strings')
    process.exit(1)
  }

  const replacement =
    output.begin +
    '\nconst grammarText = `\n' +
    grammar +
    '`\n' +
    output.end

  src = src.substring(0, startIdx) + replacement + src.substring(endIdx + output.end.length)
  fs.writeFileSync(output.file, src)
  console.log('Embedded grammar into', output.file)
}

for (const target of TARGETS) {
  const grammar = fs.readFileSync(target.grammar, 'utf8')
  for (const output of target.outputs) {
    if (output.kind === 'ts') {
      embedTS(grammar, output)
    } else if (output.kind === 'go') {
      embedGo(grammar, output)
    } else {
      console.error('Unknown embed kind:', output.kind)
      process.exit(1)
    }
  }
}

/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

package markdown

import (
	"reflect"
	"testing"

	jsonic "github.com/jsonicjs/jsonic/go"
)

// mdParse creates a jsonic instance with the Markdown plugin and parses src.
func mdParse(src string, opts ...map[string]any) ([]any, error) {
	j := jsonic.Make()
	if err := j.UseDefaults(Markdown, Defaults, opts...); err != nil {
		return nil, err
	}
	result, err := j.Parse(src)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return []any{}, nil
	}
	if arr, ok := result.([]any); ok {
		return arr, nil
	}
	return []any{}, nil
}

// normalize coerces typed numerics in expectations into the same Go runtime
// types produced by the parser so reflect.DeepEqual succeeds without coupling
// callers to the exact integer width.
func normalize(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case int:
		return x
	default:
		return v
	}
}

func assertEqual(t *testing.T, name string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(normalize(got), normalize(want)) {
		t.Errorf("%s:\n got:  %#v\n want: %#v", name, got, want)
	}
}

func TestEmpty(t *testing.T) {
	r, err := mdParse("")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	assertEqual(t, "empty", r, []any{})

	r2, _ := mdParse("\n")
	assertEqual(t, "single-newline", r2, []any{})

	r3, _ := mdParse("\n\n\n")
	assertEqual(t, "many-newlines", r3, []any{})
}

func TestHeading(t *testing.T) {
	r, _ := mdParse("# Title")
	assertEqual(t, "h1", r, []any{
		map[string]any{"type": "heading", "level": 1, "text": "Title"},
	})

	r2, _ := mdParse("## Subtitle")
	assertEqual(t, "h2", r2, []any{
		map[string]any{"type": "heading", "level": 2, "text": "Subtitle"},
	})

	r3, _ := mdParse("###### Deep")
	assertEqual(t, "h6", r3, []any{
		map[string]any{"type": "heading", "level": 6, "text": "Deep"},
	})

	r4, _ := mdParse("# One\n## Two\n### Three")
	assertEqual(t, "multiple", r4, []any{
		map[string]any{"type": "heading", "level": 1, "text": "One"},
		map[string]any{"type": "heading", "level": 2, "text": "Two"},
		map[string]any{"type": "heading", "level": 3, "text": "Three"},
	})
}

func TestParagraph(t *testing.T) {
	r, _ := mdParse("Hello world.")
	assertEqual(t, "single-line", r, []any{
		map[string]any{"type": "paragraph", "text": "Hello world."},
	})

	r2, _ := mdParse("Line one.\nLine two.")
	assertEqual(t, "two-lines", r2, []any{
		map[string]any{"type": "paragraph", "text": "Line one.\nLine two."},
	})

	r3, _ := mdParse("P1\n\nP2")
	assertEqual(t, "two-paras", r3, []any{
		map[string]any{"type": "paragraph", "text": "P1"},
		map[string]any{"type": "paragraph", "text": "P2"},
	})
}

func TestHorizontalRule(t *testing.T) {
	r, _ := mdParse("---")
	assertEqual(t, "dash", r, []any{map[string]any{"type": "hr"}})

	r2, _ := mdParse("***")
	assertEqual(t, "star", r2, []any{map[string]any{"type": "hr"}})

	r3, _ := mdParse("___")
	assertEqual(t, "underscore", r3, []any{map[string]any{"type": "hr"}})

	r4, _ := mdParse("- - -")
	assertEqual(t, "spaced", r4, []any{map[string]any{"type": "hr"}})
}

func TestUnorderedList(t *testing.T) {
	r, _ := mdParse("- a\n- b\n- c")
	assertEqual(t, "dash", r, []any{
		map[string]any{
			"type":    "list",
			"ordered": false,
			"items": []any{
				map[string]any{"text": "a"},
				map[string]any{"text": "b"},
				map[string]any{"text": "c"},
			},
		},
	})

	r2, _ := mdParse("* x\n* y")
	assertEqual(t, "star", r2, []any{
		map[string]any{
			"type":    "list",
			"ordered": false,
			"items": []any{
				map[string]any{"text": "x"},
				map[string]any{"text": "y"},
			},
		},
	})
}

func TestOrderedList(t *testing.T) {
	r, _ := mdParse("1. first\n2. second\n3. third")
	assertEqual(t, "ordered", r, []any{
		map[string]any{
			"type":    "list",
			"ordered": true,
			"items": []any{
				map[string]any{"text": "first"},
				map[string]any{"text": "second"},
				map[string]any{"text": "third"},
			},
		},
	})
}

func TestBlockquote(t *testing.T) {
	r, _ := mdParse("> quoted")
	assertEqual(t, "single", r, []any{
		map[string]any{"type": "blockquote", "text": "quoted"},
	})

	r2, _ := mdParse("> line 1\n> line 2")
	assertEqual(t, "multi", r2, []any{
		map[string]any{"type": "blockquote", "text": "line 1\nline 2"},
	})
}

func TestCodeBlock(t *testing.T) {
	r, _ := mdParse("```\nplain code\n```")
	assertEqual(t, "plain", r, []any{
		map[string]any{"type": "code", "lang": "", "text": "plain code"},
	})

	r2, _ := mdParse("```js\nconst x = 1\nconst y = 2\n```")
	assertEqual(t, "with-lang", r2, []any{
		map[string]any{
			"type": "code",
			"lang": "js",
			"text": "const x = 1\nconst y = 2",
		},
	})

	// Unclosed fence captures to end.
	r3, _ := mdParse("```py\nprint(1)\n")
	assertEqual(t, "unclosed", r3, []any{
		map[string]any{"type": "code", "lang": "py", "text": "print(1)"},
	})
}

func TestMixedDocument(t *testing.T) {
	src := `# Title

Intro paragraph
spanning two lines.

## Subsection

- one
- two

> quote line

` + "```ts\nlet a = 1\n```" + `

---

End.
`

	r, err := mdParse(src)
	if err != nil {
		t.Fatalf("mixed: %v", err)
	}

	assertEqual(t, "mixed", r, []any{
		map[string]any{"type": "heading", "level": 1, "text": "Title"},
		map[string]any{
			"type": "paragraph",
			"text": "Intro paragraph\nspanning two lines.",
		},
		map[string]any{"type": "heading", "level": 2, "text": "Subsection"},
		map[string]any{
			"type":    "list",
			"ordered": false,
			"items": []any{
				map[string]any{"text": "one"},
				map[string]any{"text": "two"},
			},
		},
		map[string]any{"type": "blockquote", "text": "quote line"},
		map[string]any{"type": "code", "lang": "ts", "text": "let a = 1"},
		map[string]any{"type": "hr"},
		map[string]any{"type": "paragraph", "text": "End."},
	})
}

func TestCRLF(t *testing.T) {
	r, _ := mdParse("# Title\r\n\r\nA line.\r\nAnother.\r\n")
	assertEqual(t, "crlf", r, []any{
		map[string]any{"type": "heading", "level": 1, "text": "Title"},
		map[string]any{"type": "paragraph", "text": "A line.\nAnother."},
	})
}

func TestTrimOption(t *testing.T) {
	r, _ := mdParse("  hello  \n  world  ", map[string]any{"trim": true})
	assertEqual(t, "trim", r, []any{
		map[string]any{"type": "paragraph", "text": "hello\nworld"},
	})
}

/* Copyright (c) 2021-2025 Richard Rodger and other contributors, MIT License */

package markdown

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	jsonic "github.com/jsonicjs/jsonic/go"
)

// TestCommonMarkConformance runs the vendored CommonMark 0.31.2 spec
// (test/fixtures/commonmark-spec.json) through the markdown parser with
// html: true and compares the rendered HTML against the expected HTML from
// the spec. It prints a per-section summary and asserts that the overall
// pass count does not regress below a recorded floor.
//
// As with the TypeScript counterpart, the parser targets block-level
// markdown only — most spec examples exercise inline constructs the parser
// does not implement, so the overall pass rate is expected to be modest.

// minCommonMarkPasses is the regression floor. Bump upward as the parser
// improves; never decrease without an explanation in the commit.
// Current baseline: 550/652 (step 14: ordered list start attr + paragraph leading/trailing strip).
const minCommonMarkPasses = 542

type commonMarkCase struct {
	Markdown  string `json:"markdown"`
	HTML      string `json:"html"`
	Example   int    `json:"example"`
	Section   string `json:"section"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func loadCommonMarkSpec(t *testing.T) []commonMarkCase {
	t.Helper()
	path := filepath.Join("..", "..", "test", "fixtures", "commonmark-spec.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read spec: %v", err)
	}
	var spec []commonMarkCase
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("failed to parse spec: %v", err)
	}
	return spec
}

func TestCommonMarkConformance(t *testing.T) {
	spec := loadCommonMarkSpec(t)

	type bucket struct{ pass, fail int }
	bySection := map[string]*bucket{}
	totalPass := 0
	totalFail := 0

	for _, ex := range spec {
		j := jsonic.Make()
		if err := j.UseDefaults(Markdown, Defaults, map[string]any{
			"html": true,
		}); err != nil {
			t.Fatalf("plugin init: %v", err)
		}

		var got string
		result, err := j.Parse(ex.Markdown)
		if err == nil {
			if arr, ok := result.([]any); ok {
				got = ToHTML(arr)
			}
		}

		b := bySection[ex.Section]
		if b == nil {
			b = &bucket{}
			bySection[ex.Section] = b
		}
		if got == ex.HTML {
			b.pass++
			totalPass++
		} else {
			b.fail++
			totalFail++
		}
	}

	sections := make([]string, 0, len(bySection))
	for name := range bySection {
		sections = append(sections, name)
	}
	sort.Strings(sections)

	fmt.Printf("CommonMark 0.31.2 conformance: %d/%d\n", totalPass, len(spec))
	for _, name := range sections {
		b := bySection[name]
		fmt.Printf("  %-42s %d/%d\n", name, b.pass, b.pass+b.fail)
	}

	if totalPass < minCommonMarkPasses {
		t.Errorf("CommonMark conformance regressed: %d passes < floor %d",
			totalPass, minCommonMarkPasses)
	}
	if totalPass+totalFail != len(spec) {
		t.Errorf("every spec example should be counted: got %d, want %d",
			totalPass+totalFail, len(spec))
	}
}

func TestCommonMarkBlockSubsets(t *testing.T) {
	t.Run("atx-headings", func(t *testing.T) {
		j := jsonic.Make()
		j.UseDefaults(Markdown, Defaults, map[string]any{"html": true})

		cases := []struct{ src, want string }{
			{"# foo\n", "<h1>foo</h1>\n"},
			{"## foo\n", "<h2>foo</h2>\n"},
			{"### foo\n", "<h3>foo</h3>\n"},
			{"#### foo\n", "<h4>foo</h4>\n"},
			{"##### foo\n", "<h5>foo</h5>\n"},
			{"###### foo\n", "<h6>foo</h6>\n"},
		}
		for _, c := range cases {
			result, _ := j.Parse(c.src)
			arr, _ := result.([]any)
			got := ToHTML(arr)
			if got != c.want {
				t.Errorf("%q: got %q, want %q", c.src, got, c.want)
			}
		}
	})

	t.Run("thematic-breaks", func(t *testing.T) {
		j := jsonic.Make()
		j.UseDefaults(Markdown, Defaults, map[string]any{"html": true})

		cases := []struct{ src, want string }{
			{"***\n", "<hr />\n"},
			{"---\n", "<hr />\n"},
			{"___\n", "<hr />\n"},
			{" - - -\n", "<hr />\n"},
		}
		for _, c := range cases {
			result, _ := j.Parse(c.src)
			arr, _ := result.([]any)
			got := ToHTML(arr)
			if got != c.want {
				t.Errorf("%q: got %q, want %q", c.src, got, c.want)
			}
		}
	})

	t.Run("fenced-code", func(t *testing.T) {
		j := jsonic.Make()
		j.UseDefaults(Markdown, Defaults, map[string]any{"html": true})

		cases := []struct{ src, want string }{
			{"```\n<\n >\n```\n", "<pre><code>&lt;\n &gt;\n</code></pre>\n"},
			{
				"```ruby\ndef foo(x)\n  return 3\nend\n```\n",
				`<pre><code class="language-ruby">def foo(x)` + "\n  return 3\nend\n</code></pre>\n",
			},
		}
		for _, c := range cases {
			result, _ := j.Parse(c.src)
			arr, _ := result.([]any)
			got := ToHTML(arr)
			if got != c.want {
				t.Errorf("%q: got %q, want %q", c.src, got, c.want)
			}
		}
	})
}

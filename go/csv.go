/* Copyright (c) 2021-2025 Richard Rodger, MIT License */

package csv

import (
	"fmt"
	"strconv"
	"strings"

	jsonic "github.com/jsonicjs/jsonic/go"
)

const Version = "0.1.3"

// --- BEGIN EMBEDDED csv-grammar.jsonic ---
const grammarText = `
# CSV Grammar Definition
# Parsed by a standard Jsonic instance and passed to jsonic.grammar()
# Function references (@ prefixed) are resolved against the refs map
#
# Token naming:
#   #LN - line ending (removed from per-instance IGNORE set)
#   #SP - whitespace  (removed from per-instance IGNORE set in strict mode)
#   #CA - comma / field separator
#   #ZZ - end of input
#   #VAL - token set: text, string, number, value literals
#
# Rules csv, newline, record, text are fully defined here.
# Rules list, elem, val are modified in code (strict mode defines from scratch;
# non-strict prepends to existing defaults to preserve JSON parsing).

{
  rule: csv: open: [
    { s: '#ZZ' }
    { s: '#LN' p: newline c: '@not-record-empty' }
    { p: record }
  ]

  rule: newline: open: [
    { s: '#LN #LN' r: newline }
    { s: '#LN' r: newline }
    { s: '#ZZ' }
    { r: record }
  ]
  rule: newline: close: [
    { s: '#LN #LN' r: newline }
    { s: '#LN' r: newline }
    { s: '#ZZ' }
    { r: record }
  ]

  rule: record: open: [
    { p: list }
  ]
  rule: record: close: [
    { s: '#ZZ' }
    { s: '#LN #ZZ' b: 1 }
    { s: '#LN' r: '@record-close-next' }
  ]

  rule: text: open: [
    { s: ['#VAL' '#SP'] b: 1 r: text n: { text: 1 } g: 'csv,space,follows' a: '@text-follows' }
    { s: ['#SP' '#VAL'] r: text n: { text: 1 } g: 'csv,space,leads' a: '@text-leads' }
    { s: ['#SP' '#CA #LN #ZZ'] b: 1 n: { text: 1 } g: 'csv,end' a: '@text-end' }
    { s: '#SP' n: { text: 1 } g: 'csv,space' a: '@text-space' p: '@text-space-push' }
    {}
  ]
}
`
// --- END EMBEDDED csv-grammar.jsonic ---

// Csv is a jsonic plugin that adds CSV parsing support.
// Options are pre-merged with Defaults by jsonic.UseDefaults.
func Csv(j *jsonic.Jsonic, options map[string]any) error {
	// Guard against re-invocation: Use() re-runs plugins on SetOptions calls.
	if j.Decoration("csv-init") != nil {
		return nil
	}
	j.Decorate("csv-init", true)

	strict := toBool(options["strict"])
	objres := toBool(options["object"])
	header := toBool(options["header"])

	trim := toBool(options["trim"])
	comment := toBool(options["comment"])
	opt_number := toBool(options["number"])
	opt_value := toBool(options["value"])

	fieldOpts, _ := options["field"].(map[string]any)
	recordOpts, _ := options["record"].(map[string]any)
	stringOpts, _ := options["string"].(map[string]any)

	record_empty := toBool(recordOpts["empty"])

	stream, _ := options["stream"].(func(string, any))

	// In strict mode, Jsonic field content is not parsed.
	if strict {
		if stringOpts["csv"] != false {
			j.SetOptions(jsonic.Options{Lex: &jsonic.LexOptions{
				Match: map[string]*jsonic.MatchSpec{
					"stringcsv": {Order: 1e5, Make: buildCsvStringMatcher(stringOpts)},
				},
			}})
		}
		j.SetOptions(jsonic.Options{Rule: &jsonic.RuleOptions{Exclude: "jsonic,imp"}})
	} else {
		// Fields may contain Jsonic content.
		if stringOpts["csv"] == true {
			j.SetOptions(jsonic.Options{Lex: &jsonic.LexOptions{
				Match: map[string]*jsonic.MatchSpec{
					"stringcsv": {Order: 1e5, Make: buildCsvStringMatcher(stringOpts)},
				},
			}})
		}
		if options["trim"] == nil {
			trim = true
		}
		if options["comment"] == nil {
			comment = true
		}
		if options["number"] == nil {
			opt_number = true
		}
		if options["value"] == nil {
			opt_value = true
		}
		j.SetOptions(jsonic.Options{Rule: &jsonic.RuleOptions{Exclude: "imp"}})
	}

	fieldSep := toString(fieldOpts["separation"])
	recordSep := toString(recordOpts["separators"])

	// Jsonic option overrides (matching TS jsonicOptions).
	jsonicOptions := jsonic.Options{
		Rule: &jsonic.RuleOptions{Start: "csv"},
		Number: &jsonic.NumberOptions{
			Lex: boolPtr(opt_number),
		},
		Value: &jsonic.ValueOptions{
			Lex: boolPtr(opt_value),
		},
		Comment: &jsonic.CommentOptions{
			Lex: boolPtr(comment),
		},
		Lex: &jsonic.LexOptions{
			EmptyResult: []any{},
		},
		Line: &jsonic.LineOptions{
			Single: boolPtr(record_empty),
		},
		Error: map[string]string{
			"csv_extra_field":   "unexpected extra field value: $fsrc",
			"csv_missing_field": "missing field",
		},
		Hint: map[string]string{
			"csv_extra_field":   "Row $row has too many fields (the first of which is: $fsrc). Only $len\nfields per row are expected.",
			"csv_missing_field": "Row $row has too few fields. $len fields per row are expected.",
		},
	}

	if strict {
		csvStringOpt := stringOpts["csv"]
		if csvStringOpt == nil || csvStringOpt == true {
			jsonicOptions.String = &jsonic.StringOptions{
				Lex:   boolPtr(false),
				Chars: "",
			}
		}
	}

	if recordSep != "" {
		jsonicOptions.Line.Chars = recordSep
		jsonicOptions.Line.RowChars = recordSep
	}

	// Fixed-token overrides: in strict mode disable JSON structural tokens
	// and the ':' key separator; swap the field separator when configured.
	if strict || fieldSep != "" {
		jsonicOptions.Fixed = &jsonic.FixedOptions{Token: map[string]*string{}}
		if strict {
			jsonicOptions.Fixed.Token["#OB"] = nil
			jsonicOptions.Fixed.Token["#CB"] = nil
			jsonicOptions.Fixed.Token["#OS"] = nil
			jsonicOptions.Fixed.Token["#CS"] = nil
			jsonicOptions.Fixed.Token["#CL"] = nil
		}
		if fieldSep != "" {
			sep := fieldSep
			jsonicOptions.Fixed.Token["#CA"] = &sep
		}
	}

	// IGNORE set: drop #LN so row breaks are significant; in strict mode
	// also drop #SP so whitespace inside fields is preserved.
	if strict {
		jsonicOptions.TokenSet = map[string][]string{"IGNORE": {"#CM"}}
	} else {
		jsonicOptions.TokenSet = map[string][]string{"IGNORE": {"#SP", "#CM"}}
	}

	j.SetOptions(jsonicOptions)

	// Named function references for declarative grammar definition.
	emptyField := toString(fieldOpts["empty"])
	nonameprefix := toString(fieldOpts["nonameprefix"])
	fieldExact := toBool(fieldOpts["exact"])
	var fieldNames []string
	if names, ok := fieldOpts["names"].([]string); ok {
		fieldNames = names
	} else if names, ok := fieldOpts["names"].([]any); ok {
		for _, n := range names {
			if s, ok := n.(string); ok {
				fieldNames = append(fieldNames, s)
			}
		}
	}

	refs := map[jsonic.FuncRef]any{

		"@csv-bo": jsonic.StateAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if ctx.Meta == nil {
				ctx.Meta = make(map[string]any)
			}
			ctx.Meta["recordI"] = 0
			if stream != nil {
				stream("start", nil)
			}
			r.Node = make([]any, 0)
		}),

		"@csv-ac": jsonic.StateAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if stream != nil {
				stream("end", nil)
			}
		}),

		"@record-bc": jsonic.StateAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			recordI, _ := ctx.Meta["recordI"].(int)
			var fields []string
			if fs, ok := ctx.Meta["fields"].([]string); ok {
				fields = fs
			}
			if fields == nil {
				fields = fieldNames
			}

			if recordI == 0 && header {
				if childArr, ok := r.Child.Node.([]any); ok {
					names := make([]string, len(childArr))
					for i, v := range childArr {
						names[i], _ = v.(string)
					}
					ctx.Meta["fields"] = names
				} else {
					ctx.Meta["fields"] = []string{}
				}
			} else {
				record, _ := r.Child.Node.([]any)
				if record == nil {
					record = []any{}
				}

				if objres {
					obj := make(map[string]any)
					var keys []string
					i := 0

					if fields != nil {
						if fieldExact && len(record) != len(fields) {
							errCode := "csv_missing_field"
							if len(record) > len(fields) {
								errCode = "csv_extra_field"
							}
							ctx.ParseErr = &jsonic.Token{
								Name: "#BD", Tin: jsonic.TinBD,
								Why: errCode, Src: errCode,
							}
							return
						}

						for fI := 0; fI < len(fields); fI++ {
							var val any = emptyField
							if fI < len(record) && !jsonic.IsUndefined(record[fI]) {
								val = record[fI]
							}
							obj[fields[fI]] = val
							keys = append(keys, fields[fI])
						}
						i = len(fields)
					}

					for ; i < len(record); i++ {
						fname := nonameprefix + strconv.Itoa(i)
						val := record[i]
						if jsonic.IsUndefined(val) {
							val = emptyField
						}
						obj[fname] = val
						keys = append(keys, fname)
					}

					out := orderedMap{keys: keys, m: obj}
					if stream != nil {
						stream("record", out)
					} else if arr, ok := r.Node.([]any); ok {
						r.Node = append(arr, out)
						if r.Parent != jsonic.NoRule && r.Parent != nil {
							r.Parent.Node = r.Node
						}
					}
				} else {
					for i := range record {
						if jsonic.IsUndefined(record[i]) {
							record[i] = emptyField
						}
					}
					if stream != nil {
						stream("record", record)
					} else if arr, ok := r.Node.([]any); ok {
						r.Node = append(arr, record)
						if r.Parent != jsonic.NoRule && r.Parent != nil {
							r.Parent.Node = r.Node
						}
					}
				}
			}
			ctx.Meta["recordI"] = recordI + 1
		}),

		"@text-bc": jsonic.StateAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if !jsonic.IsUndefined(r.Child.Node) {
				r.Parent.Node = r.Child.Node
			} else {
				r.Parent.Node = r.Node
			}
		}),

		"@text-follows": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			prev := ""
			if r.N["text"] != 1 && r.Prev != nil && r.Prev != jsonic.NoRule {
				prev, _ = r.Prev.Node.(string)
			}
			result := prev + tokenStr(r.O0)
			r.Node = result
			if r.N["text"] == 1 {
			} else if r.Prev != nil && r.Prev != jsonic.NoRule {
				r.Prev.Node = result
			}
		}),

		"@text-leads": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			prev := ""
			if r.N["text"] != 1 && r.Prev != nil && r.Prev != jsonic.NoRule {
				prev, _ = r.Prev.Node.(string)
			}
			sp := ""
			if r.N["text"] >= 2 || !trim {
				sp = r.O0.Src
			}
			result := prev + sp + r.O1.Src
			r.Node = result
			if r.N["text"] == 1 {
			} else if r.Prev != nil && r.Prev != jsonic.NoRule {
				r.Prev.Node = result
			}
		}),

		"@text-end": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			prev := ""
			if r.N["text"] != 1 && r.Prev != nil && r.Prev != jsonic.NoRule {
				prev, _ = r.Prev.Node.(string)
			}
			sp := ""
			if !trim {
				sp = r.O0.Src
			}
			result := prev + sp
			r.Node = result
			if r.N["text"] == 1 {
			} else if r.Prev != nil && r.Prev != jsonic.NoRule {
				r.Prev.Node = result
			}
		}),

		"@text-space": jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if strict {
				prev := ""
				if r.N["text"] != 1 && r.Prev != nil && r.Prev != jsonic.NoRule {
					prev, _ = r.Prev.Node.(string)
				}
				sp := ""
				if !trim {
					sp = r.O0.Src
				}
				result := prev + sp
				r.Node = result
				if r.N["text"] == 1 {
				} else if r.Prev != nil && r.Prev != jsonic.NoRule {
					r.Prev.Node = result
				}
			}
		}),

		"@not-record-empty": jsonic.AltCond(func(r *jsonic.Rule, ctx *jsonic.Context) bool {
			return !record_empty
		}),

		"@record-close-next": func(r *jsonic.Rule, ctx *jsonic.Context) string {
			if record_empty {
				return "record"
			}
			return "newline"
		},

		"@text-space-push": func(r *jsonic.Rule, ctx *jsonic.Context) string {
			if strict {
				return ""
			}
			return "val"
		},
	}

	// Parse embedded grammar definition using a separate standard Jsonic instance.
	gs, err := parseGrammarText(grammarText, refs)
	if err != nil {
		return err
	}
	if err := j.Grammar(gs); err != nil {
		return fmt.Errorf("failed to apply csv grammar: %w", err)
	}

	// Rules list, elem, val are modified in code rather than the grammar file,
	// because in non-strict mode the default jsonic alternatives must be preserved
	// to support embedded JSON values like [1,2] and {x:1}.

	LN := j.Token("#LN")
	CA := j.Token("#CA")
	SP := j.Token("#SP")
	ZZ := j.Token("#ZZ")
	VAL := j.TokenSet("VAL")

	j.Rule("list", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.Clear()
		rs.AddBO(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.Node = make([]any, 0)
		})
		rs.Open = []*jsonic.AltSpec{
			{S: [][]jsonic.Tin{{LN}}, B: 1},
			{P: "elem"},
		}
		rs.Close = []*jsonic.AltSpec{
			{S: [][]jsonic.Tin{{LN}}, B: 1},
			{S: [][]jsonic.Tin{{ZZ}}},
		}
	})

	j.Rule("elem", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.Clear()
		rs.Open = []*jsonic.AltSpec{
			{S: [][]jsonic.Tin{{CA}}, B: 1,
				A: jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
					if arr, ok := r.Node.([]any); ok {
						r.Node = append(arr, emptyField)
						if r.Parent != jsonic.NoRule && r.Parent != nil {
							r.Parent.Node = r.Node
						}
					}
					r.U["done"] = true
				})},
			{P: "val"},
		}
		rs.Close = []*jsonic.AltSpec{
			{S: [][]jsonic.Tin{{CA}, {LN, ZZ}}, B: 1,
				A: jsonic.AltAction(func(r *jsonic.Rule, ctx *jsonic.Context) {
					if arr, ok := r.Node.([]any); ok {
						r.Node = append(arr, emptyField)
						if r.Parent != jsonic.NoRule && r.Parent != nil {
							r.Parent.Node = r.Node
						}
					}
				})},
			{S: [][]jsonic.Tin{{CA}}, R: "elem"},
			{S: [][]jsonic.Tin{{LN}}, B: 1},
			{S: [][]jsonic.Tin{{ZZ}}},
		}
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			done, _ := r.U["done"].(bool)
			if !done && !jsonic.IsUndefined(r.Child.Node) {
				if arr, ok := r.Node.([]any); ok {
					r.Node = append(arr, r.Child.Node)
					if r.Parent != jsonic.NoRule && r.Parent != nil {
						r.Parent.Node = r.Node
					}
				}
			}
		})
	})

	j.Rule("val", func(rs *jsonic.RuleSpec, _ *jsonic.Parser) {
		rs.Clear()
		rs.AddBO(func(r *jsonic.Rule, ctx *jsonic.Context) {
			r.Node = jsonic.Undefined
		})
		rs.Open = []*jsonic.AltSpec{
			{S: [][]jsonic.Tin{VAL, {SP}}, B: 2, P: "text"},
			{S: [][]jsonic.Tin{{SP}}, B: 1, P: "text"},
			{S: [][]jsonic.Tin{VAL}},
			{S: [][]jsonic.Tin{{LN}}, B: 1},
		}
		rs.AddBC(func(r *jsonic.Rule, ctx *jsonic.Context) {
			if jsonic.IsUndefined(r.Node) {
				if jsonic.IsUndefined(r.Child.Node) {
					if r.OS == 0 {
						r.Node = jsonic.Undefined
					} else {
						r.Node = r.O0.ResolveVal(r, ctx)
					}
				} else {
					r.Node = r.Child.Node
				}
			}
		})
	})

	return nil
}

// Custom CSV String matcher factory.
// Handles "a""b" -> a"b quoting.
// Matches TS: buildCsvStringMatcher(options) returns make(cfg, opts) => matcher(lex).
func buildCsvStringMatcher(stringOpts map[string]any) jsonic.MakeLexMatcher {
	quote := toString(stringOpts["quote"])
	return func(cfg *jsonic.LexConfig, opts *jsonic.Options) jsonic.LexMatcher {
		return func(lex *jsonic.Lex, rule *jsonic.Rule) *jsonic.Token {
			pnt := lex.Cursor()
			src := lex.Src
			sI := pnt.SI
			srclen := len(src)

			if sI >= srclen || !strings.HasPrefix(src[sI:], quote) {
				return nil
			}

			// Only match when quote is at the start of a field.
			if sI > 0 {
				prev := rune(src[sI-1])
				_, isFixed := cfg.FixedTokens[string(prev)]
				if !isFixed && !cfg.LineChars[prev] && !cfg.SpaceChars[prev] {
					return nil
				}
			}

			q := quote
			qLen := len(q)
			rI := pnt.RI
			cI := pnt.CI
			sI += qLen
			cI += qLen

			var s strings.Builder
			for sI < srclen {
				cI++
				if strings.HasPrefix(src[sI:], q) {
					sI += qLen
					cI += qLen - 1
					if sI < srclen && strings.HasPrefix(src[sI:], q) {
						s.WriteString(q)
						sI += qLen
						cI += qLen
						continue
					}
					val := s.String()
					ssrc := src[pnt.SI:sI]
					tkn := lex.Token("#ST", jsonic.TinST, val, ssrc)
					pnt.SI = sI
					pnt.RI = rI
					pnt.CI = cI
					return tkn
				}

				ch := src[sI]
				if cfg.LineChars[rune(ch)] {
					if cfg.RowChars[rune(ch)] {
						rI++
						pnt.RI = rI
					}
					cI = 1
					s.WriteByte(ch)
					sI++
					continue
				}
				if ch < 32 {
					return nil
				}

				bI := sI
				qFirst := q[0]
				for sI < srclen && src[sI] >= 32 && src[sI] != qFirst {
					if cfg.LineChars[rune(src[sI])] {
						break
					}
					sI++
					cI++
				}
				cI--
				s.WriteString(src[bI:sI])
			}

			badSrc := src[pnt.SI:sI]
			tkn := lex.Token("#BD", jsonic.TinBD, nil, badSrc)
			tkn.Why = "unterminated_string"
			pnt.SI = sI
			pnt.RI = rI
			pnt.CI = cI
			return tkn
		}
	}
}

// Defaults matches the TS Csv.defaults. Used with jsonic.UseDefaults.
var Defaults = map[string]any{
	"trim":    nil,
	"comment": nil,
	"number":  nil,
	"value":   nil,
	"header":  true,
	"object":  true,
	"stream":  nil,
	"strict":  true,
	"field": map[string]any{
		"separation":   nil,
		"nonameprefix": "field~",
		"empty":        "",
		"names":        nil,
		"exact":        false,
	},
	"record": map[string]any{
		"separators": nil,
		"empty":      false,
	},
	"string": map[string]any{
		"quote": `"`,
		"csv":   nil,
	},
}

// parseGrammarText parses grammar text and builds a GrammarSpec with Ref support.
func parseGrammarText(text string, refs map[jsonic.FuncRef]any) (*jsonic.GrammarSpec, error) {
	parsed, err := jsonic.Make().Parse(text)
	if err != nil {
		return nil, fmt.Errorf("failed to parse grammar text: %w", err)
	}
	parsedMap, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("grammar text did not parse to a map")
	}
	gs := &jsonic.GrammarSpec{Ref: refs}
	ruleMap, ok := parsedMap["rule"].(map[string]any)
	if !ok {
		return gs, nil
	}
	gs.Rule = make(map[string]*jsonic.GrammarRuleSpec, len(ruleMap))
	for name, rDef := range ruleMap {
		rd, ok := rDef.(map[string]any)
		if !ok {
			continue
		}
		grs := &jsonic.GrammarRuleSpec{}
		if openDef, ok := rd["open"]; ok {
			grs.Open = buildGrammarAlts(openDef)
		}
		if closeDef, ok := rd["close"]; ok {
			grs.Close = buildGrammarAlts(closeDef)
		}
		gs.Rule[name] = grs
	}
	return gs, nil
}

func buildGrammarAlts(def any) []*jsonic.GrammarAltSpec {
	arr, ok := def.([]any)
	if !ok {
		return nil
	}
	alts := make([]*jsonic.GrammarAltSpec, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			alts = append(alts, &jsonic.GrammarAltSpec{})
			continue
		}
		ga := &jsonic.GrammarAltSpec{}
		if s, ok := m["s"]; ok {
			switch sv := s.(type) {
			case string:
				ga.S = sv
			case []any:
				strs := make([]string, len(sv))
				for i, v := range sv {
					strs[i], _ = v.(string)
				}
				ga.S = strs
			}
		}
		if b, ok := m["b"]; ok {
			switch bv := b.(type) {
			case float64:
				ga.B = int(bv)
			case int:
				ga.B = bv
			}
		}
		if p, ok := m["p"].(string); ok {
			ga.P = p
		}
		if r, ok := m["r"].(string); ok {
			ga.R = r
		}
		if a, ok := m["a"].(string); ok {
			ga.A = jsonic.FuncRef(a)
		}
		if c, ok := m["c"]; ok {
			switch cv := c.(type) {
			case string:
				ga.C = cv
			case map[string]any:
				ga.C = cv
			}
		}
		if n, ok := m["n"].(map[string]any); ok {
			ga.N = make(map[string]int, len(n))
			for k, v := range n {
				if nv, ok := v.(float64); ok {
					ga.N[k] = int(nv)
				} else if nv, ok := v.(int); ok {
					ga.N[k] = nv
				}
			}
		}
		if g, ok := m["g"].(string); ok {
			ga.G = g
		}
		alts = append(alts, ga)
	}
	return alts
}

func tokenStr(t *jsonic.Token) string {
	if t == nil || t.IsNoToken() {
		return ""
	}
	if t.Tin == jsonic.TinST {
		if s, ok := t.Val.(string); ok {
			return s
		}
	}
	return t.Src
}

func toBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

func boolPtr(b bool) *bool {
	return &b
}

// orderedMap maintains insertion order for JSON serialization comparison.
type orderedMap struct {
	keys []string
	m    map[string]any
}

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"internal/testenv"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// rxCorpus holds every pattern of the regexp test corpus, in first-seen
// order, with the input texts that the corpus runs it against.
type rxCorpus struct {
	patterns []string
	base     int // the patterns before the RE2 exhaustive file
	texts    map[string][]string
	seen     map[string]map[string]bool
}

func newRxCorpus() *rxCorpus {
	return &rxCorpus{texts: map[string][]string{}, seen: map[string]map[string]bool{}}
}

func (corpus *rxCorpus) add(pattern string, texts ...string) {
	seen := corpus.seen[pattern]
	if seen == nil {
		seen = map[string]bool{}
		corpus.seen[pattern] = seen
		corpus.patterns = append(corpus.patterns, pattern)
	}
	for _, text := range texts {
		if !seen[text] {
			seen[text] = true
			corpus.texts[pattern] = append(corpus.texts[pattern], text)
		}
	}
}

// loadRxCorpus reads the regexp package's own test data: the RE2 search
// file, the Fowler POSIX files, and the patterns that its test files and
// those of regexp/syntax declare. When exhaustive is set, the RE2
// exhaustive file follows, and corpus.base counts the patterns before it.
func loadRxCorpus(t *testing.T, exhaustive bool) *rxCorpus {
	dir := filepath.Join(testenv.GOROOT(t), "src", "regexp")
	corpus := newRxCorpus()
	loadRE2(t, corpus, filepath.Join(dir, "testdata", "re2-search.txt"))
	fowler, err := filepath.Glob(filepath.Join(dir, "testdata", "*.dat"))
	if err != nil || len(fowler) == 0 {
		t.Fatalf("no Fowler files in %s: %v", dir, err)
	}
	for _, file := range fowler {
		loadFowler(t, corpus, file)
	}
	// The value is the index of the pattern among the string elements of
	// one entry. Every other string of the entry is an input text.
	loadGoVars(t, corpus, filepath.Join(dir, "find_test.go"), map[string]int{"findTests": 0})
	loadGoVars(t, corpus, filepath.Join(dir, "all_test.go"), map[string]int{
		"goodRe": 0, "badRe": 0, "replaceTests": 0, "replaceLiteralTests": 0,
		"replaceFuncTests": 0, "metaTests": 0, "literalPrefixTests": 0,
		"subexpCases": 0, "splitTests": 1, "compileBenchData": 1, "minInputLenTests": 0,
	})
	loadGoVars(t, corpus, filepath.Join(dir, "exec_test.go"), map[string]int{"benchData": 1})
	loadGoVars(t, corpus, filepath.Join(dir, "onepass_test.go"), map[string]int{"onePassTests": 0, "onePassTests1": 0})
	loadGoVars(t, corpus, filepath.Join(dir, "syntax", "parse_test.go"), map[string]int{
		"parseTests": 0, "foldcaseTests": 0, "literalTests": 0, "matchnlTests": 0,
		"nomatchnlTests": 0, "invalidRegexps": 0, "onlyPerl": 0, "onlyPOSIX": 0, "stringTests": 0,
	})
	loadGoVars(t, corpus, filepath.Join(dir, "syntax", "prog_test.go"), map[string]int{"compileTests": 0})
	loadGoVars(t, corpus, filepath.Join(dir, "syntax", "simplify_test.go"), map[string]int{"simplifyTests": 0})
	corpus.base = len(corpus.patterns)
	if exhaustive {
		loadRE2(t, corpus, filepath.Join(dir, "testdata", "re2-exhaustive.txt.bz2"))
	}
	return corpus
}

// loadRE2 reads a file in the format that regexp's exec_test.go documents.
func loadRE2(t *testing.T, corpus *rxCorpus, file string) {
	handle, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	var src io.Reader = handle
	if strings.HasSuffix(file, ".bz2") {
		src = bzip2.NewReader(handle)
	}
	scanner := bufio.NewScanner(src)
	var strs []string
	inStrings := false
	count := 0
	for lineno := 1; scanner.Scan(); lineno++ {
		line := scanner.Text()
		switch {
		case line == "strings":
			strs = nil
			inStrings = true
		case line == "regexps":
			inStrings = false
		case strings.HasPrefix(line, `"`):
			str, err := strconv.Unquote(line)
			if err != nil {
				t.Fatalf("%s:%d: %v", file, lineno, err)
			}
			if inStrings {
				strs = append(strs, str)
				continue
			}
			corpus.add(str, strs...)
			corpus.add(`\A(?:`+str+`)\z`, strs...)
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("%s: no patterns", file)
	}
}

// loadFowler reads a file in the format that regexp's TestFowler documents.
func loadFowler(t *testing.T, corpus *rxCorpus, file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	notab := regexp.MustCompilePOSIX(`[^\t]+`)
	last := ""
	count := 0
Lines:
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		field := notab.FindAllString(line, -1)
		for idx, str := range field {
			if str == "NULL" {
				field[idx] = ""
			}
			if str == "NIL" {
				continue Lines
			}
		}
		if len(field) < 4 {
			continue
		}
		flag := field[0]
		switch flag[0] {
		case '?', '&', '|', ';', '{', '}':
			flag = flag[1:]
		case ':':
			var found bool
			if _, flag, found = strings.Cut(flag[1:], ":"); !found {
				continue
			}
		case 'C', 'N', 'T', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			continue
		}
		if strings.Contains(flag, "$") {
			for idx := 1; idx <= 2; idx++ {
				if str, err := strconv.Unquote(`"` + field[idx] + `"`); err == nil {
					field[idx] = str
				}
			}
		}
		if field[1] == "SAME" {
			field[1] = last
		}
		last = field[1]
		corpus.add(field[1], field[2])
		if strings.Contains(flag, "L") {
			corpus.add(regexp.QuoteMeta(field[1]), field[2])
		}
		if strings.Contains(flag, "i") {
			corpus.add("(?i)"+field[1], field[2])
		}
		count++
	}
	if count == 0 {
		t.Fatalf("%s: no patterns", file)
	}
}

// loadGoVars reads the composite literal of each named package variable of
// a Go file. vars maps a name to the index of the pattern among the string
// elements of one entry.
func loadGoVars(t *testing.T, corpus *rxCorpus, file string, vars map[string]int) {
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, decl := range parsed.Decls {
		gen, isGen := decl.(*ast.GenDecl)
		if !isGen || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vspec := spec.(*ast.ValueSpec)
			for idx, name := range vspec.Names {
				index, wanted := vars[name.Name]
				if !wanted {
					continue
				}
				lit, isLit := vspec.Values[idx].(*ast.CompositeLit)
				if !isLit {
					t.Fatalf("%s: %s is not a composite literal", file, name.Name)
				}
				for _, elt := range lit.Elts {
					strs := goStrings(elt)
					if index >= len(strs) {
						t.Fatalf("%s: an entry of %s has no pattern at string %d", file, name.Name, index)
					}
					texts := append(append([]string{}, strs[:index]...), strs[index+1:]...)
					corpus.add(strs[index], texts...)
					found[name.Name]++
				}
			}
		}
	}
	for name := range vars {
		if found[name] == 0 {
			t.Fatalf("%s: no entries in %s", file, name)
		}
	}
}

// goStrings returns the constant string values of an entry: the entry
// itself, or the elements of a struct literal in order.
func goStrings(expr ast.Expr) []string {
	if str, isStr := goString(expr); isStr {
		return []string{str}
	}
	lit, isLit := expr.(*ast.CompositeLit)
	if !isLit {
		return nil
	}
	var strs []string
	for _, elt := range lit.Elts {
		if pair, isPair := elt.(*ast.KeyValueExpr); isPair {
			elt = pair.Value
		}
		if str, isStr := goString(elt); isStr {
			strs = append(strs, str)
		}
	}
	return strs
}

func goString(expr ast.Expr) (string, bool) {
	val := goConst(expr)
	if val.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(val), true
}

// goConst evaluates literals, operators, parentheses and strings.Repeat,
// which is what the corpus files spell their patterns with.
func goConst(expr ast.Expr) constant.Value {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		return constant.MakeFromLiteral(expr.Value, expr.Kind, 0)
	case *ast.ParenExpr:
		return goConst(expr.X)
	case *ast.BinaryExpr:
		left, right := goConst(expr.X), goConst(expr.Y)
		if left.Kind() == constant.Unknown || right.Kind() == constant.Unknown {
			return constant.MakeUnknown()
		}
		if expr.Op == token.SHL || expr.Op == token.SHR {
			count, exact := constant.Uint64Val(right)
			if !exact {
				return constant.MakeUnknown()
			}
			return constant.Shift(left, expr.Op, uint(count))
		}
		return constant.BinaryOp(left, expr.Op, right)
	case *ast.CallExpr:
		sel, isSel := expr.Fun.(*ast.SelectorExpr)
		if !isSel || sel.Sel.Name != "Repeat" || len(expr.Args) != 2 {
			return constant.MakeUnknown()
		}
		if pkg, isIdent := sel.X.(*ast.Ident); !isIdent || pkg.Name != "strings" {
			return constant.MakeUnknown()
		}
		str, count := goConst(expr.Args[0]), goConst(expr.Args[1])
		num, exact := constant.Int64Val(count)
		if str.Kind() != constant.String || !exact {
			return constant.MakeUnknown()
		}
		return constant.MakeString(strings.Repeat(constant.StringVal(str), int(num)))
	}
	return constant.MakeUnknown()
}

// rxModes are the two pattern syntaxes, by the names of their functions.
var rxModes = [2]struct{ must, compile string }{
	{"MustCompile", "Compile"},
	{"MustCompilePOSIX", "CompilePOSIX"},
}

// writeDifferential writes a program that builds each corpus pattern from
// a constant and from a variable, in both modes, and compares the two.
func writeDifferential(corpus *rxCorpus) []byte {
	var buf bytes.Buffer
	buf.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"reflect\"\n\t\"regexp\"\n\t\"strings\"\n)\n\n")
	// Many patterns share one list of inputs, so each list is written once.
	setIndex := map[string]int{}
	var sets [][]string
	textSet := make([]int, len(corpus.patterns))
	for idx, pattern := range corpus.patterns {
		texts := corpus.texts[pattern]
		var key strings.Builder
		for _, text := range texts {
			fmt.Fprintf(&key, "%d:%s", len(text), text)
		}
		set, seen := setIndex[key.String()]
		if !seen {
			set = len(sets)
			setIndex[key.String()] = set
			sets = append(sets, texts)
		}
		textSet[idx] = set
	}
	fmt.Fprintf(&buf, "const basePatterns = %d\n\n", corpus.base)
	buf.WriteString("var patterns = []string{\n")
	for _, pattern := range corpus.patterns {
		fmt.Fprintf(&buf, "\t%s,\n", strconv.Quote(pattern))
	}
	buf.WriteString("}\n\nvar textSets = [][]string{\n")
	for _, set := range sets {
		buf.WriteString("\t{")
		for _, text := range set {
			fmt.Fprintf(&buf, "%s, ", strconv.Quote(text))
		}
		buf.WriteString("},\n")
	}
	buf.WriteString("}\n\nvar textSet = []int32{")
	for idx, set := range textSet {
		if idx%32 == 0 {
			buf.WriteString("\n\t")
		}
		fmt.Fprintf(&buf, "%d, ", set)
	}
	buf.WriteString("\n}\n")
	const chunk = 100
	for start := 0; start < len(corpus.patterns); start += chunk {
		fmt.Fprintf(&buf, "\nfunc chunk%d(pre [][2]*regexp.Regexp, preErr [][2]string) {\n", start/chunk)
		for idx := start; idx < min(start+chunk, len(corpus.patterns)); idx++ {
			lit := strconv.Quote(corpus.patterns[idx])
			for mode, names := range rxModes {
				var err error
				if mode == 0 {
					_, err = regexp.Compile(corpus.patterns[idx])
				} else {
					_, err = regexp.CompilePOSIX(corpus.patterns[idx])
				}
				if err == nil {
					fmt.Fprintf(&buf, "\tpre[%d][%d] = regexp.%s(%s)\n", idx, mode, names.must, lit)
				} else {
					fmt.Fprintf(&buf, "\tpreErr[%d][%d] = errText(regexp.%s(%s))\n", idx, mode, names.compile, lit)
				}
			}
		}
		buf.WriteString("}\n")
	}
	buf.WriteString("\nfunc main() {\n\tpre := make([][2]*regexp.Regexp, len(patterns))\n\tpreErr := make([][2]string, len(patterns))\n")
	for start := 0; start < len(corpus.patterns); start += chunk {
		fmt.Fprintf(&buf, "\tchunk%d(pre, preErr)\n", start/chunk)
	}
	buf.WriteString("\tcompareAll(pre, preErr)\n}\n")
	buf.WriteString(rxDifferentialMain)
	return buf.Bytes()
}

// rxDifferentialMain is the fixed half of the differential program.
const rxDifferentialMain = `
var compiled, rejected, checks, failures int

func errText(re *regexp.Regexp, err error) string {
	if re != nil || err == nil {
		return "a constant Compile of an invalid pattern returned a Regexp"
	}
	return err.Error()
}

func fail(format string, args ...any) {
	failures++
	if failures <= 50 {
		fmt.Printf("FAIL "+format+"\n", args...)
	}
}

func compareAll(pre [][2]*regexp.Regexp, preErr [][2]string) {
	for idx, pattern := range patterns {
		for mode := range 2 {
			var run *regexp.Regexp
			var err error
			if mode == 0 {
				run, err = regexp.Compile(pattern)
			} else {
				run, err = regexp.CompilePOSIX(pattern)
			}
			if err != nil {
				rejected++
				if pre[idx][mode] != nil || preErr[idx][mode] != err.Error() {
					fail("%q mode %d: constant gives %v %q, variable gives error %q", pattern, mode, pre[idx][mode], preErr[idx][mode], err)
				}
				continue
			}
			compiled++
			if pre[idx][mode] == nil {
				fail("%q mode %d: constant gives error %q, variable compiles", pattern, mode, preErr[idx][mode])
				continue
			}
			compareOne(pattern, mode, pre[idx][mode], run, textSets[textSet[idx]], idx < basePatterns)
		}
	}
	fmt.Printf("patterns %d every-method %d compiled %d rejected %d checks %d failures %d\n", len(patterns), basePatterns, compiled, rejected, checks, failures)
	if failures > 0 {
		os.Exit(1)
	}
}

// sameFunc compares one answer of the precompiled Regexp with the answer of
// the run-time one. DeepEqual tells a nil slice from an empty one, as a
// caller can.
type sameFunc func(what string, have, want any)

// compareOne checks a precompiled Regexp against a run-time one. full runs
// every method. Otherwise the inputs go through the four matching entry
// points: string, bytes, reader, and the repeated search.
func compareOne(pattern string, mode int, pre, run *regexp.Regexp, texts []string, full bool) {
	checks++
	if !reflect.DeepEqual(*pre, *run) {
		fail("%q mode %d: the precompiled Regexp differs from the run-time one", pattern, mode)
	}
	same := func(what string, have, want any) {
		checks++
		if !reflect.DeepEqual(have, want) {
			fail("%q mode %d: %s: precompiled %#v, run time %#v", pattern, mode, what, have, want)
		}
	}
	same("String", pre.String(), run.String())
	same("NumSubexp", pre.NumSubexp(), run.NumSubexp())
	same("SubexpNames", pre.SubexpNames(), run.SubexpNames())
	for _, name := range run.SubexpNames() {
		same("SubexpIndex "+name, pre.SubexpIndex(name), run.SubexpIndex(name))
	}
	preLit, preComplete := pre.LiteralPrefix()
	runLit, runComplete := run.LiteralPrefix()
	same("LiteralPrefix", [2]any{preLit, preComplete}, [2]any{runLit, runComplete})
	if full {
		preText, preErr := pre.MarshalText()
		runText, runErr := run.MarshalText()
		same("MarshalText", [2]any{preText, preErr}, [2]any{runText, runErr})
		preApp, preErr := pre.AppendText([]byte("x"))
		runApp, runErr := run.AppendText([]byte("x"))
		same("AppendText", [2]any{preApp, preErr}, [2]any{runApp, runErr})
		same("Copy", *pre.Copy(), *run.Copy())
	}
	for longest := range 2 {
		if longest == 1 {
			pre.Longest()
			run.Longest()
		}
		for _, text := range texts {
			if full {
				everyMethod(same, pre, run, text)
				continue
			}
			bin := []byte(text)
			same("MatchString "+text, pre.MatchString(text), run.MatchString(text))
			same("FindSubmatchIndex "+text, pre.FindSubmatchIndex(bin), run.FindSubmatchIndex(bin))
			same("FindReaderSubmatchIndex "+text, pre.FindReaderSubmatchIndex(strings.NewReader(text)), run.FindReaderSubmatchIndex(strings.NewReader(text)))
			same("FindAllStringSubmatchIndex "+text, pre.FindAllStringSubmatchIndex(text, -1), run.FindAllStringSubmatchIndex(text, -1))
		}
	}
}

// everyMethod compares every matching, replacing and splitting method of
// regexp.Regexp on one input.
func everyMethod(same sameFunc, pre, run *regexp.Regexp, text string) {
	bin := []byte(text)
	reader := func() *strings.Reader { return strings.NewReader(text) }
	wrap := func(str string) string { return "<" + str + ">" }
	wrapBytes := func(bin []byte) []byte { return append(append([]byte("<"), bin...), '>') }
	const template = "[$0|${1}|$name|$$]"
	same("Match", pre.Match(bin), run.Match(bin))
	same("MatchString", pre.MatchString(text), run.MatchString(text))
	same("MatchReader", pre.MatchReader(reader()), run.MatchReader(reader()))
	same("Find", pre.Find(bin), run.Find(bin))
	same("FindIndex", pre.FindIndex(bin), run.FindIndex(bin))
	same("FindString", pre.FindString(text), run.FindString(text))
	same("FindStringIndex", pre.FindStringIndex(text), run.FindStringIndex(text))
	same("FindReaderIndex", pre.FindReaderIndex(reader()), run.FindReaderIndex(reader()))
	same("FindSubmatch", pre.FindSubmatch(bin), run.FindSubmatch(bin))
	same("FindSubmatchIndex", pre.FindSubmatchIndex(bin), run.FindSubmatchIndex(bin))
	same("FindStringSubmatch", pre.FindStringSubmatch(text), run.FindStringSubmatch(text))
	same("FindStringSubmatchIndex", pre.FindStringSubmatchIndex(text), run.FindStringSubmatchIndex(text))
	same("FindReaderSubmatchIndex", pre.FindReaderSubmatchIndex(reader()), run.FindReaderSubmatchIndex(reader()))
	for _, count := range []int{-1, 0, 1, 2} {
		same("FindAll", pre.FindAll(bin, count), run.FindAll(bin, count))
		same("FindAllIndex", pre.FindAllIndex(bin, count), run.FindAllIndex(bin, count))
		same("FindAllString", pre.FindAllString(text, count), run.FindAllString(text, count))
		same("FindAllStringIndex", pre.FindAllStringIndex(text, count), run.FindAllStringIndex(text, count))
		same("FindAllSubmatch", pre.FindAllSubmatch(bin, count), run.FindAllSubmatch(bin, count))
		same("FindAllSubmatchIndex", pre.FindAllSubmatchIndex(bin, count), run.FindAllSubmatchIndex(bin, count))
		same("FindAllStringSubmatch", pre.FindAllStringSubmatch(text, count), run.FindAllStringSubmatch(text, count))
		same("FindAllStringSubmatchIndex", pre.FindAllStringSubmatchIndex(text, count), run.FindAllStringSubmatchIndex(text, count))
		same("Split", pre.Split(text, count), run.Split(text, count))
	}
	same("ReplaceAll", pre.ReplaceAll(bin, []byte(template)), run.ReplaceAll(bin, []byte(template)))
	same("ReplaceAllString", pre.ReplaceAllString(text, template), run.ReplaceAllString(text, template))
	same("ReplaceAllLiteral", pre.ReplaceAllLiteral(bin, []byte(template)), run.ReplaceAllLiteral(bin, []byte(template)))
	same("ReplaceAllLiteralString", pre.ReplaceAllLiteralString(text, template), run.ReplaceAllLiteralString(text, template))
	same("ReplaceAllFunc", pre.ReplaceAllFunc(bin, wrapBytes), run.ReplaceAllFunc(bin, wrapBytes))
	same("ReplaceAllStringFunc", pre.ReplaceAllStringFunc(text, wrap), run.ReplaceAllStringFunc(text, wrap))
	if match := run.FindSubmatchIndex(bin); match != nil {
		same("Expand", pre.Expand(nil, []byte(template), bin, match), run.Expand(nil, []byte(template), bin, match))
		same("ExpandString", pre.ExpandString(nil, template, text, match), run.ExpandString(nil, template, text, match))
	}
}
`

// TestRegexpPrecompileDifferential builds every pattern of the regexp test
// corpus from a constant, which the compiler precompiles, and from a
// variable, which compiles at run time. The two must be deeply equal, and
// they must give the same answers on every corpus input. Every method runs
// on the patterns outside the RE2 exhaustive file. Its tens of thousands of
// patterns run the four matching entry points.
func TestRegexpPrecompileDifferential(t *testing.T) {
	testenv.MustHaveGoRun(t)
	t.Parallel()
	corpus := loadRxCorpus(t, true)
	src := filepath.Join(t.TempDir(), "differential.go")
	if err := os.WriteFile(src, writeDifferential(corpus), 0o666); err != nil {
		t.Fatal(err)
	}
	out, err := testenv.Command(t, testenv.GoToolPath(t), "run", src).CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("differential program failed: %v", err)
	}
	if !bytes.Contains(out, []byte(" failures 0\n")) {
		t.Fatalf("differential program printed no summary")
	}
}

// TestRegexpPrecompileNoRuntimeCompile builds a program whose patterns are
// all constant and checks that the linker found no reachable regexp parser
// or compiler in it. A program that adds one variable pattern must have
// them, which shows the check can fail.
func TestRegexpPrecompileNoRuntimeCompile(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	t.Parallel()
	corpus := loadRxCorpus(t, false)
	var body bytes.Buffer
	const chunk = 100
	calls := 0
	for _, pattern := range corpus.patterns {
		for mode, names := range rxModes {
			var err error
			if mode == 0 {
				_, err = regexp.Compile(pattern)
			} else {
				_, err = regexp.CompilePOSIX(pattern)
			}
			if err != nil {
				continue
			}
			if calls%chunk == 0 {
				if calls > 0 {
					body.WriteString("}\n")
				}
				fmt.Fprintf(&body, "\nfunc chunk%d() {\n", calls/chunk)
			}
			fmt.Fprintf(&body, "\tuse(regexp.%s(%s))\n", names.must, strconv.Quote(pattern))
			calls++
		}
	}
	body.WriteString("}\n")
	t.Logf("%d constant calls", calls)
	// syntax.Parse inlines into regexp.compile, so its body syntax.parse is
	// the name the linker keeps.
	forbidden := []string{"regexp.compile", "regexp/syntax.parse", "regexp/syntax.Compile", "regexp.compileOnePass"}
	for _, variable := range []bool{false, true} {
		dir := t.TempDir()
		var src bytes.Buffer
		src.WriteString("package main\n\nimport (\n\t\"os\"\n\t\"regexp\"\n)\n\nvar total int\n\n")
		src.WriteString("func use(re *regexp.Regexp) {\n\tif re.MatchString(\"abc\") {\n\t\ttotal++\n\t}\n}\n")
		src.Write(body.Bytes())
		src.WriteString("\nfunc main() {\n")
		for idx := range (calls + chunk - 1) / chunk {
			fmt.Fprintf(&src, "\tchunk%d()\n", idx)
		}
		if variable {
			src.WriteString("\tre, _ := regexp.Compile(os.Args[0])\n\tuse(re)\n")
		}
		src.WriteString("\tos.Exit(total & 1)\n}\n")
		if err := os.WriteFile(filepath.Join(dir, "main.go"), src.Bytes(), 0o666); err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(dir, "main.exe")
		build := testenv.Command(t, testenv.GoToolPath(t), "build", "-o", exe, filepath.Join(dir, "main.go"))
		build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
		out, err := testenv.Command(t, testenv.GoToolPath(t), "tool", "nm", exe).CombinedOutput()
		if err != nil {
			t.Fatalf("nm: %v\n%s", err, out)
		}
		var present []string
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			for _, name := range forbidden {
				if fields[2] == name {
					present = append(present, name)
				}
			}
		}
		switch {
		case !variable && len(present) > 0:
			t.Errorf("a program with only constant patterns links %v", present)
		case variable && len(present) != len(forbidden):
			t.Errorf("a program with a variable pattern links %v, want all of %v", present, forbidden)
		}
	}
}

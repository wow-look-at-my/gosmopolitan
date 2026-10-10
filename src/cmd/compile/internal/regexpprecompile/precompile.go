// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

// Package regexpprecompile compiles constant regexp patterns at build time.
//
// The pass finds each call to regexp.Compile, CompilePOSIX, MustCompile
// and MustCompilePOSIX whose pattern the compiler can resolve. It runs the
// regexp package that is linked into the compiler on that pattern, and it
// writes the finished *regexp.Regexp out as read-only data. The call then
// becomes a call to an unexported regexp function that copies that data.
// No program built by this compiler parses or compiles such a pattern.
//
// The data walk is generic: it copies every field of the object graph that
// regexp.Compile returns, so no pattern and no regexp feature is special.
// The compiler's regexp and the target's regexp are the same source, and a
// struct layout that differs between them stops the build.
//
// The pass runs in three stages, and each stage is a phase of -bench: scan
// finds the calls, compile runs the compiler's regexp on each pattern, and
// emit writes the data and rewrites the calls. -d=regexpprecompile prints
// each site.
package regexpprecompile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/constant"
	"internal/buildcfg"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/staticdata"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/src"
)

// entry describes one regexp function that the pass rewrites.
type entry struct {
	posix  bool   // the call uses POSIX syntax and leftmost-longest matching
	must   bool   // the call panics when the pattern is invalid
	helper string // the regexp function that copies the template
}

var entries = map[string]entry{
	"Compile":          {posix: false, must: false, helper: "precompiledErr"},
	"CompilePOSIX":     {posix: true, must: false, helper: "precompiledErr"},
	"MustCompile":      {posix: false, must: true, helper: "precompiled"},
	"MustCompilePOSIX": {posix: true, must: true, helper: "precompiled"},
}

// patternKey names one pattern in one syntax.
type patternKey struct {
	posix   bool
	pattern string
}

// result holds the compile and the emit of one patternKey.
type result struct {
	compiled *regexp.Regexp
	err      error
	took     time.Duration // the time of the compiler's regexp compile
	tmpl     *obj.LSym     // the template, which the first emit sets
	size     int64         // bytes of read-only data that the emit wrote
}

// site is one call with a resolved pattern.
type site struct {
	call   *ir.CallExpr
	callee *ir.Name
	ent    entry
	key    patternKey
}

type pass struct {
	reported map[string]bool // diagnostics that are already reported
	sites    []site
	results  map[patternKey]*result
	order    []patternKey // the keys of results, in the order of the scan

	calls     int64 // calls to the four functions
	dynamic   int64 // calls with a pattern that is not constant
	warned    int64 // performance warnings
	rewritten int64 // calls that now copy a template
	bytes     int64 // read-only data that the emit wrote
	syms      int64 // symbols that the emit declared
}

// Package rewrites the calls in every function of pkg. It runs after
// inlining, so a pattern that only becomes constant in an inlined body is
// precompiled too.
func Package(pkg *ir.Package) {
	base.Timer.Start("fe", "regexp-precompile", "scan")
	// The bootstrap compiler links the bootstrap toolchain's regexp, which
	// is not the source of the regexp that it compiles.
	if base.CompilerBootstrap {
		return
	}
	pass := &pass{reported: map[string]bool{}, results: map[patternKey]*result{}}
	for _, function := range pkg.Funcs {
		ir.Visit(function, pass.scan)
	}
	base.Timer.AddEvent(pass.calls, "calls")
	base.Timer.AddEvent(pass.dynamic, "dynamic")
	base.Timer.AddEvent(pass.warned, "warnings")

	base.Timer.Start("fe", "regexp-precompile", "compile")
	pass.compilePatterns()
	base.Timer.AddEvent(int64(len(pass.order)), "patterns")

	base.Timer.Start("fe", "regexp-precompile", "emit")
	pass.emitSites()
	base.Timer.AddEvent(pass.rewritten, "sites")
	base.Timer.AddEvent(pass.bytes, "B")
	base.Timer.AddEvent(pass.syms, "syms")
	base.ExitIfErrors()
}

// scan records each call to one of the four functions.
func (pass *pass) scan(node ir.Node) {
	call, isCall := node.(*ir.CallExpr)
	if !isCall || call.Op() != ir.OCALLFUNC || len(call.Args) != 1 || call.IsDDD {
		return
	}
	callee := ir.StaticCalleeName(ir.StaticValue(call.Fun))
	if callee == nil || callee.Sym().Pkg.Path != "regexp" {
		return
	}
	ent, rewrites := entries[callee.Sym().Name]
	if !rewrites {
		return
	}
	pass.calls++
	pattern, resolved := constString(call.Args[0])
	if !resolved {
		pass.dynamic++
		pass.debugDynamic(call.Pos(), callee.Sym().Name, dynamicReason(call.Args[0]))
		if ent.must {
			pass.warnDynamic(call.Pos(), callee.Sym().Name)
		}
		return
	}
	key := patternKey{ent.posix, pattern}
	if pass.results[key] == nil {
		pass.results[key] = &result{}
		pass.order = append(pass.order, key)
	}
	pass.sites = append(pass.sites, site{call, callee, ent, key})
}

// compilePatterns compiles each distinct pattern once with the compiler's
// own regexp package.
func (pass *pass) compilePatterns() {
	for _, key := range pass.order {
		res := pass.results[key]
		start := time.Now()
		if key.posix {
			res.compiled, res.err = regexp.CompilePOSIX(key.pattern)
		} else {
			res.compiled, res.err = regexp.Compile(key.pattern)
		}
		res.took = time.Since(start)
	}
}

// emitSites writes the template of each valid pattern once and rewrites
// each call to copy it.
func (pass *pass) emitSites() {
	for _, cur := range pass.sites {
		res := pass.results[cur.key]
		name := cur.callee.Sym().Name
		pos := cur.call.Pos()
		if res.err != nil {
			if cur.ent.must {
				pass.report("error", pos, func() {
					base.ErrorfAt(pos, 0, "regexp.%s(%s) always panics: %v", name, quote(cur.key.pattern), res.err)
				})
			} else {
				// Compile returns the error at run time, which the program may expect.
				pass.debugDynamic(pos, name, "the pattern is invalid, and Compile returns the error at run time")
			}
			continue
		}
		reType := cur.callee.Type().Result(0).Type
		shared := res.tmpl != nil
		if !shared {
			emit := template(pos, cur.key, res.compiled, reType.Elem())
			res.tmpl, res.size = emit.root, emit.size
			pass.bytes += emit.size
			pass.syms += int64(emit.syms)
		}
		pass.rewritten++
		if base.Debug.RegexpPrecompile >= 1 {
			note := ""
			if shared {
				note = ", shared with an earlier site"
			}
			pass.report("precompiled", pos, func() {
				base.WarnfAt(pos, "regexp precompile: regexp.%s(%s) precompiled: compile %v, %d B read-only data%s", name, quote(cur.key.pattern), res.took, res.size, note)
			})
		}
		cur.call.Fun = helper(cur.ent.helper, reType, cur.callee.Type())
		cur.call.Args = []ir.Node{typecheck.LinksymAddr(pos, res.tmpl, reType.Elem()), cur.call.Args[0]}
	}
}

// warnDynamic reports a pattern that compiles at run time. Code of this
// tree and an inlined body from another function get no warning. The first
// is not the user's code, and the second has its own. A test package of the
// standard library and a program of the test directory have no -std flag,
// so their GOROOT path identifies them.
func (pass *pass) warnDynamic(pos src.XPos, name string) {
	inner := base.Ctxt.InnermostPos(pos).Base()
	if base.Flag.Std || inner.InliningIndex() >= 0 || inGOROOT(inner.Filename()) {
		return
	}
	pass.report("warning", pos, func() {
		pass.warned++
		base.WarnfAt(pos, "performance warning: the pattern of regexp.%s is not constant, so it compiles at run time", name)
	})
}

// debugDynamic prints a site that stays dynamic, for -d=regexpprecompile=2.
func (pass *pass) debugDynamic(pos src.XPos, name, reason string) {
	if base.Debug.RegexpPrecompile < 2 {
		return
	}
	pass.report("dynamic", pos, func() {
		base.WarnfAt(pos, "regexp precompile: regexp.%s stays dynamic: %s", name, reason)
	})
}

// dynamicReason tells why the compiler cannot resolve the pattern node.
func dynamicReason(node ir.Node) string {
	node = ir.StaticValue(node)
	switch node.Op() {
	case ir.ONAME:
		name := node.(*ir.Name)
		switch name.Class {
		case ir.PPARAM:
			return fmt.Sprintf("the pattern is the parameter %v", name.Sym())
		case ir.PEXTERN:
			return fmt.Sprintf("the pattern is the package variable %v, which any code can assign", name.Sym())
		case ir.PAUTO:
			return fmt.Sprintf("the pattern is the local variable %v, which has more than one assignment or has its address taken", name.Sym())
		}
		return fmt.Sprintf("the pattern is the variable %v", name.Sym())
	case ir.OCALLFUNC, ir.OCALLINTER, ir.OCALLMETH:
		return "the pattern is the result of a call that the compiler did not inline"
	case ir.OADDSTR:
		for _, part := range node.(*ir.AddStringExpr).List {
			if _, resolved := constString(part); !resolved {
				return "a part of the concatenation is not constant: " + dynamicReason(part)
			}
		}
	}
	return fmt.Sprintf("the compiler cannot resolve a %v expression", node.Op())
}

// inGOROOT reports whether file is a source file of this tree.
func inGOROOT(file string) bool {
	root := goroot()
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, file)
	return err == nil && filepath.IsLocal(rel)
}

// goroot returns the tree this compiler belongs to. The go command passes
// GOROOT in the environment. A tool that runs the compiler itself may not,
// and a release toolchain is built with -trimpath, so it records none. The
// executable's own place in the tree answers then.
var goroot = sync.OnceValue(func() string {
	if buildcfg.GOROOT != "" {
		return buildcfg.GOROOT
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return ""
	}
	// The executable is GOROOT/bin/go or GOROOT/pkg/tool/GOOS_GOARCH/compile.
	dir := filepath.Dir(exe)
	for range 3 {
		if info, err := os.Stat(filepath.Join(dir, "src", "runtime")); err == nil && info.IsDir() {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
})

// report runs diagnose once per kind of diagnostic and source position.
func (pass *pass) report(kind string, pos src.XPos, diagnose func()) {
	key := kind + "\x00" + base.FmtPos(pos)
	if pass.reported[key] {
		return
	}
	pass.reported[key] = true
	diagnose()
}

// constString returns the value of a string expression that the compiler
// can resolve: a constant, a local variable with one constant assignment,
// the result of an inlined call, or a concatenation of these.
func constString(node ir.Node) (string, bool) {
	node = ir.StaticValue(node)
	switch node.Op() {
	case ir.OLITERAL:
		if val := node.Val(); val.Kind() == constant.String {
			return constant.StringVal(val), true
		}
	case ir.OADDSTR:
		var joined strings.Builder
		for _, part := range node.(*ir.AddStringExpr).List {
			str, resolved := constString(part)
			if !resolved {
				return "", false
			}
			joined.WriteString(str)
		}
		return joined.String(), true
	}
	return "", false
}

// quote spells a pattern the way regexp.MustCompile spells it in its panic.
func quote(str string) string {
	if strconv.CanBackquote(str) {
		return "`" + str + "`"
	}
	return strconv.Quote(str)
}

var helpers = map[string]*ir.Name{}

// helper returns the regexp function name that copies a template. The
// signature takes the template and the pattern argument, and it returns what
// the callee returns.
func helper(name string, reType, orig *types.Type) *ir.Name {
	if cached := helpers[name]; cached != nil {
		return cached
	}
	pkg := types.NewPkg("go.regexp", "regexp")
	pkg.Prefix = "regexp"
	params := []*types.Field{
		types.NewField(src.NoXPos, nil, reType),
		types.NewField(src.NoXPos, nil, types.Types[types.TSTRING]),
	}
	var results []*types.Field
	for _, res := range orig.Results() {
		results = append(results, types.NewField(src.NoXPos, nil, res.Type))
	}
	function := ir.NewFunc(src.NoXPos, src.NoXPos, pkg.Lookup(name), types.NewSignature(nil, params, results))
	helpers[name] = function.Nname
	return function.Nname
}

// template writes compiled as read-only data of type reType. Every package
// that uses the same pattern writes the same symbols, and the linker keeps
// one.
func template(pos src.XPos, key patternKey, compiled *regexp.Regexp, reType *types.Type) *emitter {
	mode := "perl"
	if key.posix {
		mode = "posix"
	}
	sum := sha256.Sum256([]byte(mode + "\x00" + key.pattern))
	emit := &emitter{
		pos:    pos,
		prefix: "regexp..precompiled." + hex.EncodeToString(sum[:16]),
		objs:   map[objKey]*obj.LSym{},
		texts:  map[string]bool{},
	}
	emit.root = base.Ctxt.Lookup(emit.prefix)
	if !emit.root.OnList() {
		emit.write(emit.root, reType, 1, reflect.ValueOf(compiled))
	}
	return emit
}

// objKey identifies one object of the compiler's regexp value.
type objKey struct {
	addr  uintptr
	count int
	elem  *types.Type
}

// emitter writes one regexp value as linker symbols.
type emitter struct {
	pos    src.XPos
	prefix string
	serial int
	objs   map[objKey]*obj.LSym
	root   *obj.LSym
	texts  map[string]bool // string contents that size already counts
	size   int64           // bytes of the symbols and of the string contents
	syms   int             // symbols that write declared
}

// object returns the symbol for count elements of type elem that start at
// the address of host. host is a pointer or a slice value.
func (emit *emitter) object(elem *types.Type, count int, host reflect.Value) *obj.LSym {
	key := objKey{host.Pointer(), count, elem}
	if lsym := emit.objs[key]; lsym != nil {
		return lsym
	}
	if elem.Size() == 0 || count == 0 {
		return ir.Syms.Zerobase
	}
	emit.serial++
	lsym := base.Ctxt.Lookup(fmt.Sprintf("%s.%d", emit.prefix, emit.serial))
	emit.objs[key] = lsym
	emit.write(lsym, elem, count, host)
	return lsym
}

// write fills lsym with count elements of type elem from host and declares
// it. host is a pointer to one element or a slice of count elements.
func (emit *emitter) write(lsym *obj.LSym, elem *types.Type, count int, host reflect.Value) {
	types.CalcSize(elem)
	for idx := range count {
		var val reflect.Value
		if host.Kind() == reflect.Pointer {
			val = host.Elem()
		} else {
			val = host.Index(idx)
		}
		emit.value(lsym, int64(idx)*elem.Size(), elem, val)
	}
	objw.Global(lsym, int32(elem.Size()*int64(count)), obj.DUPOK|obj.RODATA|obj.LOCAL)
	lsym.Align = int16(elem.Alignment())
	emit.size += elem.Size() * int64(count)
	emit.syms++
}

// value writes host at offset off of lsym as a value of type typ.
func (emit *emitter) value(lsym *obj.LSym, off int64, typ *types.Type, host reflect.Value) {
	switch {
	case typ.IsBoolean():
		if host.Kind() != reflect.Bool {
			emit.mismatch(typ, host)
		}
		if host.Bool() {
			objw.Uint8(lsym, int(off), 1)
		}
	case typ.IsInteger():
		emit.integer(lsym, off, typ, host)
	case typ.IsString():
		if host.Kind() != reflect.String {
			emit.mismatch(typ, host)
		}
		if str := host.String(); str != "" {
			objw.SymPtr(lsym, int(off), staticdata.StringSym(emit.pos, str), 0)
			objw.Uintptr(lsym, int(off)+types.PtrSize, uint64(len(str)))
			if !emit.texts[str] {
				emit.texts[str] = true
				emit.size += int64(len(str))
			}
		}
	case typ.IsPtr():
		if host.Kind() != reflect.Pointer {
			emit.mismatch(typ, host)
		}
		if !host.IsNil() {
			objw.SymPtr(lsym, int(off), emit.object(typ.Elem(), 1, host), 0)
		}
	case typ.IsSlice():
		if host.Kind() != reflect.Slice {
			emit.mismatch(typ, host)
		}
		if !host.IsNil() {
			full := host.Slice(0, host.Cap())
			objw.SymPtr(lsym, int(off), emit.object(typ.Elem(), full.Len(), full), 0)
			objw.Uintptr(lsym, int(off)+types.PtrSize, uint64(host.Len()))
			objw.Uintptr(lsym, int(off)+2*types.PtrSize, uint64(host.Cap()))
		}
	case typ.IsStruct():
		if host.Kind() != reflect.Struct || host.NumField() != typ.NumFields() {
			emit.mismatch(typ, host)
		}
		for idx, field := range typ.Fields() {
			if host.Type().Field(idx).Name != field.Sym.Name {
				emit.mismatch(typ, host)
			}
			emit.value(lsym, off+field.Offset, field.Type, host.Field(idx))
		}
	case typ.IsArray():
		if host.Kind() != reflect.Array || int64(host.Len()) != typ.NumElem() {
			emit.mismatch(typ, host)
		}
		for idx := range host.Len() {
			emit.value(lsym, off+int64(idx)*typ.Elem().Size(), typ.Elem(), host.Index(idx))
		}
	default:
		emit.mismatch(typ, host)
	}
}

// integer writes an integer field. The target's size can differ from the
// host's, so the value must fit.
func (emit *emitter) integer(lsym *obj.LSym, off int64, typ *types.Type, host reflect.Value) {
	bits := uint(typ.Size() * 8)
	var raw uint64
	switch host.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		num := host.Int()
		if !typ.IsSigned() || (bits < 64 && (num < -1<<(bits-1) || num >= 1<<(bits-1))) {
			emit.mismatch(typ, host)
		}
		raw = uint64(num)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		num := host.Uint()
		if typ.IsSigned() || (bits < 64 && num >= 1<<bits) {
			emit.mismatch(typ, host)
		}
		raw = num
	default:
		emit.mismatch(typ, host)
	}
	if raw != 0 {
		objw.UintN(lsym, int(off), raw, int(typ.Size()))
	}
}

// mismatch stops the build. The compiler's regexp and the target's regexp
// disagree, so the compiler cannot build a Regexp for this target.
func (emit *emitter) mismatch(typ *types.Type, host reflect.Value) {
	base.FatalfAt(emit.pos, "regexp precompile: compiler's %v does not match target's %v; rebuild the toolchain from this tree", host.Type(), typ)
}

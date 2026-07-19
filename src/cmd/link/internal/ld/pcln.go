// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/goobj"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"cmd/link/internal/loader"
	"cmd/link/internal/sym"
	"cmp"
	"fmt"
	"internal/abi"
	"internal/buildcfg"
	"math/bits"
	"path/filepath"
	"slices"
	"strings"
)

// funcSize is the size of the fixed part of the _func object in
// runtime/runtime2.go. The record continues with the presence-bitmap
// encoded pcdata and funcdata offset arrays (see funcShape).
const funcSize = 10 * 4

// funcShape describes the encoded shape of a _func record: which pcdata
// tables and funcdata slots have offset entries following the fixed part
// of the record. Bit i of pcMask is set iff pcdata table i is present;
// bit j of fdMask is set iff funcdata slot j is present. Only present
// entries are written, in increasing index order, pcdata array first.
//
// Keep in sync with runtime/runtime2.go:_func and
// runtime/symtab.go:pcdatastart,funcdata.
type funcShape struct {
	pcMask uint8
	fdMask uint8
}

// dataBytes returns the total size in bytes of the variable-length
// pcdata/funcdata offset arrays of a record with this shape.
func (sh funcShape) dataBytes() int64 {
	return int64(bits.OnesCount8(sh.pcMask)+bits.OnesCount8(sh.fdMask)) * 4
}

// funcPcdataOffsets returns the pctab offsets of the pcdata tables of s,
// indexed by pcdata table index, together with the presence bitmap of the
// nonzero offsets. An offset of 0 means "no table" (pctab offset 0 is
// reserved by generatePctab), and is encoded as an absent slot.
//
// pcinline and pcdata must come from ldr.PcdataAuxs(s), and generatePctab
// must already have assigned the pctab offsets as the symbol values.
func funcPcdataOffsets(ldr *loader.Loader, s loader.Sym, fi loader.FuncInfo, pcinline loader.Sym, pcdata []loader.Sym) (offs [8]uint32, mask uint8) {
	n := numPCData(ldr, s, fi)
	if n > 8 {
		panic(fmt.Sprintf("%s: too many pcdata tables for the presence bitmap: %d", ldr.SymName(s), n))
	}
	for j, pcSym := range pcdata {
		offs[j] = uint32(ldr.SymValue(pcSym))
	}
	if fi.NumInlTree() > 0 {
		offs[abi.PCDATA_InlTreeIndex] = uint32(ldr.SymValue(pcinline))
	}
	for j := uint32(0); j < n; j++ {
		if offs[j] != 0 {
			mask |= 1 << j
		}
	}
	return offs, mask
}

// funcFuncdataMask returns the presence bitmap of the funcdata slots of s.
// A slot is present iff it has a real funcdata symbol (see ignoreFuncData);
// funcdata must come from funcData(ldr, s, fi, inlSym, ...).
func funcFuncdataMask(ldr *loader.Loader, s loader.Sym, funcdata []loader.Sym) (mask uint8) {
	if len(funcdata) > 8 {
		panic(fmt.Sprintf("%s: too many funcdata slots for the presence bitmap: %d", ldr.SymName(s), len(funcdata)))
	}
	for j, fdSym := range funcdata {
		if !ignoreFuncData(ldr, s, j, fdSym) {
			mask |= 1 << uint(j)
		}
	}
	return mask
}

// pclntab holds the state needed for pclntab generation.
type pclntab struct {
	// The first and last functions found.
	firstFunc, lastFunc loader.Sym

	// Running total size of pclntab.
	size int64

	// runtime.pclntab's symbols
	carrier     loader.Sym
	pclntab     loader.Sym
	pcheader    loader.Sym
	funcnametab loader.Sym
	findfunctab loader.Sym
	cutab       loader.Sym
	filetab     loader.Sym
	pctab       loader.Sym
	funcdata    loader.Sym

	// The number of functions + number of TEXT sections - 1. This is such an
	// unexpected value because platforms that have more than one TEXT section
	// get a dummy function inserted between because the external linker can place
	// functions in those areas. We mark those areas as not covered by the Go
	// runtime.
	//
	// On most platforms this is the number of reachable functions.
	nfunc int32

	// The number of filenames in runtime.filetab.
	nfiles uint32
}

// addGeneratedSym adds a generator symbol to pclntab, returning the new Sym.
// It is the caller's responsibility to save the symbol in state.
func (state *pclntab) addGeneratedSym(ctxt *Link, name string, size int64, align int32, f generatorFunc) loader.Sym {
	size = Rnd(size, int64(ctxt.Arch.PtrSize))
	state.size += size
	s := ctxt.createGeneratorSymbol(name, 0, sym.SPCLNTAB, size, f)
	ldr := ctxt.loader
	ldr.SetSymAlign(s, align)
	ldr.SetAttrReachable(s, true)
	ldr.SetCarrierSym(s, state.carrier)
	ldr.SetAttrNotInSymbolTable(s, true)

	if align > ldr.SymAlign(state.carrier) {
		ldr.SetSymAlign(state.carrier, align)
	}

	return s
}

// makePclntab makes a pclntab object, and assembles all the compilation units
// we'll need to write pclntab. Returns the pclntab structure, a slice of the
// CompilationUnits we need, and a slice of the function symbols we need to
// generate pclntab.
func makePclntab(ctxt *Link, container loader.Bitmap) (*pclntab, []*sym.CompilationUnit, []loader.Sym) {
	ldr := ctxt.loader
	state := new(pclntab)

	// Gather some basic stats and info.
	seenCUs := make(map[*sym.CompilationUnit]struct{})
	compUnits := []*sym.CompilationUnit{}
	funcs := []loader.Sym{}

	for _, s := range ctxt.Textp {
		if !emitPcln(ctxt, s, container) {
			continue
		}
		funcs = append(funcs, s)
		state.nfunc++
		if state.firstFunc == 0 {
			state.firstFunc = s
		}
		state.lastFunc = s

		// We need to keep track of all compilation units we see. Some symbols
		// (eg, go.buildid, _cgoexp_, etc) won't have a compilation unit.
		cu := ldr.SymUnit(s)
		if _, ok := seenCUs[cu]; cu != nil && !ok {
			seenCUs[cu] = struct{}{}
			cu.PclnIndex = len(compUnits)
			compUnits = append(compUnits, cu)
		}
	}
	return state, compUnits, funcs
}

func emitPcln(ctxt *Link, s loader.Sym, container loader.Bitmap) bool {
	if ctxt.Target.IsRISCV64() {
		// Avoid adding local symbols to the pcln table - RISC-V
		// linking generates a very large number of these, particularly
		// for HI20 symbols (which we need to load in order to be able
		// to resolve relocations). Unnecessarily including all of
		// these symbols quickly blows out the size of the pcln table
		// and overflows hash buckets.
		symName := ctxt.loader.SymName(s)
		if symName == "" || strings.HasPrefix(symName, ".L") {
			return false
		}
	}

	// We want to generate func table entries only for the "lowest
	// level" symbols, not containers of subsymbols.
	return !container.Has(s)
}

func computeDeferReturn(ctxt *Link, deferReturnSym, s loader.Sym) uint32 {
	ldr := ctxt.loader
	target := ctxt.Target
	deferreturn := uint32(0)
	lastWasmAddr := uint32(0)

	relocs := ldr.Relocs(s)
	for ri := 0; ri < relocs.Count(); ri++ {
		r := relocs.At(ri)
		if target.IsWasm() && r.Type() == objabi.R_ADDR {
			// wasm/ssa.go generates an ARESUMEPOINT just
			// before the deferreturn call. The "PC" of
			// the deferreturn call is stored in the
			// R_ADDR relocation on the ARESUMEPOINT.
			lastWasmAddr = uint32(r.Add())
		}
		if r.Type().IsDirectCall() && (r.Sym() == deferReturnSym || ldr.IsDeferReturnTramp(r.Sym())) {
			if target.IsWasm() {
				deferreturn = lastWasmAddr - 1
			} else {
				// Note: the relocation target is in the call instruction, but
				// is not necessarily the whole instruction (for instance, on
				// x86 the relocation applies to bytes [1:5] of the 5 byte call
				// instruction).
				deferreturn = uint32(r.Off())
				switch target.Arch.Family {
				case sys.I386:
					deferreturn--
					if ctxt.BuildMode == BuildModeShared || ctxt.linkShared || ctxt.BuildMode == BuildModePlugin {
						// In this mode, we need to get the address from GOT,
						// with two additional instructions like
						//
						// CALL    __x86.get_pc_thunk.bx(SB)       // 5 bytes
						// LEAL    _GLOBAL_OFFSET_TABLE_<>(BX), BX // 6 bytes
						//
						// We need to back off to the get_pc_thunk call.
						// (See progedit in cmd/internal/obj/x86/obj6.go)
						deferreturn -= 11
					}
				case sys.AMD64:
					deferreturn--

				case sys.ARM, sys.ARM64, sys.Loong64, sys.MIPS, sys.MIPS64, sys.PPC64, sys.RISCV64:
					// no change
				case sys.S390X:
					deferreturn -= 2
				default:
					panic(fmt.Sprint("Unhandled architecture:", target.Arch.Family))
				}
			}
			break // only need one
		}
	}
	return deferreturn
}

// genInlTreeSym generates the InlTree sym for a function with the
// specified FuncInfo.
func genInlTreeSym(ctxt *Link, cu *sym.CompilationUnit, fi loader.FuncInfo, arch *sys.Arch, nameOffsets map[loader.Sym]uint32) loader.Sym {
	ldr := ctxt.loader
	its := ldr.CreateExtSym("", 0)
	inlTreeSym := ldr.MakeSymbolUpdater(its)
	// Note: the generated symbol is given a type of sym.SGOFUNC, as a
	// signal to the symtab() phase that it needs to be grouped in with
	// other similar symbols (gcdata, etc); the dodata() phase will
	// eventually switch the type back to SRODATA.
	inlTreeSym.SetType(sym.SPCLNTAB)
	ldr.SetAttrReachable(its, true)
	ldr.SetSymAlign(its, 4) // it has 32-bit fields
	ninl := fi.NumInlTree()
	for i := 0; i < int(ninl); i++ {
		call := fi.InlTree(i)
		nameOff, ok := nameOffsets[call.Func]
		if !ok {
			panic("couldn't find function name offset")
		}

		inlFunc := ldr.FuncInfo(call.Func)
		var funcID abi.FuncID
		startLine := int32(0)
		if inlFunc.Valid() {
			funcID = inlFunc.FuncID()
			startLine = inlFunc.StartLine()
		} else if !ctxt.linkShared {
			// Inlined functions are always Go functions, and thus
			// must have FuncInfo.
			//
			// Unfortunately, with -linkshared, the inlined
			// function may be external symbols (from another
			// shared library), and we don't load FuncInfo from the
			// shared library. We will report potentially incorrect
			// FuncID in this case. See https://go.dev/issue/55954.
			panic(fmt.Sprintf("inlined function %s missing func info", ldr.SymName(call.Func)))
		}

		// Construct runtime.inlinedCall value.
		const size = 16
		inlTreeSym.SetUint8(arch, int64(i*size+0), uint8(funcID))
		// Bytes 1-3 are unused.
		inlTreeSym.SetUint32(arch, int64(i*size+4), nameOff)
		inlTreeSym.SetUint32(arch, int64(i*size+8), uint32(call.ParentPC))
		inlTreeSym.SetUint32(arch, int64(i*size+12), uint32(startLine))
	}
	return its
}

// makeInlSyms returns a map of loader.Sym that are created inlSyms.
func makeInlSyms(ctxt *Link, funcs []loader.Sym, nameOffsets map[loader.Sym]uint32) map[loader.Sym]loader.Sym {
	ldr := ctxt.loader
	// Create the inline symbols we need.
	inlSyms := make(map[loader.Sym]loader.Sym)
	for _, s := range funcs {
		if fi := ldr.FuncInfo(s); fi.Valid() {
			fi.Preload()
			if fi.NumInlTree() > 0 {
				inlSyms[s] = genInlTreeSym(ctxt, ldr.SymUnit(s), fi, ctxt.Arch, nameOffsets)
			}
		}
	}
	return inlSyms
}

// generatePCHeader creates the runtime.pcheader symbol, setting it up as a
// generator to fill in its data later.
func (state *pclntab) generatePCHeader(ctxt *Link) {
	ldr := ctxt.loader
	size := int64(8 + 8*ctxt.Arch.PtrSize)
	writeHeader := func(ctxt *Link, s loader.Sym) {
		header := ctxt.loader.MakeSymbolUpdater(s)

		writeSymOffset := func(off int64, ws loader.Sym) int64 {
			diff := ldr.SymValue(ws) - ldr.SymValue(s)
			if diff <= 0 {
				name := ldr.SymName(ws)
				panic(fmt.Sprintf("expected runtime.pcheader(%x) to be placed before %s(%x)", ldr.SymValue(s), name, ldr.SymValue(ws)))
			}
			return header.SetUintptr(ctxt.Arch, off, uintptr(diff))
		}

		// Write header.
		// Keep in sync with runtime/symtab.go:pcHeader and package debug/gosym.
		header.SetUint32(ctxt.Arch, 0, uint32(abi.CurrentPCLnTabMagic))
		header.SetUint8(ctxt.Arch, 6, uint8(ctxt.Arch.MinLC))
		header.SetUint8(ctxt.Arch, 7, uint8(ctxt.Arch.PtrSize))
		off := header.SetUint(ctxt.Arch, 8, uint64(state.nfunc))
		off = header.SetUint(ctxt.Arch, off, uint64(state.nfiles))
		off = header.SetUintptr(ctxt.Arch, off, 0) // unused
		off = writeSymOffset(off, state.funcnametab)
		off = writeSymOffset(off, state.cutab)
		off = writeSymOffset(off, state.filetab)
		off = writeSymOffset(off, state.pctab)
		off = writeSymOffset(off, state.pclntab)
		if off != size {
			panic(fmt.Sprintf("pcHeader size: %d != %d", off, size))
		}
	}

	state.pcheader = state.addGeneratedSym(ctxt, "runtime.pcheader", size, int32(ctxt.Arch.PtrSize), writeHeader)
}

// walkFuncs iterates over the funcs, calling a function for each unique
// function and inlined function.
func walkFuncs(ctxt *Link, funcs []loader.Sym, f func(loader.Sym)) {
	ldr := ctxt.loader
	seen := make(map[loader.Sym]struct{})
	for _, s := range funcs {
		if _, ok := seen[s]; !ok {
			f(s)
			seen[s] = struct{}{}
		}

		fi := ldr.FuncInfo(s)
		if !fi.Valid() {
			continue
		}
		fi.Preload()
		for i, ni := 0, fi.NumInlTree(); i < int(ni); i++ {
			call := fi.InlTree(i).Func
			if _, ok := seen[call]; !ok {
				f(call)
				seen[call] = struct{}{}
			}
		}
	}
}

// splitFuncName splits a function name into a shared package-path prefix
// and a per-name suffix, at the first '.' that follows the last '/' before
// any '['. The prefix is either empty or ends in '.', and prefix+suffix is
// always exactly the original name. Keeping the split before any '['
// guarantees generic shape brackets live entirely in the suffix, which
// runtime.funcNamePiecesForPrint relies on.
func splitFuncName(name string) (prefix, suffix string) {
	end := strings.IndexByte(name, '[')
	if end < 0 {
		end = len(name)
	}
	slash := strings.LastIndexByte(name[:end], '/')
	dot := strings.IndexByte(name[slash+1:end], '.')
	if dot < 0 {
		return "", name
	}
	i := slash + 1 + dot + 1
	return name[:i], name[i:]
}

// uvarintLen returns the number of bytes the uvarint encoding of v takes.
func uvarintLen(v uint64) int64 {
	n := int64(1)
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// setUvarint writes v at off as a uvarint, returning the offset past it.
func setUvarint(sb *loader.SymbolBuilder, arch *sys.Arch, off int64, v uint64) int64 {
	for v >= 0x80 {
		off = sb.SetUint8(arch, off, byte(v)|0x80)
		v >>= 7
	}
	return sb.SetUint8(arch, off, byte(v))
}

// generateFuncnametab creates the function name table. Returns a map of
// func symbol to the name offset in runtime.funcnametab.
//
// The compact pclntab format (abi.CosmoPCLnTabMagic) shares package-path
// prefixes between names instead of storing every name whole:
//
//	uint32 nprefix
//	nprefix*uint32  offset of each NUL-terminated prefix string,
//	                relative to the start of the table
//	prefix strings  NUL-terminated, contiguous, in index order
//	name entries    uvarint(prefix index) ++ suffix ++ NUL
//
// A name offset (_func.nameOff, inlinedCall.nameOff) addresses one of the
// trailing entries; the full name is prefix+suffix (see splitFuncName).
// Prefix indexes are assigned by descending use count so the hottest
// prefixes get 1-byte uvarints. Entries follow the header, offset table
// and prefix strings, so offset 0 can never address a real entry,
// preserving the runtime's nameOff==0 "no name" sentinel.
//
// Keep in sync with runtime/symtab.go:(*moduledata).funcNamePieces and
// debug/gosym/pclntab.go:funcName (verCosmo).
func (state *pclntab) generateFuncnametab(ctxt *Link, funcs []loader.Sym) map[loader.Sym]uint32 {
	ldr := ctxt.loader
	nameOffsets := make(map[loader.Sym]uint32, state.nfunc)

	// Collect the unique function syms in walk order and count how often
	// each name prefix is used.
	type prefixInfo struct {
		idx   int // final prefix index
		count int
	}
	prefixes := make(map[string]*prefixInfo)
	var prefixList []string // first-seen order, then sorted by use count
	var walkSyms []loader.Sym
	walkFuncs(ctxt, funcs, func(s loader.Sym) {
		walkSyms = append(walkSyms, s)
		name := ldr.SymName(s)
		prefix, suffix := splitFuncName(name)
		if prefix+suffix != name || (prefix != "" && prefix[len(prefix)-1] != '.') {
			panic(fmt.Sprintf("bad function name split: %q -> %q + %q", name, prefix, suffix))
		}
		p := prefixes[prefix]
		if p == nil {
			p = &prefixInfo{}
			prefixes[prefix] = p
			prefixList = append(prefixList, prefix)
		}
		p.count++
	})

	// Assign prefix indexes by descending use count (ties broken by
	// first-seen order, keeping the layout deterministic) so the
	// most-referenced prefixes get the shortest uvarint encodings.
	slices.SortStableFunc(prefixList, func(a, b string) int {
		return cmp.Compare(prefixes[b].count, prefixes[a].count)
	})
	for i, p := range prefixList {
		prefixes[p].idx = i
	}

	// Lay out the table: header, prefix offset table, prefix strings, then
	// one entry per name.
	size := int64(4 + 4*len(prefixList))
	prefixOffs := make([]uint32, len(prefixList))
	for i, p := range prefixList {
		prefixOffs[i] = uint32(size)
		size += int64(len(p) + 1) // NULL terminate
	}
	for _, s := range walkSyms {
		prefix, suffix := splitFuncName(ldr.SymName(s))
		nameOffsets[s] = uint32(size)
		size += uvarintLen(uint64(prefixes[prefix].idx)) + int64(len(suffix)+1) // NULL terminate
	}

	writeFuncNameTab := func(ctxt *Link, s loader.Sym) {
		symtab := ctxt.loader.MakeSymbolUpdater(s)
		off := symtab.SetUint32(ctxt.Arch, 0, uint32(len(prefixList)))
		for i := range prefixList {
			off = symtab.SetUint32(ctxt.Arch, off, prefixOffs[i])
		}
		for i, p := range prefixList {
			symtab.AddCStringAt(int64(prefixOffs[i]), p)
		}
		for s, off := range nameOffsets {
			prefix, suffix := splitFuncName(ctxt.loader.SymName(s))
			end := setUvarint(symtab, ctxt.Arch, int64(off), uint64(prefixes[prefix].idx))
			symtab.AddCStringAt(end, suffix)
		}
	}

	state.funcnametab = state.addGeneratedSym(ctxt, "runtime.funcnametab", size, 4, writeFuncNameTab)
	return nameOffsets
}

// walkFilenames walks funcs, calling a function for each filename used in each
// function's line table.
func walkFilenames(ctxt *Link, funcs []loader.Sym, f func(*sym.CompilationUnit, goobj.CUFileIndex)) {
	ldr := ctxt.loader

	// Loop through all functions, finding the filenames we need.
	for _, s := range funcs {
		fi := ldr.FuncInfo(s)
		if !fi.Valid() {
			continue
		}
		fi.Preload()

		cu := ldr.SymUnit(s)
		for i, nf := 0, int(fi.NumFile()); i < nf; i++ {
			f(cu, fi.File(i))
		}
		for i, ninl := 0, int(fi.NumInlTree()); i < ninl; i++ {
			call := fi.InlTree(i)
			f(cu, call.File)
		}
	}
}

// splitFileName splits an expanded file name into a shared directory
// prefix and a base name, at the last '/'. The prefix is either empty or
// ends in '/', and prefix+base is always exactly the expanded name.
func splitFileName(file string) (dir, base string) {
	slash := strings.LastIndexByte(file, '/')
	return file[:slash+1], file[slash+1:]
}

// generateFilenameTabs creates LUTs needed for filename lookup. Returns a slice
// of the index at which each CU begins in runtime.cutab.
//
// Function objects keep track of the files they reference to print the stack.
// This function creates a per-CU list of filenames if CU[M] references
// files[1-N], the following is generated:
//
//	runtime.cutab:
//	  CU[M]
//	   offsetToFilename[0]
//	   offsetToFilename[1]
//	   ..
//
//	runtime.filetab
//	  uint32 ndir
//	  ndir*uint32   offset of each NUL-terminated directory string,
//	                relative to the start of the table
//	  dir strings   NUL-terminated, contiguous, in index order
//	  file entries  uvarint(dir index) ++ base name ++ NUL
//
// The compact pclntab format (abi.CosmoPCLnTabMagic) shares directory
// prefixes between file names: a cutab value addresses one of the trailing
// file entries, and the full name is dir+base (see splitFileName).
// Directory indexes are assigned by descending use count so the hottest
// directories get 1-byte uvarints. The entries start directly after the
// last directory string, which is how debug/gosym iterates them.
//
// Looking up a filename then becomes:
//  0. Given a func, and filename index [K]
//  1. Get Func.CUIndex:       M := func.cuOffset
//  2. Find the entry offset:  fileOffset := runtime.cutab[M+K]
//  3. Decode the entry:       dir, base at runtime.filetab[fileOffset]
//
// Keep in sync with runtime/symtab.go:funcfilePieces and
// debug/gosym/pclntab.go:fileString,initFileMap (verCosmo).
func (state *pclntab) generateFilenameTabs(ctxt *Link, compUnits []*sym.CompilationUnit, funcs []loader.Sym) []uint32 {
	// On a per-CU basis, keep track of all the filenames we need.
	//
	// Note, that we store the filenames in a separate section in the object
	// files, and deduplicate based on the actual value. It would be better to
	// store the filenames as symbols, using content addressable symbols (and
	// then not loading extra filenames), and just use the hash value of the
	// symbol name to do this cataloging.
	//
	// TODO: Store filenames as symbols. (Note this would be easiest if you
	// also move strings to ALWAYS using the larger content addressable hash
	// function, and use that hash value for uniqueness testing.)
	cuEntries := make([]goobj.CUFileIndex, len(compUnits))
	fileOffsets := make(map[string]uint32)

	// Walk the filenames, collecting the unique raw filenames in first-seen
	// order and the max file index we've seen per CU so we can calculate
	// how large the CU->global table needs to be.
	var fileList []string
	walkFilenames(ctxt, funcs, func(cu *sym.CompilationUnit, i goobj.CUFileIndex) {
		// Note we use the raw filename for lookup, but store the
		// expanded filename.
		filename := cu.FileTable[i]
		if _, ok := fileOffsets[filename]; !ok {
			fileOffsets[filename] = 0 // real offset assigned below
			fileList = append(fileList, filename)
		}

		// Find the maximum file index we've seen.
		if cuEntries[cu.PclnIndex] < i+1 {
			cuEntries[cu.PclnIndex] = i + 1 // Store max + 1
		}
	})

	// Collect the unique directories and count how often each is used.
	type dirInfo struct {
		idx   int // final directory index
		count int
	}
	dirs := make(map[string]*dirInfo)
	var dirList []string // first-seen order, then sorted by use count
	for _, filename := range fileList {
		expanded := expandFile(filename)
		dir, base := splitFileName(expanded)
		if dir+base != expanded || (dir != "" && dir[len(dir)-1] != '/') {
			panic(fmt.Sprintf("bad file name split: %q -> %q + %q", expanded, dir, base))
		}
		d := dirs[dir]
		if d == nil {
			d = &dirInfo{}
			dirs[dir] = d
			dirList = append(dirList, dir)
		}
		d.count++
	}

	// Assign directory indexes by descending use count (ties broken by
	// first-seen order, keeping the layout deterministic).
	slices.SortStableFunc(dirList, func(a, b string) int {
		return cmp.Compare(dirs[b].count, dirs[a].count)
	})
	for i, d := range dirList {
		dirs[d].idx = i
	}

	// Lay out the table: header, directory offset table, directory
	// strings, then one entry per file.
	fileSize := int64(4 + 4*len(dirList))
	dirOffs := make([]uint32, len(dirList))
	for i, d := range dirList {
		dirOffs[i] = uint32(fileSize)
		fileSize += int64(len(d) + 1) // NULL terminate
	}
	for _, filename := range fileList {
		dir, base := splitFileName(expandFile(filename))
		fileOffsets[filename] = uint32(fileSize)
		fileSize += uvarintLen(uint64(dirs[dir].idx)) + int64(len(base)+1) // NULL terminate
	}

	// Calculate the size of the runtime.cutab variable.
	var totalEntries uint32
	cuOffsets := make([]uint32, len(cuEntries))
	for i, entries := range cuEntries {
		// Note, cutab is a slice of uint32, so an offset to a cu's entry is just the
		// running total of all cu indices we've needed to store so far, not the
		// number of bytes we've stored so far.
		cuOffsets[i] = totalEntries
		totalEntries += uint32(entries)
	}

	// Write cutab.
	writeCutab := func(ctxt *Link, s loader.Sym) {
		sb := ctxt.loader.MakeSymbolUpdater(s)

		var off int64
		for i, max := range cuEntries {
			// Write the per CU LUT.
			cu := compUnits[i]
			for j := goobj.CUFileIndex(0); j < max; j++ {
				fileOffset, ok := fileOffsets[cu.FileTable[j]]
				if !ok {
					// We're looping through all possible file indices. It's possible a file's
					// been deadcode eliminated, and although it's a valid file in the CU, it's
					// not needed in this binary. When that happens, use an invalid offset.
					fileOffset = ^uint32(0)
				}
				off = sb.SetUint32(ctxt.Arch, off, fileOffset)
			}
		}
	}
	state.cutab = state.addGeneratedSym(ctxt, "runtime.cutab", int64(totalEntries*4), 4, writeCutab)

	// Write filetab.
	writeFiletab := func(ctxt *Link, s loader.Sym) {
		sb := ctxt.loader.MakeSymbolUpdater(s)

		off := sb.SetUint32(ctxt.Arch, 0, uint32(len(dirList)))
		for i := range dirList {
			off = sb.SetUint32(ctxt.Arch, off, dirOffs[i])
		}
		for i, d := range dirList {
			sb.AddCStringAt(int64(dirOffs[i]), d)
		}
		for filename, loc := range fileOffsets {
			dir, base := splitFileName(expandFile(filename))
			end := setUvarint(sb, ctxt.Arch, int64(loc), uint64(dirs[dir].idx))
			sb.AddCStringAt(end, base)
		}
	}
	state.nfiles = uint32(len(fileOffsets))
	state.filetab = state.addGeneratedSym(ctxt, "runtime.filetab", fileSize, 4, writeFiletab)

	return cuOffsets
}

// generatePctab creates the runtime.pctab variable, holding all the
// deduplicated pcdata.
func (state *pclntab) generatePctab(ctxt *Link, funcs []loader.Sym) {
	ldr := ctxt.loader

	// Pctab offsets of 0 are considered invalid in the runtime. We respect
	// that by just padding a single byte at the beginning of runtime.pctab,
	// that way no real offsets can be zero.
	size := int64(1)

	// Walk the functions, finding offset to store each pcdata.
	seen := make(map[loader.Sym]struct{})
	saveOffset := func(pcSym loader.Sym) {
		if _, ok := seen[pcSym]; !ok {
			datSize := ldr.SymSize(pcSym)
			if datSize != 0 {
				ldr.SetSymValue(pcSym, size)
			} else {
				// Invalid PC data, record as zero.
				ldr.SetSymValue(pcSym, 0)
			}
			size += datSize
			seen[pcSym] = struct{}{}
		}
	}
	var pcsp, pcline, pcfile, pcinline loader.Sym
	var pcdata []loader.Sym
	for _, s := range funcs {
		fi := ldr.FuncInfo(s)
		if !fi.Valid() {
			continue
		}
		fi.Preload()
		pcsp, pcfile, pcline, pcinline, pcdata = ldr.PcdataAuxs(s, pcdata)

		pcSyms := []loader.Sym{pcsp, pcfile, pcline}
		for _, pcSym := range pcSyms {
			saveOffset(pcSym)
		}
		for _, pcSym := range pcdata {
			saveOffset(pcSym)
		}
		if fi.NumInlTree() > 0 {
			saveOffset(pcinline)
		}
	}

	// TODO: There is no reason we need a generator for this variable, and it
	// could be moved to a carrier symbol. However, carrier symbols containing
	// carrier symbols don't work yet (as of Aug 2020). Once this is fixed,
	// runtime.pctab could just be a carrier sym.
	writePctab := func(ctxt *Link, s loader.Sym) {
		ldr := ctxt.loader
		sb := ldr.MakeSymbolUpdater(s)
		for sym := range seen {
			sb.SetBytesAt(ldr.SymValue(sym), ldr.Data(sym))
		}
	}

	state.pctab = state.addGeneratedSym(ctxt, "runtime.pctab", size, 1, writePctab)
}

// generateFuncdata writes out the funcdata information.
func (state *pclntab) generateFuncdata(ctxt *Link, funcs []loader.Sym, inlsyms map[loader.Sym]loader.Sym) {
	ldr := ctxt.loader

	// Walk the functions and collect the funcdata.
	seen := make(map[loader.Sym]struct{}, len(funcs))
	fdSyms := make([]loader.Sym, 0, len(funcs))
	fd := []loader.Sym{}
	for _, s := range funcs {
		fi := ldr.FuncInfo(s)
		if !fi.Valid() {
			continue
		}
		fi.Preload()
		fd := funcData(ldr, s, fi, inlsyms[s], fd)
		for j, fdSym := range fd {
			if ignoreFuncData(ldr, s, j, fdSym) {
				continue
			}

			if _, ok := seen[fdSym]; !ok {
				fdSyms = append(fdSyms, fdSym)
				seen[fdSym] = struct{}{}
			}
		}
	}
	seen = nil

	// Sort the funcdata in reverse order by alignment
	// to minimize alignment gaps. Use a stable sort
	// for reproducible results.
	var maxAlign int32
	slices.SortStableFunc(fdSyms, func(a, b loader.Sym) int {
		aAlign := symalign(ldr, a)
		bAlign := symalign(ldr, b)

		// Remember maximum alignment.
		maxAlign = max(maxAlign, aAlign, bAlign)

		// Negate to sort by decreasing alignment.
		return -cmp.Compare(aAlign, bAlign)
	})

	// We will output the symbols in the order of fdSyms.
	// Set the value of each symbol to its offset in the funcdata.
	// This way when writeFuncs writes out the funcdata offset,
	// it can simply write out the symbol value.

	// Accumulated size of funcdata info.
	size := int64(0)

	for _, fdSym := range fdSyms {
		datSize := ldr.SymSize(fdSym)
		if datSize == 0 {
			ctxt.Errorf(fdSym, "zero size funcdata")
			continue
		}

		size = Rnd(size, int64(symalign(ldr, fdSym)))
		ldr.SetSymValue(fdSym, size)
		size += datSize

		// We do not put the funcdata symbols in the symbol table.
		ldr.SetAttrNotInSymbolTable(fdSym, true)

		// Mark the symbol as special so that it does not get
		// adjusted by the section offset.
		ldr.SetAttrSpecial(fdSym, true)
	}

	// Funcdata symbols are permitted to have R_ADDROFF relocations,
	// which the linker can fully resolve.
	resolveRelocs := func(ldr *loader.Loader, fdSym loader.Sym, data []byte) {
		relocs := ldr.Relocs(fdSym)
		for i := 0; i < relocs.Count(); i++ {
			r := relocs.At(i)
			if r.Type() != objabi.R_ADDROFF {
				ctxt.Errorf(fdSym, "unsupported reloc %d (%s) for funcdata symbol", r.Type(), sym.RelocName(ctxt.Target.Arch, r.Type()))
				return
			}
			if r.Siz() != 4 {
				ctxt.Errorf(fdSym, "unsupported ADDROFF reloc size %d for funcdata symbol", r.Siz())
				return
			}
			rs := r.Sym()
			if r.Weak() && !ldr.AttrReachable(rs) {
				return
			}
			sect := ldr.SymSect(rs)
			if sect == nil {
				ctxt.Errorf(fdSym, "missing section for relocation target %s for funcdata symbol", ldr.SymName(rs))
			}
			o := ldr.SymValue(rs)
			if sect.Name != ".text" {
				o -= int64(sect.Vaddr)
			} else {
				// With multiple .text sections the offset
				// is from the start of the first one.
				o -= int64(Segtext.Sections[0].Vaddr)
				if ctxt.Target.IsWasm() {
					if o&(1<<16-1) != 0 {
						ctxt.Errorf(fdSym, "textoff relocation does not target function entry for funcdata symbol: %s %#x", ldr.SymName(rs), o)
					}
					o >>= 16
				}
			}
			o += r.Add()
			if o != int64(int32(o)) && o != int64(uint32(o)) {
				ctxt.Errorf(fdSym, "ADDROFF relocation out of range for funcdata symbol: %#x", o)
			}
			ctxt.Target.Arch.ByteOrder.PutUint32(data[r.Off():], uint32(o))
		}
	}

	writeFuncData := func(ctxt *Link, s loader.Sym) {
		ldr := ctxt.loader
		sb := ldr.MakeSymbolUpdater(s)
		for _, fdSym := range fdSyms {
			off := ldr.SymValue(fdSym)
			fdSymData := ldr.Data(fdSym)
			sb.SetBytesAt(off, fdSymData)
			// Resolve any R_ADDROFF relocations.
			resolveRelocs(ldr, fdSym, sb.Data()[off:off+int64(len(fdSymData))])
		}
	}

	state.funcdata = state.addGeneratedSym(ctxt, "go:func.*", size, maxAlign, writeFuncData)

	// Because the funcdata previously was not in pclntab,
	// we need to keep the visible symbol so that tools can find it.
	ldr.SetAttrNotInSymbolTable(state.funcdata, false)
}

// ignoreFuncData reports whether we should ignore a funcdata symbol.
//
// cmd/internal/obj optimistically populates ArgsPointerMaps and
// ArgInfo for assembly functions, hoping that the compiler will
// emit appropriate symbols from their Go stub declarations. If
// it didn't though, just ignore it.
//
// TODO(cherryyz): Fix arg map generation (see discussion on CL 523335).
func ignoreFuncData(ldr *loader.Loader, s loader.Sym, j int, fdSym loader.Sym) bool {
	if fdSym == 0 {
		return true
	}
	if (j == abi.FUNCDATA_ArgsPointerMaps || j == abi.FUNCDATA_ArgInfo) && ldr.IsFromAssembly(s) && ldr.Data(fdSym) == nil {
		return true
	}
	return false
}

// numPCData returns the number of PCData syms for the FuncInfo.
// NB: Preload must be called on valid FuncInfos before calling this function.
func numPCData(ldr *loader.Loader, s loader.Sym, fi loader.FuncInfo) uint32 {
	if !fi.Valid() {
		return 0
	}
	numPCData := uint32(ldr.NumPcdata(s))
	if fi.NumInlTree() > 0 {
		if numPCData < abi.PCDATA_InlTreeIndex+1 {
			numPCData = abi.PCDATA_InlTreeIndex + 1
		}
	}
	return numPCData
}

// generateFunctab creates the runtime.functab
//
// runtime.functab contains two things:
//
//   - pc->func look up table.
//   - array of func objects, interleaved with pcdata and funcdata
func (state *pclntab) generateFunctab(ctxt *Link, funcs []loader.Sym, inlSyms map[loader.Sym]loader.Sym, cuOffsets []uint32, nameOffsets map[loader.Sym]uint32) {
	// Calculate the size of the table.
	size, startLocations, shapes := state.calculateFunctabSize(ctxt, funcs, inlSyms)
	writePcln := func(ctxt *Link, s loader.Sym) {
		ldr := ctxt.loader
		sb := ldr.MakeSymbolUpdater(s)
		// Write the data.
		writePCToFunc(ctxt, sb, funcs, startLocations)
		writeFuncs(ctxt, sb, funcs, inlSyms, startLocations, shapes, cuOffsets, nameOffsets)
	}
	state.pclntab = state.addGeneratedSym(ctxt, "runtime.functab", size, 4, writePcln)
}

// funcData returns the funcdata and offsets for the FuncInfo.
// The funcdata are written into runtime.functab after each func
// object. This is a helper function to make querying the FuncInfo object
// cleaner.
//
// NB: Preload must be called on the FuncInfo before calling.
// NB: fdSyms is used as scratch space.
func funcData(ldr *loader.Loader, s loader.Sym, fi loader.FuncInfo, inlSym loader.Sym, fdSyms []loader.Sym) []loader.Sym {
	fdSyms = fdSyms[:0]
	if fi.Valid() {
		fdSyms = ldr.Funcdata(s, fdSyms)
		if fi.NumInlTree() > 0 {
			if len(fdSyms) < abi.FUNCDATA_InlTree+1 {
				fdSyms = append(fdSyms, make([]loader.Sym, abi.FUNCDATA_InlTree+1-len(fdSyms))...)
			}
			fdSyms[abi.FUNCDATA_InlTree] = inlSym
		}
	}
	return fdSyms
}

// calculateFunctabSize calculates the size of the pclntab, the offsets in
// the output buffer for individual func entries, and the encoded shape
// (pcdata/funcdata presence bitmaps) of every func entry.
func (state pclntab) calculateFunctabSize(ctxt *Link, funcs []loader.Sym, inlSyms map[loader.Sym]loader.Sym) (int64, []uint32, []funcShape) {
	ldr := ctxt.loader
	startLocations := make([]uint32, len(funcs))
	shapes := make([]funcShape, len(funcs))

	// Allocate space for the pc->func table. This structure consists of a pc offset
	// and an offset to the func structure. After that, we have a single pc
	// value that marks the end of the last function in the binary.
	size := int64(int(state.nfunc)*2*4 + 4)

	// Now find the space for the func objects. We do this in a running manner,
	// so that we can find individual starting locations.
	var pcdata, funcdata []loader.Sym
	for i, s := range funcs {
		// _func records are 4-byte aligned (every field is uint32 or uint8).
		size = Rnd(size, 4)
		startLocations[i] = uint32(size)
		fi := ldr.FuncInfo(s)
		size += funcSize
		if fi.Valid() {
			fi.Preload()
			var pcinline loader.Sym
			_, _, _, pcinline, pcdata = ldr.PcdataAuxs(s, pcdata)
			_, shapes[i].pcMask = funcPcdataOffsets(ldr, s, fi, pcinline, pcdata)
			funcdata = funcData(ldr, s, fi, inlSyms[s], funcdata)
			shapes[i].fdMask = funcFuncdataMask(ldr, s, funcdata)
			size += shapes[i].dataBytes()
		}
	}

	return size, startLocations, shapes
}

// textOff computes the offset of a text symbol, relative to textStart,
// similar to an R_ADDROFF relocation,  for various runtime metadata and
// tables (see runtime/symtab.go:(*moduledata).textAddr).
func textOff(ctxt *Link, s loader.Sym, textStart int64) uint32 {
	ldr := ctxt.loader
	off := ldr.SymValue(s) - textStart
	if off < 0 {
		panic(fmt.Sprintf("expected func %s(%x) to be placed at or after textStart (%x)", ldr.SymName(s), ldr.SymValue(s), textStart))
	}
	if ctxt.IsWasm() {
		// On Wasm, the function table contains just the function index, whereas
		// the "PC" (s's Value) is function index << 16 + block index (see
		// ../wasm/asm.go:assignAddress).
		if off&(1<<16-1) != 0 {
			ctxt.Errorf(s, "nonzero PC_B at function entry: %#x", off)
		}
		off >>= 16
	}
	if int64(uint32(off)) != off {
		ctxt.Errorf(s, "textOff overflow: %#x", off)
	}
	return uint32(off)
}

// writePCToFunc writes the PC->func lookup table.
func writePCToFunc(ctxt *Link, sb *loader.SymbolBuilder, funcs []loader.Sym, startLocations []uint32) {
	ldr := ctxt.loader
	textStart := ldr.SymValue(ldr.Lookup("runtime.text", 0))
	pcOff := func(s loader.Sym) uint32 {
		return textOff(ctxt, s, textStart)
	}
	for i, s := range funcs {
		sb.SetUint32(ctxt.Arch, int64(i*2*4), pcOff(s))
		sb.SetUint32(ctxt.Arch, int64((i*2+1)*4), startLocations[i])
	}

	// Final entry of table is just end pc offset.
	lastFunc := funcs[len(funcs)-1]
	lastPC := pcOff(lastFunc) + uint32(ldr.SymSize(lastFunc))
	if ctxt.IsWasm() {
		lastPC = pcOff(lastFunc) + 1 // On Wasm it is function index (see above)
	}
	sb.SetUint32(ctxt.Arch, int64(len(funcs))*2*4, lastPC)
}

// writeFuncs writes the func structures and pcdata to runtime.functab.
func writeFuncs(ctxt *Link, sb *loader.SymbolBuilder, funcs []loader.Sym, inlSyms map[loader.Sym]loader.Sym, startLocations []uint32, shapes []funcShape, cuOffsets []uint32, nameOffsets map[loader.Sym]uint32) {
	ldr := ctxt.loader
	deferReturnSym := ldr.Lookup("runtime.deferreturn", abiInternalVer)
	textStart := ldr.SymValue(ldr.Lookup("runtime.text", 0))
	funcdata := []loader.Sym{}
	var pcsp, pcfile, pcline, pcinline loader.Sym
	var pcdata []loader.Sym

	// Write the individual func objects (runtime._func struct).
	for i, s := range funcs {
		startLine := int32(0)
		fi := ldr.FuncInfo(s)
		if fi.Valid() {
			fi.Preload()
			pcsp, pcfile, pcline, pcinline, pcdata = ldr.PcdataAuxs(s, pcdata)
			startLine = fi.StartLine()
		}
		shape := shapes[i]

		off := int64(startLocations[i])
		// entryOff uint32 (offset of func entry PC from textStart)
		entryOff := textOff(ctxt, s, textStart)
		off = sb.SetUint32(ctxt.Arch, off, entryOff)

		// nameOff int32
		nameOff, ok := nameOffsets[s]
		if !ok {
			panic("couldn't find function name offset")
		}
		off = sb.SetUint32(ctxt.Arch, off, nameOff)

		// args int32
		// TODO: Move into funcinfo.
		args := uint32(0)
		if fi.Valid() {
			args = uint32(fi.Args())
		}
		off = sb.SetUint32(ctxt.Arch, off, args)

		// deferreturn
		deferreturn := computeDeferReturn(ctxt, deferReturnSym, s)
		off = sb.SetUint32(ctxt.Arch, off, deferreturn)

		// pcsp, pcfile, pcln
		if fi.Valid() {
			off = sb.SetUint32(ctxt.Arch, off, uint32(ldr.SymValue(pcsp)))
			off = sb.SetUint32(ctxt.Arch, off, uint32(ldr.SymValue(pcfile)))
			off = sb.SetUint32(ctxt.Arch, off, uint32(ldr.SymValue(pcline)))
		} else {
			off += 12
		}

		// Store the offset to compilation unit's file table.
		cuIdx := ^uint32(0)
		if cu := ldr.SymUnit(s); cu != nil {
			cuIdx = cuOffsets[cu.PclnIndex]
		}
		off = sb.SetUint32(ctxt.Arch, off, cuIdx)

		// startLine int32
		off = sb.SetUint32(ctxt.Arch, off, uint32(startLine))

		// funcID uint8
		var funcID abi.FuncID
		if fi.Valid() {
			funcID = fi.FuncID()
		}
		off = sb.SetUint8(ctxt.Arch, off, uint8(funcID))

		// flag uint8
		var flag abi.FuncFlag
		if fi.Valid() {
			flag = fi.FuncFlag()
		}
		off = sb.SetUint8(ctxt.Arch, off, uint8(flag))

		// pcdataMask, funcdataMask uint8 presence bitmaps.
		// funcdataMask must be the final entry.
		off = sb.SetUint8(ctxt.Arch, off, shape.pcMask)
		off = sb.SetUint8(ctxt.Arch, off, shape.fdMask)

		if off != int64(startLocations[i])+funcSize {
			panic("_func fixed part size mismatch")
		}

		// Output the offsets of the present pcdata tables, in index order.
		if fi.Valid() {
			pcOffs, pcMask := funcPcdataOffsets(ldr, s, fi, pcinline, pcdata)
			if pcMask != shape.pcMask {
				panic("pcdata presence mask changed between size calculation and write")
			}
			for j := 0; j < 8; j++ {
				if pcMask&(1<<j) != 0 {
					off = sb.SetUint32(ctxt.Arch, off, pcOffs[j])
				}
			}
		}

		// Write the present funcdata refs as offsets from go:func.*, in
		// slot order. Absent slots (see ignoreFuncData) have a cleared
		// bit in funcdataMask and no entry.
		funcdata = funcData(ldr, s, fi, inlSyms[s], funcdata)
		if funcFuncdataMask(ldr, s, funcdata) != shape.fdMask {
			panic("funcdata presence mask changed between size calculation and write")
		}
		for j := range funcdata {
			if shape.fdMask&(1<<uint(j)) != 0 {
				off = sb.SetUint32(ctxt.Arch, off, uint32(ldr.SymValue(funcdata[j])))
			}
		}

		if off != int64(startLocations[i])+funcSize+shape.dataBytes() {
			panic("_func record size mismatch")
		}
	}
}

// pclntab initializes the pclntab symbol with
// runtime function and file name information.

// pclntab generates the pcln table for the link output.
func (ctxt *Link) pclntab(container loader.Bitmap) *pclntab {
	// Go 1.2's symtab layout is documented in golang.org/s/go12symtab, but the
	// layout and data has changed since that time.
	//
	// As of August 2020, here's the layout of pclntab:
	//
	//  .gopclntab/__gopclntab [elf/macho section]
	//    runtime.pclntab
	//      Carrier symbol for the entire pclntab section.
	//
	//      runtime.pcheader  (see: runtime/symtab.go:pcHeader)
	//        8-byte magic
	//        nfunc [thearch.ptrsize bytes]
	//        offset to runtime.funcnametab from the beginning of runtime.pcheader
	//        offset to runtime.pclntab_old from beginning of runtime.pcheader
	//
	//      runtime.funcnametab
	//        uint32 count + uint32 offsets of the shared package-prefix
	//        strings, the NUL terminated prefix strings, then per-name
	//        entries: uvarint prefix index + NUL terminated name suffix
	//        (see generateFuncnametab)
	//
	//      runtime.cutab
	//        for i=0..#CUs
	//          for j=0..#max used file index in CU[i]
	//            uint32 offset into runtime.filetab for the filename[j]
	//
	//      runtime.filetab
	//        uint32 count + uint32 offsets of the shared directory
	//        strings, the NUL terminated directory strings, then per-file
	//        entries: uvarint dir index + NUL terminated base name
	//        (see generateFilenameTabs)
	//
	//      runtime.pctab
	//        []byte of deduplicated pc data.
	//
	//      runtime.functab
	//        function table, alternating PC and offset to func struct [each entry thearch.ptrsize bytes]
	//        end PC [thearch.ptrsize bytes]
	//        func structures, pcdata offsets, func data.
	//
	//      runtime.funcdata
	//        []byte of deduplicated funcdata

	state, compUnits, funcs := makePclntab(ctxt, container)

	ldr := ctxt.loader
	state.carrier = ldr.LookupOrCreateSym("runtime.pclntab", 0)
	ldr.MakeSymbolUpdater(state.carrier).SetType(sym.SPCLNTAB)
	ldr.SetAttrReachable(state.carrier, true)
	setCarrierSym(sym.SPCLNTAB, state.carrier)

	// Aign pclntab to at least a pointer boundary,
	// for pcHeader. This may be raised further by subsymbols.
	ldr.SetSymAlign(state.carrier, int32(ctxt.Arch.PtrSize))

	state.generatePCHeader(ctxt)
	nameOffsets := state.generateFuncnametab(ctxt, funcs)
	cuOffsets := state.generateFilenameTabs(ctxt, compUnits, funcs)
	state.generatePctab(ctxt, funcs)
	inlSyms := makeInlSyms(ctxt, funcs, nameOffsets)
	state.generateFunctab(ctxt, funcs, inlSyms, cuOffsets, nameOffsets)
	state.generateFuncdata(ctxt, funcs, inlSyms)

	return state
}

func expandGoroot(s string) string {
	const n = len("$GOROOT")
	if len(s) >= n+1 && s[:n] == "$GOROOT" && (s[n] == '/' || s[n] == '\\') {
		if final := buildcfg.GOROOT; final != "" {
			return filepath.ToSlash(filepath.Join(final, s[n:]))
		}
	}
	return s
}

const (
	SUBBUCKETS    = 16
	SUBBUCKETSIZE = abi.FuncTabBucketSize / SUBBUCKETS
	NOIDX         = 0x7fffffff
)

// findfunctab generates a lookup table to quickly find the containing
// function for a pc. See src/runtime/symtab.go:findfunc for details.
func (ctxt *Link) findfunctab(state *pclntab, container loader.Bitmap) {
	ldr := ctxt.loader

	// find min and max address
	min := ldr.SymValue(ctxt.Textp[0])
	lastp := ctxt.Textp[len(ctxt.Textp)-1]
	max := ldr.SymValue(lastp) + ldr.SymSize(lastp)

	// for each subbucket, compute the minimum of all symbol indexes
	// that map to that subbucket.
	n := int32((max - min + SUBBUCKETSIZE - 1) / SUBBUCKETSIZE)

	nbuckets := int32((max - min + abi.FuncTabBucketSize - 1) / abi.FuncTabBucketSize)

	size := 4*int64(nbuckets) + int64(n)

	writeFindFuncTab := func(_ *Link, s loader.Sym) {
		t := ldr.MakeSymbolUpdater(s)

		indexes := make([]int32, n)
		for i := int32(0); i < n; i++ {
			indexes[i] = NOIDX
		}
		idx := int32(0)
		for i, s := range ctxt.Textp {
			if !emitPcln(ctxt, s, container) {
				continue
			}
			p := ldr.SymValue(s)
			var e loader.Sym
			i++
			if i < len(ctxt.Textp) {
				e = ctxt.Textp[i]
			}
			for e != 0 && !emitPcln(ctxt, e, container) && i < len(ctxt.Textp) {
				e = ctxt.Textp[i]
				i++
			}
			q := max
			if e != 0 {
				q = ldr.SymValue(e)
			}

			//fmt.Printf("%d: [%x %x] %s\n", idx, p, q, ldr.SymName(s))
			for ; p < q; p += SUBBUCKETSIZE {
				i = int((p - min) / SUBBUCKETSIZE)
				if indexes[i] > idx {
					indexes[i] = idx
				}
			}

			i = int((q - 1 - min) / SUBBUCKETSIZE)
			if indexes[i] > idx {
				indexes[i] = idx
			}
			idx++
		}

		// fill in table
		for i := int32(0); i < nbuckets; i++ {
			base := indexes[i*SUBBUCKETS]
			if base == NOIDX {
				Errorf("hole in findfunctab")
			}
			t.SetUint32(ctxt.Arch, int64(i)*(4+SUBBUCKETS), uint32(base))
			for j := int32(0); j < SUBBUCKETS && i*SUBBUCKETS+j < n; j++ {
				idx = indexes[i*SUBBUCKETS+j]
				if idx == NOIDX {
					Errorf("hole in findfunctab")
				}
				if idx-base >= 256 {
					Errorf("too many functions in a findfunc bucket! %d/%d %d %d", i, nbuckets, j, idx-base)
				}

				t.SetUint8(ctxt.Arch, int64(i)*(4+SUBBUCKETS)+4+int64(j), uint8(idx-base))
			}
		}
	}

	state.findfunctab = ctxt.createGeneratorSymbol("runtime.findfunctab", 0, sym.SPCLNTAB, size, writeFindFuncTab)
	ldr.SetSymAlign(state.findfunctab, 4)
	ldr.SetAttrReachable(state.findfunctab, true)
	ldr.SetAttrLocal(state.findfunctab, true)
}

// findContainerSyms returns a bitmap, indexed by symbol number, where there's
// a 1 for every container symbol.
func (ctxt *Link) findContainerSyms() loader.Bitmap {
	ldr := ctxt.loader
	container := loader.MakeBitmap(ldr.NSym())
	// Find container symbols and mark them as such.
	for _, s := range ctxt.Textp {
		outer := ldr.OuterSym(s)
		if outer != 0 {
			container.Set(outer)
		}
	}
	return container
}

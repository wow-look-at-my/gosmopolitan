// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"bytes"
	"cmd/internal/obj"
	"cmd/internal/obj/wasm"
	"cmd/internal/objabi"
	"cmd/link/internal/ld"
	"cmd/link/internal/loader"
	"cmd/link/internal/sym"
	"fmt"
	"internal/abi"
	"internal/buildcfg"
	"io"
	"regexp"
)

const (
	I32 = 0x7F
	I64 = 0x7E
	F32 = 0x7D
	F64 = 0x7C
)

const (
	sectionCustom    = 0
	sectionType      = 1
	sectionImport    = 2
	sectionFunction  = 3
	sectionTable     = 4
	sectionMemory    = 5
	sectionGlobal    = 6
	sectionExport    = 7
	sectionStart     = 8
	sectionElement   = 9
	sectionCode      = 10
	sectionData      = 11
	sectionDataCount = 12
)

// funcValueOffset is the offset between the PC_F value of a function and the index of the function in WebAssembly
const funcValueOffset = 0x1000 // TODO(neelance): make function addresses play nice with heap addresses

func gentext(ctxt *ld.Link, ldr *loader.Loader) {
}

type wasmFunc struct {
	Module string
	Name   string
	Sym    loader.Sym // 0 for host imports
	Type   uint32
	Code   []byte
}

type wasmFuncType struct {
	Params  []byte
	Results []byte
}

func readWasmImport(ldr *loader.Loader, s loader.Sym) obj.WasmImport {
	var wi obj.WasmImport
	wi.Read(ldr.Data(s))
	return wi
}

var wasmFuncTypes = map[string]*wasmFuncType{
	"_rt0_wasm_js":            {Params: []byte{}},                                         //
	"_rt0_wasm_wasip1":        {Params: []byte{}},                                         //
	"_rt0_wasm_wasip1_lib":    {Params: []byte{}},                                         //
	"wasm_export__start":      {},                                                         //
	"wasm_export_run":         {Params: []byte{I32, I32}},                                 // argc, argv
	"wasm_export_resume":      {Params: []byte{}},                                         //
	"wasm_export_getsp":       {Results: []byte{I32}},                                     // sp
	"wasm_export_thread_run":  {Params: []byte{I32}},                                      // worker id (GOWASM=threads)
	"wasm_pc_f_loop":          {Params: []byte{}},                                         //
	"wasm_pc_f_loop_export":   {Params: []byte{I32}},                                      // pc_f
	"runtime.wasmTruncS":      {Params: []byte{F64}, Results: []byte{I64}},                // x -> int(x)
	"runtime.wasmTruncU":      {Params: []byte{F64}, Results: []byte{I64}},                // x -> uint(x)
	"gcWriteBarrier":          {Params: []byte{I64}, Results: []byte{I64}},                // #bytes -> bufptr
	"runtime.gcWriteBarrier1": {Results: []byte{I64}},                                     // -> bufptr
	"runtime.gcWriteBarrier2": {Results: []byte{I64}},                                     // -> bufptr
	"runtime.gcWriteBarrier3": {Results: []byte{I64}},                                     // -> bufptr
	"runtime.gcWriteBarrier4": {Results: []byte{I64}},                                     // -> bufptr
	"runtime.gcWriteBarrier5": {Results: []byte{I64}},                                     // -> bufptr
	"runtime.gcWriteBarrier6": {Results: []byte{I64}},                                     // -> bufptr
	"runtime.gcWriteBarrier7": {Results: []byte{I64}},                                     // -> bufptr
	"runtime.gcWriteBarrier8": {Results: []byte{I64}},                                     // -> bufptr
	"runtime.notInitialized":  {},                                                         //
	"cmpbody":                 {Params: []byte{I64, I64, I64, I64}, Results: []byte{I64}}, // a, alen, b, blen -> -1/0/1
	"memeqbody":               {Params: []byte{I64, I64, I64}, Results: []byte{I64}},      // a, b, len -> 0/1
	"memcmp":                  {Params: []byte{I32, I32, I32}, Results: []byte{I32}},      // a, b, len -> <0/0/>0
	"memchr":                  {Params: []byte{I32, I32, I32}, Results: []byte{I32}},      // s, c, len -> index
}

func assignAddress(ldr *loader.Loader, sect *sym.Section, n int, s loader.Sym, va uint64, isTramp bool) (*sym.Section, int, uint64) {
	// WebAssembly functions do not live in the same address space as the linear memory.
	// Instead, WebAssembly automatically assigns indices. Imported functions (section "import")
	// have indices 0 to n. They are followed by native functions (sections "function" and "code")
	// with indices n+1 and following.
	//
	// The following rules describe how wasm handles function indices and addresses:
	//   PC_F = funcValueOffset + WebAssembly function index (not including the imports)
	//   s.Value = PC = PC_F<<16 + PC_B
	//
	// The funcValueOffset is necessary to avoid conflicts with expectations
	// that the Go runtime has about function addresses.
	// The field "s.Value" corresponds to the concept of PC at runtime.
	// However, there is no PC register, only PC_F and PC_B. PC_F denotes the function,
	// PC_B the resume point inside of that function. The entry of the function has PC_B = 0.
	ldr.SetSymSect(s, sect)
	ldr.SetSymValue(s, int64(funcValueOffset+va/abi.MINFUNC)<<16) // va starts at zero
	va += uint64(abi.MINFUNC)
	return sect, n, va
}

type wasmDataSect struct {
	sect *sym.Section
	data []byte
}

var dataSects []wasmDataSect

func asmb(ctxt *ld.Link, ldr *loader.Loader) {
	sections := []*sym.Section{
		ldr.SymSect(ldr.Lookup("runtime.rodata", 0)),
		ldr.SymSect(ldr.Lookup("runtime.typelink", 0)),
		ldr.SymSect(ldr.Lookup("runtime.itablink", 0)),
		ldr.SymSect(ldr.Lookup("runtime.firstmoduledata", 0)),
		ldr.SymSect(ldr.Lookup("runtime.pclntab", 0)),
		ldr.SymSect(ldr.Lookup("runtime.noptrdata", 0)),
		ldr.SymSect(ldr.Lookup("runtime.data", 0)),
	}

	dataSects = make([]wasmDataSect, len(sections))
	for i, sect := range sections {
		data := ld.DatblkBytes(ctxt, int64(sect.Vaddr), int64(sect.Length))
		dataSects[i] = wasmDataSect{sect, data}
	}
}

// asmb writes the final WebAssembly module binary.
// Spec: https://webassembly.github.io/spec/core/binary/modules.html
func asmb2(ctxt *ld.Link, ldr *loader.Loader) {
	if buildcfg.GOWASM.Threads && buildcfg.GOOS != "js" {
		// The shared linear memory a GOWASM=threads module needs must be
		// supplied by the host (wasm_exec.js does this for GOOS=js);
		// wasip1 runtimes have no protocol for importing memory.
		ld.Exitf("GOWASM=threads is only supported on GOOS=js")
	}

	types := []*wasmFuncType{
		// For normal Go functions, the single parameter is PC_B,
		// the return value is
		// 0 if the function returned normally or
		// 1 if the stack needs to be unwound.
		{Params: []byte{I32}, Results: []byte{I32}},
	}

	// collect host imports (functions that get imported from the WebAssembly host, usually JavaScript)
	// we store the import index of each imported function, so the R_WASMIMPORT relocation
	// can write the correct index after a "call" instruction
	// these are added as import statements to the top of the WebAssembly binary
	var hostImports []*wasmFunc
	hostImportMap := make(map[loader.Sym]int64)
	for _, fn := range ctxt.Textp {
		relocs := ldr.Relocs(fn)
		for ri := 0; ri < relocs.Count(); ri++ {
			r := relocs.At(ri)
			if r.Type() == objabi.R_WASMIMPORT {
				if wsym := ldr.WasmImportSym(fn); wsym != 0 {
					wi := readWasmImport(ldr, wsym)
					hostImportMap[fn] = int64(len(hostImports))
					hostImports = append(hostImports, &wasmFunc{
						Module: wi.Module,
						Name:   wi.Name,
						Type: lookupType(&wasmFuncType{
							Params:  fieldsToTypes(wi.Params),
							Results: fieldsToTypes(wi.Results),
						}, &types),
					})
				} else {
					panic(fmt.Sprintf("missing wasm symbol for %s", ldr.SymName(r.Sym())))
				}
			}
		}
	}

	// dwarf reports whether DWARF debug info is being emitted. In that
	// case the relocated LEB128 fields inside function bodies keep the
	// fixed width the assembler reserved for them, so that the byte
	// offsets baked into the DWARF sections match the code the module
	// actually carries. Without DWARF they are compacted to minimal
	// LEB128s instead.
	dwarf := false
	if len(ctxt.Textp) > 0 {
		_, dwarf = ld.WasmCodeOffset(ctxt.Textp[0])
	}

	// collect functions with WebAssembly body
	var buildid []byte
	fns := make([]*wasmFunc, len(ctxt.Textp))
	for i, fn := range ctxt.Textp {
		wfn := new(bytes.Buffer)
		if ldr.SymName(fn) == "go:buildid" {
			writeUleb128(wfn, 0) // number of sets of locals
			writeI32Const(wfn, 0)
			wfn.WriteByte(0x0b) // end
			buildid = ldr.Data(fn)
		} else {
			// The assembler reserved fixed-width placeholders for the
			// relocated LEB128 fields (see obj/wasm.RelocLEBSize);
			// overwrite them with the final values here.
			relocs := ldr.Relocs(fn)
			P := ldr.Data(fn)
			off := int32(0)
			for ri := 0; ri < relocs.Count(); ri++ {
				r := relocs.At(ri)
				if r.Siz() == 0 {
					continue // skip marker relocations
				}
				wfn.Write(P[off:r.Off()])
				off = r.Off() + int32(r.Siz()) // skip the placeholder
				rs := r.Sym()
				var v int64
				switch r.Type() {
				case objabi.R_ADDR:
					v = ldr.SymValue(rs) + r.Add()
				case objabi.R_CALL:
					v = int64(len(hostImports)) + ldr.SymValue(rs)>>16 - funcValueOffset
				case objabi.R_WASMIMPORT:
					v = hostImportMap[rs]
				default:
					ldr.Errorf(fn, "bad reloc type %d (%s)", r.Type(), sym.RelocName(ctxt.Arch, r.Type()))
					continue
				}
				if dwarf {
					if err := writeSleb128FixedLength(wfn, v, int(r.Siz())); err != nil {
						ldr.Errorf(fn, "cannot encode relocation value for %s: %v (link with -ldflags=-w to disable DWARF)", ldr.SymName(rs), err)
					}
				} else {
					writeSleb128(wfn, v)
				}
			}
			wfn.Write(P[off:])
		}

		typ := uint32(0)
		if sig, ok := wasmFuncTypes[ldr.SymName(fn)]; ok {
			typ = lookupType(sig, &types)
		}
		if s := ldr.WasmTypeSym(fn); s != 0 {
			var o obj.WasmFuncType
			o.Read(ldr.Data(s))
			t := &wasmFuncType{
				Params:  fieldsToTypes(o.Params),
				Results: fieldsToTypes(o.Results),
			}
			typ = lookupType(t, &types)
		}

		name := nameRegexp.ReplaceAllString(ldr.SymName(fn), "_")
		fns[i] = &wasmFunc{Name: name, Sym: fn, Type: typ, Code: wfn.Bytes()}
	}

	segments := dataSegments()

	// Under GOWASM=threads the linker appends synthetic functions after
	// all Go functions (see threadsSyntheticFuncs). They are exported but
	// deliberately kept out of the CallIndirect table, so the PC_F mapping
	// and the element section are unchanged.
	var syntheticFns []*wasmFunc
	if buildcfg.GOWASM.Threads {
		syntheticFns = threadsSyntheticFuncs(&types, segments)
		if len(syntheticFns) != ld.WasmThreadsNumSyntheticFuncs {
			ld.Exitf("internal error: threadsSyntheticFuncs emitted %d functions, ld.WasmThreadsNumSyntheticFuncs says %d", len(syntheticFns), ld.WasmThreadsNumSyntheticFuncs)
		}
	}
	allFns := fns
	if len(syntheticFns) > 0 {
		allFns = append(fns[:len(fns):len(fns)], syntheticFns...)
	}

	ctxt.Out.Write([]byte{0x00, 0x61, 0x73, 0x6d}) // magic
	ctxt.Out.Write([]byte{0x01, 0x00, 0x00, 0x00}) // version

	// Add any buildid early in the binary:
	if len(buildid) != 0 {
		writeBuildID(ctxt, buildid)
	}

	writeTypeSec(ctxt, types)
	writeImportSec(ctxt, ldr, hostImports)
	writeFunctionSec(ctxt, allFns)
	writeTableSec(ctxt, fns)
	writeMemorySec(ctxt, ldr)
	writeGlobalSec(ctxt)
	writeExportSec(ctxt, ldr, len(hostImports), len(fns), syntheticFns)
	writeElementSec(ctxt, uint64(len(hostImports)), uint64(len(fns)))
	if buildcfg.GOWASM.Threads {
		// memory.init/data.drop in _initmem require the DataCount section
		// (it lets the code section validate data segment indices without
		// a forward scan).
		writeDataCountSec(ctxt, len(segments))
	}
	writeCodeSec(ctxt, allFns)
	writeDataSec(ctxt, segments)
	if dwarf {
		writeDwarfSections(ctxt)
	}
	// The name section goes before the producers section: the tool
	// conventions place producers after name, and LLVM's wasm reader
	// rejects modules that order them the other way around.
	if !*ld.FlagS {
		writeNameSec(ctxt, len(hostImports), allFns)
	}
	writeProducerSec(ctxt)
}

func lookupType(sig *wasmFuncType, types *[]*wasmFuncType) uint32 {
	for i, t := range *types {
		if bytes.Equal(sig.Params, t.Params) && bytes.Equal(sig.Results, t.Results) {
			return uint32(i)
		}
	}
	*types = append(*types, sig)
	return uint32(len(*types) - 1)
}

func writeSecHeader(ctxt *ld.Link, id uint8) int64 {
	ctxt.Out.WriteByte(id)
	sizeOffset := ctxt.Out.Offset()
	ctxt.Out.Write(make([]byte, 5)) // placeholder for length
	return sizeOffset
}

func writeSecSize(ctxt *ld.Link, sizeOffset int64) {
	endOffset := ctxt.Out.Offset()
	ctxt.Out.SeekSet(sizeOffset)
	writeUleb128FixedLength(ctxt.Out, uint64(endOffset-sizeOffset-5), 5)
	ctxt.Out.SeekSet(endOffset)
}

func writeBuildID(ctxt *ld.Link, buildid []byte) {
	sizeOffset := writeSecHeader(ctxt, sectionCustom)
	writeName(ctxt.Out, "go:buildid")
	ctxt.Out.Write(buildid)
	writeSecSize(ctxt, sizeOffset)
}

// writeTypeSec writes the section that declares all function types
// so they can be referenced by index.
func writeTypeSec(ctxt *ld.Link, types []*wasmFuncType) {
	sizeOffset := writeSecHeader(ctxt, sectionType)

	writeUleb128(ctxt.Out, uint64(len(types)))

	for _, t := range types {
		ctxt.Out.WriteByte(0x60) // functype
		writeUleb128(ctxt.Out, uint64(len(t.Params)))
		for _, v := range t.Params {
			ctxt.Out.WriteByte(v)
		}
		writeUleb128(ctxt.Out, uint64(len(t.Results)))
		for _, v := range t.Results {
			ctxt.Out.WriteByte(v)
		}
	}

	writeSecSize(ctxt, sizeOffset)
}

// writeImportSec writes the section that lists the functions that get
// imported from the WebAssembly host, usually JavaScript. With
// GOWASM=threads it additionally imports the shared linear memory
// (which replaces the module-local memory writeMemorySec would declare).
func writeImportSec(ctxt *ld.Link, ldr *loader.Loader, hostImports []*wasmFunc) {
	sizeOffset := writeSecHeader(ctxt, sectionImport)

	numImports := uint64(len(hostImports))
	if buildcfg.GOWASM.Threads {
		numImports++ // the shared linear memory
	}

	writeUleb128(ctxt.Out, numImports) // number of imports
	for _, fn := range hostImports {
		if fn.Module != "" {
			writeName(ctxt.Out, fn.Module)
		} else {
			writeName(ctxt.Out, wasm.GojsModule) // provided by the import object in wasm_exec.js
		}
		writeName(ctxt.Out, fn.Name)
		ctxt.Out.WriteByte(0x00) // func import
		writeUleb128(ctxt.Out, uint64(fn.Type))
	}

	if buildcfg.GOWASM.Threads {
		// The threads proposal requires shared memories to declare a
		// maximum size, and a module cannot declare its own memory as
		// shared-importable: shared linear memory must be created by the
		// host (as a SharedArrayBuffer-backed WebAssembly.Memory) and
		// imported. wasm_exec.js reads these limits from the binary
		// header and supplies a matching memory in the import object.
		// The memory import does not disturb the function index space:
		// memories have their own index space, and this import stays
		// index 0 there, so the data section and the "mem" export are
		// unchanged.
		writeName(ctxt.Out, wasm.GojsModule)
		writeName(ctxt.Out, "mem")                         // go._importedMemory in wasm_exec.js
		ctxt.Out.WriteByte(0x02)                           // mem import
		ctxt.Out.WriteByte(0x03)                           // limits: shared, min and max present
		writeUleb128(ctxt.Out, initialMemoryPages(ldr))    // minimum (initial) memory size
		writeUleb128(ctxt.Out, sharedMemoryMaximumPages()) // maximum memory size
	}

	writeSecSize(ctxt, sizeOffset)
}

// writeFunctionSec writes the section that declares the types of functions.
// The bodies of these functions will later be provided in the "code" section.
func writeFunctionSec(ctxt *ld.Link, fns []*wasmFunc) {
	sizeOffset := writeSecHeader(ctxt, sectionFunction)

	writeUleb128(ctxt.Out, uint64(len(fns)))
	for _, fn := range fns {
		writeUleb128(ctxt.Out, uint64(fn.Type))
	}

	writeSecSize(ctxt, sizeOffset)
}

// writeTableSec writes the section that declares tables. Currently there is only a single table
// that is used by the CallIndirect operation to dynamically call any function.
// The contents of the table get initialized by the "element" section.
func writeTableSec(ctxt *ld.Link, fns []*wasmFunc) {
	sizeOffset := writeSecHeader(ctxt, sectionTable)

	numElements := uint64(funcValueOffset + len(fns))
	writeUleb128(ctxt.Out, 1)           // number of tables
	ctxt.Out.WriteByte(0x70)            // type: anyfunc
	ctxt.Out.WriteByte(0x00)            // no max
	writeUleb128(ctxt.Out, numElements) // min

	writeSecSize(ctxt, sizeOffset)
}

const wasmPageSize = 64 << 10 // 64KB

// initialMemoryPages returns the minimum (initial) linear memory size in
// wasm pages: the end of the data segments plus 1 MB for runtime init
// allocating a few pages.
func initialMemoryPages(ldr *loader.Loader) uint64 {
	dataEnd := uint64(ldr.SymValue(ldr.Lookup("runtime.end", 0)))
	var initialSize = dataEnd + 1<<20 // 1 MB, for runtime init allocating a few pages
	return initialSize / wasmPageSize
}

// sharedMemoryMaximumPages returns the maximum size of the imported shared
// linear memory of a GOWASM=threads module, in wasm pages. Unlike a
// module-local memory, a shared memory is required by the threads proposal
// to declare a maximum, and it can never grow past it. 2 GiB (half the
// wasm32 address space) is the default; there is currently no flag to
// override it.
func sharedMemoryMaximumPages() uint64 {
	const maxSize = 2048 << 20 // 2 GiB
	return maxSize / wasmPageSize
}

// writeMemorySec writes the section that declares linear memories. Currently one linear memory is being used.
// Linear memory always starts at address zero. More memory can be requested with the GrowMemory instruction.
// With GOWASM=threads no module-local memory is declared: the shared linear
// memory is imported instead (see writeImportSec), and this section is
// omitted entirely.
func writeMemorySec(ctxt *ld.Link, ldr *loader.Loader) {
	if buildcfg.GOWASM.Threads {
		return
	}

	sizeOffset := writeSecHeader(ctxt, sectionMemory)

	writeUleb128(ctxt.Out, 1)                       // number of memories
	ctxt.Out.WriteByte(0x00)                        // no maximum memory size
	writeUleb128(ctxt.Out, initialMemoryPages(ldr)) // minimum (initial) memory size

	writeSecSize(ctxt, sizeOffset)
}

// writeGlobalSec writes the section that declares global variables.
func writeGlobalSec(ctxt *ld.Link) {
	sizeOffset := writeSecHeader(ctxt, sectionGlobal)

	globalRegs := []byte{
		I32, // 0: SP
		I64, // 1: CTXT
		I64, // 2: g
		I64, // 3: RET0
		I64, // 4: RET1
		I64, // 5: RET2
		I64, // 6: RET3
		I32, // 7: PAUSE
	}
	if buildcfg.GOWASM.Threads {
		// 8: the per-instance "observed grow epoch" consumed by the
		// atomic-access grow-observation guard (growEpochGlobal /
		// writeGrowEpochGuard in cmd/internal/obj/wasm, which must stay
		// in sync with this index). Initial 0 = "no grows observed",
		// matching runtime.wasmGrowEpoch's zero value; being a global,
		// every instance sharing the memory gets its own copy.
		globalRegs = append(globalRegs, I32)
	}

	writeUleb128(ctxt.Out, uint64(len(globalRegs))) // number of globals

	for _, typ := range globalRegs {
		ctxt.Out.WriteByte(typ)
		ctxt.Out.WriteByte(0x01) // var
		switch typ {
		case I32:
			writeI32Const(ctxt.Out, 0)
		case I64:
			writeI64Const(ctxt.Out, 0)
		}
		ctxt.Out.WriteByte(0x0b) // end
	}

	writeSecSize(ctxt, sizeOffset)
}

// writeExportSec writes the section that declares exports.
// Exports can be accessed by the WebAssembly host, usually JavaScript.
// The wasm_export_* functions and the linear memory get exported. Under
// GOWASM=threads the linker-synthesized functions (function indices
// lenHostImports+numFns and up, see threadsSyntheticFuncs) are exported
// as well.
func writeExportSec(ctxt *ld.Link, ldr *loader.Loader, lenHostImports, numFns int, syntheticFns []*wasmFunc) {
	sizeOffset := writeSecHeader(ctxt, sectionExport)

	switch buildcfg.GOOS {
	case "wasip1":
		writeUleb128(ctxt.Out, uint64(2+len(ldr.WasmExports))) // number of exports
		var entry, entryExpName string
		switch ctxt.BuildMode {
		case ld.BuildModeExe:
			entry = "_rt0_wasm_wasip1"
			entryExpName = "_start"
		case ld.BuildModeCShared:
			entry = "_rt0_wasm_wasip1_lib"
			entryExpName = "_initialize"
		}
		s := ldr.Lookup(entry, 0)
		if s == 0 {
			ld.Errorf("export symbol %s not defined", entry)
		}
		idx := uint32(lenHostImports) + uint32(ldr.SymValue(s)>>16) - funcValueOffset
		writeName(ctxt.Out, entryExpName)   // the wasi entrypoint
		ctxt.Out.WriteByte(0x00)            // func export
		writeUleb128(ctxt.Out, uint64(idx)) // funcidx
		for _, s := range ldr.WasmExports {
			idx := uint32(lenHostImports) + uint32(ldr.SymValue(s)>>16) - funcValueOffset
			writeName(ctxt.Out, ldr.SymName(s))
			ctxt.Out.WriteByte(0x00)            // func export
			writeUleb128(ctxt.Out, uint64(idx)) // funcidx
		}
		writeName(ctxt.Out, "memory") // memory in wasi
		ctxt.Out.WriteByte(0x02)      // mem export
		writeUleb128(ctxt.Out, 0)     // memidx
	case "js":
		exports := []struct{ sym, name string }{
			{"wasm_export_run", "run"},
			{"wasm_export_resume", "resume"},
			{"wasm_export_getsp", "getsp"},
		}
		if buildcfg.GOWASM.Threads {
			// The worker-thread entry point: a pool worker instance calls
			// this instead of run/resume (see lib/wasm/wasm_exec_worker.js
			// and runtime/sys_wasmthreads.s).
			exports = append(exports, struct{ sym, name string }{"wasm_export_thread_run", "wasm_thread_run"})
		}
		writeUleb128(ctxt.Out, uint64(1+len(exports)+len(ldr.WasmExports)+len(syntheticFns))) // number of exports
		for _, e := range exports {
			s := ldr.Lookup(e.sym, 0)
			if s == 0 {
				ld.Errorf("export symbol %s not defined", e.sym)
			}
			idx := uint32(lenHostImports) + uint32(ldr.SymValue(s)>>16) - funcValueOffset
			writeName(ctxt.Out, e.name)         // inst.exports.run/resume/getsp/wasm_thread_run in wasm_exec*.js
			ctxt.Out.WriteByte(0x00)            // func export
			writeUleb128(ctxt.Out, uint64(idx)) // funcidx
		}
		for _, s := range ldr.WasmExports {
			idx := uint32(lenHostImports) + uint32(ldr.SymValue(s)>>16) - funcValueOffset
			writeName(ctxt.Out, ldr.SymName(s))
			ctxt.Out.WriteByte(0x00)            // func export
			writeUleb128(ctxt.Out, uint64(idx)) // funcidx
		}
		for i, fn := range syntheticFns {
			writeName(ctxt.Out, fn.Name)                            // _initmem, wasm_probe_atomic_add
			ctxt.Out.WriteByte(0x00)                                // func export
			writeUleb128(ctxt.Out, uint64(lenHostImports+numFns+i)) // funcidx
		}
		writeName(ctxt.Out, "mem") // inst.exports.mem in wasm_exec.js
		ctxt.Out.WriteByte(0x02)   // mem export
		writeUleb128(ctxt.Out, 0)  // memidx
	default:
		ld.Exitf("internal error: writeExportSec: unrecognized GOOS %s", buildcfg.GOOS)
	}

	writeSecSize(ctxt, sizeOffset)
}

// writeElementSec writes the section that initializes the tables declared by the "table" section.
// The table for CallIndirect gets initialized in a very simple way so that each table index (PC_F value)
// maps linearly to the function index (numImports + PC_F).
func writeElementSec(ctxt *ld.Link, numImports, numFns uint64) {
	sizeOffset := writeSecHeader(ctxt, sectionElement)

	writeUleb128(ctxt.Out, 1) // number of element segments

	writeUleb128(ctxt.Out, 0) // tableidx
	writeI32Const(ctxt.Out, funcValueOffset)
	ctxt.Out.WriteByte(0x0b) // end

	writeUleb128(ctxt.Out, numFns) // number of entries
	for i := uint64(0); i < numFns; i++ {
		writeUleb128(ctxt.Out, numImports+i)
	}

	writeSecSize(ctxt, sizeOffset)
}

// writeCodeSec writes the section that provides the function bodies for the functions
// declared by the "func" section.
func writeCodeSec(ctxt *ld.Link, fns []*wasmFunc) {
	sizeOffset := writeSecHeader(ctxt, sectionCode)

	contentsStart := ctxt.Out.Offset()
	writeUleb128(ctxt.Out, uint64(len(fns))) // number of code entries
	for _, fn := range fns {
		writeUleb128(ctxt.Out, uint64(len(fn.Code)))
		// If DWARF is enabled, check that the body lands exactly at the
		// code-section offset recorded as its DWARF address.
		if fn.Sym != 0 {
			if want, ok := ld.WasmCodeOffset(fn.Sym); ok && want != ctxt.Out.Offset()-contentsStart {
				ld.Exitf("internal error: DWARF code offset mismatch for %s: DWARF has %#x, code section has %#x", fn.Name, want, ctxt.Out.Offset()-contentsStart)
			}
		}
		ctxt.Out.Write(fn.Code)
	}

	writeSecSize(ctxt, sizeOffset)
}

// writeDwarfSections emits the DWARF debug info as custom sections, one
// per DWARF section, named after it (".debug_info", ".debug_line", ...).
// This is the standard way of embedding DWARF in WebAssembly modules,
// understood by LLVM tooling and browser devtools. Code addresses in
// the DWARF refer to byte offsets relative to the start of the code
// section's contents; see the "DWARF for WebAssembly" comment in
// ../ld/dwarf.go.
func writeDwarfSections(ctxt *ld.Link) {
	for _, sec := range ld.WasmDwarfSections(ctxt) {
		if len(sec.Data) == 0 {
			continue
		}
		sizeOffset := writeSecHeader(ctxt, sectionCustom)
		writeName(ctxt.Out, sec.Name)
		ctxt.Out.Write(sec.Data)
		writeSecSize(ctxt, sizeOffset)
	}
}

type dataSegment struct {
	offset int32
	data   []byte
}

// dataSegments computes the data segments of the module from the data
// sections collected by asmb: zero runs are omitted, so each segment
// carries only non-zero bytes plus the linear-memory offset they belong
// at.
func dataSegments() []*dataSegment {
	// Omit blocks of zeroes and instead emit data segments with offsets skipping the zeroes.
	// This reduces the size of the WebAssembly binary. We use 8 bytes as an estimate for the
	// overhead of adding a new segment (same as wasm-opt's memory-packing optimization uses).
	const segmentOverhead = 8

	// Generate at most this many segments. A higher number of segments gets rejected by some WebAssembly runtimes.
	const maxNumSegments = 100000

	var segments []*dataSegment
	for secIndex, ds := range dataSects {
		data := ds.data
		offset := int32(ds.sect.Vaddr)

		// skip leading zeroes
		for len(data) > 0 && data[0] == 0 {
			data = data[1:]
			offset++
		}

		for len(data) > 0 {
			dataLen := int32(len(data))
			var segmentEnd, zeroEnd int32
			if len(segments)+(len(dataSects)-secIndex) == maxNumSegments {
				segmentEnd = dataLen
				zeroEnd = dataLen
			} else {
				for {
					// look for beginning of zeroes
					for segmentEnd < dataLen && data[segmentEnd] != 0 {
						segmentEnd++
					}
					// look for end of zeroes
					zeroEnd = segmentEnd
					for zeroEnd < dataLen && data[zeroEnd] == 0 {
						zeroEnd++
					}
					// emit segment if omitting zeroes reduces the output size
					if zeroEnd-segmentEnd >= segmentOverhead || zeroEnd == dataLen {
						break
					}
					segmentEnd = zeroEnd
				}
			}

			segments = append(segments, &dataSegment{
				offset: offset,
				data:   data[:segmentEnd],
			})
			data = data[zeroEnd:]
			offset += zeroEnd
		}
	}

	return segments
}

// writeDataCountSec writes the section that declares the number of data
// segments ahead of the code section, so that memory.init/data.drop
// instructions (used by the synthetic _initmem function of GOWASM=threads
// modules) can be validated in a single pass. It is only emitted under
// GOWASM=threads; ordinary modules do not use bulk-memory instructions.
func writeDataCountSec(ctxt *ld.Link, numSegments int) {
	sizeOffset := writeSecHeader(ctxt, sectionDataCount)
	writeUleb128(ctxt.Out, uint64(numSegments))
	writeSecSize(ctxt, sizeOffset)
}

// writeDataSec writes the section that provides data that will be used to initialize the linear memory.
//
// Ordinarily the segments are active: the wasm runtime applies them to the
// linear memory on every instantiation. Under GOWASM=threads that would be
// wrong - the linear memory is shared, and instantiating a worker instance
// against it would clobber the live heap and runtime state of the main
// instance - so the segments are emitted as passive instead, and only the
// first (main) instantiation applies them by calling the synthetic
// exported _initmem function (wasm_exec.js does this in Go.run; worker
// instances must never call it). See threadsSyntheticFuncs.
func writeDataSec(ctxt *ld.Link, segments []*dataSegment) {
	sizeOffset := writeSecHeader(ctxt, sectionData)

	writeUleb128(ctxt.Out, uint64(len(segments))) // number of data entries
	for _, seg := range segments {
		if buildcfg.GOWASM.Threads {
			writeUleb128(ctxt.Out, 1) // passive segment
		} else {
			writeUleb128(ctxt.Out, 0) // active segment, memidx 0
			writeI32Const(ctxt.Out, seg.offset)
			ctxt.Out.WriteByte(0x0b) // end
		}
		writeUleb128(ctxt.Out, uint64(len(seg.data)))
		ctxt.Out.Write(seg.data)
	}

	writeSecSize(ctxt, sizeOffset)
}

// threadsSyntheticFuncs builds the synthetic functions the linker appends
// to a GOWASM=threads module, in function index order following the Go
// functions. There are exactly ld.WasmThreadsNumSyntheticFuncs of them
// (the DWARF code offsets account for the widened function count field):
//
//   - _initmem applies the module's passive data segments to the shared
//     linear memory (memory.init) and then drops them (data.drop). The
//     host must call it exactly once, on the first (main) instance,
//     before any Go code runs; worker instances sharing the memory must
//     never call it. This is the "JS tells the instance" init-gating
//     model (emscripten uses the same approach).
//
//   - wasm_probe_atomic_add(addr, delta uint32) uint32 performs a single
//     seq-cst i32.atomic.rmw.add (a real 0xFE threads-proposal
//     instruction) on the shared memory and returns the old value. It
//     runs without any Go runtime state, so a worker instance can call it
//     before the runtime is thread-aware; the wasm-threads pool demo and
//     tests use it to prove cross-instance atomic visibility. It will be
//     superseded by real scheduler integration in a later phase.
func threadsSyntheticFuncs(types *[]*wasmFuncType, segments []*dataSegment) []*wasmFunc {
	initmem := new(bytes.Buffer)
	writeUleb128(initmem, 0) // no locals
	for i, seg := range segments {
		writeI32Const(initmem, seg.offset)           // destination address
		writeI32Const(initmem, 0)                    // source offset within the segment
		writeI32Const(initmem, int32(len(seg.data))) // length
		initmem.WriteByte(0xFC)                      // misc-op prefix
		writeUleb128(initmem, 0x08)                  // memory.init
		writeUleb128(initmem, uint64(i))             // dataidx
		initmem.WriteByte(0x00)                      // memidx
	}
	for i := range segments {
		initmem.WriteByte(0xFC)          // misc-op prefix
		writeUleb128(initmem, 0x09)      // data.drop
		writeUleb128(initmem, uint64(i)) // dataidx
	}
	initmem.WriteByte(0x0b) // end

	probe := new(bytes.Buffer)
	writeUleb128(probe, 0) // no locals
	// The caller-supplied address may lie in memory another thread grew,
	// and this instance's ATOMIC bounds check may not have observed that
	// grow yet (see writeGrowEpochGuard in cmd/internal/obj/wasm). The
	// probe runs without Go runtime state, so instead of the epoch guard
	// it resyncs unconditionally with memory.grow 0 - cheap, and the
	// probe is a test-only diagnostic.
	probe.WriteByte(0x41)  // i32.const
	probe.WriteByte(0x00)  // 0
	probe.WriteByte(0x40)  // memory.grow
	probe.WriteByte(0x00)  // memidx
	probe.WriteByte(0x1a)  // drop
	probe.WriteByte(0x20)  // local.get
	writeUleb128(probe, 0) // 0: addr
	probe.WriteByte(0x20)  // local.get
	writeUleb128(probe, 1) // 1: delta
	probe.WriteByte(0xFE)  // atomic (threads-proposal) prefix
	probe.WriteByte(0x1E)  // i32.atomic.rmw.add
	writeUleb128(probe, 2) // memarg alignment 2^2 = 4 bytes (natural, required)
	writeUleb128(probe, 0) // memarg offset
	probe.WriteByte(0x0b)  // end

	return []*wasmFunc{
		{Name: "_initmem", Type: lookupType(&wasmFuncType{}, types), Code: initmem.Bytes()},
		{Name: "wasm_probe_atomic_add", Type: lookupType(&wasmFuncType{Params: []byte{I32, I32}, Results: []byte{I32}}, types), Code: probe.Bytes()},
	}
}

// writeProducerSec writes an optional section that reports the source language and compiler version.
func writeProducerSec(ctxt *ld.Link) {
	sizeOffset := writeSecHeader(ctxt, sectionCustom)
	writeName(ctxt.Out, "producers")

	writeUleb128(ctxt.Out, 2) // number of fields

	writeName(ctxt.Out, "language")       // field name
	writeUleb128(ctxt.Out, 1)             // number of values
	writeName(ctxt.Out, "Go")             // value: name
	writeName(ctxt.Out, buildcfg.Version) // value: version

	writeName(ctxt.Out, "processed-by")   // field name
	writeUleb128(ctxt.Out, 1)             // number of values
	writeName(ctxt.Out, "Go cmd/compile") // value: name
	writeName(ctxt.Out, buildcfg.Version) // value: version

	writeSecSize(ctxt, sizeOffset)
}

var nameRegexp = regexp.MustCompile(`[^\w.]`)

// writeNameSec writes an optional section that assigns names to the functions declared by the "func" section.
// The names are only used by WebAssembly stack traces, debuggers and decompilers.
// TODO(neelance): add symbol table of DATA symbols
func writeNameSec(ctxt *ld.Link, firstFnIndex int, fns []*wasmFunc) {
	sizeOffset := writeSecHeader(ctxt, sectionCustom)
	writeName(ctxt.Out, "name")

	sizeOffset2 := writeSecHeader(ctxt, 0x01) // function names
	writeUleb128(ctxt.Out, uint64(len(fns)))
	for i, fn := range fns {
		writeUleb128(ctxt.Out, uint64(firstFnIndex+i))
		writeName(ctxt.Out, fn.Name)
	}
	writeSecSize(ctxt, sizeOffset2)

	writeSecSize(ctxt, sizeOffset)
}

type nameWriter interface {
	io.ByteWriter
	io.Writer
}

func writeI32Const(w io.ByteWriter, v int32) {
	w.WriteByte(0x41) // i32.const
	writeSleb128(w, int64(v))
}

func writeI64Const(w io.ByteWriter, v int64) {
	w.WriteByte(0x42) // i64.const
	writeSleb128(w, v)
}

func writeName(w nameWriter, name string) {
	writeUleb128(w, uint64(len(name)))
	w.Write([]byte(name))
}

func writeUleb128(w io.ByteWriter, v uint64) {
	if v < 128 {
		w.WriteByte(uint8(v))
		return
	}
	more := true
	for more {
		c := uint8(v & 0x7f)
		v >>= 7
		more = v != 0
		if more {
			c |= 0x80
		}
		w.WriteByte(c)
	}
}

func writeUleb128FixedLength(w io.ByteWriter, v uint64, length int) {
	for i := 0; i < length; i++ {
		c := uint8(v & 0x7f)
		v >>= 7
		if i < length-1 {
			c |= 0x80
		}
		w.WriteByte(c)
	}
	if v != 0 {
		panic("writeUleb128FixedLength: length too small")
	}
}

func writeSleb128(w io.ByteWriter, v int64) {
	more := true
	for more {
		c := uint8(v & 0x7f)
		s := uint8(v & 0x40)
		v >>= 7
		more = !((v == 0 && s == 0) || (v == -1 && s != 0))
		if more {
			c |= 0x80
		}
		w.WriteByte(c)
	}
}

// writeSleb128FixedLength writes v as an SLEB128 encoding of exactly
// length bytes, padding with redundant continuation bytes, or reports
// an error if v does not fit. For non-negative values the result is
// bit-identical to a padded ULEB128 of the same length, so it is also
// valid for the unsigned fields (call target indices) the linker
// relocates.
func writeSleb128FixedLength(w io.ByteWriter, v int64, length int) error {
	max := int64(1) << (7*length - 1)
	if v < -max || v >= max {
		return fmt.Errorf("value %d does not fit in %d LEB128 bytes", v, length)
	}
	for i := 0; i < length-1; i++ {
		w.WriteByte(uint8(v&0x7f) | 0x80)
		v >>= 7
	}
	w.WriteByte(uint8(v & 0x7f))
	return nil
}

func fieldsToTypes(fields []obj.WasmField) []byte {
	b := make([]byte, len(fields))
	for i, f := range fields {
		switch f.Type {
		case obj.WasmI32, obj.WasmPtr, obj.WasmBool:
			b[i] = I32
		case obj.WasmI64:
			b[i] = I64
		case obj.WasmF32:
			b[i] = F32
		case obj.WasmF64:
			b[i] = F64
		default:
			panic(fmt.Sprintf("fieldsToTypes: unknown field type: %d", f.Type))
		}
	}
	return b
}

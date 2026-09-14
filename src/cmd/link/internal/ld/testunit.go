// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"

	"cmd/link/internal/loader"
)

// A binary can hold the tests of several packages, and the go command keys
// each package's cached test result on the code that package's tests run. The
// binary as a whole is the wrong scope: every package in it shares those
// bytes, so one package's edit would discard every result. The right scope is
// what the tests reach, which is a question about the symbol graph and is
// answerable only here.
//
// -testunits names a file describing the roots of each package's tests, and
// -testunitdigest names the file this writes: one line per package, holding
// the package and a digest of every symbol reachable from its roots.
//
// The digest covers each reachable symbol's name, kind, size, content and
// relocation targets, combined with exclusive-or so that the order symbols
// are visited in does not reach the result. A symbol appears once in a set,
// so nothing cancels.
//
// Reachability here follows every relocation, weak ones included, and knows
// nothing of the method and interface pruning the dead code pass does. That
// makes it a superset of what survives the link, which is the safe direction:
// a digest may cover code the binary drops, which costs a test run that was
// not needed, and it never misses code the tests do run, which would serve a
// stale result.
type testUnitRoots struct {
	id    string
	roots []string
}

// readTestUnits reads the roots file -testunits names. Each line is one of
// "barrier <symbol>", "unit <id>" or "root <symbol>", and each root belongs
// to the unit named above it.
//
// A barrier symbol is neither traversed nor covered. The generated main's
// table of units names every package's test functions, so a walk through it
// reaches all of them, and its content changes whenever any package in the
// binary does. Each unit's own roots are named here instead.
func readTestUnits(path string) (barriers []string, units []testUnitRoots, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	scan := bufio.NewScanner(file)
	scan.Buffer(nil, 1<<20)
	for line := 1; scan.Scan(); line++ {
		text := strings.TrimSpace(scan.Text())
		if text == "" {
			continue
		}
		verb, arg, ok := strings.Cut(text, " ")
		if !ok {
			return nil, nil, fmt.Errorf("%s:%d: no argument for %q", path, line, verb)
		}
		switch verb {
		case "barrier":
			barriers = append(barriers, arg)
		case "unit":
			units = append(units, testUnitRoots{id: arg})
		case "root":
			if len(units) == 0 {
				return nil, nil, fmt.Errorf("%s:%d: root before any unit", path, line)
			}
			units[len(units)-1].roots = append(units[len(units)-1].roots, arg)
		default:
			return nil, nil, fmt.Errorf("%s:%d: unknown verb %q", path, line, verb)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, nil, err
	}
	return barriers, units, nil
}

// testUnitDigests writes a digest of the code each unit's tests reach. It runs
// before the dead code pass, over the whole symbol graph, because a unit's
// roots reach code another unit's do not and the pass keeps the union.
func testUnitDigests(ctxt *Link) {
	if *flagTestUnits == "" {
		return
	}
	if *flagTestUnitDigest == "" {
		Exitf("-testunits given without -testunitdigest")
	}
	barrierNames, units, err := readTestUnits(*flagTestUnits)
	if err != nil {
		Exitf("reading -testunits: %v", err)
	}
	ldr := ctxt.loader

	blocked := make(map[loader.Sym]bool)
	for _, name := range barrierNames {
		for _, sym := range lookupBothABIs(ldr, name) {
			blocked[sym] = true
		}
	}

	// A unit's digest is built one chunk of 64 units at a time, so that one
	// walk of the graph serves 64 of them: a symbol carries the bits of the
	// units that reach it.
	digests := make(map[string]string, len(units))
	for start := 0; start < len(units); start += 64 {
		end := min(start+64, len(units))
		chunk := units[start:end]
		reach := testUnitReach(ctxt, chunk, blocked)
		sums := make([][sha256.Size]byte, len(chunk))
		counts := make([]uint64, len(chunk))
		for idx := 1; idx < ldr.NSym(); idx++ {
			sym := loader.Sym(idx)
			mask := reach[sym]
			if mask == 0 {
				continue
			}
			sum := testUnitSymbolDigest(ldr, sym)
			for bit := 0; bit < len(chunk); bit++ {
				if mask&(1<<uint(bit)) == 0 {
					continue
				}
				for pos := range sum {
					sums[bit][pos] ^= sum[pos]
				}
				counts[bit]++
			}
		}
		for bit, unit := range chunk {
			whole := sha256.New()
			fmt.Fprintf(whole, "testunit %s %d\n", unit.id, counts[bit])
			whole.Write(sums[bit][:])
			digests[unit.id] = hex.EncodeToString(whole.Sum(nil))
		}
	}

	ids := make([]string, 0, len(digests))
	for id := range digests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&out, "%s %s\n", id, digests[id])
	}
	if err := os.WriteFile(*flagTestUnitDigest, []byte(out.String()), 0666); err != nil {
		Exitf("writing -testunitdigest: %v", err)
	}
}

// testUnitReach answers, for every symbol, the units of chunk whose roots
// reach it. Bit n of the answer belongs to chunk[n].
func testUnitReach(ctxt *Link, chunk []testUnitRoots, blocked map[loader.Sym]bool) []uint64 {
	ldr := ctxt.loader
	reach := make([]uint64, ldr.NSym())
	var work []loader.Sym
	add := func(sym loader.Sym, mask uint64) {
		if sym == 0 || blocked[sym] {
			return
		}
		if reach[sym]&mask == mask {
			return
		}
		reach[sym] |= mask
		work = append(work, sym)
	}

	// Program startup is what every unit's tests run on: the runtime, the
	// testing package and whatever starts them. It is the same code for each
	// unit, and a change in it reruns them all.
	common := uint64(0)
	for bit := range chunk {
		common |= 1 << uint(bit)
	}
	for _, name := range testUnitCommonRoots(ctxt) {
		for _, sym := range lookupBothABIs(ldr, name) {
			add(sym, common)
		}
	}
	for bit, unit := range chunk {
		for _, name := range unit.roots {
			for _, sym := range lookupBothABIs(ldr, name) {
				add(sym, 1<<uint(bit))
			}
		}
	}

	for len(work) > 0 {
		sym := work[len(work)-1]
		work = work[:len(work)-1]
		mask := reach[sym]
		relocs := ldr.Relocs(sym)
		for idx := 0; idx < relocs.Count(); idx++ {
			add(relocs.At(idx).Sym(), mask)
		}
		if outer := ldr.OuterSym(sym); outer != 0 {
			add(outer, mask)
		}
	}
	return reach
}

// testUnitCommonRoots names the symbols program startup itself reaches, which
// belong to every unit in the binary.
func testUnitCommonRoots(ctxt *Link) []string {
	names := []string{"main.main", "main..inittask", "runtime.unreachableMethod"}
	if *flagEntrySymbol != "" {
		names = append(names, *flagEntrySymbol)
	}
	if ctxt.mainInittasks != 0 {
		names = append(names, ctxt.loader.SymName(ctxt.mainInittasks))
	}
	return names
}

// lookupBothABIs answers the symbols named name in either ABI, because a
// function is reached by its internal-ABI symbol and data by version 0.
func lookupBothABIs(ldr *loader.Loader, name string) []loader.Sym {
	var out []loader.Sym
	if sym := ldr.Lookup(name, 0); sym != 0 {
		out = append(out, sym)
	}
	if abiInternalVer != 0 {
		if sym := ldr.Lookup(name, abiInternalVer); sym != 0 {
			out = append(out, sym)
		}
	}
	return out
}

// testUnitSymbolDigest answers a digest of what a symbol contributes to a
// program: its identity, its kind, its bytes, and the symbols its relocations
// name. Relocation targets go in by name rather than by index, so the digest
// does not move when an unrelated symbol is added or dropped.
func testUnitSymbolDigest(ldr *loader.Loader, sym loader.Sym) [sha256.Size]byte {
	hash := sha256.New()
	fmt.Fprintf(hash, "sym %s %d %d %d\n", ldr.SymName(sym), ldr.SymVersion(sym), ldr.SymType(sym), ldr.SymSize(sym))
	hash.Write(ldr.Data(sym))
	relocs := ldr.Relocs(sym)
	for idx := 0; idx < relocs.Count(); idx++ {
		rel := relocs.At(idx)
		target := ""
		if rel.Sym() != 0 {
			target = ldr.SymName(rel.Sym())
		}
		fmt.Fprintf(hash, "rel %d %d %d %d %s\n", rel.Off(), rel.Siz(), rel.Type(), rel.Add(), target)
	}
	var out [sha256.Size]byte
	copy(out[:], hash.Sum(nil))
	return out
}

// Command pooldemo is the Go side of the GOWASM=threads worker-pool demo
// (threads phase B1). Driven by ../pool_demo.js under Node.js:
//
//  1. This program boots normally on the main instance (passive data
//     segments applied by the linker-emitted _initmem export, which
//     wasm_exec.js calls in Go.run), prints, fills a 1 MiB heap buffer,
//     mutates package-level initialized data (so a re-applied active data
//     segment would revert it), and checksums both.
//  2. It publishes the address of a shared counter cell via the
//     __demoReady JS callback and parks.
//  3. The JS driver spawns N worker instances against the same shared
//     memory; each hammers the counter through the linker-synthesized
//     wasm_probe_atomic_add export (a wasm i32.atomic.rmw.add - a real
//     0xFE threads-proposal instruction, not JS Atomics).
//  4. The driver calls __goFinishDemo(expected); this program reads the
//     counter with atomic.LoadUint32 (an i32.atomic.load on the main
//     instance) and re-checksums its state. The counter must equal the
//     exact expected sum (cross-instance atomic visibility) and the
//     checksum must be unchanged (worker instantiation wrote nothing to
//     the shared memory - passive segments + init gating work).
package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"sync/atomic"
	"syscall/js"
	"unsafe"
)

// counter is the shared cell the worker instances hammer. A package-level
// var has a stable address for the lifetime of the program, so its
// address can be handed to the workers.
var counter uint32

// seg is package-level initialized data: its initial bytes live in a data
// segment of the wasm binary. main mutates it before the workers spawn;
// if worker instantiation re-applied the data segments (i.e. if they were
// still active), the mutation would be reverted and the checksum would
// change.
var seg = [16]uint32{
	0x243F6A88, 0x85A308D3, 0x13198A2E, 0x03707344,
	0xA4093822, 0x299F31D0, 0x082EFA98, 0xEC4E6C89,
	0x452821E6, 0x38D01377, 0xBE5466CF, 0x34E90C6C,
	0xC0AC29B7, 0xC97C50DD, 0x3F84D5B5, 0xB5470917,
}

// checksum covers the heap buffer and the (mutated) initialized data.
func checksum(heap []byte) uint32 {
	h := crc32.NewIEEE()
	h.Write(heap)
	var b [4]byte
	for _, v := range seg {
		binary.LittleEndian.PutUint32(b[:], v)
		h.Write(b[:])
	}
	return h.Sum32()
}

func main() {
	fmt.Println("pooldemo: Go main is running on the primary instance")

	// 1 MiB of pseudorandom heap data (xorshift32).
	heap := make([]byte, 1<<20)
	x := uint32(0x9E3779B9)
	for i := range heap {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		heap[i] = byte(x)
	}
	// Mutate the segment-resident initialized data.
	for i := range seg {
		seg[i] ^= 0xA5A5A5A5
	}
	before := checksum(heap)
	fmt.Printf("pooldemo: Go state checksum before worker spawn: %#08x\n", before)

	status := make(chan int, 1)
	finish := js.FuncOf(func(this js.Value, args []js.Value) any {
		expected := uint32(args[0].Int())
		got := atomic.LoadUint32(&counter) // i32.atomic.load on the main instance
		after := checksum(heap)
		code := 0
		if got == expected {
			fmt.Printf("pooldemo: shared counter = %d, expected %d - worker atomic ops all visible: OK\n", got, expected)
		} else {
			fmt.Printf("pooldemo: FAIL: shared counter = %d, expected %d\n", got, expected)
			code = 1
		}
		if after == before {
			fmt.Printf("pooldemo: Go state checksum after workers:  %#08x - heap and data segments not clobbered: OK\n", after)
		} else {
			fmt.Printf("pooldemo: FAIL: checksum changed %#08x -> %#08x: worker instantiation clobbered the shared memory\n", before, after)
			code = 1
		}
		status <- code
		return nil
	})
	defer finish.Release()
	js.Global().Set("__goFinishDemo", finish)
	js.Global().Get("__demoReady").Invoke(int64(uintptr(unsafe.Pointer(&counter))))

	code := <-status
	if code == 0 {
		fmt.Println("POOLDEMO: PASS")
	} else {
		fmt.Println("POOLDEMO: FAIL")
	}
	os.Exit(code)
}

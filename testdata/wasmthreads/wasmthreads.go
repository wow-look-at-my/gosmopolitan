// wasmthreads is the GOWASM=threads CI smoke program: it hammers
// sync/atomic, sync.Mutex, sync.Once, and channels, and checks every
// result, so a miscompiled 0xFE atomic op fails loudly. The runtime is
// still single-threaded with GOWASM=threads (toolchain-only phase); this
// exercises the threads-proposal instructions and the imported shared
// memory, not real parallelism.
package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

const (
	goroutines = 16
	iters      = 20000
)

func main() {
	bad := false
	check := func(name string, got, want any) {
		if got != want {
			fmt.Printf("FAIL %s: got %v want %v\n", name, got, want)
			bad = true
		} else {
			fmt.Printf("ok   %s = %v\n", name, got)
		}
	}

	// sync/atomic: Add/Load/Store/Swap/CAS on 32 and 64 bit.
	var (
		a32 atomic.Int32
		a64 atomic.Int64
		u32 atomic.Uint32
		u64 atomic.Uint64
		b   atomic.Bool
		p   atomic.Pointer[int]
		wg  sync.WaitGroup
	)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				a32.Add(1)
				a64.Add(3)
				u32.Add(2)
				u64.Add(4)
			}
		}()
	}
	wg.Wait()
	check("Int32.Add", a32.Load(), int32(goroutines*iters))
	check("Int64.Add", a64.Load(), int64(3*goroutines*iters))
	check("Uint32.Add", u32.Load(), uint32(2*goroutines*iters))
	check("Uint64.Add", u64.Load(), uint64(4*goroutines*iters))

	// Swap and CompareAndSwap.
	casOK := 0
	for i := 0; i < 1000; i++ {
		old := u64.Load()
		if u64.CompareAndSwap(old, old+1) {
			casOK++
		}
	}
	check("Uint64.CAS successes", casOK, 1000)
	prev := a32.Swap(-7)
	check("Int32.Swap prev", prev, int32(goroutines*iters))
	check("Int32.Swap new", a32.Load(), int32(-7))

	b.Store(true)
	check("Bool.Store/Load", b.Load(), true)
	v := 42
	p.Store(&v)
	check("Pointer deref", *p.Load(), 42)

	// sync.Mutex counter.
	var (
		mu  sync.Mutex
		cnt int
	)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				mu.Lock()
				cnt++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	check("Mutex counter", cnt, goroutines*iters)

	// Channels: ping-pong pipeline sum.
	ch := make(chan int, 64)
	done := make(chan int64)
	go func() {
		var sum int64
		for x := range ch {
			sum += int64(x)
		}
		done <- sum
	}()
	var want int64
	for i := 0; i < 100000; i++ {
		ch <- i
		want += int64(i)
	}
	close(ch)
	check("channel sum", <-done, want)

	// sync.Once + WaitGroup under contention.
	var once sync.Once
	onceRuns := 0
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			once.Do(func() { onceRuns++ })
		}()
	}
	wg.Wait()
	check("sync.Once runs", onceRuns, 1)

	if bad {
		fmt.Println("ATOMICS: FAIL")
		os.Exit(1)
	}
	fmt.Println("ATOMICS: PASS")
}

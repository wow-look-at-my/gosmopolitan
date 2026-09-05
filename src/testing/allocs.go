// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"os"
	"runtime"
)

// allocsFork asks the calling test for a process of its own. AllocsPerRun
// panics with it when other tests share this process, and tRunner recovers it
// and forks, so the measurement re-runs alone in the child. AllocsPerRun takes
// no *T, so this panic is how it reaches the test that called it.
//
// Anywhere the panic does not reach tRunner - a recover in the caller, or a
// goroutine that is not a test - it prints this message, and the advice is the
// same.
type allocsFork struct{}

func (allocsFork) Error() string {
	return "testing: AllocsPerRun measures the whole process, and tests are parallel by default in this fork; call t.Fork or t.Serial first"
}

// allocsIndependent reports whether the caller already has this process to
// itself. A forked child runs one test, a serial test stops every other one,
// and a caller with no parallel test in flight - a benchmark, a root test -
// shares the process with nothing.
func allocsIndependent() bool {
	return os.Getenv(forkTargetEnv) != "" ||
		serialExclusive.Load() ||
		parallelStart.Load() == parallelStop.Load()
}

// AllocsPerRun returns the average number of allocations during calls to f.
// Although the return value has type float64, it will always be an integral value.
//
// To compute the number of allocations, the function will first be run once as
// a warm-up. The average number of allocations over the specified number of
// runs will then be measured and returned.
//
// AllocsPerRun sets [runtime.GOMAXPROCS] to 1 during its measurement and will restore
// it before returning.
//
// The count and the GOMAXPROCS change are process-wide, and tests are parallel
// by default, so a caller that shares this process with other tests gets one of
// its own: AllocsPerRun forks the test the way [T.Fork] does, and the child
// re-runs that test alone from the top. A test that already called [T.Fork] or
// [T.Serial] measures here.
func AllocsPerRun(runs int, f func()) (avg float64) {
	if !allocsIndependent() {
		panic(allocsFork{})
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	// Warm up the function
	f()

	// Measure the starting statistics
	var memstats runtime.MemStats
	runtime.ReadMemStats(&memstats)
	mallocs := 0 - memstats.Mallocs

	// Run the function the specified number of times
	for i := 0; i < runs; i++ {
		f()
	}

	// Read the final statistics
	runtime.ReadMemStats(&memstats)
	mallocs += memstats.Mallocs

	// Average the mallocs over the runs (not counting the warm-up).
	// We are forced to return a float64 because the API is silly, but do
	// the division as integers so we can ask if AllocsPerRun()==1
	// instead of AllocsPerRun()<2.
	return float64(mallocs / uint64(runs))
}

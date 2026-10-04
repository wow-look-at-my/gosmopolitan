// cgoprobe runs C code compiled into a cosmo APE. Each line names one check.
// A check that fails prints FAIL and the program exits 1.
package main

/*
#include "probe.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

//export goTriple
func goTriple(value C.int) C.int {
	return value * 3
}

func main() {
	failed := false
	check := func(name string, good bool, detail string) {
		state := "ok"
		if !good {
			state = "FAIL"
			failed = true
		}
		fmt.Printf("%s %s: %s\n", state, name, detail)
	}

	sum := C.probe_add(2, 3)
	check("add", sum == 5, fmt.Sprint(sum))

	word := C.CString("hello")
	count := C.probe_printf(word)
	check("printf", count == 19, fmt.Sprint(count))

	ret, err := C.probe_errno()
	check("errno", ret == -1 && errors.Is(err, syscall.ENOENT), fmt.Sprint(ret, " ", err))

	back := C.probe_callback(7)
	check("callback", back == 22, fmt.Sprint(back))

	threaded := C.probe_thread(5)
	check("thread", threaded == 15, fmt.Sprint(threaded))

	// Many goroutines in C and back force new threads, which the runtime starts through pthread_create.
	var group sync.WaitGroup
	var bad atomic.Int32
	for worker := range 32 {
		group.Go(func() {
			for round := range 200 {
				if C.probe_callback(C.int(worker+round)) != C.int(3*(worker+round)+1) {
					bad.Add(1)
				}
				if round%50 == 0 {
					runtime.GC()
				}
			}
		})
	}
	group.Wait()
	check("concurrent", bad.Load() == 0, fmt.Sprint(bad.Load(), " wrong"))

	// Only libcosmo's failing dlerror stub names cosmo_dlopen.
	var msg [512]C.char
	missing := C.probe_dlopen_missing(&msg[0], C.int(len(msg)))
	text := C.GoString(&msg[0])
	check("dlerror", missing == 0 && text != "" && !strings.Contains(text, "cosmo_dlopen"), text)

	// "dlopen LIB SYM" also loads a host library, which needs a host that has one.
	if len(os.Args) == 4 && os.Args[1] == "dlopen" {
		lib, sym := C.CString(os.Args[2]), C.CString(os.Args[3])
		got := C.probe_dlopen(lib, sym, &msg[0], C.int(len(msg)))
		check("dlopen", got == 0, fmt.Sprint(got, " ", C.GoString(&msg[0])))
	}

	fmt.Printf("arch %s\n", runtime.GOARCH)
	if failed {
		os.Exit(1)
	}
}

// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"sync"
	"testing"
)

type response struct {
}

type myError struct {
}

func (myError) Error() string { return "" }

type async struct {
	resp *response
	err  error
}

// sendRequests sends count fresh requests on ch, through a select when
// useSelect is set. Done is never ready, so every select sends.
func sendRequests(ch chan<- *async, done <-chan struct{}, useSelect bool, count int) {
	for range count {
		if useSelect {
			select {
			case ch <- &async{resp: nil, err: myError{}}:
			case <-done:
			}
			continue
		}
		ch <- &async{resp: nil, err: myError{}}
	}
}

func TestChanSendSelectBarrier(t *testing.T) {
	t.Parallel()
	testChanSendBarrier(true)
}

func TestChanSendBarrier(t *testing.T) {
	t.Parallel()
	testChanSendBarrier(false)
}

func testChanSendBarrier(useSelect bool) {
	var wg sync.WaitGroup
	outer := 100
	inner := 100000
	if testing.Short() || runtime.GOARCH == "wasm" {
		outer = 10
		inner = 1000
	}
	// Each worker hands inner requests across one unbuffered channel. A
	// handoff either wakes the parked receiver or takes from the parked
	// sender, so both direct copies run with a fresh value every time.
	for range outer {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := make(chan *async)
			done := make(chan struct{})
			go sendRequests(ch, done, useSelect, inner)
			var garbage []byte
			for range inner {
				req := <-ch
				runtime.Gosched()
				if _, ok := req.err.(myError); !ok {
					panic(1)
				}
				garbage = makeByte()
			}
			_ = garbage
		}()
	}
	wg.Wait()
}

//go:noinline
func makeByte() []byte {
	return make([]byte, 1<<10)
}

// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package js

import (
	"internal/synctest"
	"sync"
)

var (
	funcsMu    sync.Mutex
	funcs             = make(map[uint32]func(Value, []Value) any)
	nextFuncID uint32 = 1
)

// Func is a wrapped Go function to be called by JavaScript.
type Func struct {
	Value  // the JavaScript function that invokes the Go function
	bubble *synctest.Bubble
	id     uint32
}

// FuncOf returns a function to be used by JavaScript.
//
// The Go function fn is called with the value of JavaScript's "this" keyword and the
// arguments of the invocation. The return value of the invocation is
// the result of the Go function mapped back to JavaScript according to ValueOf.
//
// Invoking the wrapped Go function from JavaScript will
// pause the event loop and spawn a new goroutine.
// Other wrapped functions which are triggered during a call from Go to JavaScript
// get executed on the same goroutine.
//
// As a consequence, if one wrapped function blocks, JavaScript's event loop
// is blocked until that function returns. Hence, calling any async JavaScript
// API, which requires the event loop, like fetch (http.Client), will cause an
// immediate deadlock. Therefore a blocking function should explicitly start a
// new goroutine.
//
// Func.Release must be called to free up resources when the function will not be invoked any more.
func FuncOf(fn func(this Value, args []Value) any) Func {
	funcsMu.Lock()
	id := nextFuncID
	nextFuncID++
	bubble := synctest.Acquire()
	if bubble != nil {
		origFn := fn
		fn = func(this Value, args []Value) any {
			var r any
			bubble.Run(func() {
				r = origFn(this, args)
			})
			return r
		}
	}
	funcs[id] = fn
	funcsMu.Unlock()
	return Func{
		id:     id,
		bubble: bubble,
		Value:  jsGo.Call("_makeFuncWrapper", id),
	}
}

// Release frees up resources allocated for the function.
// The function must not be invoked after calling Release.
// It is allowed to call Release while the function is still running.
func (c Func) Release() {
	c.bubble.Release()
	funcsMu.Lock()
	delete(funcs, c.id)
	funcsMu.Unlock()
}

// setEventHandler is defined in the runtime package.
func setEventHandler(fn func() bool)

// deadlockProbe is defined in the runtime package.
func deadlockProbe()

func init() {
	setEventHandler(handleEvent)
}

// handleEvent retrieves the pending event (window._pendingEvent) and calls the js.Func on it.
// It returns true if an event was handled.
func handleEvent() bool {
	// Retrieve the event from js
	cb := jsGo.Get("_pendingEvent")
	if cb.IsNull() {
		return false
	}
	jsGo.Set("_pendingEvent", Null())

	id := uint32(cb.Get("id").Int())
	if id == 0 { // zero indicates deadlock
		// Tell the runtime this event is the environment's deadlock
		// probe rather than a user callback, then park inside the
		// event so checkdead reports the deadlock.
		deadlockProbe()
		select {}
	}

	// Retrieve the associated js.Func
	funcsMu.Lock()
	f, ok := funcs[id]
	funcsMu.Unlock()
	if !ok {
		Global().Get("console").Call("error", "call to released function")
		return true
	}

	// Call the js.Func with arguments
	this := cb.Get("this")
	argsObj := cb.Get("args")
	args := make([]Value, argsObj.Length())
	for i := range args {
		args[i] = argsObj.Index(i)
	}
	result := f(this, args)

	// Return the result to js
	cb.Set("result", result)
	return true
}

// Await blocks the calling goroutine until the JavaScript promise v settles,
// then returns how it settled.
//
// Await treats v the way JavaScript's Promise.resolve does: a non-promise,
// non-thenable value settles immediately with itself, so Await(v) then simply
// returns (v, nil).
//
// If the promise is fulfilled, Await returns the fulfilled value and a nil
// error. If the promise is rejected, Await returns Undefined() and an error
// of type Error whose Value field holds the rejection reason - a reason that
// is not an Error object (for example a plain thrown string) is reported by
// Error.Error as the reason's own string representation.
//
// While the goroutine waits, other goroutines and the JavaScript event loop
// keep running. Await must therefore never be called from within a function
// wrapped by FuncOf (or from any code that runs during a call from
// JavaScript into Go): the promise's callbacks can only fire once the
// JavaScript event loop regains control, and that cannot happen until every
// active call from JavaScript into Go has returned. Blocking there deadlocks
// the program; when nothing else is pending, the runtime reports it as
// "fatal error: all goroutines are asleep - deadlock!" with a note that a
// goroutine is blocked in a call from JavaScript. To use Await in reaction
// to a JavaScript callback, start a new goroutine from the callback and call
// Await on that goroutine instead.
func Await(v Value) (Value, error) {
	type settlement struct {
		value Value
		ok    bool
	}
	// Buffered, so the callback below can deliver the settlement and return
	// to the event loop without waiting for this goroutine to receive it.
	done := make(chan settlement, 1)
	settle := func(ok bool) Func {
		return FuncOf(func(this Value, args []Value) any {
			s := settlement{ok: ok} // the zero value is Undefined()
			if len(args) > 0 {
				s.value = args[0]
			}
			done <- s
			return nil
		})
	}
	onFulfilled := settle(true)
	defer onFulfilled.Release()
	onRejected := settle(false)
	defer onRejected.Release()

	promiseConstructor.Call("resolve", v).Call("then", onFulfilled, onRejected)
	s := <-done
	if !s.ok {
		return Undefined(), Error{Value: s.value}
	}
	return s.value, nil
}

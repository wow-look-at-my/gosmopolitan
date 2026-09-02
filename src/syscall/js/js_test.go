// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

// To run these tests:
//
// - Install Node
// - Add /path/to/go/lib/wasm to your $PATH (so that "go test" can find
//   "go_js_wasm_exec").
// - GOOS=js GOARCH=wasm go test
//
// See -exec in "go help test", and "go help run" for details.

package js_test

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"syscall/js"
	"testing"
)

var dummys = js.Global().Call("eval", `({
	someBool: true,
	someString: "abc\u1234",
	someInt: 42,
	someFloat: 42.123,
	someArray: [41, 42, 43],
	someDate: new Date(),
	add: function(a, b) {
		return a + b;
	},
	zero: 0,
	stringZero: "0",
	NaN: NaN,
	emptyObj: {},
	emptyArray: [],
	Infinity: Infinity,
	NegInfinity: -Infinity,
	objNumber0: new Number(0),
	objBooleanFalse: new Boolean(false),
})`)

//go:wasmimport _gotest add
func testAdd(uint32, uint32) uint32

func TestWasmImport(t *testing.T) {
	a := uint32(3)
	b := uint32(5)
	want := a + b
	if got := testAdd(a, b); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// testCallExport is imported from host (wasm_exec.js), which calls testExport.
//
//go:wasmimport _gotest callExport
func testCallExport(a int32, b int64) int64

//go:wasmexport testExport
func testExport(a int32, b int64) int64 {
	testExportCalled = true
	// test stack growth
	growStack(1000)
	// force a goroutine switch
	ch := make(chan int64)
	go func() {
		ch <- int64(a)
		ch <- b
	}()
	return <-ch + <-ch
}

//go:wasmexport testExport0
func testExport0() { // no arg or result (see issue 69584)
	runtime.GC()
}

var testExportCalled bool

func growStack(n int64) {
	if n > 0 {
		growStack(n - 1)
	}
}

func TestWasmExport(t *testing.T) {
	testExportCalled = false
	a := int32(123)
	b := int64(456)
	want := int64(a) + b
	if got := testCallExport(a, b); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if !testExportCalled {
		t.Error("testExport not called")
	}
}

func TestBool(t *testing.T) {
	want := true
	o := dummys.Get("someBool")
	if got := o.Bool(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	dummys.Set("otherBool", want)
	if got := dummys.Get("otherBool").Bool(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if !dummys.Get("someBool").Equal(dummys.Get("someBool")) {
		t.Errorf("same value not equal")
	}
}

func TestString(t *testing.T) {
	want := "abc\u1234"
	o := dummys.Get("someString")
	if got := o.String(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	dummys.Set("otherString", want)
	if got := dummys.Get("otherString").String(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if !dummys.Get("someString").Equal(dummys.Get("someString")) {
		t.Errorf("same value not equal")
	}

	if got, want := js.Undefined().String(), "<undefined>"; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got, want := js.Null().String(), "<null>"; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got, want := js.ValueOf(true).String(), "<boolean: true>"; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got, want := js.ValueOf(42.5).String(), "<number: 42.5>"; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got, want := js.Global().Call("Symbol").String(), "<symbol>"; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got, want := js.Global().String(), "<object>"; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got, want := js.Global().Get("setTimeout").String(), "<function>"; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestInt(t *testing.T) {
	want := 42
	o := dummys.Get("someInt")
	if got := o.Int(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	dummys.Set("otherInt", want)
	if got := dummys.Get("otherInt").Int(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if !dummys.Get("someInt").Equal(dummys.Get("someInt")) {
		t.Errorf("same value not equal")
	}
	if got := dummys.Get("zero").Int(); got != 0 {
		t.Errorf("got %#v, want %#v", got, 0)
	}
}

func TestIntConversion(t *testing.T) {
	testIntConversion(t, 0)
	testIntConversion(t, 1)
	testIntConversion(t, -1)
	testIntConversion(t, 1<<20)
	testIntConversion(t, -1<<20)
	testIntConversion(t, 1<<40)
	testIntConversion(t, -1<<40)
	testIntConversion(t, 1<<60)
	testIntConversion(t, -1<<60)
}

func testIntConversion(t *testing.T, want int) {
	if got := js.ValueOf(want).Int(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestFloat(t *testing.T) {
	want := 42.123
	o := dummys.Get("someFloat")
	if got := o.Float(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	dummys.Set("otherFloat", want)
	if got := dummys.Get("otherFloat").Float(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if !dummys.Get("someFloat").Equal(dummys.Get("someFloat")) {
		t.Errorf("same value not equal")
	}
}

func TestObject(t *testing.T) {
	if !dummys.Get("someArray").Equal(dummys.Get("someArray")) {
		t.Errorf("same value not equal")
	}

	// An object and its prototype should not be equal.
	proto := js.Global().Get("Object").Get("prototype")
	o := js.Global().Call("eval", "new Object()")
	if proto.Equal(o) {
		t.Errorf("object equals to its prototype")
	}
}

func TestFrozenObject(t *testing.T) {
	o := js.Global().Call("eval", "(function () { let o = new Object(); o.field = 5; Object.freeze(o); return o; })()")
	want := 5
	if got := o.Get("field").Int(); want != got {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestEqual(t *testing.T) {
	if !dummys.Get("someFloat").Equal(dummys.Get("someFloat")) {
		t.Errorf("same float is not equal")
	}
	if !dummys.Get("emptyObj").Equal(dummys.Get("emptyObj")) {
		t.Errorf("same object is not equal")
	}
	if dummys.Get("someFloat").Equal(dummys.Get("someInt")) {
		t.Errorf("different values are not unequal")
	}
}

func TestNaN(t *testing.T) {
	if !dummys.Get("NaN").IsNaN() {
		t.Errorf("JS NaN is not NaN")
	}
	if !js.ValueOf(math.NaN()).IsNaN() {
		t.Errorf("Go NaN is not NaN")
	}
	if dummys.Get("NaN").Equal(dummys.Get("NaN")) {
		t.Errorf("NaN is equal to NaN")
	}
}

func TestUndefined(t *testing.T) {
	if !js.Undefined().IsUndefined() {
		t.Errorf("undefined is not undefined")
	}
	if !js.Undefined().Equal(js.Undefined()) {
		t.Errorf("undefined is not equal to undefined")
	}
	if dummys.IsUndefined() {
		t.Errorf("object is undefined")
	}
	if js.Undefined().IsNull() {
		t.Errorf("undefined is null")
	}
	if dummys.Set("test", js.Undefined()); !dummys.Get("test").IsUndefined() {
		t.Errorf("could not set undefined")
	}
}

func TestNull(t *testing.T) {
	if !js.Null().IsNull() {
		t.Errorf("null is not null")
	}
	if !js.Null().Equal(js.Null()) {
		t.Errorf("null is not equal to null")
	}
	if dummys.IsNull() {
		t.Errorf("object is null")
	}
	if js.Null().IsUndefined() {
		t.Errorf("null is undefined")
	}
	if dummys.Set("test", js.Null()); !dummys.Get("test").IsNull() {
		t.Errorf("could not set null")
	}
	if dummys.Set("test", nil); !dummys.Get("test").IsNull() {
		t.Errorf("could not set nil")
	}
}

func TestLength(t *testing.T) {
	if got := dummys.Get("someArray").Length(); got != 3 {
		t.Errorf("got %#v, want %#v", got, 3)
	}
}

func TestGet(t *testing.T) {
	// positive cases get tested per type

	expectValueError(t, func() {
		dummys.Get("zero").Get("badField")
	})
}

func TestSet(t *testing.T) {
	// positive cases get tested per type

	expectValueError(t, func() {
		dummys.Get("zero").Set("badField", 42)
	})
}

func TestDelete(t *testing.T) {
	dummys.Set("test", 42)
	dummys.Delete("test")
	if dummys.Call("hasOwnProperty", "test").Bool() {
		t.Errorf("property still exists")
	}

	expectValueError(t, func() {
		dummys.Get("zero").Delete("badField")
	})
}

func TestIndex(t *testing.T) {
	if got := dummys.Get("someArray").Index(1).Int(); got != 42 {
		t.Errorf("got %#v, want %#v", got, 42)
	}

	expectValueError(t, func() {
		dummys.Get("zero").Index(1)
	})
}

func TestSetIndex(t *testing.T) {
	dummys.Get("someArray").SetIndex(2, 99)
	if got := dummys.Get("someArray").Index(2).Int(); got != 99 {
		t.Errorf("got %#v, want %#v", got, 99)
	}

	expectValueError(t, func() {
		dummys.Get("zero").SetIndex(2, 99)
	})
}

func TestCall(t *testing.T) {
	var i int64 = 40
	if got := dummys.Call("add", i, 2).Int(); got != 42 {
		t.Errorf("got %#v, want %#v", got, 42)
	}
	if got := dummys.Call("add", js.Global().Call("eval", "40"), 2).Int(); got != 42 {
		t.Errorf("got %#v, want %#v", got, 42)
	}

	expectPanic(t, func() {
		dummys.Call("zero")
	})
	expectValueError(t, func() {
		dummys.Get("zero").Call("badMethod")
	})
}

func TestInvoke(t *testing.T) {
	var i int64 = 40
	if got := dummys.Get("add").Invoke(i, 2).Int(); got != 42 {
		t.Errorf("got %#v, want %#v", got, 42)
	}

	expectValueError(t, func() {
		dummys.Get("zero").Invoke()
	})
}

func TestNew(t *testing.T) {
	if got := js.Global().Get("Array").New(42).Length(); got != 42 {
		t.Errorf("got %#v, want %#v", got, 42)
	}

	expectValueError(t, func() {
		dummys.Get("zero").New()
	})
}

func TestInstanceOf(t *testing.T) {
	someArray := js.Global().Get("Array").New()
	if got, want := someArray.InstanceOf(js.Global().Get("Array")), true; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got, want := someArray.InstanceOf(js.Global().Get("Function")), false; got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestType(t *testing.T) {
	if got, want := js.Undefined().Type(), js.TypeUndefined; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := js.Null().Type(), js.TypeNull; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := js.ValueOf(true).Type(), js.TypeBoolean; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := js.ValueOf(0).Type(), js.TypeNumber; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := js.ValueOf(42).Type(), js.TypeNumber; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := js.ValueOf("test").Type(), js.TypeString; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := js.Global().Get("Symbol").Invoke("test").Type(), js.TypeSymbol; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := js.Global().Get("Array").New().Type(), js.TypeObject; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := js.Global().Get("Array").Type(), js.TypeFunction; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

type object = map[string]any
type array = []any

func TestValueOf(t *testing.T) {
	a := js.ValueOf(array{0, array{0, 42, 0}, 0})
	if got := a.Index(1).Index(1).Int(); got != 42 {
		t.Errorf("got %v, want %v", got, 42)
	}

	o := js.ValueOf(object{"x": object{"y": 42}})
	if got := o.Get("x").Get("y").Int(); got != 42 {
		t.Errorf("got %v, want %v", got, 42)
	}
}

func TestZeroValue(t *testing.T) {
	var v js.Value
	if !v.IsUndefined() {
		t.Error("zero js.Value is not js.Undefined()")
	}
}

func TestFuncOf(t *testing.T) {
	c := make(chan struct{})
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if got := args[0].Int(); got != 42 {
			t.Errorf("got %#v, want %#v", got, 42)
		}
		c <- struct{}{}
		return nil
	})
	defer cb.Release()
	js.Global().Call("setTimeout", cb, 0, 42)
	<-c
}

func TestInvokeFunction(t *testing.T) {
	called := false
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		cb2 := js.FuncOf(func(this js.Value, args []js.Value) any {
			called = true
			return 42
		})
		defer cb2.Release()
		return cb2.Invoke()
	})
	defer cb.Release()
	if got := cb.Invoke().Int(); got != 42 {
		t.Errorf("got %#v, want %#v", got, 42)
	}
	if !called {
		t.Error("function not called")
	}
}

func TestInterleavedFunctions(t *testing.T) {
	c1 := make(chan struct{})
	c2 := make(chan struct{})

	js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) any {
		c1 <- struct{}{}
		<-c2
		return nil
	}), 0)

	<-c1
	c2 <- struct{}{}
	// this goroutine is running, but the callback of setTimeout did not return yet, invoke another function now
	f := js.FuncOf(func(this js.Value, args []js.Value) any {
		return nil
	})
	f.Invoke()
}

func ExampleFuncOf() {
	var cb js.Func
	cb = js.FuncOf(func(this js.Value, args []js.Value) any {
		fmt.Println("button clicked")
		cb.Release() // release the function if the button will not be clicked again
		return nil
	})
	js.Global().Get("document").Call("getElementById", "myButton").Call("addEventListener", "click", cb)
}

// See
// - https://developer.mozilla.org/en-US/docs/Glossary/Truthy
// - https://stackoverflow.com/questions/19839952/all-falsey-values-in-javascript/19839953#19839953
// - http://www.ecma-international.org/ecma-262/5.1/#sec-9.2
func TestTruthy(t *testing.T) {
	want := true
	for _, key := range []string{
		"someBool", "someString", "someInt", "someFloat", "someArray", "someDate",
		"stringZero", // "0" is truthy
		"add",        // functions are truthy
		"emptyObj", "emptyArray", "Infinity", "NegInfinity",
		// All objects are truthy, even if they're Number(0) or Boolean(false).
		"objNumber0", "objBooleanFalse",
	} {
		if got := dummys.Get(key).Truthy(); got != want {
			t.Errorf("%s: got %#v, want %#v", key, got, want)
		}
	}

	want = false
	if got := dummys.Get("zero").Truthy(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got := dummys.Get("NaN").Truthy(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got := js.ValueOf("").Truthy(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got := js.Null().Truthy(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if got := js.Undefined().Truthy(); got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func expectValueError(t *testing.T, fn func()) {
	defer func() {
		err := recover()
		if _, ok := err.(*js.ValueError); !ok {
			t.Errorf("expected *js.ValueError, got %T", err)
		}
	}()
	fn()
}

func expectPanic(t *testing.T, fn func()) {
	defer func() {
		err := recover()
		if err == nil {
			t.Errorf("expected panic")
		}
	}()
	fn()
}

var copyTests = []struct {
	srcLen  int
	dstLen  int
	copyLen int
}{
	{5, 3, 3},
	{3, 5, 3},
	{0, 0, 0},
}

func TestCopyBytesToGo(t *testing.T) {
	for _, tt := range copyTests {
		t.Run(fmt.Sprintf("%d-to-%d", tt.srcLen, tt.dstLen), func(t *testing.T) {
			src := js.Global().Get("Uint8Array").New(tt.srcLen)
			if tt.srcLen >= 2 {
				src.SetIndex(1, 42)
			}
			dst := make([]byte, tt.dstLen)

			if got, want := js.CopyBytesToGo(dst, src), tt.copyLen; got != want {
				t.Errorf("copied %d, want %d", got, want)
			}
			if tt.dstLen >= 2 {
				if got, want := int(dst[1]), 42; got != want {
					t.Errorf("got %d, want %d", got, want)
				}
			}
		})
	}
}

func TestCopyBytesToJS(t *testing.T) {
	for _, tt := range copyTests {
		t.Run(fmt.Sprintf("%d-to-%d", tt.srcLen, tt.dstLen), func(t *testing.T) {
			src := make([]byte, tt.srcLen)
			if tt.srcLen >= 2 {
				src[1] = 42
			}
			dst := js.Global().Get("Uint8Array").New(tt.dstLen)

			if got, want := js.CopyBytesToJS(dst, src), tt.copyLen; got != want {
				t.Errorf("copied %d, want %d", got, want)
			}
			if tt.dstLen >= 2 {
				if got, want := dst.Index(1).Int(), 42; got != want {
					t.Errorf("got %d, want %d", got, want)
				}
			}
		})
	}
}

func TestGarbageCollection(t *testing.T) {
	before := js.JSGo.Get("_values").Length()
	for i := 0; i < 1000; i++ {
		_ = js.Global().Get("Object").New().Call("toString").String()
		runtime.GC()
	}
	after := js.JSGo.Get("_values").Length()
	if after-before > 500 {
		t.Errorf("garbage collection ineffective")
	}
}

// This table is used for allocation tests. We expect a specific allocation
// behavior to be seen, depending on the number of arguments applied to various
// JavaScript functions.
// Note: All JavaScript functions return a JavaScript array, which will cause
// one allocation to be created to track the Value.gcPtr for the Value finalizer.
var allocTests = []struct {
	argLen   int // The number of arguments to use for the syscall
	expected int // The expected number of allocations
}{
	// For less than or equal to 16 arguments, we expect 1 allocation:
	// - makeValue new(ref)
	{0, 1},
	{2, 1},
	{15, 1},
	{16, 1},
	// For greater than 16 arguments, we expect 3 allocation:
	// - makeValue: new(ref)
	// - makeArgSlices: argVals = make([]Value, size)
	// - makeArgSlices: argRefs = make([]ref, size)
	{17, 3},
	{32, 3},
	{42, 3},
}

// TestCallAllocations ensures the correct allocation profile for Value.Call
func TestCallAllocations(t *testing.T) {
	t.Serial() // AllocsPerRun measures the whole process.
	for _, test := range allocTests {
		args := make([]any, test.argLen)

		tmpArray := js.Global().Get("Array").New(0)
		numAllocs := testing.AllocsPerRun(100, func() {
			tmpArray.Call("concat", args...)
		})

		if numAllocs != float64(test.expected) {
			t.Errorf("got numAllocs %#v, want %#v", numAllocs, test.expected)
		}
	}
}

// TestInvokeAllocations ensures the correct allocation profile for Value.Invoke
func TestInvokeAllocations(t *testing.T) {
	t.Serial() // AllocsPerRun measures the whole process.
	for _, test := range allocTests {
		args := make([]any, test.argLen)

		tmpArray := js.Global().Get("Array").New(0)
		concatFunc := tmpArray.Get("concat").Call("bind", tmpArray)
		numAllocs := testing.AllocsPerRun(100, func() {
			concatFunc.Invoke(args...)
		})

		if numAllocs != float64(test.expected) {
			t.Errorf("got numAllocs %#v, want %#v", numAllocs, test.expected)
		}
	}
}

// TestNewAllocations ensures the correct allocation profile for Value.New
func TestNewAllocations(t *testing.T) {
	t.Serial() // AllocsPerRun measures the whole process.
	arrayConstructor := js.Global().Get("Array")

	for _, test := range allocTests {
		args := make([]any, test.argLen)

		numAllocs := testing.AllocsPerRun(100, func() {
			arrayConstructor.New(args...)
		})

		if numAllocs != float64(test.expected) {
			t.Errorf("got numAllocs %#v, want %#v", numAllocs, test.expected)
		}
	}
}

// BenchmarkDOM is a simple benchmark which emulates a webapp making DOM operations.
// It creates a div, and sets its id. Then searches by that id and sets some data.
// Finally it removes that div.
func BenchmarkDOM(b *testing.B) {
	document := js.Global().Get("document")
	if document.IsUndefined() {
		b.Skip("Not a browser environment. Skipping.")
	}
	const data = "someString"
	for i := 0; i < b.N; i++ {
		div := document.Call("createElement", "div")
		div.Call("setAttribute", "id", "myDiv")
		document.Get("body").Call("appendChild", div)
		myDiv := document.Call("getElementById", "myDiv")
		myDiv.Set("innerHTML", data)

		if got, want := myDiv.Get("innerHTML").String(), data; got != want {
			b.Errorf("got %s, want %s", got, want)
		}
		document.Get("body").Call("removeChild", div)
	}
}

func TestGlobal(t *testing.T) {
	ident := js.FuncOf(func(this js.Value, args []js.Value) any {
		return args[0]
	})
	defer ident.Release()

	if got := ident.Invoke(js.Global()); !got.Equal(js.Global()) {
		t.Errorf("got %#v, want %#v", got, js.Global())
	}
}

// Note: misusing Await from inside a js.FuncOf callback is deliberately not
// tested here: it is a hard deadlock, so the runtime kills the whole test
// binary with "fatal error: all goroutines are asleep - deadlock!" (plus the
// note that a goroutine is blocked in a call from JavaScript). The Await doc
// comment documents that behavior.

func TestAwaitResolved(t *testing.T) {
	v, err := js.Await(js.Global().Get("Promise").Call("resolve", 42))
	if err != nil {
		t.Fatalf("Await returned error %v, want nil", err)
	}
	if got := v.Int(); got != 42 {
		t.Errorf("Await returned %d, want 42", got)
	}
}

func TestAwaitResolvedAsync(t *testing.T) {
	// A promise that settles only after a real trip through the event loop.
	p := js.Global().Call("eval", `new Promise((resolve) => setTimeout(() => resolve("late"), 20))`)
	v, err := js.Await(p)
	if err != nil {
		t.Fatalf("Await returned error %v, want nil", err)
	}
	if got := v.String(); got != "late" {
		t.Errorf("Await returned %q, want %q", got, "late")
	}
}

func TestAwaitRejectedErrorObject(t *testing.T) {
	p := js.Global().Call("eval", `Promise.reject(new Error("boom"))`)
	v, err := js.Await(p)
	if err == nil {
		t.Fatal("Await returned nil error, want rejection")
	}
	if !v.IsUndefined() {
		t.Errorf("Await returned value %v, want undefined", v)
	}
	var jsErr js.Error
	if !errors.As(err, &jsErr) {
		t.Fatalf("Await error has type %T, want js.Error", err)
	}
	if got := jsErr.Value.Get("message").String(); got != "boom" {
		t.Errorf("rejection reason has message %q, want %q", got, "boom")
	}
	if got, want := err.Error(), "JavaScript error: boom"; got != want {
		t.Errorf("err.Error() = %q, want %q", got, want)
	}
}

func TestAwaitRejectedString(t *testing.T) {
	// A promise can be rejected with a non-object reason: throw "boom-string".
	p := js.Global().Call("eval", `new Promise(() => { throw "boom-string"; })`)
	_, err := js.Await(p)
	if err == nil {
		t.Fatal("Await returned nil error, want rejection")
	}
	var jsErr js.Error
	if !errors.As(err, &jsErr) {
		t.Fatalf("Await error has type %T, want js.Error", err)
	}
	if got := jsErr.Value.String(); got != "boom-string" {
		t.Errorf("rejection reason is %q, want %q", got, "boom-string")
	}
	if got, want := err.Error(), "JavaScript error: boom-string"; got != want {
		t.Errorf("err.Error() = %q, want %q", got, want)
	}
}

func TestAwaitNonPromise(t *testing.T) {
	v, err := js.Await(js.ValueOf("plain value"))
	if err != nil {
		t.Fatalf("Await returned error %v, want nil", err)
	}
	if got := v.String(); got != "plain value" {
		t.Errorf("Await returned %q, want %q", got, "plain value")
	}

	v, err = js.Await(js.Undefined())
	if err != nil {
		t.Fatalf("Await(undefined) returned error %v, want nil", err)
	}
	if !v.IsUndefined() {
		t.Errorf("Await(undefined) returned %v, want undefined", v)
	}
}

func TestAwaitThenable(t *testing.T) {
	// Promise.resolve assimilates non-promise thenables.
	p := js.Global().Call("eval", `({ then(resolve) { resolve("thenable-ok"); } })`)
	v, err := js.Await(p)
	if err != nil {
		t.Fatalf("Await returned error %v, want nil", err)
	}
	if got := v.String(); got != "thenable-ok" {
		t.Errorf("Await returned %q, want %q", got, "thenable-ok")
	}
}

// testCopyRoundTrip copies elems to a fresh JavaScript TypedArray with
// CopyToJS, verifies the JS-side contents, copies them back with CopyToGo,
// and verifies the round trip.
func testCopyRoundTrip[E comparable](t *testing.T, ctor string, elems []E, get func(js.Value) E) {
	t.Helper()
	arr := js.Global().Get(ctor).New(len(elems))
	if got, want := js.CopyToJS(arr, elems), len(elems); got != want {
		t.Fatalf("CopyToJS copied %d elements, want %d", got, want)
	}
	for i, want := range elems {
		if got := get(arr.Index(i)); got != want {
			t.Errorf("%s[%d] = %v, want %v", ctor, i, got, want)
		}
	}
	back := make([]E, len(elems))
	if got, want := js.CopyToGo(back, arr), len(elems); got != want {
		t.Fatalf("CopyToGo copied %d elements, want %d", got, want)
	}
	for i, want := range elems {
		if back[i] != want {
			t.Errorf("back[%d] = %v, want %v", i, back[i], want)
		}
	}
}

func TestCopyToJSAndBack(t *testing.T) {
	t.Run("Int8", func(t *testing.T) {
		testCopyRoundTrip(t, "Int8Array", []int8{-128, -1, 0, 1, 127}, func(v js.Value) int8 { return int8(v.Int()) })
	})
	t.Run("Uint8", func(t *testing.T) {
		testCopyRoundTrip(t, "Uint8Array", []uint8{0, 1, 128, 255}, func(v js.Value) uint8 { return uint8(v.Int()) })
	})
	t.Run("Int16", func(t *testing.T) {
		testCopyRoundTrip(t, "Int16Array", []int16{-32768, -1, 0, 1, 32767}, func(v js.Value) int16 { return int16(v.Int()) })
	})
	t.Run("Uint16", func(t *testing.T) {
		testCopyRoundTrip(t, "Uint16Array", []uint16{0, 1, 40000, 65535}, func(v js.Value) uint16 { return uint16(v.Int()) })
	})
	t.Run("Int32", func(t *testing.T) {
		testCopyRoundTrip(t, "Int32Array", []int32{-2147483648, -1, 0, 1, 2147483647}, func(v js.Value) int32 { return int32(v.Int()) })
	})
	t.Run("Uint32", func(t *testing.T) {
		testCopyRoundTrip(t, "Uint32Array", []uint32{0, 1, 3000000000, 4294967295}, func(v js.Value) uint32 { return uint32(v.Int()) })
	})
	t.Run("Float32", func(t *testing.T) {
		testCopyRoundTrip(t, "Float32Array", []float32{-1.5, 0, 0.25, 3.5}, func(v js.Value) float32 { return float32(v.Float()) })
	})
	t.Run("Float64", func(t *testing.T) {
		testCopyRoundTrip(t, "Float64Array", []float64{-1.5, 0, math.Pi, 1e300}, func(v js.Value) float64 { return v.Float() })
	})
}

func TestCopyToGoMinLength(t *testing.T) {
	for _, tt := range copyTests {
		t.Run(fmt.Sprintf("%d-to-%d", tt.srcLen, tt.dstLen), func(t *testing.T) {
			src := js.Global().Get("Float32Array").New(tt.srcLen)
			if tt.srcLen >= 2 {
				src.SetIndex(1, 42)
			}
			dst := make([]float32, tt.dstLen)

			if got, want := js.CopyToGo(dst, src), tt.copyLen; got != want {
				t.Errorf("copied %d, want %d", got, want)
			}
			if tt.dstLen >= 2 && tt.srcLen >= 2 {
				if got, want := dst[1], float32(42); got != want {
					t.Errorf("got %v, want %v", got, want)
				}
			}
		})
	}
}

func TestCopyToJSMinLength(t *testing.T) {
	for _, tt := range copyTests {
		t.Run(fmt.Sprintf("%d-to-%d", tt.srcLen, tt.dstLen), func(t *testing.T) {
			src := make([]int16, tt.srcLen)
			if tt.srcLen >= 2 {
				src[1] = 42
			}
			dst := js.Global().Get("Int16Array").New(tt.dstLen)

			if got, want := js.CopyToJS(dst, src), tt.copyLen; got != want {
				t.Errorf("copied %d, want %d", got, want)
			}
			if tt.dstLen >= 2 && tt.srcLen >= 2 {
				if got, want := dst.Index(1).Int(), 42; got != want {
					t.Errorf("got %d, want %d", got, want)
				}
			}
		})
	}
}

func TestCopyKindMismatchPanics(t *testing.T) {
	// src is a Float32Array, dst wants an Int16Array.
	expectPanic(t, func() {
		js.CopyToGo(make([]int16, 4), js.Global().Get("Float32Array").New(4))
	})
	// dst is an Int8Array, src is []float64.
	expectPanic(t, func() {
		js.CopyToJS(js.Global().Get("Int8Array").New(4), make([]float64, 4))
	})
	// src is not a TypedArray at all.
	expectPanic(t, func() {
		js.CopyToGo(make([]int32, 4), js.Global().Get("Array").New(4))
	})
	// Unsupported Go slice types.
	expectPanic(t, func() {
		js.CopyToGo(make([]string, 4), js.Global().Get("Int32Array").New(4))
	})
	expectPanic(t, func() {
		js.CopyToJS(js.Global().Get("Int32Array").New(4), 42)
	})
	// Unlike CopyBytesToGo/CopyBytesToJS, the strictly-typed functions do
	// not accept a Uint8ClampedArray for []uint8.
	expectPanic(t, func() {
		js.CopyToGo(make([]uint8, 4), js.Global().Get("Uint8ClampedArray").New(4))
	})
}

func TestCopyToGoAfterMemoryGrowth(t *testing.T) {
	const n = 1024
	src := js.Global().Get("Float64Array").New(n)
	for i := 0; i < n; i++ {
		src.SetIndex(i, float64(i)/2)
	}
	// Force the Go heap to grow so that the WebAssembly memory buffer is
	// replaced. A copy through a stale view of the old (detached) buffer
	// would read zeros or throw.
	ballast := make([][]byte, 64)
	for i := range ballast {
		ballast[i] = make([]byte, 1<<20)
	}
	dst := make([]float64, n)
	if got := js.CopyToGo(dst, src); got != n {
		t.Fatalf("copied %d, want %d", got, n)
	}
	runtime.KeepAlive(ballast)
	for i, got := range dst {
		if want := float64(i) / 2; got != want {
			t.Fatalf("dst[%d] = %v, want %v", i, got, want)
		}
	}
}

func TestAwaitConcurrent(t *testing.T) {
	makePromise := js.Global().Call("eval", `(i, ms) => new Promise((resolve) => setTimeout(() => resolve("settled-" + i), ms))`)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := fmt.Sprintf("settled-%d", i)
			var p js.Value
			if i%2 == 0 {
				// Real event-loop asynchrony, with distinct delays.
				p = makePromise.Invoke(i, 5+i*3)
			} else {
				p = js.Global().Get("Promise").Call("resolve", want)
			}
			v, err := js.Await(p)
			if err != nil {
				t.Errorf("goroutine %d: Await returned error %v", i, err)
				return
			}
			if got := v.String(); got != want {
				t.Errorf("goroutine %d: Await returned %q, want %q", i, got, want)
			}
		}()
	}
	wg.Wait()
}

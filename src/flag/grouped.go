// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flag

// groupedTestBinary is "1" when the linker built this binary from several
// packages' tests, which cmd/go does to pay one compile and one link for all of
// them (cmd/go/internal/load/testgroup.go).
//
// Every member's init runs on every start, whatever -test.unit selects, so two
// members that both declare a flag named "update" both register it. That is a
// redefinition, and it panics before any test body runs.
//
// The flag package cannot ask the testing package which kind of binary this is,
// because testing imports flag. The linker sets this instead.
var groupedTestBinary string

// grouped reports whether a redefinition is the expected consequence of several
// packages' tests sharing one binary.
func grouped() bool { return groupedTestBinary == "1" }

// fanValue holds every Value registered under one name and hands a parsed
// argument to all of them.
//
// One invocation runs one member's tests, so the argument was meant for that
// member. Setting the others costs nothing: their tests do not run, and their
// variables are read by nobody.
type fanValue struct {
	values []Value
}

// add appends a Value that a later member registered under the same name.
func (fan *fanValue) add(value Value) { fan.values = append(fan.values, value) }

// String answers for the first registration, which is what defined the flag's
// type and its default.
func (fan *fanValue) String() string {
	if len(fan.values) == 0 {
		return ""
	}
	return fan.values[0].String()
}

// Set assigns to every registration and reports the first failure, after trying
// them all. A member whose Set rejects the argument must not leave its siblings
// holding the default.
func (fan *fanValue) Set(text string) error {
	var first error
	for _, value := range fan.values {
		if err := value.Set(text); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Get answers for the first registration when it can, so flag.Getter keeps
// working through the fan.
func (fan *fanValue) Get() any {
	if len(fan.values) == 0 {
		return nil
	}
	if getter, isGetter := fan.values[0].(Getter); isGetter {
		return getter.Get()
	}
	return nil
}

// IsBoolFlag delegates, because the parser reads it to decide whether the next
// argument belongs to this flag. Answering wrongly here would consume the
// argument after a bool flag.
func (fan *fanValue) IsBoolFlag() bool {
	if len(fan.values) == 0 {
		return false
	}
	asBool, isBool := fan.values[0].(boolFlag)
	return isBool && asBool.IsBoolFlag()
}

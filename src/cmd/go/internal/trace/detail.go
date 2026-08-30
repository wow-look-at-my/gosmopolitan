// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package trace

import (
	"context"
	"encoding/json"
	"internal/trace/traceviewer/format"
	"io"
	"time"
)

// What this file adds to the tracer, and why each piece is needed to answer
// "what was this build doing".
//
// A begin/end pair carries a name and nothing else, so a slice reading
// "Executing action (build)" names neither the package nor the module it
// built. Every span therefore takes args, and the build fills them in.
//
// A cache probe is measured in microseconds and there are tens of thousands
// of them. A begin/end pair costs two events and has to nest, which a probe
// issued from inside another span does not. Event records one complete
// slice instead, with its own start and duration.
//
// A lane is a number the viewer sorts by, so a trace of a parallel build is
// a wall of anonymous rows. NameLane gives each one the name of the worker
// that owns it.
//
// A Lane is the handle for code that has no context to pass. The cache is
// the case that forces it: its interface takes no context, and threading one
// through 91 call sites to record a probe is not a trade worth making.

const (
	phaseComplete = "X"
	phaseInstant  = "i"
	phaseCounter  = "C"
	phaseMetadata = "M"

	scopeThread = "t"
)

// Enabled reports whether this context records anything. Callers use it to
// skip building args nothing will read.
func Enabled(ctx context.Context) bool {
	_, ok := getTraceContext(ctx)
	return ok
}

// A Lane is one row in the viewer: the thread a context's events land on.
// The zero Lane is inert, so a caller holds one whether or not tracing is on
// and never tests for it again.
type Lane struct {
	t   *tracer
	tid uint64
}

// LaneOf returns the lane this context's events land on.
func LaneOf(ctx context.Context) Lane {
	tc, ok := getTraceContext(ctx)
	if !ok {
		return Lane{}
	}
	return Lane{t: tc.t, tid: tc.tid}
}

// Enabled reports whether this lane records anything.
func (l Lane) Enabled() bool { return l.t != nil }

// Event records one complete slice: work that already happened, with its own
// start and duration. Use it for an operation too short or too deeply nested
// to bracket with a span.
func (l Lane) Event(name, category string, start time.Time, dur time.Duration, args map[string]any) {
	if l.t == nil {
		return
	}
	ev := &format.Event{
		Name:     name,
		Category: category,
		Phase:    phaseComplete,
		Time:     float64(start.UnixNano()) / float64(time.Microsecond),
		Dur:      float64(dur.Nanoseconds()) / float64(time.Microsecond),
		TID:      l.tid,
	}
	if len(args) > 0 {
		ev.Arg = args
	}
	l.t.writeEvent(ev)
}

// Since records a complete slice that ran from start until now.
func (l Lane) Since(name, category string, start time.Time, args map[string]any) {
	if l.t == nil {
		return
	}
	l.Event(name, category, start, time.Since(start), args)
}

// Instant records a moment with no duration: a decision, a threshold
// crossed, a message sent.
func (l Lane) Instant(name, category string, args map[string]any) {
	if l.t == nil {
		return
	}
	ev := &format.Event{
		Name:     name,
		Category: category,
		Phase:    phaseInstant,
		Scope:    scopeThread,
		Time:     float64(time.Now().UnixNano()) / float64(time.Microsecond),
		TID:      l.tid,
	}
	if len(args) > 0 {
		ev.Arg = args
	}
	l.t.writeEvent(ev)
}

// Counter records named values the viewer draws as a graph over time. Each
// key is one series.
func (l Lane) Counter(name string, values map[string]any) {
	if l.t == nil || len(values) == 0 {
		return
	}
	l.t.writeEvent(&format.Event{
		Name:  name,
		Phase: phaseCounter,
		Time:  float64(time.Now().UnixNano()) / float64(time.Microsecond),
		TID:   l.tid,
		Arg:   values,
	})
}

// Name labels this lane in the viewer and fixes where it sorts. Lanes sort
// by index first, so a caller numbers them in the order it wants read.
func (l Lane) Name(name string, sortIndex int) {
	if l.t == nil {
		return
	}
	l.t.writeEvent(&format.Event{
		Name:  "thread_name",
		Phase: phaseMetadata,
		TID:   l.tid,
		Arg:   map[string]any{"name": name},
	})
	l.t.writeEvent(&format.Event{
		Name:  "thread_sort_index",
		Phase: phaseMetadata,
		TID:   l.tid,
		Arg:   map[string]any{"sort_index": sortIndex},
	})
}

// StartSpanArgs is StartSpan with the detail already known at the start.
func StartSpanArgs(ctx context.Context, name string, args map[string]any) (context.Context, *Span) {
	ctx, span := StartSpan(ctx, name)
	span.SetArgs(args)
	return ctx, span
}

// StartNamedGoroutine puts the context on a fresh lane and labels it, so the
// row reads as the worker that owns it rather than as a number.
func StartNamedGoroutine(ctx context.Context, name string, sortIndex int) context.Context {
	ctx = StartGoroutine(ctx)
	LaneOf(ctx).Name(name, sortIndex)
	return ctx
}

// NameProcess labels the whole trace.
func NameProcess(ctx context.Context, name string) {
	NameProcessID(ctx, 0, name)
}

// NameProcessID labels one process group in the trace. A trace holds more
// than one when a build runs a second go command -- the cosmo
// sibling-architecture build -- and its events are folded in here.
func NameProcessID(ctx context.Context, pid uint64, name string) {
	tc, ok := getTraceContext(ctx)
	if !ok {
		return
	}
	tc.t.writeEvent(&format.Event{
		Name:  "process_name",
		Phase: phaseMetadata,
		PID:   pid,
		Arg:   map[string]any{"name": name},
	})
}

// Import folds a trace another process wrote into this one, under pid.
//
// Two go commands cannot write one trace file: each truncates it at start
// and their writes interleave into a document neither can parse. So a child
// build writes its own, and the parent merges it here once the child has
// exited. Chrome's format separates processes by pid, so the child's rows
// arrive as their own group rather than on top of the parent's.
//
// A trace that cannot be read is not worth failing a build over, so a
// malformed or missing file is reported and the rest of the trace stands.
func Import(ctx context.Context, r io.Reader, pid uint64, name string) error {
	tc, ok := getTraceContext(ctx)
	if !ok {
		return nil
	}
	var events []format.Event
	if err := json.NewDecoder(r).Decode(&events); err != nil {
		return err
	}
	NameProcessID(ctx, pid, name)
	for i := range events {
		events[i].PID = pid
		tc.t.writeEvent(&events[i])
	}
	return nil
}

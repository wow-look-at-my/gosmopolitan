// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"encoding/hex"
	"time"

	"cmd/go/internal/cache"
	"cmd/go/internal/trace"
)

// What a build's trace has to say, and what it took to say it.
//
// A slice reading "Executing action (build)" names neither the package it
// built nor the module that package came from, so a trace of a thousand
// actions is a thousand rows nobody can tell apart. Every span an action
// emits therefore carries traceArgs: the import path, the module and its
// version, where the sources live, the target file, and the action ID the
// cache was keyed by. Selecting any slice answers "what is this, and where
// did it come from".
//
// The cache is the other half. A hit produces no subprocess and writes no
// file the build did not already have, so it shows up as a gap rather than
// as work -- which is exactly backwards, since a hit is the reason the build
// was fast. Each action runs the cache through cache.Traced on its own lane,
// so a lookup is a slice like any other and names the tier that answered.

// lane returns the trace row this action's work is recorded on. The zero
// Lane, which records nothing, is what an untraced build gets.
func (a *Action) lane() trace.Lane {
	if a == nil {
		return trace.Lane{}
	}
	return a.traceLane
}

// cache returns the build cache, recording every operation onto this
// action's lane. It is cache.Default() unchanged when tracing is off.
func (a *Action) cache() cache.Cache {
	return cache.Traced(cache.Default(), a.lane())
}

// adoptLane records this action's work on the lane of the action currently
// running.
//
// A lane is set on the action a worker picked up, and one action routinely
// does the cache work of another: the "build check cache" action looks the
// build action's outputs up, and reports on the build action's behalf. The
// build action has no lane yet -- it runs later, possibly on another worker
// -- so without this the whole cache-lookup path records nothing, which is
// how a trace ends up saying a build consulted the cache twice when it
// consulted it three hundred times.
//
// Safe because the borrower runs strictly before the lender: an action
// cannot start until every action it depends on has finished, and the
// lender's own lane is assigned when it starts.
func (a *Action) adoptLane(from *Action) {
	if a != nil && from != nil {
		a.traceLane = from.traceLane
	}
}

// traceArgs is what every span this action emits carries. It answers, for
// any slice in the viewer, which package and module the work belonged to.
func (a *Action) traceArgs() map[string]any {
	if a == nil {
		return nil
	}
	args := map[string]any{"mode": a.Mode}
	if a.Target != "" {
		args["target"] = a.Target
	}
	if a.Objdir != "" {
		args["objdir"] = a.Objdir
	}
	if a.actionID != (cache.ActionID{}) {
		args["action_id"] = hex.EncodeToString(a.actionID[:])
	}
	if a.buildID != "" {
		args["build_id"] = a.buildID
	}
	if len(a.Deps) > 0 {
		args["deps"] = len(a.Deps)
	}
	p := a.Package
	if p == nil {
		return args
	}
	args["package"] = p.ImportPath
	if p.Dir != "" {
		args["dir"] = p.Dir
	}
	switch {
	case p.Module != nil:
		args["module"] = p.Module.Path
		if p.Module.Version != "" {
			args["module_version"] = p.Module.Version
		}
	case p.Standard:
		// A standard-library package belongs to no module, and saying so is
		// the answer to "where did this come from" rather than a gap in it.
		args["module"] = "std"
	case p.Goroot:
		args["module"] = "goroot"
	}
	if len(p.GoFiles)+len(p.CgoFiles)+len(p.SFiles) > 0 {
		args["files"] = len(p.GoFiles) + len(p.CgoFiles) + len(p.SFiles)
	}
	return args
}

// traceStep records one phase inside an action -- the action-ID hash, the
// cache lookup, a compile -- as its own slice, carrying the action's
// attribution plus whatever the phase itself has to say.
func (a *Action) traceStep(name string, start time.Time, extra map[string]any) {
	lane := a.lane()
	if !lane.Enabled() {
		return
	}
	args := a.traceArgs()
	for k, v := range extra {
		args[k] = v
	}
	lane.Since(name, "build", start, args)
}

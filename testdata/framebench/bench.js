// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// bench.js drives the framebench wasm module under Node.js.
//
// Usage:
//   node bench.js <framebench.wasm> [--frames N] [--warmup N] [--markstep] [--frame-budget MS]
//
// Each "frame" is one call to the module's bench_frame export (~10k
// allocations of mixed sizes). The measured frame time is the wall time
// of that call: GC assists, GC start/termination pauses, and any
// in-frame background marking all land inside it, exactly like they
// land inside a requestAnimationFrame callback in a browser.
//
// With --markstep, the harness calls the runtime's exported budgeted
// GC mark step (go_gc_mark_step) after each frame with the frame
// budget left over, so mark work runs in idle time between frames
// instead of synchronously inside the next frames.

"use strict";

const fs = require("fs");
const path = require("path");

const args = process.argv.slice(2);
if (args.length < 1) {
	console.error("usage: node bench.js <framebench.wasm> [--frames N] [--warmup N] [--markstep] [--frame-budget MS]");
	process.exit(1);
}

const wasmPath = args[0];
let frames = 2000;
let warmup = 300;
let markstep = false;
let frameBudgetMs = 16.7;
for (let i = 1; i < args.length; i++) {
	switch (args[i]) {
	case "--frames":
		frames = parseInt(args[++i], 10);
		break;
	case "--warmup":
		warmup = parseInt(args[++i], 10);
		break;
	case "--markstep":
		markstep = true;
		break;
	case "--frame-budget":
		frameBudgetMs = parseFloat(args[++i]);
		break;
	default:
		console.error("unknown flag:", args[i]);
		process.exit(1);
	}
}

// Load the Go js/wasm glue from this repo's lib/wasm.
globalThis.require = require;
globalThis.fs = fs;
globalThis.path = path;
globalThis.TextEncoder = require("util").TextEncoder;
globalThis.TextDecoder = require("util").TextDecoder;
require(path.join(__dirname, "..", "..", "lib", "wasm", "wasm_exec.js"));

const go = new Go();
go.exit = (code) => {
	if (code !== 0) {
		console.error("go program exited with code", code);
		process.exit(code);
	}
};

function percentile(sorted, p) {
	if (sorted.length === 0) {
		return 0;
	}
	const idx = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
	return sorted[Math.max(0, idx)];
}

WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject).then((result) => {
	// go.run executes the program synchronously until main blocks and
	// the runtime pauses; after that the exports are callable.
	go.run(result.instance).catch((err) => {
		console.error(err);
		process.exit(1);
	});
	const exp = result.instance.exports;

	if (markstep && typeof exp.go_gc_mark_step !== "function") {
		console.error("--markstep requested but the wasm module has no go_gc_mark_step export " +
			"(runtime built without the budgeted mark step?)");
		process.exit(1);
	}

	const durations = new Float64Array(frames);
	let stepCalls = 0;
	let stepTimeMs = 0;
	let frame = 0;
	let gcStart = 0;
	let measureStart = 0;

	function tick() {
		const measuring = frame >= warmup;
		const i = frame - warmup;
		if (i === 0) {
			gcStart = exp.bench_numgc();
			measureStart = performance.now();
		}
		if (i >= frames) {
			return finish();
		}

		const t0 = performance.now();
		exp.bench_frame();
		const t1 = performance.now();
		if (measuring) {
			durations[i] = t1 - t0;
		}

		if (markstep) {
			// Donate the rest of the frame budget (minus a safety
			// margin for the harness itself) to the GC.
			const budget = frameBudgetMs - (t1 - t0) - 1;
			if (budget > 0.2) {
				const s0 = performance.now();
				exp.go_gc_mark_step(budget);
				stepTimeMs += performance.now() - s0;
				stepCalls++;
			}
		}

		frame++;
		setTimeout(tick, 0);
	}

	function finish() {
		const gcCycles = exp.bench_numgc() - gcStart;
		const totalMs = performance.now() - measureStart;
		const heapMB = exp.bench_heap_mb();
		const sorted = Array.from(durations).sort((a, b) => a - b);
		const sum = sorted.reduce((a, b) => a + b, 0);
		const over8 = sorted.filter((d) => d > 8).length;
		const over167 = sorted.filter((d) => d > 16.7).length;
		const r = {
			config: markstep ? "markstep" : "baseline",
			frames: frames,
			warmup: warmup,
			frame_budget_ms: frameBudgetMs,
			p50_ms: +percentile(sorted, 50).toFixed(3),
			p90_ms: +percentile(sorted, 90).toFixed(3),
			p99_ms: +percentile(sorted, 99).toFixed(3),
			max_ms: +percentile(sorted, 100).toFixed(3),
			mean_ms: +(sum / sorted.length).toFixed(3),
			frames_over_8ms: over8,
			frames_over_16_7ms: over167,
			gc_cycles: gcCycles,
			markstep_calls: stepCalls,
			markstep_total_ms: +stepTimeMs.toFixed(1),
			total_wall_ms: +totalMs.toFixed(1),
			heap_alloc_mb: +heapMB.toFixed(2),
			node: process.version,
		};
		console.log(JSON.stringify(r));
		console.log(`${r.config}: frames=${r.frames} p50=${r.p50_ms}ms p90=${r.p90_ms}ms ` +
			`p99=${r.p99_ms}ms max=${r.max_ms}ms gc_cycles=${r.gc_cycles} ` +
			`over8ms=${r.frames_over_8ms} over16.7ms=${r.frames_over_16_7ms}`);
		process.exit(0);
	}

	setTimeout(tick, 0);
}).catch((err) => {
	console.error(err);
	process.exit(1);
});

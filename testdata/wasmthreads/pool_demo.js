// pool_demo.js drives the GOWASM=threads worker-pool demo (threads phase
// B1) under Node.js:
//
//	node pool_demo.js <lib/wasm dir> <pooldemo wasm binary> [numWorkers] [callsPerWorker]
//
// It boots the Go program (pooldemo/main.go) normally on the main
// instance, then spawns a pool of worker instances of the SAME compiled
// module against the SAME shared WebAssembly.Memory. Each worker hammers
// a shared counter cell (whose address the Go side publishes) through
// the linker-synthesized wasm_probe_atomic_add export - a wasm
// i32.atomic.rmw.add, i.e. a real 0xFE threads-proposal instruction
// executed inside wasm, not a JS Atomics call. Afterwards the Go side
// verifies the exact expected sum (cross-instance atomic visibility) and
// that its heap/data checksums are unchanged (worker instantiation wrote
// nothing to the shared memory: passive data segments + _initmem gating).
//
// Since the threads runtime phases (B2/B3), the Go runtime also needs a
// RUNTIME worker pool to boot at all: runtime.main locks the main
// goroutine to the main thread, so its first blocking channel receive
// (gcenable) hands the P to a new M, and newosproc needs a pool worker
// parked in wasm_thread_run to claim it (a 10s fatal otherwise). This
// driver therefore pre-spawns GOWASMTHREADSPOOL runtime workers exactly
// like wasm_exec_node.js, in addition to the probe workers above.
//
// Exit-code discipline: a Go exit on ANY instance (main or runtime pool
// worker) before the demo completed, a wasm trap, a worker error, or the
// event loop draining early all exit this process nonzero. (Before this
// hardening, a boot-time runtime fatal left the driver parked on a
// promise only Go could resolve; the event loop drained and node exited
// 0 with "fatal error: ..." in the output.)

"use strict";

if (process.argv.length < 4) {
	console.error("usage: node pool_demo.js <lib/wasm dir> <pooldemo wasm binary> [numWorkers] [callsPerWorker]");
	process.exit(1);
}

const path = require("path");
const fs = require("fs");

const execDir = path.resolve(process.argv[2]);
const wasmPath = process.argv[3];
const numWorkers = parseInt(process.argv[4] ?? "4", 10);
const callsPerWorker = parseInt(process.argv[5] ?? "50000", 10);

globalThis.require = require;
globalThis.fs = fs;
globalThis.path = path;
globalThis.TextEncoder = require("util").TextEncoder;
globalThis.TextDecoder = require("util").TextDecoder;

require(path.join(execDir, "wasm_exec.js"));
const { GoWorkerPool } = require(path.join(execDir, "wasm_exec_pool_node.js"));

(async () => {
	const wasmBytes = fs.readFileSync(wasmPath);
	const go = new Go();
	go.argv = [path.basename(wasmPath)];
	go.env = Object.assign({ TMPDIR: require("os").tmpdir() }, process.env);

	// finished flips right before the demo hands control back to Go for
	// the final verification: from then on a Go exit is the expected way
	// out. Until then, ANY exit - main instance or runtime pool worker -
	// is a failure and must take the process down nonzero immediately
	// (the driver below awaits promises only live Go code can resolve,
	// so it could never reach its own exit-code check).
	let finished = false;
	// completed flips right before the deliberate final process.exit; the
	// process 'exit' guard below turns any OTHER exit-0 (an event-loop
	// drain with the demo incomplete) into a failure.
	let completed = false;
	let exitCode;
	go.exit = (code) => {
		exitCode = code;
		if (!finished) {
			console.error(`pool_demo: FAIL: Go program exited with code ${code} before the demo completed`);
			process.exit(code !== 0 ? code : 1);
		}
	};
	process.on("exit", (code) => {
		if (code !== 0 || completed) {
			return;
		}
		if (!go.exited) {
			// Deadlock: make Go print error and stack traces (the same
			// exit-time probe wasm_exec_node.js runs).
			try {
				go._pendingEvent = { id: 0 };
				go._resume();
			} catch (err) {
				console.error(err);
			}
		}
		console.error("pool_demo: FAIL: event loop drained before the demo completed");
		process.exitCode = 1;
	});

	const memory = go.provideMemory(new Uint8Array(wasmBytes.buffer, wasmBytes.byteOffset, wasmBytes.byteLength));
	if (memory === undefined) {
		console.error("pool_demo: not a GOWASM=threads module (no imported memory); build with GOOS=js GOARCH=wasm GOWASM=threads");
		process.exit(1);
	}
	if (!(memory.buffer instanceof SharedArrayBuffer)) {
		console.error("pool_demo: memory is not backed by a SharedArrayBuffer");
		process.exit(1);
	}

	// Compile once; the same module is instantiated on the main thread,
	// on every runtime pool worker, and on every probe worker.
	const module = await WebAssembly.compile(wasmBytes);

	// The Go program calls __demoReady(counterAddr) once its state is
	// set up, and parks until __goFinishDemo(expected) is called.
	const ready = new Promise((resolve) => {
		globalThis.__demoReady = (counterAddr) => resolve(counterAddr);
	});

	const instance = await WebAssembly.instantiate(module, go.importObject);

	// Pre-spawn the runtime worker pool (mirrors wasm_exec_node.js): each
	// worker instantiates the module against the shared memory and parks
	// inside the wasm_thread_run export, waiting for the Go runtime to
	// hand it an M (runtime.newosproc). GOWASMTHREADSPOOL sets the pool
	// size (default 4, matching the runtime's wasmPoolSize).
	let poolSize = parseInt(process.env.GOWASMTHREADSPOOL ?? "", 10);
	if (Number.isNaN(poolSize) || poolSize < 0) {
		poolSize = 4;
	}
	if (poolSize > 0) {
		const { Worker } = require("worker_threads");
		for (let i = 1; i <= poolSize; i++) {
			const w = new Worker(path.join(execDir, "wasm_exec_worker_node.js"), {
				workerData: {
					id: i,
					module: module,
					memory: memory,
					goRuntime: true,
					threadRun: true,
					timeOrigin: go._timeOrigin,
					perfOrigin: performance.timeOrigin,
				},
			});
			w.on("message", (msg) => {
				if (msg === null || typeof msg !== "object") {
					return;
				}
				if (msg.type === "exit") {
					// Go code on the worker called runtime.exit. Legal
					// only as the demo's own final exit; anything else
					// (e.g. a fatal error on a worker M) must take the
					// whole process down nonzero.
					if (finished && msg.code === 0) {
						process.exit(0);
					}
					console.error(`pool_demo: FAIL: Go exited with code ${msg.code} on runtime pool worker ${i} before the demo completed`);
					process.exit(msg.code !== 0 ? msg.code : 1);
				}
				if (msg.type === "error") {
					console.error(msg.message);
					process.exit(1);
				}
			});
			w.on("error", (err) => {
				console.error(err);
				process.exit(1);
			});
			w.on("exit", (code) => {
				// A runtime pool worker thread must never die while the
				// demo runs. This also catches a worker-side fatal whose
				// exit message got lost: the worker's host exit hook
				// still ends the thread with the fatal's code.
				if (finished && code === 0) {
					return;
				}
				console.error(`pool_demo: FAIL: runtime pool worker ${i} exited with code ${code} before the demo completed`);
				process.exit(code !== 0 ? code : 1);
			});
			w.unref();
		}
	}

	const runDone = go.run(instance).catch((err) => {
		// A wasm trap (or any exception out of run/resume) never counts
		// as demo success.
		console.error("pool_demo: FAIL: Go program crashed:", err);
		process.exit(1);
	});

	const counterAddr = await ready;
	console.log(`pool_demo: Go published shared counter address 0x${counterAddr.toString(16)}`);

	const pool = new GoWorkerPool(module, memory, numWorkers);
	await pool.ready();
	console.log(`pool_demo: ${numWorkers} worker instances instantiated against the shared memory`);

	// Hammer the counter from all workers concurrently. Worker i adds
	// (i+1) per call, callsPerWorker times, via wasm_probe_atomic_add
	// (one wasm i32.atomic.rmw.add per call).
	let expected = 0;
	const jobs = [];
	for (let i = 0; i < numWorkers; i++) {
		const delta = i + 1;
		expected += delta * callsPerWorker;
		jobs.push(pool.call(i, "wasm_probe_atomic_add", [counterAddr, delta], callsPerWorker));
	}
	await Promise.all(jobs);
	await pool.terminate();
	console.log(`pool_demo: workers done: ${numWorkers} x ${callsPerWorker} atomic adds, expected total ${expected}`);

	// Cross-check from the JS side too (SharedArrayBuffer view).
	const jsValue = Atomics.load(new Int32Array(memory.buffer), counterAddr >> 2);
	console.log(`pool_demo: JS Atomics.load cross-check reads ${jsValue}`);

	// Hand control back to Go for the authoritative verification
	// (atomic.LoadUint32 + state checksum on the main instance). From
	// here on the Go program exiting is the expected path out.
	finished = true;
	globalThis.__goFinishDemo(expected);
	await runDone;
	completed = true;
	process.exit(exitCode ?? 1);
})().catch((err) => {
	console.error(err);
	process.exit(1);
});

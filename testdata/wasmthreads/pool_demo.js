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
	let exitCode;
	go.exit = (code) => { exitCode = code; };

	const memory = go.provideMemory(new Uint8Array(wasmBytes.buffer, wasmBytes.byteOffset, wasmBytes.byteLength));
	if (memory === undefined) {
		console.error("pool_demo: not a GOWASM=threads module (no imported memory); build with GOOS=js GOARCH=wasm GOWASM=threads");
		process.exit(1);
	}
	if (!(memory.buffer instanceof SharedArrayBuffer)) {
		console.error("pool_demo: memory is not backed by a SharedArrayBuffer");
		process.exit(1);
	}

	// Compile once; the same module is instantiated on the main thread
	// and on every worker.
	const module = await WebAssembly.compile(wasmBytes);

	// The Go program calls __demoReady(counterAddr) once its state is
	// set up, and parks until __goFinishDemo(expected) is called.
	const ready = new Promise((resolve) => {
		globalThis.__demoReady = (counterAddr) => resolve(counterAddr);
	});

	const instance = await WebAssembly.instantiate(module, go.importObject);
	const runDone = go.run(instance); // resolves when the Go program exits

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
	// (atomic.LoadUint32 + state checksum on the main instance).
	globalThis.__goFinishDemo(expected);
	await runDone;
	process.exit(exitCode ?? 1);
})().catch((err) => {
	console.error(err);
	process.exit(1);
});

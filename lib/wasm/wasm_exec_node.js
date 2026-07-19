// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

"use strict";

if (process.argv.length < 3) {
	console.error("usage: go_js_wasm_exec [wasm binary] [arguments]");
	process.exit(1);
}

const nodeVersion = parseInt(process.versions.node.split(".")[0], 10);
if (nodeVersion < 18) {
	console.error(`Go programs compiled for js/wasm require Node.js 18 or newer (running ${process.version})`);
	process.exit(1);
}

globalThis.require = require;
globalThis.fs = require("fs");
globalThis.path = require("path");
globalThis.TextEncoder = require("util").TextEncoder;
globalThis.TextDecoder = require("util").TextDecoder;

// globalThis.performance and globalThis.crypto are provided by Node.js 18+.

require("./wasm_exec");

const go = new Go();
go.argv = process.argv.slice(2);
go.env = Object.assign({ TMPDIR: require("os").tmpdir() }, process.env);
go.exit = process.exit;
const wasmBytes = fs.readFileSync(process.argv[2]);
// GOWASM=threads modules import a shared linear memory instead of
// exporting their own; provideMemory detects this and supplies one.
const sharedMemory = go.provideMemory(new Uint8Array(wasmBytes.buffer, wasmBytes.byteOffset, wasmBytes.byteLength));
(async () => {
	let instance;
	if (sharedMemory !== undefined) {
		// Threads module: compile once, then instantiate. The compiled
		// module is kept on go._module so an embedder can instantiate
		// worker instances against the same module and shared memory
		// (see wasm_exec_pool_node.js).
		go._module = await WebAssembly.compile(wasmBytes);
		instance = await WebAssembly.instantiate(go._module, go.importObject);

		// Pre-spawn the runtime worker pool: each worker instantiates the
		// module against the shared memory and parks inside the
		// wasm_thread_run export, waiting for the Go runtime to hand it
		// an M (runtime.newosproc). GOWASMTHREADSPOOL sets the pool size
		// (default 4, 0 disables; newosproc throws after 10s if no worker
		// is available). The workers are unref'd so an idle pool never
		// keeps the process alive.
		let poolSize = parseInt(process.env.GOWASMTHREADSPOOL ?? "", 10);
		if (Number.isNaN(poolSize) || poolSize < 0) {
			poolSize = 4;
		}
		go._threadWorkers = [];
		if (poolSize > 0) {
			const { Worker } = require("worker_threads");
			for (let i = 1; i <= poolSize; i++) {
				const w = new Worker(path.join(__dirname, "wasm_exec_worker_node.js"), {
					workerData: {
						id: i,
						module: go._module,
						memory: sharedMemory,
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
						// Go code on the worker called runtime.exit (e.g. a
						// fatal error): take the whole process down.
						process.exit(msg.code);
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
				w.unref();
				go._threadWorkers.push(w);
			}
		}
	} else {
		// Ordinary module: the exact classic path.
		instance = (await WebAssembly.instantiate(wasmBytes, go.importObject)).instance;
	}
	process.on("exit", (code) => { // Node.js exits if no event handler is pending
		if (code === 0 && !go.exited) {
			// deadlock, make Go print error and stack traces
			go._pendingEvent = { id: 0 };
			go._resume();
		}
	});
	return go.run(instance);
})().catch((err) => {
	console.error(err);
	process.exit(1);
});

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Node.js worker_threads bootstrap for the GOWASM=threads worker pool:
// wires GoWasmWorker (wasm_exec_worker.js, host-agnostic) to parentPort.
// Spawned by wasm_exec_pool_node.js (probe workers) or wasm_exec_node.js
// (runtime pool workers) with workerData = {id, module, memory,
// goRuntime, threadRun, timeOrigin, perfOrigin}; see wasm_exec_worker.js
// for the message protocol.

"use strict";

const { parentPort, workerData } = require("worker_threads");

// The runtime imports of a goRuntime worker use fs (wasmWrite) and the
// global crypto/performance, mirroring wasm_exec.js on the main thread.
globalThis.fs = require("fs");

require("./wasm_exec_worker");

const worker = new GoWasmWorker(
	(msg) => parentPort.postMessage(msg),
	(code) => process.exit(code), // in a worker thread this stops only this thread
);
parentPort.on("message", (msg) => { worker.handleMessage(msg); });
if (workerData !== null && typeof workerData === "object" && workerData.module !== undefined) {
	worker.handleMessage({
		type: "init",
		id: workerData.id,
		module: workerData.module,
		memory: workerData.memory,
		goRuntime: workerData.goRuntime === true,
		threadRun: workerData.threadRun === true,
		timeOrigin: workerData.timeOrigin,
		perfOrigin: workerData.perfOrigin,
	});
}

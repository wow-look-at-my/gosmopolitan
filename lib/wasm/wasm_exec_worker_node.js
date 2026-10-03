// Copyright The Go Authors. All rights reserved.

// Node.js worker_threads bootstrap for the GOWASM=threads worker pool.

"use strict";

const { parentPort, workerData } = require("worker_threads");

// The runtime imports of a goRuntime worker use fs (wasmWrite) and the global crypto/performance.
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

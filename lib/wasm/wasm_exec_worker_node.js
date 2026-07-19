// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Node.js worker_threads bootstrap for the GOWASM=threads worker pool:
// wires GoWasmWorker (wasm_exec_worker.js, host-agnostic) to parentPort.
// Spawned by wasm_exec_pool_node.js with workerData = {id, module,
// memory}; see wasm_exec_worker.js for the message protocol.

"use strict";

const { parentPort, workerData } = require("worker_threads");

require("./wasm_exec_worker");

const worker = new GoWasmWorker((msg) => parentPort.postMessage(msg));
parentPort.on("message", (msg) => { worker.handleMessage(msg); });
if (workerData !== null && typeof workerData === "object" && workerData.module !== undefined) {
	worker.handleMessage({ type: "init", id: workerData.id, module: workerData.module, memory: workerData.memory });
}

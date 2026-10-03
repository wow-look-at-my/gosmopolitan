// Copyright The Go Authors. All rights reserved.

// Main-thread side of the GOWASM=threads worker pool for Node.js.

"use strict";

const { Worker } = require("worker_threads");
const path = require("path");

class GoWorkerPool {
	// module: a compiled WebAssembly.Module of a GOWASM=threads binary. memory:
	// the shared WebAssembly.Memory the main instance runs on
	// (go.provideMemory's return value). size: number of workers to pre-spawn.
	constructor(module, memory, size) {
		if (!(memory instanceof WebAssembly.Memory) || !(memory.buffer instanceof SharedArrayBuffer)) {
			throw new Error("GoWorkerPool: memory must be a shared WebAssembly.Memory (GOWASM=threads module + go.provideMemory)");
		}
		this._pending = new Map(); // seq -> {resolve, reject}
		this._seq = 0;
		this.workers = [];
		for (let id = 0; id < size; id++) {
			const worker = new Worker(path.join(__dirname, "wasm_exec_worker_node.js"), {
				workerData: { id: id, module: module, memory: memory },
			});
			const entry = { id: id, worker: worker };
			entry.ready = new Promise((resolve, reject) => {
				entry._readyResolve = resolve;
				entry._readyReject = reject;
			});
			worker.on("message", (msg) => this._onMessage(entry, msg));
			worker.on("error", (err) => this._onError(entry, err));
			this.workers.push(entry);
		}
	}

	// ready resolves once every worker has instantiated the module
	// against the shared memory.
	ready() {
		return Promise.all(this.workers.map((w) => w.ready));
	}

	// call invokes exported function `name` with `args` on worker `id`, `repeat`
	// times in a tight loop, and resolves with the last return value.
	call(id, name, args = [], repeat = 1) {
		const seq = this._seq++;
		return new Promise((resolve, reject) => {
			this._pending.set(seq, { resolve: resolve, reject: reject });
			this.workers[id].worker.postMessage({ type: "call", seq: seq, name: name, args: args, repeat: repeat });
		});
	}

	// broadcast runs the same call on every worker and resolves with the
	// array of results.
	broadcast(name, args = [], repeat = 1) {
		return Promise.all(this.workers.map((w) => this.call(w.id, name, args, repeat)));
	}

	// terminate stops all workers.
	terminate() {
		const err = new Error("GoWorkerPool: terminated");
		for (const p of this._pending.values()) {
			p.reject(err);
		}
		this._pending.clear();
		return Promise.all(this.workers.map((w) => w.worker.terminate()));
	}

	_onMessage(entry, msg) {
		switch (msg.type) {
			case "ready":
				entry._readyResolve(msg);
				return;
			case "result": {
				const p = this._pending.get(msg.seq);
				if (p !== undefined) {
					this._pending.delete(msg.seq);
					p.resolve(msg.value);
				}
				return;
			}
			case "error": {
				const err = new Error(msg.message);
				if (msg.seq !== undefined) {
					const p = this._pending.get(msg.seq);
					if (p !== undefined) {
						this._pending.delete(msg.seq);
						p.reject(err);
						return;
					}
				}
				entry._readyReject(err);
				return;
			}
		}
	}

	_onError(entry, err) {
		entry._readyReject(err);
		for (const p of this._pending.values()) {
			p.reject(err);
		}
		this._pending.clear();
	}
}

module.exports = { GoWorkerPool };

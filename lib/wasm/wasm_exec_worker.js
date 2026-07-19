// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Worker-side core of the GOWASM=threads worker pool. Host-agnostic: it
// only needs postMessage-style plumbing, so it can be driven by a Node.js
// worker_threads wrapper (wasm_exec_worker_node.js) or, in a browser, by
// a Web Worker that loads this file via importScripts and forwards
// onmessage/postMessage the same way.
//
// A GoWasmWorker instantiates the (already compiled) GOWASM=threads
// module against the SAME shared WebAssembly.Memory as the main
// instance. Because the module's data segments are passive and only the
// main instance runs the synthetic _initmem export (see
// cmd/link/internal/wasm and Go.run in wasm_exec.js), instantiating here
// writes nothing to the shared memory: the worker gets fresh
// per-instance globals (SP etc.) and its own function table, and the
// live heap/runtime state of the main instance is untouched.
//
// The worker does NOT run the Go runtime: until the scheduler is
// thread-aware (a later phase), every gojs.* runtime import is stubbed
// to throw. Workers may only call exports that need no Go runtime state,
// such as the linker-synthesized wasm_probe_atomic_add.
//
// Message protocol (all messages are plain structured-cloneable objects):
//
//	main -> worker  {type: "init", id, module, memory}
//	                  instantiate `module` (a WebAssembly.Module) against
//	                  `memory` (the shared WebAssembly.Memory).
//	worker -> main  {type: "ready", id}
//	                  instantiation finished.
//	main -> worker  {type: "call", seq, name, args = [], repeat = 1}
//	                  call exported function `name` with `args`, `repeat`
//	                  times in a tight loop (so a hammer loop does not pay
//	                  one message round-trip per call).
//	worker -> main  {type: "result", seq, value}
//	                  the return value of the last call.
//	worker -> main  {type: "error", seq?, message}
//	                  init or call failed; seq is present for call errors.

"use strict";

(() => {
	globalThis.GoWasmWorker = class {
		// post is the function used to send messages back to the main
		// thread (e.g. parentPort.postMessage on Node.js).
		constructor(post) {
			this._post = post;
			this._inst = null;
			this.id = undefined;
		}

		// handleMessage processes one message from the main thread per
		// the protocol above. It returns a promise; errors are reported
		// back as {type: "error"} messages, not thrown.
		async handleMessage(msg) {
			switch (msg.type) {
				case "init":
					try {
						this.id = msg.id;
						this._inst = await WebAssembly.instantiate(msg.module, this._stubImports(msg.module, msg.memory));
						this._post({ type: "ready", id: this.id });
					} catch (err) {
						this._post({ type: "error", message: `worker ${msg.id}: init: ${err && err.message ? err.message : err}` });
					}
					return;
				case "call":
					try {
						const fn = this._inst?.exports[msg.name];
						if (typeof fn !== "function") {
							throw new Error(`no exported function ${JSON.stringify(msg.name)}`);
						}
						const args = msg.args ?? [];
						const repeat = msg.repeat ?? 1;
						let value;
						for (let i = 0; i < repeat; i++) {
							value = fn(...args);
						}
						this._post({ type: "result", seq: msg.seq, value: value });
					} catch (err) {
						this._post({ type: "error", seq: msg.seq, message: `worker ${this.id}: call ${msg.name}: ${err && err.message ? err.message : err}` });
					}
					return;
				default:
					this._post({ type: "error", message: `worker ${this.id}: unknown message type ${JSON.stringify(msg.type)}` });
					return;
			}
		}

		// _stubImports builds an import object satisfying every import the
		// module declares: the imported linear memory is the shared memory,
		// and every function import is a stub that throws, because the Go
		// runtime cannot run on a worker instance yet.
		_stubImports(module, memory) {
			const imports = {};
			for (const imp of WebAssembly.Module.imports(module)) {
				const mod = imports[imp.module] ?? (imports[imp.module] = {});
				switch (imp.kind) {
					case "memory":
						mod[imp.name] = memory;
						break;
					case "function": {
						const name = `${imp.module}.${imp.name}`;
						const id = this.id;
						mod[imp.name] = () => {
							throw new Error(`worker ${id}: Go runtime import ${name} called on a worker instance (Go code cannot run on workers before the scheduler phase)`);
						};
						break;
					}
					default:
						throw new Error(`unsupported import kind ${JSON.stringify(imp.kind)} for ${imp.module}.${imp.name}`);
				}
			}
			return imports;
		}
	};
})();

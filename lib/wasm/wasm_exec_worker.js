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
// per-instance globals (SP, g, PAUSE etc.) and its own function table,
// and the live heap/runtime state of the main instance is untouched.
//
// Two kinds of workers exist:
//
//   - Probe workers (init without goRuntime): every gojs.* import is
//     stubbed to throw; the worker may only call exports that need no Go
//     runtime state, such as the linker-synthesized
//     wasm_probe_atomic_add.
//
//   - Runtime pool workers (init with goRuntime: true and threadRun:
//     true): the pure-runtime imports (wasmExit, wasmWrite, nanotime1,
//     walltime, getRandomData, resetMemoryDataView) are real, so real Go
//     code can run on this instance once the runtime hands it an M. The
//     worker calls the wasm_thread_run export, which parks inside wasm
//     in a futex wait on the runtime's spawn mailbox and NEVER returns;
//     from then on this worker thread is a Go M running the scheduler.
//     Event-loop imports (scheduleTimeoutEvent...) and all syscall/js
//     imports still throw: JavaScript values live on the main thread,
//     and the runtime keeps all event-loop work on the main M
//     (host-call forwarding from worker Ms is a later phase).
//
// Message protocol (all messages are plain structured-cloneable objects):
//
//	main -> worker  {type: "init", id, module, memory,
//	                 goRuntime = false, threadRun = false,
//	                 timeOrigin, perfOrigin}
//	                  instantiate `module` (a WebAssembly.Module) against
//	                  `memory` (the shared WebAssembly.Memory). goRuntime
//	                  selects the runtime imports described above;
//	                  timeOrigin/perfOrigin are the main instance's
//	                  nanotime base (go._timeOrigin) and the main
//	                  thread's performance.timeOrigin, required with
//	                  goRuntime so every thread reads one runtime clock.
//	                  If threadRun is true, call wasm_thread_run(id)
//	                  right after posting ready (the call never returns;
//	                  no further messages are processed).
//	worker -> main  {type: "ready", id}
//	                  instantiation finished.
//	main -> worker  {type: "call", seq, name, args = [], repeat = 1}
//	                  call exported function `name` with `args`, `repeat`
//	                  times in a tight loop (so a hammer loop does not pay
//	                  one message round-trip per call).
//	worker -> main  {type: "result", seq, value}
//	                  the return value of the last call.
//	worker -> main  {type: "exit", code}
//	                  Go code on this worker called runtime.exit (e.g. a
//	                  fatal throw); the whole program should exit with
//	                  `code`. Sent just before the host exit hook runs.
//	worker -> main  {type: "error", seq?, message}
//	                  init or call failed; seq is present for call errors.

"use strict";

(() => {
	globalThis.GoWasmWorker = class {
		// post is the function used to send messages back to the main
		// thread (e.g. parentPort.postMessage on Node.js).
		//
		// hostExit, if given, is called as hostExit(code) after Go code
		// running on this worker instance called runtime.exit; it should
		// terminate the worker thread (process.exit in a node worker).
		constructor(post, hostExit) {
			this._post = post;
			this._hostExit = hostExit;
			this._inst = null;
			this._memory = null;
			this._timeOrigin = 0;
			this._perfOrigin = 0;
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
						this._memory = msg.memory;
						this._timeOrigin = msg.timeOrigin ?? 0;
						this._perfOrigin = msg.perfOrigin ?? 0;
						const imports = msg.goRuntime === true
							? this._runtimeImports(msg.module, msg.memory)
							: this._stubImports(msg.module, msg.memory);
						this._inst = await WebAssembly.instantiate(msg.module, imports);
						this._post({ type: "ready", id: this.id });
						if (msg.threadRun === true) {
							// Enter the runtime's worker pool. This blocks
							// inside wasm (a futex wait on the runtime's
							// spawn mailbox) and never returns: this thread
							// now belongs to the Go scheduler. The ready
							// message above was already serialized, so the
							// main thread still receives it.
							this._inst.exports.wasm_thread_run(this.id);
							throw new Error("wasm_thread_run returned");
						}
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
		// runtime cannot run on a probe worker instance.
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
							throw new Error(`worker ${id}: Go runtime import ${name} called on a worker instance (syscall/js and the event loop are main-thread-only under GOWASM=threads)`);
						};
						break;
					}
					default:
						throw new Error(`unsupported import kind ${JSON.stringify(imp.kind)} for ${imp.module}.${imp.name}`);
				}
			}
			return imports;
		}

		// _runtimeImports is _stubImports plus real implementations of the
		// pure-runtime gojs imports, enough for Go code (an M) to run on
		// this instance: write to stdout/stderr, read the shared clock,
		// exit, get randomness. Event-loop and syscall/js imports remain
		// throwing stubs - the runtime never calls them on a worker M, and
		// user code must not either (documented limitation of this phase).
		_runtimeImports(module, memory) {
			const imports = this._stubImports(module, memory);
			const gojs = imports.gojs ?? (imports.gojs = {});
			// All views are created per call: the shared memory can be
			// grown by any thread at any time, so a cached view could come
			// up short.
			const mem = () => new DataView(this._memory.buffer);
			const getInt64 = (sp) => {
				const dv = mem();
				const low = dv.getUint32(sp + 0, true);
				const high = dv.getInt32(sp + 4, true);
				return low + high * 4294967296;
			};
			const setInt64 = (sp, v) => {
				const dv = mem();
				dv.setUint32(sp + 0, v, true);
				dv.setUint32(sp + 4, Math.floor(v / 4294967296), true);
			};

			// func wasmExit(code int32)
			gojs["runtime.wasmExit"] = (sp) => {
				sp >>>= 0;
				const code = mem().getInt32(sp + 8, true);
				// Tell the main thread first (it exits the whole process
				// when it sees this, if its event loop is running), then
				// take this worker thread down via the host hook. Best
				// effort: if the main thread is blocked in a futex wait it
				// cannot process the message, but everything printed via
				// wasmWrite before this point is already on the terminal.
				this._post({ type: "exit", code: code });
				if (this._hostExit !== undefined) {
					this._hostExit(code);
				}
				throw new Error(`worker ${this.id}: Go exited with code ${code}`);
			};

			// func wasmWrite(fd uintptr, p unsafe.Pointer, n int32)
			gojs["runtime.wasmWrite"] = (sp) => {
				sp >>>= 0;
				const fd = getInt64(sp + 8);
				const p = getInt64(sp + 16);
				const n = mem().getInt32(sp + 24, true);
				// fs.writeSync rejects views backed by a SharedArrayBuffer;
				// write a copy. fs is provided by the host bootstrap
				// (wasm_exec_worker_node.js), like in wasm_exec.js.
				fs.writeSync(fd, new Uint8Array(this._memory.buffer, p, n).slice());
			};

			// func resetMemoryDataView()
			gojs["runtime.resetMemoryDataView"] = (sp) => {
				// Views are created per call here; nothing cached to reset.
			};

			// func nanotime1() int64
			gojs["runtime.nanotime1"] = (sp) => {
				sp >>>= 0;
				// The same clock as the main instance's nanotime1:
				// main: timeOrigin + performance.now()_main
				//     = timeOrigin + (absMonotonicMs - perfOrigin_main)
				// where absMonotonicMs = performance.timeOrigin +
				// performance.now() measured on any thread (both derive
				// from one process-wide monotonic clock in Node.js).
				setInt64(sp + 8, (this._timeOrigin + (performance.timeOrigin + performance.now() - this._perfOrigin)) * 1000000);
			};

			// func walltime() (sec int64, nsec int32)
			gojs["runtime.walltime"] = (sp) => {
				sp >>>= 0;
				const msec = (new Date).getTime();
				setInt64(sp + 8, msec / 1000);
				mem().setInt32(sp + 16, (msec % 1000) * 1000000, true);
			};

			// func getRandomData(r []byte)
			gojs["runtime.getRandomData"] = (sp) => {
				sp >>>= 0;
				const array = getInt64(sp + 8);
				const len = getInt64(sp + 16);
				// crypto.getRandomValues rejects SharedArrayBuffer-backed
				// views; fill a copy and write it back.
				const tmp = new Uint8Array(len);
				crypto.getRandomValues(tmp);
				new Uint8Array(this._memory.buffer, array, len).set(tmp);
			};

			return imports;
		}
	};
})();

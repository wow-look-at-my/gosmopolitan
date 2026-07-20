// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// run.js drives the streaming fetch request-body end-to-end test.
//
// Usage:
//   node run.js <lib/wasm dir> <jsfetchstream.wasm>
//
// It starts a plain node:http server on 127.0.0.1, then runs the
// js/wasm guest (main.go, built with the fork toolchain) under Node.js
// with GODEBUG=jsfetchnode=1 and FETCHSTREAM_ADDR pointing at the
// server. All assertions live in the guest; the driver's own failure
// modes are a guest crash and the 120s watchdog.
//
// Endpoints:
//   POST /upload?name=X  count bytes/chunks, respond with a JSON digest
//                        of the received body once it ends
//   POST /hang           count bytes (as name "hang"), never respond,
//                        record whether the request aborted
//   GET  /got?name=X     body bytes received so far for X (plain text)
//   GET  /status         {"hangAborted": bool}

"use strict";

const http = require("node:http");
const crypto = require("node:crypto");
const path = require("node:path");
const { spawn } = require("node:child_process");

const args = process.argv.slice(2);
if (args.length !== 2) {
	console.error("usage: node run.js <lib/wasm dir> <jsfetchstream.wasm>");
	process.exit(1);
}
const [libWasmDir, wasmPath] = args;

const uploads = new Map(); // name -> {bytes, chunks}
let hangAborted = false;

function counter(name) {
	let c = uploads.get(name);
	if (c === undefined) {
		c = { bytes: 0, chunks: 0 };
		uploads.set(name, c);
	}
	return c;
}

const server = http.createServer((req, res) => {
	const url = new URL(req.url, "http://127.0.0.1");
	req.on("error", () => {}); // an aborted upload resets the socket; don't crash

	if (req.method === "POST" && url.pathname === "/upload") {
		const c = counter(url.searchParams.get("name") ?? "");
		const hash = crypto.createHash("sha256");
		req.on("data", (chunk) => {
			hash.update(chunk);
			c.bytes += chunk.length;
			c.chunks++;
		});
		req.on("end", () => {
			res.setHeader("Content-Type", "application/json");
			res.end(JSON.stringify({
				sha256: hash.digest("hex"),
				bytes: c.bytes,
				chunks: c.chunks,
				contentLength: req.headers["content-length"] ?? "",
				transferEncoding: req.headers["transfer-encoding"] ?? "",
			}));
		});
		return;
	}

	if (req.method === "POST" && url.pathname === "/hang") {
		const c = counter("hang");
		let ended = false;
		req.on("data", (chunk) => {
			c.bytes += chunk.length;
			c.chunks++;
		});
		req.on("end", () => {
			ended = true; // deliberately never respond
		});
		req.on("close", () => {
			if (!ended) {
				hangAborted = true;
			}
		});
		return;
	}

	if (req.method === "GET" && url.pathname === "/got") {
		res.end(String(uploads.get(url.searchParams.get("name") ?? "")?.bytes ?? 0));
		return;
	}

	if (req.method === "GET" && url.pathname === "/status") {
		res.setHeader("Content-Type", "application/json");
		res.end(JSON.stringify({ hangAborted }));
		return;
	}

	res.statusCode = 404;
	res.end("not found");
});

server.listen(0, "127.0.0.1", () => {
	const addr = server.address();
	const godebug = (process.env.GODEBUG ? process.env.GODEBUG + "," : "") + "jsfetchnode=1";
	const child = spawn(process.execPath, [path.join(libWasmDir, "wasm_exec_node.js"), wasmPath], {
		stdio: "inherit",
		env: {
			...process.env,
			GODEBUG: godebug,
			FETCHSTREAM_ADDR: `127.0.0.1:${addr.port}`,
		},
	});
	const watchdog = setTimeout(() => {
		console.error("jsfetchstream: watchdog fired after 120s; killing the guest");
		child.kill("SIGKILL");
	}, 120000);
	child.on("exit", (code, signal) => {
		clearTimeout(watchdog);
		server.close();
		if (signal !== null) {
			console.error(`jsfetchstream: guest killed by ${signal}`);
			process.exit(1);
		}
		process.exit(code);
	});
});

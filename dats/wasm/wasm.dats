# The wasm ports: std builds for both, the object writer's host-side
# tests, the two transports that need a real host (node fetch streams,
# the wasip1sock reference host) and the wasmexport compiler tests. The
# toolchain must already be built; node 18+ and wazero must be on PATH.
# GOOS is pinned on every host-side go test: the fork defaults to cosmo.
tests:
	- desc: std builds for js/wasm and wasip1/wasm
	  cmd: export PATH="$PWD/bin:$PATH"; GOOS=js GOARCH=wasm go build std && GOOS=wasip1 GOARCH=wasm go build std
	  timeout: 5m
	  exit: 0

	- desc: the wasm object writer passes its tests on the host
	  cmd: export PATH="$PWD/bin:$PATH"; GOOS=linux GOARCH=amd64 go test cmd/internal/obj/wasm
	  timeout: 5m
	  outputs:
		stdout:
			- "ok  \tcmd/internal/obj/wasm"

	# GODEBUG=jsfetchnode=1 request bodies of unknown length stream, rather than buffer.
	- desc: fetch uploads stream under node
	  cmd: export PATH="$PWD/bin:$PWD/lib/wasm:$PATH"; (cd testdata/jsfetchstream && GOOS=js GOARCH=wasm go build -o "$TMPDIR/jsfetchstream.wasm" .) && node testdata/jsfetchstream/run.js lib/wasm "$TMPDIR/jsfetchstream.wasm"
	  timeout: 5m
	  exit: 0

	- desc: GOWASI=wasmedgesock carries TCP and UDP through the wasip1sock reference host
	  cmd: export PATH="$PWD/bin:$PATH"; cd testdata/wasip1sock/host && GOOS=linux GOARCH=amd64 go test ./...
	  timeout: 10m
	  exit: 0

	- desc: the wasmexport compiler tests pass on both targets
	  cmd: export PATH="$PWD/bin:$PWD/lib/wasm:$PATH"; GOWASIRUNTIME=wazero GOOS=linux GOARCH=amd64 go test cmd/internal/testdir -run 'Test/wasmexport' -target=js/wasm && GOWASIRUNTIME=wazero GOOS=linux GOARCH=amd64 go test cmd/internal/testdir -run 'Test/wasmexport' -target=wasip1/wasm
	  timeout: 5m
	  exit: 0

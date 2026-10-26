# The wasip1 exec wrapper keeps compiled modules in a cache CI shares between runners. A fake wasmtime compiles for a named host.
tests:
	- desc: a module compiled for one host is not served to another
	  cmd: |
		set -e
		export PATH="$PWD/dats/wasm/fakewasmtime:$PATH" GOWASIP1_PRECOMPILED={outputs.cache}
		mkdir -p {outputs.src}
		printf 'module bytes' >{outputs.src}/prog.wasm
		FAKE_WASMTIME_HOST=has_avx512bitalg lib/wasm/go_wasip1_wasm_exec {outputs.src}/prog.wasm
		FAKE_WASMTIME_HOST=plain lib/wasm/go_wasip1_wasm_exec {outputs.src}/prog.wasm
		FAKE_WASMTIME_HOST=has_avx512bitalg lib/wasm/go_wasip1_wasm_exec {outputs.src}/prog.wasm
	  outputs:
		stdout:
			0: "^compiled for has_avx512bitalg$"
			1: "^compiled for plain$"
			2: "^compiled for has_avx512bitalg$"

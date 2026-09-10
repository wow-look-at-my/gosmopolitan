# GOWASM=threads under node. The demos and their gates are dats/wasmthreads.sh.
# The toolchain must already be built, and node 18+ must be on PATH.
tests:
	- desc: the threads demos run and every gate holds
	  cmd: bash dats/wasmthreads.sh
	  exit: 0

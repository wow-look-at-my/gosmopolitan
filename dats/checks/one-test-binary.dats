# go test over many packages links one test binary per port, and dist test
# runs std and cmd that way. A package given a binary of its own fails this.
#
# The toolchain must already be built: run make.bash first, or let the
# CI build leg do it.
tests:
	- desc: std and cmd share one test binary on cosmo
	  cmd: export PATH="$PWD/bin:$PATH"; dats/checks/one-test-binary.sh cosmo amd64 std cmd
	  exit: 0

	- desc: std and cmd share one test binary on js/wasm
	  cmd: export PATH="$PWD/bin:$PATH"; dats/checks/one-test-binary.sh js wasm std cmd
	  exit: 0

	- desc: std and cmd share one test binary on wasip1/wasm
	  cmd: export PATH="$PWD/bin:$PATH"; dats/checks/one-test-binary.sh wasip1 wasm std cmd
	  exit: 0

	- desc: the checker refuses two test binaries
	  cmd: export PATH="$PWD/bin:$PATH"; GOFLAGS="-c -o=$TMPDIR" dats/checks/one-test-binary.sh cosmo amd64 errors strings; test $? -eq 1
	  exit: 0

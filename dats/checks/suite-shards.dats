# The suite and wasm-suite legs each run one part of `go tool dist test`,
# -shard=K/N. A test registered in no part, or a part no leg runs, would
# never run and never fail. These hold the split to the whole suite, for
# every port the legs test. The toolchain must already be built.
tests:
	- desc: the parts of every port's dist test list are its whole list, once each
	  cmd: export PATH="$PWD/bin:$PATH" GOCACHE="$TMPDIR/gocache"; dats/checks/suite-shards.sh lists 2 cosmo/amd64 cosmo/arm64 js/wasm wasip1/wasm
	  timeout: 10m
	  exit: 0

	- desc: every sharded matrix in the workflow runs every part
	  cmd: dats/checks/suite-shards.sh matrix .github/workflows/cosmo-ci.yml
	  exit: 0

	- desc: the guard refuses a part list that drops a test
	  cmd: |
		printf 'case "$*" in\n*-shard=0/2*) echo alpha ;;\n*-shard=1/2*) echo beta ;;\n*) printf "alpha\\nbeta\\ngamma\\n" ;;\nesac\n' > "$TMPDIR/drops.sh"
		rc=0; SUITE_SHARDS_DIST="sh $TMPDIR/drops.sh" dats/checks/suite-shards.sh lists 2 js/wasm || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard refuses a test in two parts
	  cmd: |
		printf 'case "$*" in\n*-shard=0/2*) printf "alpha\\nbeta\\n" ;;\n*-shard=1/2*) printf "beta\\ngamma\\n" ;;\n*) printf "alpha\\nbeta\\ngamma\\n" ;;\nesac\n' > "$TMPDIR/twice.sh"
		rc=0; SUITE_SHARDS_DIST="sh $TMPDIR/twice.sh" dats/checks/suite-shards.sh lists 2 js/wasm || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard accepts parts that are the whole list
	  cmd: |
		printf 'case "$*" in\n*-shard=0/2*) echo alpha ;;\n*-shard=1/2*) printf "beta\\ngamma\\n" ;;\n*) printf "alpha\\nbeta\\ngamma\\n" ;;\nesac\n' > "$TMPDIR/whole.sh"
		SUITE_SHARDS_DIST="sh $TMPDIR/whole.sh" dats/checks/suite-shards.sh lists 2 js/wasm
	  exit: 0

	- desc: the guard refuses a matrix that leaves a part unrun
	  cmd: |
		printf '        shard: [0]\n        shards: [2]\n' > "$TMPDIR/short.yml"
		rc=0; dats/checks/suite-shards.sh matrix "$TMPDIR/short.yml" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard refuses a workflow that names no parts at all
	  cmd: |
		printf 'jobs:\n  suite:\n    runs-on: ubuntu-latest\n' > "$TMPDIR/none.yml"
		rc=0; dats/checks/suite-shards.sh matrix "$TMPDIR/none.yml" || rc=$?
		test "$rc" -eq 2
	  exit: 0

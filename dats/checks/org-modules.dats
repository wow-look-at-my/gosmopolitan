# The org module script tests, CI cases included. On a runner no coding agent
# is an ancestor, so org_ci_build.txt runs its CI cases here and nowhere an
# agent can reach. The toolchain must already be built.
tests:
	- desc: every org module script test passes, CI cases included
	  cmd: |
		set -eu -o pipefail
		export GOCACHE="$(mktemp -d)" PATH="$PWD/bin:$PATH"
		cd src/cmd/go
		go test -run 'TestScript/org_' -v . 2>&1 | grep -E -e '--- (PASS|FAIL|SKIP)' -e 'script_test.go:[0-9]+: (FAIL|SKIP)' -e '^(ok|FAIL)' -e '[Ee]rror|not found|panic:'
	  outputs:
		stdout:
			- "--- PASS: TestScript/org_ci_build"
			- "--- PASS: TestScript/org_branch_head"
			- "--- PASS: TestScript/org_declared_sync"
			- "--- PASS: TestScript/org_heads_passed"
			- "--- PASS: TestScript/org_local_run"
	  timeout: 20m
	  exit: 0

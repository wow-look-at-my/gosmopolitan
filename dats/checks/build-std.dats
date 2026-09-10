# The uprev tripwire: every std package builds for cosmo on both
# architectures, and the go-toolchain consumers that reach the cosmo
# syscall surface build against it. The execution suites compile only
# what fizzbuzz and runtimeprobe import, so a package an upstream merge
# re-partitioned out of cosmo shows up here first. The toolchain must
# already be built.
tests:
	- desc: std builds for cosmo on amd64 and arm64
	  cmd: export PATH="$PWD/bin:$PATH"; GOOS=cosmo GOARCH=amd64 go build std && GOOS=cosmo GOARCH=arm64 go build std
	  timeout: 15m
	  exit: 0

	- desc: x/sys, modernc libc and sqlite build for cosmo on both architectures
	  cmd: |
		set -eu
		export PATH="$PWD/bin:$PATH"
		cd "$(mktemp -d)"
		go mod init cosmo-downstream-smoke
		go get golang.org/x/sys@latest modernc.org/libc@latest modernc.org/sqlite@latest
		GOOS=cosmo GOARCH=amd64 go build golang.org/x/sys/unix modernc.org/libc modernc.org/sqlite
		GOOS=cosmo GOARCH=arm64 go build golang.org/x/sys/unix modernc.org/libc modernc.org/sqlite
	  timeout: 10m
	  exit: 0

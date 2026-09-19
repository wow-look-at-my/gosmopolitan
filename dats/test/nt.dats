# The NT host: the APE binaries the build legs made boot here and answer,
# and the runner offers AF_UNIX, which the cosmo unix socket layer needs.
# Runs in the test job on windows, over the binaries it downloaded.
tests:
	- desc: AF_UNIX binds on this runner, natively and through .NET
	  cmd: pwsh -NoProfile -File dats/test/af-unix.ps1
	  exit: 0

	- desc: the Linux- and Windows-origin fizzbuzz boot and answer
	  cmd: pwsh -NoProfile -File dats/test/nt-boot.ps1
	  exit: 0

	# What a stripped PATH does to a program this fork never built. The suite
	# leg's largest red is children that die under one, so the answer says
	# whether the host or the binary decides it.
	- desc: a system binary and an upstream Go binary start from a copy on this host
	  cmd: pwsh -NoProfile -File dats/test/nt-strippedpath.ps1
	  exit: 0
	  outputs:
		stdout:
			- "system binary from a copy, PATH=dot: exit 0"
			- "system binary from a copy, PATH=empty: exit 0"
			- "upstream gofmt from a copy, PATH=inherited: exit 0"
			- "upstream gofmt from a copy, PATH=dot: exit 0"
			- "upstream gofmt from a copy, PATH=empty: exit 0"

	# The swig suite's -flto link writes an image NT calls invalid. This says
	# whether gcc writes a runnable one under -flto here without our object.
	- desc: mingw gcc writes a runnable image under -flto on this host
	  cmd: pwsh -NoProfile -File dats/test/nt-lto.ps1
	  exit: 0

	# This host needs no loader: the APE is a PE, and the OS maps the payload
	# out of it. readonly-boot.dats makes the same claim for linux and darwin.
	- desc: a read-only host still runs the program
	  cmd: pwsh -NoProfile -File dats/test/readonly-boot.ps1
	  exit: 0
	  outputs:
		stdout:
			- "read-only NT boot: fizzbuzz"

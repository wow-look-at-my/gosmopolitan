# The GOOS=cosmo package tests, run on this host through the misc/cosmo exec
# wrappers. They live here rather than in the workflow because each one names
# the tests it covers, and a name list is a test-selection decision: a workflow
# step is a scheduler, not a place to keep one.
tests:
	- desc: the cosmo syscall shim package
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go test -count=1 internal/runtime/syscall/cosmo
	  outputs:
		stdout:
			- "ok  \tinternal/runtime/syscall/cosmo"

	- desc: the runtime's Apple ABI pins, signal tables and the NT DNS layout pin
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go test -count=1 -run 'TestCosmoXnuItimervalABI|TestCosmoTimevalTranslation|TestCosmoSig|TestCosmoDarwinFutexDelay|TestNTFixedInfoLayout' runtime
	  outputs:
		stdout:
			- "ok  \truntime"

	# A name list, not the whole package: syscall's suite needs a real host
	# surface, while these are the Apple-struct conversions and the auxv shim
	# the emulation can host-test.
	- desc: the syscall package's Apple conversions and the auxv shim
	  cmd: export PATH="$PWD/bin:$PWD/misc/cosmo:$PATH"; GOOS=cosmo go test -count=1 -run 'TestDarwinStatfsToLinux|TestDarwinMntFlagsToLinux|TestDarwinUtsnameToLinux|TestLinuxStructSizes|TestOpenAuxv' syscall
	  outputs:
		stdout:
			- "ok  \tsyscall"

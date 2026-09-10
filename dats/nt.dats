# The NT host: the APE binaries the build legs made boot here and answer,
# and the runner offers AF_UNIX, which the cosmo unix socket layer needs.
# Runs in the test job on windows, over the binaries it downloaded.
tests:
	- desc: AF_UNIX binds on this runner, natively and through .NET
	  cmd: pwsh -NoProfile -File dats/af-unix.ps1
	  exit: 0

	- desc: the Linux- and Windows-origin fizzbuzz boot and answer
	  cmd: pwsh -NoProfile -File dats/nt-boot.ps1
	  exit: 0

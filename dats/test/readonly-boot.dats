# An APE boots on a host with nothing writable. The loader reads the payload
# out of the APE where it lies, so no path needs a write. These cases say so
# on the host the test job runs on, over a binary a build leg made.
#
# readonly-boot.sh makes the APE's directory, the unpack directory and the
# loader's directory read-only, then proves a write there fails before it
# trusts the run. Root gets bind mounts, and anybody else gets a mode bit.
#
# The Windows half of this claim is in nt.dats: that host needs no loader,
# because the OS maps the payload out of the PE the APE already is.
tests:
	- desc: a read-only host with a resident loader runs the program
	  cmd: dats/test/readonly-boot.sh resident binaries/ape-binary-Linux/fizzbuzz.com
	  exit: 0
	  outputs:
		stdout:
			- "read-only boot through a resident loader: fizzbuzz"

	- desc: a read-only host with no loader refuses, and names the fix
	  cmd: dats/test/readonly-boot.sh refuse binaries/ape-binary-Linux/fizzbuzz.com
	  exit: 0
	  outputs:
		stdout:
			- "refuses: exit 121, and names the fix"

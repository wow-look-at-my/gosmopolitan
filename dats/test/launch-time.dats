# The launch cost of an APE through its boot script, on the host the test
# job runs on. dats prints the duration of each case. The installed case
# execs the loader where it lies, which names the process after the
# loader. The cached case is the default boot. On darwin it execs the
# loader from a per-user cache under the program's name.
tests:
	- desc: 200 launches through a loader the script execs where it lies
	  cmd: dats/test/launch-time.sh installed binaries/ape-binary-Linux/fizzbuzz.com 200
	  exit: 0
	  outputs:
		stdout:
			- "200 installed launches: fizzbuzz"

	- desc: 200 launches of the default boot
	  cmd: dats/test/launch-time.sh cached binaries/ape-binary-Linux/fizzbuzz.com 200
	  exit: 0
	  outputs:
		stdout:
			- "200 cached launches: fizzbuzz"

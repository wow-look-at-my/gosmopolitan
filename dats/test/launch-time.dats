# The launch cost of an APE through this host's loader, on the host the test
# job runs on. dats prints the duration of the case, and that duration is
# the cost of the given number of launches.
tests:
	- desc: 200 launches through the loader
	  cmd: dats/test/launch-time.sh binaries/ape-binary-Linux/fizzbuzz.com 200
	  exit: 0
	  outputs:
		stdout:
			- "200 launches: fizzbuzz"

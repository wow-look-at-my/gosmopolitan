# The loader's launch cost, on the host the test job runs on. Each case
# starts the program the same number of times, so the difference between
# their durations is what the loader's re-exec costs. That re-exec is what
# gives ps and htop the program's argv in place of the loader's.
tests:
	- desc: 200 launches through the loader and its re-exec
	  cmd: dats/test/launch-time.sh reexec binaries/ape-binary-Linux/fizzbuzz.com 200
	  exit: 0
	  outputs:
		stdout:
			- "200 reexec launches: fizzbuzz"

	- desc: 200 launches that start in second-run form, with no re-exec
	  cmd: dats/test/launch-time.sh once binaries/ape-binary-Linux/fizzbuzz.com 200
	  exit: 0
	  outputs:
		stdout:
			- "200 once launches: fizzbuzz"

# T.Serial stops every other test in the process, so it waits on a condition
# only another test can clear. Asking for it from inside a synctest bubble
# parks a bubble goroutine on a condition no bubble goroutine can signal, and
# synctest reports a deadlock naming neither Serial nor the helper that asked.
tests:
	- desc: no test opens a synctest bubble that waits on the serial barrier
	  cmd: dats/bubble-serial.sh .
	  exit: 0

	- desc: the guard refuses a bubble whose body reaches Serial
	  cmd: mkdir -p "$TMPDIR/b/src/pkg" && printf 'package pkg\n\nfunc Helper(t testing.TB) {\n\tif s, ok := t.(interface{ Serial() }); ok {\n\t\ts.Serial()\n\t}\n}\n\nfunc TestBad(t *testing.T) {\n\tsynctest.Test(t, testBad)\n}\nfunc testBad(t *testing.T) {\n\tHelper(t)\n}\n' > "$TMPDIR/b/src/pkg/x_test.go"; dats/bubble-serial.sh "$TMPDIR/b"; test $? -eq 2
	  exit: 0

	- desc: the guard accepts a wrapper that takes the barrier first
	  cmd: mkdir -p "$TMPDIR/c/src/pkg" && printf 'package pkg\n\nfunc Helper(t testing.TB) {\n\tif s, ok := t.(interface{ Serial() }); ok {\n\t\ts.Serial()\n\t}\n}\n\nfunc TestGood(t *testing.T) {\n\tt.Serial()\n\tsynctest.Test(t, testGood)\n}\nfunc testGood(t *testing.T) {\n\tHelper(t)\n}\n' > "$TMPDIR/c/src/pkg/x_test.go"; dats/bubble-serial.sh "$TMPDIR/c"
	  exit: 0

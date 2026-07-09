// Windows halves of the signal checks: there is no kill(2) to send a
// signal to yourself and no SIGUSR1/2, so these print explicit skip
// lines (still matching apetest's "ok <name>" contract).

//go:build windows

package main

func checkSignalNotify() {
	ok("sigterm", "skipped-windows")
	ok("sigusr2", "skipped-windows")
}

func raiseFatalChild() {
	// Unreachable: checkWaitSig never spawns the child on Windows.
}

func checkWaitSigStatus(err error) {
	fail("waitsig", "unexpected call on windows (err=%v)", err)
}

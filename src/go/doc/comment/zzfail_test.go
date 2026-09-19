package comment

import "testing"

func TestDeliberateFailure(tst *testing.T) {
	tst.Logf("context line that must survive")
	tst.Errorf("got %d, want %d", 3, 4)
}

func TestDeliberatePanic(tst *testing.T) {
	tst.Logf("panic context that must survive")
}

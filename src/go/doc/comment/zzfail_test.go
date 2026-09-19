package comment

import "testing"

func TestDeliberateFailure(tst *testing.T) {
	tst.Logf("context line that must survive")
	tst.Errorf("got %d, want %d", 3, 4)
}

func TestDeliberateFatalInSubtest(tst *testing.T) {
	tst.Run("inner", func(sub *testing.T) {
		sub.Logf("subtest context that must survive")
		sub.Fatalf("inner exploded")
	})
}

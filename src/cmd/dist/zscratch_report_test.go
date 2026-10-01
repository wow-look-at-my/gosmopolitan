package main

import (
	"os"
	"strings"
	"testing"
)

func TestScratchRealStream(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("SCRATCH_STREAM"))
	if err != nil {
		t.Fatal(err)
	}
	inp := strings.ReplaceAll(string(raw), ":nethttpomithttp2", "")
	got, _, done := runReport(inp)
	t.Logf("done=%v", done)
	t.Logf("got=%q", got)
	for _, size := range []int{1, 7, 4096, 32768} {
		var out strings.Builder
		var timings testTimings
		rep := newTestReport(&out, &timings, nil)
		for off := 0; off < len(inp); off += size {
			rep.Write([]byte(inp[off:min(off+size, len(inp))]))
		}
		rep.Flush()
		t.Logf("size=%d got=%q", size, out.String())
	}
}

package main

import (
	"fmt"
	"os"
	"strings"
)

// checkBootImage measures what the Linux memfd loader staged for this
// process against the whole APE on disk. The loader copies only the page
// span the host payload's program headers cover, so a pristine APE that
// booted through it runs from an image smaller than its file: the script
// header, the other architectures' payloads and any appended blob are
// outside it, and the payload reaches those through the on-disk path.
// apetest holds the artifact, so that side derives the exact span from
// the payload's own program headers; this side reports the two sizes.
func checkBootImage() {
	if cosmoHostOS() != "linux" {
		ok("bootimage", fmt.Sprintf("skipped on %s (no memfd loader)", cosmoHostOS()))
		return
	}
	link, err := os.Readlink("/proc/self/exe")
	if err != nil {
		fail("bootimage", "readlink /proc/self/exe: %v", err)
		return
	}
	if !strings.HasPrefix(link, "/memfd:") {
		fail("bootimage", "/proc/self/exe is %q, want the loader's memfd: run a pristine copy", link)
		return
	}
	mem, err := os.Stat("/proc/self/exe")
	if err != nil {
		fail("bootimage", "stat /proc/self/exe: %v", err)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		fail("bootimage", "os.Executable: %v", err)
		return
	}
	ape, err := os.Stat(exe)
	if err != nil {
		fail("bootimage", "stat %s: %v", exe, err)
		return
	}
	staged, file := mem.Size(), ape.Size()
	if staged >= file {
		fail("bootimage", "staged %d bytes of a %d byte APE, want a strict part of it", staged, file)
		return
	}
	ok("bootimage", fmt.Sprintf("staged=%d file=%d", staged, file))
}

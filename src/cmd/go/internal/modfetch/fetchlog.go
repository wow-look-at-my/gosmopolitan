// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package modfetch

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"cmd/go/internal/modfetch/codehost"

	"golang.org/x/mod/module"
)

// fetchLog is where a fetchReport prints. Tests replace it.
var fetchLog io.Writer = os.Stderr

// A fetchReport is the line the go command prints for a module version it
// fetched, when the version is complete in the module cache.
type fetchReport struct {
	mod   module.Version
	start time.Time
	rec   *codehost.Fetch
	once  sync.Once
}

func newFetchReport(mod module.Version) *fetchReport {
	return &fetchReport{mod: mod, start: time.Now(), rec: new(codehost.Fetch)}
}

// finish prints the line once. A fetch that found the module already in the
// cache names no route and prints nothing. It accepts a nil report.
func (rep *fetchReport) finish(err error) {
	if rep == nil {
		return
	}
	rep.once.Do(func() {
		route, size, transfer, first := rep.rec.Stats()
		if err == nil && route == "" {
			return
		}
		// The total is the wall time of this download plus any part of the transfer that ran before it began.
		total := time.Since(rep.start)
		if !first.IsZero() && first.Before(rep.start) {
			total += min(transfer, rep.start.Sub(first))
		}
		fmt.Fprintln(fetchLog, fetchLine(moduleName(rep.mod), route, size, transfer, total, err != nil))
	})
}

// moduleName is how "go: downloading" names mod. A toolchain module names the
// toolchain, as "go1.13.1 (darwin/amd64)".
func moduleName(mod module.Version) string {
	if mod.Path != "golang.org/toolchain" {
		return mod.Path + " " + mod.Version
	}
	_, vers, _ := strings.Cut(mod.Version, "-")
	if dot := strings.LastIndex(vers, "."); dot >= 0 {
		goos, goarch, _ := strings.Cut(vers[dot+1:], "-")
		vers = vers[:dot] + " (" + goos + "/" + goarch + ")"
	}
	return vers
}

// fetchLine formats the report of one download.
func fetchLine(name, route string, size int64, transfer, total time.Duration, failed bool) string {
	var line strings.Builder
	fmt.Fprintf(&line, "go: downloading %s: ", name)
	switch {
	case failed:
		line.WriteString("failed")
	case size <= 0:
		fmt.Fprintf(&line, "%s, nothing transferred", route)
	default:
		fmt.Fprintf(&line, "%s, %s in %s", route, formatBytes(float64(size)), formatSeconds(transfer))
		if transfer > 0 {
			fmt.Fprintf(&line, " (%s/s)", formatBytes(float64(size)/transfer.Seconds()))
		}
	}
	fmt.Fprintf(&line, ", %s total", formatSeconds(total))
	return line.String()
}

// formatBytes writes size in decimal units, as "512 B" or "1.2 MB".
func formatBytes(size float64) string {
	if size < 1000 {
		return fmt.Sprintf("%.0f B", size)
	}
	for _, unit := range []string{"kB", "MB", "GB"} {
		size /= 1000
		if size < 1000 {
			return fmt.Sprintf("%.1f %s", size, unit)
		}
	}
	return fmt.Sprintf("%.1f TB", size/1000)
}

func formatSeconds(took time.Duration) string {
	return fmt.Sprintf("%.2fs", took.Seconds())
}

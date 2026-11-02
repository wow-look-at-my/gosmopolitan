// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || cosmo

package syscall

// Getloadavg returns the one, five and fifteen minute load averages from
// procfs. A Cosmopolitan build on a host without procfs reports the open
// error, because getloadavg is not a system call there.
func Getloadavg() (loadavg [3]float64, err error) {
	fd, err := Open("/proc/loadavg", O_RDONLY|O_CLOEXEC, 0)
	if err != nil {
		return loadavg, err
	}
	defer Close(fd)
	var buf [128]byte
	n, err := Read(fd, buf[:])
	if err != nil {
		return loadavg, err
	}
	rest := buf[:n]
	for i := range loadavg {
		field, tail, ok := loadField(rest)
		if !ok {
			return loadavg, EINVAL
		}
		loadavg[i] = field
		rest = tail
	}
	return loadavg, nil
}

// loadField reads one decimal field and the bytes after it.
func loadField(b []byte) (float64, []byte, bool) {
	whole := 0.0
	frac := 0.0
	div := 1.0
	seen := false
	dot := false
	i := 0
	for ; i < len(b); i++ {
		c := b[i]
		if c >= '0' && c <= '9' {
			seen = true
			if dot {
				div *= 10
				frac += float64(c-'0') / div
			} else {
				whole = whole*10 + float64(c-'0')
			}
			continue
		}
		if c == '.' && !dot {
			dot = true
			continue
		}
		break
	}
	if !seen {
		return 0, nil, false
	}
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return whole + frac, b[i:], true
}

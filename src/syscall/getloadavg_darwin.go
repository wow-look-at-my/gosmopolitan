// Copyright The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin

package syscall

// Getloadavg returns the one, five and fifteen minute load averages.
//
// The kernel answers vm.loadavg with struct loadavg: the fixed-point
// averages, then the scale they are divided by.
func Getloadavg() (loadavg [3]float64, err error) {
	raw, err := Sysctl("vm.loadavg")
	if err != nil {
		return loadavg, err
	}
	b := []byte(raw)
	if len(b) < 20 {
		return loadavg, EINVAL
	}
	scale := float64(loadLe32(b[16:20]))
	if scale == 0 {
		return loadavg, EINVAL
	}
	for i := range loadavg {
		loadavg[i] = float64(loadLe32(b[i*4:i*4+4])) / scale
	}
	return loadavg, nil
}

// loadLe32 reads a little-endian uint32. Every darwin target is little-endian.
func loadLe32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

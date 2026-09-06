// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "unsafe"

// ntSendfileChunk is the bounce buffer one pass copies. It is a local
// array, so it also decides how much stack this call needs.
const ntSendfileChunk = 8 << 10

// ntEmuSendfile implements the Linux sendfile syscall.
//
// Windows has no sendfile. TransmitFile moves a file to a socket
// without a copy, but it serves only a socket, and Linux's sendfile
// serves any writable descriptor. So this copies through a buffer,
// which is what a caller falling back to io.Copy would do anyway. What
// it buys is that the fallback is not needed: internal/poll's SendFile
// reaches the syscall on every host an APE runs on.
//
// The offset rules are Linux's. With off nil the read advances the
// input file's own position. With off set the position stays where it
// is, and off names where to read and receives the offset after the
// last byte read.
func ntEmuSendfile(out, in int32, off *int64, count uintptr) (r1, r2, errno uintptr) {
	ein, ok := ntFDLookup(in)
	if !ok {
		return ntFail3(ntEBADF)
	}
	// Linux takes only a mmapable input, which rules out a socket and
	// a pipe. A caller handed one of those must fall back.
	if ein.kind != ntFDFile {
		return ntFail3(ntEINVAL)
	}
	if _, ok := ntFDLookup(out); !ok {
		return ntFail3(ntEBADF)
	}
	start := int64(0)
	if off != nil {
		if *off < 0 {
			return ntFail3(ntEINVAL)
		}
		start = *off
	}
	if count > 1<<31-1 {
		count = 1 << 31 // one pass never asks for more than an int32
	}

	var buf [ntSendfileChunk]byte
	var done uintptr
	for done < count {
		want := count - done
		if want > ntSendfileChunk {
			want = ntSendfileChunk
		}
		var got, eno uintptr
		if off != nil {
			got, _, eno = ntEmuPreadPwrite(in, unsafe.Pointer(&buf[0]), int32(want), start+int64(done), false)
		} else {
			got, _, eno = ntEmuRead(in, unsafe.Pointer(&buf[0]), int32(want))
		}
		if eno != 0 {
			// A partial transfer is a success: Linux reports the bytes
			// it moved and leaves the error for the next call.
			if done > 0 {
				break
			}
			return ntFail3(eno)
		}
		if got == 0 {
			break // end of the input file
		}

		// The output may take less than one pass read, so keep writing
		// until the buffer is gone. Stopping early would drop bytes
		// that no longer exist anywhere else.
		var sent uintptr
		for sent < got {
			n, _, weno := ntEmuWrite(out, unsafe.Pointer(&buf[sent]), int32(got-sent))
			if weno != 0 {
				if done+sent > 0 {
					return done + sent, 0, 0
				}
				return ntFail3(weno)
			}
			if n == 0 {
				return done + sent, 0, 0
			}
			sent += n
		}
		done += sent
		if got < want {
			break // a short read is the end of the file
		}
	}

	if off != nil {
		*off = start + int64(done)
	}
	return done, 0, 0
}

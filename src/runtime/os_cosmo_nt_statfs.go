// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "unsafe"

// statfs(2) and fstatfs(2) on an NT host.
//
// NT has no single call that answers this. Three do, over a VOLUME
// rather than a path, so each emulation first maps its argument to the
// volume's mount point with GetVolumePathNameW. GetDiskFreeSpaceW gives
// the cluster geometry, GetDiskFreeSpaceExW the quota-aware free space,
// and GetVolumeInformationW the name length and the filesystem name.
//
// Two Linux fields have no NT source and stay ZERO rather than invented.
// Files and Ffree count inodes, which NTFS does not expose. Fsid has no
// counterpart either. A field this host cannot answer must read as
// unknown, not as a plausible number nothing measured.

// ntLinuxStatfs is the struct statfs(2) fills. It must match
// syscall.Statfs_t for GOOS=cosmo on amd64.
type ntLinuxStatfs struct {
	Type    int64
	Bsize   int64
	Blocks  uint64
	Bfree   uint64
	Bavail  uint64
	Files   uint64
	Ffree   uint64
	Fsid    [2]int32
	Namelen int64
	Frsize  int64
	Flags   int64
	Spare   [4]int64
}

// Linux statfs f_type magics for the filesystems Windows names, and the
// FILE_* volume flag whose Linux ST_* counterpart exists.
const (
	ntMagicNTFS  = 0x5346544e
	ntMagicFAT   = 0x00004d44
	ntMagicEXFAT = 0x2011bab0
	ntMagicREFS  = 0x72654653

	ntFileReadOnlyVolume = 0x00080000
	ntLinuxSTRdonly      = 0x1
)

// ntVolumePathW maps any path on a volume to that volume's mount point,
// which is what the three volume calls below take. The result keeps the
// NUL GetVolumePathNameW writes.
func ntVolumePathW(w []uint16) ([]uint16, uintptr) {
	if ntGetVolumePathNameWFn == 0 {
		return nil, ntENOSYS
	}
	// _NT_MAX_PATH+1 is what the API documents as always sufficient.
	vol := make([]uint16, _NT_MAX_PATH+1)
	r, werr := ntcallE(ntGetVolumePathNameWFn, uintptr(unsafe.Pointer(&w[0])),
		uintptr(unsafe.Pointer(&vol[0])), uintptr(len(vol)), 0, 0, 0, 0)
	if r == 0 {
		return nil, ntErrno(werr)
	}
	return vol, 0
}

// ntStatfsVolume fills dst for the volume whose mount point is volW.
//
// The cluster geometry is what Linux reports as a block, so Bsize and
// Frsize are bytes-per-sector times sectors-per-cluster and the three
// counts are in clusters. GetDiskFreeSpaceW's own free-cluster count is
// the volume's, which ignores a per-user quota; Bavail therefore comes
// from GetDiskFreeSpaceExW's caller-available figure instead, converted
// to clusters. That is the same split Linux draws between f_bfree and
// f_bavail.
func ntStatfsVolume(volW []uint16, dst *ntLinuxStatfs) uintptr {
	if ntGetDiskFreeSpaceWFn == 0 {
		return ntENOSYS
	}
	var sectorsPerCluster, bytesPerSector, freeClusters, totalClusters uint32
	r, werr := ntcallE(ntGetDiskFreeSpaceWFn, uintptr(unsafe.Pointer(&volW[0])),
		uintptr(unsafe.Pointer(&sectorsPerCluster)),
		uintptr(unsafe.Pointer(&bytesPerSector)),
		uintptr(unsafe.Pointer(&freeClusters)),
		uintptr(unsafe.Pointer(&totalClusters)), 0, 0)
	if r == 0 {
		return ntErrno(werr)
	}
	cluster := uint64(sectorsPerCluster) * uint64(bytesPerSector)
	if cluster == 0 {
		// A zero block size would make every byte figure below a
		// division by zero. Refuse rather than report nonsense.
		return ntEINVAL
	}

	*dst = ntLinuxStatfs{}
	dst.Bsize = int64(cluster)
	dst.Frsize = int64(cluster)
	dst.Blocks = uint64(totalClusters)
	dst.Bfree = uint64(freeClusters)
	dst.Bavail = uint64(freeClusters)

	// GetDiskFreeSpaceW's cluster counts saturate on a volume larger
	// than the 32-bit fields hold, so the Ex form's byte totals win
	// wherever it answers. Its first output is the quota-aware figure.
	if ntGetDiskFreeSpaceExWFn != 0 {
		var availToCaller, totalBytes, totalFree uint64
		r, _ := ntcallE(ntGetDiskFreeSpaceExWFn, uintptr(unsafe.Pointer(&volW[0])),
			uintptr(unsafe.Pointer(&availToCaller)),
			uintptr(unsafe.Pointer(&totalBytes)),
			uintptr(unsafe.Pointer(&totalFree)), 0, 0, 0)
		if r != 0 {
			dst.Blocks = totalBytes / cluster
			dst.Bfree = totalFree / cluster
			dst.Bavail = availToCaller / cluster
		}
	}

	if ntGetVolumeInformationWFn != 0 {
		var serial, maxComponent, fsFlags uint32
		var fsName [16]uint16
		r, _ := ntcallE(ntGetVolumeInformationWFn, uintptr(unsafe.Pointer(&volW[0])),
			0, 0,
			uintptr(unsafe.Pointer(&serial)),
			uintptr(unsafe.Pointer(&maxComponent)),
			uintptr(unsafe.Pointer(&fsFlags)),
			uintptr(unsafe.Pointer(&fsName[0])))
		if r != 0 {
			dst.Namelen = int64(maxComponent)
			dst.Type = ntFsMagic(fsName[:])
			if fsFlags&ntFileReadOnlyVolume != 0 {
				dst.Flags = ntLinuxSTRdonly
			}
			// The serial number is the closest thing NT has to an fsid,
			// and it identifies the volume the same way. Linux splits
			// its own across two words, so the high half stays zero.
			dst.Fsid[0] = int32(serial)
		}
	}
	return 0
}

// ntFsMagic maps a Windows filesystem name to the Linux f_type magic for
// the same filesystem. A name with no Linux counterpart reports zero,
// which is what Linux itself reports for a filesystem with no magic.
func ntFsMagic(name []uint16) int64 {
	switch {
	case ntFsNameIs(name, "NTFS"):
		return ntMagicNTFS
	case ntFsNameIs(name, "ReFS"):
		return ntMagicREFS
	case ntFsNameIs(name, "exFAT"):
		return ntMagicEXFAT
	case ntFsNameIs(name, "FAT32"), ntFsNameIs(name, "FAT"):
		return ntMagicFAT
	}
	return 0
}

// ntFsNameIs compares a NUL-terminated UTF-16 filesystem name against an
// ASCII one, case-insensitively. Windows spells these names in ASCII, so
// a unit above 0x7f can only be a different name.
func ntFsNameIs(name []uint16, want string) bool {
	for i := 0; i < len(want); i++ {
		if i >= len(name) {
			return false
		}
		c := name[i]
		if c >= 0x80 {
			return false
		}
		b := byte(c)
		if b >= 'a' && b <= 'z' {
			b -= 'a' - 'A'
		}
		w := want[i]
		if w >= 'a' && w <= 'z' {
			w -= 'a' - 'A'
		}
		if b != w {
			return false
		}
	}
	return len(name) > len(want) && name[len(want)] == 0
}

// ntEmuStatfs emulates statfs(2).
func ntEmuStatfs(cpath *byte, dst *ntLinuxStatfs) (r1, r2, errno uintptr) {
	if dst == nil {
		return ntFail3(ntEFAULT)
	}
	w := ntPathW(ntCPath(cpath))
	if w == nil {
		return ntFail3(ntENOENT)
	}
	volW, eno := ntVolumePathW(w)
	if eno != 0 {
		return ntFail3(eno)
	}
	if eno := ntStatfsVolume(volW, dst); eno != 0 {
		return ntFail3(eno)
	}
	return 0, 0, 0
}

// ntEmuFstatfs emulates fstatfs(2). NT answers this over a path, so the
// descriptor's own path is recovered from its handle first.
func ntEmuFstatfs(fd int32, dst *ntLinuxStatfs) (r1, r2, errno uintptr) {
	if dst == nil {
		return ntFail3(ntEFAULT)
	}
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind != ntFDFile && e.kind != ntFDDir {
		// A socket or a pipe belongs to no filesystem. Linux reports
		// ENOSYS for exactly this case.
		return ntFail3(ntENOSYS)
	}
	if ntGetFinalPathNameByHandleWFn == 0 {
		return ntFail3(ntENOSYS)
	}
	w := make([]uint16, _NT_MAX_PATH+1)
	n, werr := ntcallE(ntGetFinalPathNameByHandleWFn, e.handle,
		uintptr(unsafe.Pointer(&w[0])), uintptr(len(w)-1), 0, 0, 0, 0)
	if n == 0 || n >= uintptr(len(w)) {
		if n == 0 {
			return ntFail3(ntErrno(werr))
		}
		return ntFail3(ntENAMETOOLONG)
	}
	w[n] = 0
	volW, eno := ntVolumePathW(w)
	if eno != 0 {
		return ntFail3(eno)
	}
	if eno := ntStatfsVolume(volW, dst); eno != 0 {
		return ntFail3(eno)
	}
	return 0, 0, 0
}

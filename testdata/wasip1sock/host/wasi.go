// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The wasi_snapshot_preview1 host functions: the baseline surface a Go
// wasip1 binary needs, plus the WasmEdge socket extension.

package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

var (
	i32 = api.ValueTypeI32
	i64 = api.ValueTypeI64
)

// register instantiates the custom wasi_snapshot_preview1 module in r.
func (h *wasiHost) register(ctx context.Context, r wazero.Runtime) error {
	b := r.NewHostModuleBuilder("wasi_snapshot_preview1")

	export := func(name string, params, results []api.ValueType, fn func(ctx context.Context, mod api.Module, stack []uint64)) {
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, mod api.Module, stack []uint64) {
				fn(ctx, mod, stack)
			}), params, results).
			Export(name)
	}
	errnoFn := func(name string, params []api.ValueType, fn func(mod api.Module, stack []uint64) uint16) {
		export(name, params, []api.ValueType{i32}, func(ctx context.Context, mod api.Module, stack []uint64) {
			errno := fn(mod, stack)
			if h.trace && errno != 0 {
				h.tracef("%s -> errno %d", name, errno)
			}
			stack[0] = uint64(errno)
		})
	}
	// stub registers an always-ENOSYS function so that unrelated
	// imports (file system paths and the like) fail cleanly instead
	// of failing instantiation.
	stub := func(name string, params []api.ValueType, errno uint16) {
		errnoFn(name, params, func(api.Module, []uint64) uint16 {
			h.tracef("stub %s", name)
			return errno
		})
	}

	// args and environ.
	errnoFn("args_sizes_get", []api.ValueType{i32, i32}, func(mod api.Module, s []uint64) uint16 {
		return h.sizesGet(mod, uint32(s[0]), uint32(s[1]), h.args)
	})
	errnoFn("args_get", []api.ValueType{i32, i32}, func(mod api.Module, s []uint64) uint16 {
		return h.vectorGet(mod, uint32(s[0]), uint32(s[1]), h.args)
	})
	errnoFn("environ_sizes_get", []api.ValueType{i32, i32}, func(mod api.Module, s []uint64) uint16 {
		return h.sizesGet(mod, uint32(s[0]), uint32(s[1]), h.env)
	})
	errnoFn("environ_get", []api.ValueType{i32, i32}, func(mod api.Module, s []uint64) uint16 {
		return h.vectorGet(mod, uint32(s[0]), uint32(s[1]), h.env)
	})

	// clocks.
	errnoFn("clock_time_get", []api.ValueType{i32, i64, i32}, func(mod api.Module, s []uint64) uint16 {
		var ns uint64
		switch uint32(s[0]) {
		case clockRealtime:
			ns = uint64(time.Now().UnixNano())
		case clockMonotonic:
			ns = uint64(time.Since(h.monoStart))
		default:
			return wasiEINVAL
		}
		if !mod.Memory().WriteUint64Le(uint32(s[2]), ns) {
			return wasiEFAULT
		}
		return wasiSuccess
	})
	errnoFn("clock_res_get", []api.ValueType{i32, i32}, func(mod api.Module, s []uint64) uint16 {
		if !mod.Memory().WriteUint64Le(uint32(s[1]), 1000) {
			return wasiEFAULT
		}
		return wasiSuccess
	})

	// stdio and socket I/O.
	errnoFn("fd_write", []api.ValueType{i32, i32, i32, i32}, h.fdWrite)
	errnoFn("fd_read", []api.ValueType{i32, i32, i32, i32}, h.fdRead)
	errnoFn("fd_close", []api.ValueType{i32}, h.fdClose)
	errnoFn("fd_fdstat_get", []api.ValueType{i32, i32}, h.fdFdstatGet)
	errnoFn("fd_fdstat_set_flags", []api.ValueType{i32, i32}, h.fdFdstatSetFlags)

	// no preopens: the fd 3+ prestat probe stops at the first EBADF.
	stub("fd_prestat_get", []api.ValueType{i32, i32}, wasiEBADF)
	stub("fd_prestat_dir_name", []api.ValueType{i32, i32, i32}, wasiEBADF)

	errnoFn("poll_oneoff", []api.ValueType{i32, i32, i32, i32}, h.pollOneoff)

	errnoFn("random_get", []api.ValueType{i32, i32}, func(mod api.Module, s []uint64) uint16 {
		buf := make([]byte, uint32(s[1]))
		rand.Read(buf)
		if !mod.Memory().Write(uint32(s[0]), buf) {
			return wasiEFAULT
		}
		return wasiSuccess
	})
	errnoFn("sched_yield", nil, func(api.Module, []uint64) uint16 {
		return wasiSuccess
	})

	export("proc_exit", []api.ValueType{i32}, nil, func(ctx context.Context, mod api.Module, s []uint64) {
		exitCode := uint32(s[0])
		_ = mod.CloseWithExitCode(ctx, exitCode)
		panic(wazerosys.NewExitError(exitCode))
	})

	// File system stubs: enough for code paths that probe for files
	// (time zone data and the like) to fail softly with ENOENT/EBADF.
	stub("path_open", []api.ValueType{i32, i32, i32, i32, i32, i64, i64, i32, i32}, wasiENOENT)
	stub("path_filestat_get", []api.ValueType{i32, i32, i32, i32, i32}, wasiENOENT)
	stub("path_create_directory", []api.ValueType{i32, i32, i32}, wasiENOSYS)
	stub("path_remove_directory", []api.ValueType{i32, i32, i32}, wasiENOSYS)
	stub("path_unlink_file", []api.ValueType{i32, i32, i32}, wasiENOSYS)
	stub("path_rename", []api.ValueType{i32, i32, i32, i32, i32, i32}, wasiENOSYS)
	stub("path_readlink", []api.ValueType{i32, i32, i32, i32, i32, i32}, wasiENOSYS)
	stub("path_symlink", []api.ValueType{i32, i32, i32, i32, i32}, wasiENOSYS)
	stub("path_link", []api.ValueType{i32, i32, i32, i32, i32, i32, i32}, wasiENOSYS)
	stub("fd_seek", []api.ValueType{i32, i64, i32, i32}, wasiEBADF)
	stub("fd_tell", []api.ValueType{i32, i32}, wasiEBADF)
	stub("fd_filestat_get", []api.ValueType{i32, i32}, wasiEBADF)
	stub("fd_filestat_set_size", []api.ValueType{i32, i64}, wasiEBADF)
	stub("fd_filestat_set_times", []api.ValueType{i32, i64, i64, i32}, wasiEBADF)
	stub("fd_pread", []api.ValueType{i32, i32, i32, i64, i32}, wasiEBADF)
	stub("fd_pwrite", []api.ValueType{i32, i32, i32, i64, i32}, wasiEBADF)
	stub("fd_readdir", []api.ValueType{i32, i32, i32, i64, i32}, wasiEBADF)
	stub("fd_datasync", []api.ValueType{i32}, wasiEBADF)
	stub("fd_sync", []api.ValueType{i32}, wasiEBADF)
	stub("fd_renumber", []api.ValueType{i32, i32}, wasiEBADF)
	stub("fd_advise", []api.ValueType{i32, i64, i64, i32}, wasiEBADF)
	stub("fd_allocate", []api.ValueType{i32, i64, i64}, wasiEBADF)
	stub("path_filestat_set_times", []api.ValueType{i32, i32, i32, i32, i64, i64, i32}, wasiENOSYS)
	stub("proc_raise", []api.ValueType{i32}, wasiENOSYS)

	// The WasmEdge socket extension, v0.4.3 SDK generation.
	errnoFn("sock_open", []api.ValueType{i32, i32, i32}, h.sockOpen)
	errnoFn("sock_bind", []api.ValueType{i32, i32, i32}, h.sockBind)
	errnoFn("sock_listen", []api.ValueType{i32, i32}, h.sockListen)
	errnoFn("sock_accept", []api.ValueType{i32, i32}, h.sockAccept)
	errnoFn("sock_connect", []api.ValueType{i32, i32, i32}, h.sockConnect)
	errnoFn("sock_send", []api.ValueType{i32, i32, i32, i32, i32}, h.sockSend)
	errnoFn("sock_recv", []api.ValueType{i32, i32, i32, i32, i32, i32}, h.sockRecv)
	errnoFn("sock_shutdown", []api.ValueType{i32, i32}, h.sockShutdown)
	errnoFn("sock_getlocaladdr", []api.ValueType{i32, i32, i32, i32}, h.sockGetlocaladdr)
	errnoFn("sock_getpeeraddr", []api.ValueType{i32, i32, i32, i32}, h.sockGetpeeraddr)
	errnoFn("sock_setsockopt", []api.ValueType{i32, i32, i32, i32, i32}, h.sockSetsockopt)
	errnoFn("sock_getsockopt", []api.ValueType{i32, i32, i32, i32, i32}, h.sockGetsockopt)

	_, err := b.Instantiate(ctx)
	return err
}

func (h *wasiHost) sizesGet(mod api.Module, countPtr, bufSizePtr uint32, list []string) uint16 {
	mem := mod.Memory()
	total := 0
	for _, s := range list {
		total += len(s) + 1
	}
	if !mem.WriteUint32Le(countPtr, uint32(len(list))) || !mem.WriteUint32Le(bufSizePtr, uint32(total)) {
		return wasiEFAULT
	}
	return wasiSuccess
}

func (h *wasiHost) vectorGet(mod api.Module, arrayPtr, bufPtr uint32, list []string) uint16 {
	mem := mod.Memory()
	off := bufPtr
	for i, s := range list {
		if !mem.WriteUint32Le(arrayPtr+uint32(4*i), off) {
			return wasiEFAULT
		}
		if !mem.Write(off, append([]byte(s), 0)) {
			return wasiEFAULT
		}
		off += uint32(len(s)) + 1
	}
	return wasiSuccess
}

func (h *wasiHost) fdWrite(mod api.Module, s []uint64) uint16 {
	fd, iovs, iovsLen, nwrittenPtr := int32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3])
	mem := mod.Memory()
	data, ok := gatherIOVecs(mem, iovs, iovsLen)
	if !ok {
		return wasiEFAULT
	}

	h.mu.Lock()
	e, found := h.fds[fd]
	if !found {
		h.mu.Unlock()
		return wasiEBADF
	}
	switch e.kind {
	case fdKindStdout, fdKindStderr:
		w := h.stdout
		if e.kind == fdKindStderr {
			w = h.stderr
		}
		h.mu.Unlock()
		n, err := w.Write(data)
		if err != nil {
			return wasiEIO
		}
		if !mem.WriteUint32Le(nwrittenPtr, uint32(n)) {
			return wasiEFAULT
		}
		return wasiSuccess
	case fdKindSock:
		n, errno := h.sockWriteLocked(e.sock, data)
		h.mu.Unlock()
		if errno != 0 {
			return errno
		}
		if !mem.WriteUint32Le(nwrittenPtr, n) {
			return wasiEFAULT
		}
		return wasiSuccess
	}
	h.mu.Unlock()
	return wasiEBADF
}

// sockWriteLocked writes data to the socket. Called with h.mu held;
// unlocks around the host write and re-locks before returning.
func (h *wasiHost) sockWriteLocked(sk *sockFD, data []byte) (uint32, uint16) {
	if sk.connecting {
		return 0, wasiEAGAIN
	}
	if sk.soError != 0 {
		return 0, sk.soError
	}
	if sk.conn == nil {
		return 0, wasiENOTCONN
	}
	if sk.wclosed {
		return 0, wasiEPIPE
	}
	conn := sk.conn
	h.mu.Unlock()
	// A short deadline keeps a full TCP send buffer from wedging the
	// single-threaded module: the guest sees a short write or EAGAIN
	// and comes back through the poller.
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	n, err := conn.Write(data)
	conn.SetWriteDeadline(time.Time{})
	h.mu.Lock()
	if n > 0 {
		return uint32(n), wasiSuccess
	}
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return 0, wasiEAGAIN
		}
		return 0, mapNetErrno(err)
	}
	return 0, wasiSuccess
}

func (h *wasiHost) fdRead(mod api.Module, s []uint64) uint16 {
	fd, iovs, iovsLen, nreadPtr := int32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3])
	mem := mod.Memory()

	h.mu.Lock()
	e, found := h.fds[fd]
	if !found {
		h.mu.Unlock()
		return wasiEBADF
	}
	switch e.kind {
	case fdKindStdin:
		h.mu.Unlock()
		// No stdin: report EOF.
		if !mem.WriteUint32Le(nreadPtr, 0) {
			return wasiEFAULT
		}
		return wasiSuccess
	case fdKindSock:
		sk := e.sock
		if sk.ln != nil {
			h.mu.Unlock()
			return wasiENOTSUP
		}
		if sk.connecting {
			h.mu.Unlock()
			return wasiEAGAIN
		}
		if sk.soError != 0 {
			errno := sk.soError
			h.mu.Unlock()
			return errno
		}
		if sk.conn == nil {
			h.mu.Unlock()
			return wasiENOTCONN
		}
		for sk.rbuf.Len() == 0 {
			if sk.rerr != nil {
				if errors.Is(sk.rerr, io.EOF) {
					h.mu.Unlock()
					if !mem.WriteUint32Le(nreadPtr, 0) {
						return wasiEFAULT
					}
					return wasiSuccess
				}
				errno := mapNetErrno(sk.rerr)
				h.mu.Unlock()
				if errno == wasiSuccess {
					errno = wasiEIO
				}
				return errno
			}
			if sk.nonblock {
				h.mu.Unlock()
				return wasiEAGAIN
			}
			ch := h.gen
			h.mu.Unlock()
			<-ch
			h.mu.Lock()
		}
		capacity, ok := iovecsCap(mem, iovs, iovsLen)
		if !ok {
			h.mu.Unlock()
			return wasiEFAULT
		}
		// Copy: the slice returned by Next is only valid until the
		// pump goroutine writes to the buffer again.
		data := append([]byte(nil), sk.rbuf.Next(int(capacity))...)
		h.notify() // read pump may be paused on the soft cap
		h.mu.Unlock()
		n, ok := scatterIOVecs(mem, iovs, iovsLen, data)
		if !ok {
			return wasiEFAULT
		}
		if !mem.WriteUint32Le(nreadPtr, n) {
			return wasiEFAULT
		}
		return wasiSuccess
	}
	h.mu.Unlock()
	return wasiEBADF
}

func (h *wasiHost) fdClose(mod api.Module, s []uint64) uint16 {
	fd := int32(s[0])
	h.mu.Lock()
	defer h.mu.Unlock()
	e, found := h.fds[fd]
	if !found {
		return wasiEBADF
	}
	if e.kind == fdKindSock {
		sk := e.sock
		if sk.conn != nil {
			sk.conn.Close()
		}
		if sk.ln != nil {
			sk.ln.Close()
			for _, c := range sk.pending {
				c.Close()
			}
		}
	}
	delete(h.fds, fd)
	h.notify()
	return wasiSuccess
}

func (h *wasiHost) fdFdstatGet(mod api.Module, s []uint64) uint16 {
	fd, bufPtr := int32(s[0]), uint32(s[1])
	mem := mod.Memory()
	h.mu.Lock()
	defer h.mu.Unlock()
	e, found := h.fds[fd]
	if !found {
		return wasiEBADF
	}
	var filetype byte = filetypeCharacterDevice
	var flags uint16
	if e.kind == fdKindSock {
		filetype = filetypeSocketStream
		if e.sock.sotype == wasmedgeSockDgram {
			filetype = filetypeSocketDgram
		}
		if e.sock.nonblock {
			flags |= fdflagNonblock
		}
	} else {
		flags |= fdflagNonblock // stdio was set nonblocking by the guest
	}
	buf := make([]byte, 24)
	buf[0] = filetype
	binary.LittleEndian.PutUint16(buf[2:], flags)
	binary.LittleEndian.PutUint64(buf[8:], ^uint64(0))  // rights base: all
	binary.LittleEndian.PutUint64(buf[16:], ^uint64(0)) // rights inheriting: all
	if !mem.Write(bufPtr, buf) {
		return wasiEFAULT
	}
	return wasiSuccess
}

func (h *wasiHost) fdFdstatSetFlags(mod api.Module, s []uint64) uint16 {
	fd, flags := int32(s[0]), uint32(s[1])
	h.mu.Lock()
	defer h.mu.Unlock()
	e, found := h.fds[fd]
	if !found {
		return wasiEBADF
	}
	if e.kind == fdKindSock {
		e.sock.nonblock = flags&fdflagNonblock != 0
	}
	// Accept (and ignore) flag changes on stdio.
	return wasiSuccess
}

// pollOneoff implements poll_oneoff over clock subscriptions and
// fd_read/fd_write readiness of socket fds.
func (h *wasiHost) pollOneoff(mod api.Module, s []uint64) uint16 {
	inPtr, outPtr, nsubs, neventsPtr := uint32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3])
	mem := mod.Memory()
	if nsubs == 0 {
		return wasiEINVAL
	}

	type clockSub struct {
		userdata uint64
		deadline time.Time
	}
	type fdSub struct {
		userdata uint64
		fd       int32
		write    bool
	}
	var clocks []clockSub
	var fds []fdSub

	now := time.Now()
	for i := uint32(0); i < nsubs; i++ {
		base := inPtr + 48*i
		userdata, ok := mem.ReadUint64Le(base)
		if !ok {
			return wasiEFAULT
		}
		tag, ok := mem.ReadUint64Le(base + 8) // low byte is the tag
		if !ok {
			return wasiEFAULT
		}
		switch byte(tag) {
		case eventtypeClock:
			id, _ := mem.ReadUint32Le(base + 16)
			timeout, _ := mem.ReadUint64Le(base + 24)
			flags, _ := mem.ReadUint32Le(base + 40)
			var deadline time.Time
			if flags&1 != 0 { // SUBSCRIPTION_CLOCK_ABSTIME
				switch id {
				case clockRealtime:
					deadline = time.Unix(0, int64(timeout))
				default:
					deadline = h.monoStart.Add(time.Duration(timeout))
				}
			} else {
				deadline = now.Add(time.Duration(timeout))
			}
			clocks = append(clocks, clockSub{userdata, deadline})
		case eventtypeFdRead, eventtypeFdWrite:
			fd, _ := mem.ReadUint32Le(base + 16)
			fds = append(fds, fdSub{userdata, int32(fd), byte(tag) == eventtypeFdWrite})
		default:
			return wasiEINVAL
		}
	}

	writeEvent := func(off uint32, userdata uint64, errno uint16, typ byte, hangup bool) bool {
		buf := make([]byte, 32)
		binary.LittleEndian.PutUint64(buf[0:], userdata)
		binary.LittleEndian.PutUint16(buf[8:], errno)
		buf[10] = typ
		if hangup {
			binary.LittleEndian.PutUint16(buf[24:], 1)
		}
		return mem.Write(off, buf)
	}

	for {
		h.mu.Lock()
		var nevents uint32
		out := outPtr
		emit := func(userdata uint64, errno uint16, typ byte, hangup bool) bool {
			if !writeEvent(out, userdata, errno, typ, hangup) {
				return false
			}
			out += 32
			nevents++
			return true
		}
		for _, f := range fds {
			e, found := h.fds[f.fd]
			if !found || e.kind != fdKindSock {
				typ := byte(eventtypeFdRead)
				if f.write {
					typ = eventtypeFdWrite
				}
				if !emit(f.userdata, wasiEBADF, typ, false) {
					h.mu.Unlock()
					return wasiEFAULT
				}
				continue
			}
			sk := e.sock
			if f.write && sk.writable() {
				if !emit(f.userdata, 0, eventtypeFdWrite, false) {
					h.mu.Unlock()
					return wasiEFAULT
				}
			} else if !f.write && sk.readable() {
				hangup := sk.rerr != nil
				if !emit(f.userdata, 0, eventtypeFdRead, hangup) {
					h.mu.Unlock()
					return wasiEFAULT
				}
			}
		}
		if nevents == 0 {
			for _, c := range clocks {
				if !c.deadline.After(time.Now()) {
					if !emit(c.userdata, 0, eventtypeClock, false) {
						h.mu.Unlock()
						return wasiEFAULT
					}
				}
			}
		}
		if nevents > 0 {
			h.mu.Unlock()
			if !mem.WriteUint32Le(neventsPtr, nevents) {
				return wasiEFAULT
			}
			return wasiSuccess
		}
		ch := h.gen
		h.mu.Unlock()

		var timer *time.Timer
		var timeout <-chan time.Time
		if len(clocks) > 0 {
			earliest := clocks[0].deadline
			for _, c := range clocks[1:] {
				if c.deadline.Before(earliest) {
					earliest = c.deadline
				}
			}
			d := time.Until(earliest)
			if d < 0 {
				d = 0
			}
			timer = time.NewTimer(d)
			timeout = timer.C
		}
		select {
		case <-ch:
		case <-timeout:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// Socket extension implementations.

func (h *wasiHost) sockOpen(mod api.Module, s []uint64) uint16 {
	family, sotype, fdPtr := int32(s[0]), int32(s[1]), uint32(s[2])
	switch family {
	case wasmedgeAFInet4, wasmedgeAFInet6:
	default:
		return wasiEAFNOSUPPORT
	}
	switch sotype {
	case wasmedgeSockStream:
	case wasmedgeSockDgram:
		return wasiEPROTONOSUPPORT // datagram sockets not implemented yet
	default:
		return wasiEPROTONOSUPPORT
	}
	h.mu.Lock()
	fd := h.newFD(&fdEntry{kind: fdKindSock, sock: &sockFD{family: family, sotype: sotype}})
	h.mu.Unlock()
	if !mod.Memory().WriteUint32Le(fdPtr, uint32(fd)) {
		return wasiEFAULT
	}
	h.tracef("sock_open family=%d type=%d -> fd %d", family, sotype, fd)
	return wasiSuccess
}

func (h *wasiHost) sockBind(mod api.Module, s []uint64) uint16 {
	fd, addrPtr, port := int32(s[0]), uint32(s[1]), uint32(s[2])
	ip, _, _, ok := readWasiAddressIP(mod.Memory(), addrPtr)
	if !ok {
		return wasiEFAULT
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sk, errno := h.sock(fd)
	if errno != 0 {
		return errno
	}
	if sk.conn != nil || sk.ln != nil || sk.connecting {
		return wasiEINVAL
	}
	sk.boundIP = ipFor(sk.family, ip)
	sk.boundPort = port
	h.tracef("sock_bind fd=%d %s", fd, hostAddr(sk.boundIP, sk.boundPort))
	return wasiSuccess
}

func (h *wasiHost) sockListen(mod api.Module, s []uint64) uint16 {
	fd := int32(s[0])
	h.mu.Lock()
	sk, errno := h.sock(fd)
	if errno != 0 {
		h.mu.Unlock()
		return errno
	}
	if sk.ln != nil || sk.conn != nil || sk.connecting {
		h.mu.Unlock()
		return wasiEINVAL
	}
	addr := hostAddr(sk.boundIP, sk.boundPort)
	h.mu.Unlock()

	ln, err := net.Listen("tcp", addr)

	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.tracef("sock_listen fd=%d %s: %v", fd, addr, err)
		return mapNetErrno(err)
	}
	sk.ln = ln
	h.startAcceptPump(sk, ln)
	h.tracef("sock_listen fd=%d on %s", fd, ln.Addr())
	return wasiSuccess
}

func (h *wasiHost) sockAccept(mod api.Module, s []uint64) uint16 {
	fd, fdPtr := int32(s[0]), uint32(s[1])
	h.mu.Lock()
	sk, errno := h.sock(fd)
	if errno != 0 {
		h.mu.Unlock()
		return errno
	}
	if sk.ln == nil {
		h.mu.Unlock()
		return wasiEINVAL
	}
	for len(sk.pending) == 0 {
		if sk.lnErr != nil {
			errno := mapNetErrno(sk.lnErr)
			h.mu.Unlock()
			return errno
		}
		if sk.nonblock {
			h.mu.Unlock()
			return wasiEAGAIN
		}
		ch := h.gen
		h.mu.Unlock()
		<-ch
		h.mu.Lock()
	}
	conn := sk.pending[0]
	sk.pending = sk.pending[1:]
	newSk := &sockFD{family: sk.family, sotype: sk.sotype, conn: conn}
	newFD := h.newFD(&fdEntry{kind: fdKindSock, sock: newSk})
	h.startReadPump(newSk, conn)
	h.notify()
	h.mu.Unlock()
	if !mod.Memory().WriteUint32Le(fdPtr, uint32(newFD)) {
		return wasiEFAULT
	}
	h.tracef("sock_accept fd=%d -> fd %d (%s)", fd, newFD, conn.RemoteAddr())
	return wasiSuccess
}

func (h *wasiHost) sockConnect(mod api.Module, s []uint64) uint16 {
	fd, addrPtr, port := int32(s[0]), uint32(s[1]), uint32(s[2])
	ip, _, _, ok := readWasiAddressIP(mod.Memory(), addrPtr)
	if !ok {
		return wasiEFAULT
	}
	h.mu.Lock()
	sk, errno := h.sock(fd)
	if errno != 0 {
		h.mu.Unlock()
		return errno
	}
	if sk.ln != nil {
		h.mu.Unlock()
		return wasiEINVAL
	}
	if sk.connecting {
		h.mu.Unlock()
		return wasiEALREADY
	}
	if sk.conn != nil {
		h.mu.Unlock()
		return wasiEISCONN
	}
	dst := ipFor(sk.family, ip)
	if dst == nil {
		h.mu.Unlock()
		return wasiEINVAL
	}
	addr := net.JoinHostPort(dst.String(), fmt.Sprint(port))
	var laddr *net.TCPAddr
	if sk.boundIP != nil || sk.boundPort != 0 {
		laddr = &net.TCPAddr{IP: sk.boundIP, Port: int(sk.boundPort)}
	}
	dial := func() (net.Conn, error) {
		d := net.Dialer{Timeout: 2 * time.Minute}
		if laddr != nil {
			d.LocalAddr = laddr
		}
		return d.Dial("tcp", addr)
	}
	if !sk.nonblock {
		h.mu.Unlock()
		conn, err := dial()
		h.mu.Lock()
		defer h.mu.Unlock()
		if err != nil {
			h.tracef("sock_connect fd=%d %s: %v", fd, addr, err)
			return mapNetErrno(err)
		}
		sk.conn = conn
		h.startReadPump(sk, conn)
		h.notify()
		return wasiSuccess
	}
	sk.connecting = true
	h.tracef("sock_connect fd=%d %s (in progress)", fd, addr)
	go func() {
		conn, err := dial()
		h.mu.Lock()
		defer h.mu.Unlock()
		sk.connecting = false
		if err != nil {
			sk.soError = mapNetErrno(err)
			if sk.soError == wasiSuccess {
				sk.soError = wasiEIO
			}
			h.tracef("sock_connect fd=%d %s failed: %v (errno %d)", fd, addr, err, sk.soError)
		} else {
			sk.conn = conn
			h.startReadPump(sk, conn)
			h.tracef("sock_connect fd=%d connected %s -> %s", fd, conn.LocalAddr(), conn.RemoteAddr())
		}
		h.notify()
	}()
	h.mu.Unlock()
	return wasiEINPROGRESS
}

func (h *wasiHost) sockSend(mod api.Module, s []uint64) uint16 {
	fd, iovs, iovsLen, sendLenPtr := int32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[4])
	mem := mod.Memory()
	data, ok := gatherIOVecs(mem, iovs, iovsLen)
	if !ok {
		return wasiEFAULT
	}
	h.mu.Lock()
	sk, errno := h.sock(fd)
	if errno != 0 {
		h.mu.Unlock()
		return errno
	}
	n, errno := h.sockWriteLocked(sk, data)
	h.mu.Unlock()
	if errno != 0 {
		return errno
	}
	if !mem.WriteUint32Le(sendLenPtr, n) {
		return wasiEFAULT
	}
	return wasiSuccess
}

func (h *wasiHost) sockRecv(mod api.Module, s []uint64) uint16 {
	// (fd, iovs, iovsLen, flags, recvLenPtr, oflagsPtr)
	stack := []uint64{s[0], s[1], s[2], s[4]}
	if errno := h.fdRead(mod, stack); errno != 0 {
		return errno
	}
	if !mod.Memory().WriteUint32Le(uint32(s[5]), 0) {
		return wasiEFAULT
	}
	return wasiSuccess
}

func (h *wasiHost) sockShutdown(mod api.Module, s []uint64) uint16 {
	fd, how := int32(s[0]), uint32(s[1])
	h.mu.Lock()
	defer h.mu.Unlock()
	sk, errno := h.sock(fd)
	if errno != 0 {
		return errno
	}
	if sk.conn == nil {
		return wasiENOTCONN
	}
	type crw interface {
		CloseRead() error
		CloseWrite() error
	}
	c, ok := sk.conn.(crw)
	if how&2 != 0 {
		sk.wclosed = true
		if ok {
			c.CloseWrite()
		}
	}
	if how&1 != 0 {
		if ok {
			c.CloseRead()
		}
	}
	h.notify()
	return wasiSuccess
}

func (h *wasiHost) sockGetlocaladdr(mod api.Module, s []uint64) uint16 {
	return h.sockGetaddr(mod, s, false)
}

func (h *wasiHost) sockGetpeeraddr(mod api.Module, s []uint64) uint16 {
	return h.sockGetaddr(mod, s, true)
}

func (h *wasiHost) sockGetaddr(mod api.Module, s []uint64, peer bool) uint16 {
	fd, addrPtr, typePtr, portPtr := int32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3])
	mem := mod.Memory()
	h.mu.Lock()
	sk, errno := h.sock(fd)
	if errno != 0 {
		h.mu.Unlock()
		return errno
	}
	var a net.Addr
	switch {
	case sk.conn != nil:
		if peer {
			a = sk.conn.RemoteAddr()
		} else {
			a = sk.conn.LocalAddr()
		}
	case sk.ln != nil && !peer:
		a = sk.ln.Addr()
	default:
		h.mu.Unlock()
		return wasiENOTCONN
	}
	h.mu.Unlock()
	ip, kind, port := splitIPPort(a)
	if ip == nil {
		return wasiENOTSUP
	}
	bufPtr, ok1 := mem.ReadUint32Le(addrPtr)
	bufLen, ok2 := mem.ReadUint32Le(addrPtr + 4)
	if !ok1 || !ok2 {
		return wasiEFAULT
	}
	if uint32(len(ip)) > bufLen {
		return wasiEFAULT
	}
	if !mem.Write(bufPtr, ip) {
		return wasiEFAULT
	}
	if !mem.WriteUint32Le(typePtr, kind) || !mem.WriteUint32Le(portPtr, port) {
		return wasiEFAULT
	}
	return wasiSuccess
}

func (h *wasiHost) sockSetsockopt(mod api.Module, s []uint64) uint16 {
	level, name := int32(s[1]), int32(s[2])
	if level != wasmedgeSolSocket {
		return wasiEINVAL
	}
	switch name {
	case wasmedgeSoReuseaddr, wasmedgeSoKeepalive:
		// Accepted; the host-side defaults already behave this way.
		return wasiSuccess
	default:
		return wasiSuccess
	}
}

func (h *wasiHost) sockGetsockopt(mod api.Module, s []uint64) uint16 {
	fd, level, name, flagPtr, flagSizePtr := int32(s[0]), int32(s[1]), int32(s[2]), uint32(s[3]), uint32(s[4])
	mem := mod.Memory()
	if level != wasmedgeSolSocket {
		return wasiEINVAL
	}
	h.mu.Lock()
	sk, errno := h.sock(fd)
	if errno != 0 {
		h.mu.Unlock()
		return errno
	}
	var val int32
	switch name {
	case wasmedgeSoError:
		if sk.connecting {
			val = wasiEINPROGRESS
		} else {
			val = int32(sk.soError)
		}
	case wasmedgeSoType:
		val = sk.sotype
	default:
		val = 0
	}
	h.mu.Unlock()
	if !mem.WriteUint32Le(flagPtr, uint32(val)) {
		return wasiEFAULT
	}
	if !mem.WriteUint32Le(flagSizePtr, 4) {
		return wasiEFAULT
	}
	return wasiSuccess
}

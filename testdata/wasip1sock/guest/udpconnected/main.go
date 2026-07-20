// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// udpconnected exercises connected UDP: net.Dial("udp") to the echo
// server named by $UDPECHO_ADDR, plain Write/Read round trips that
// must preserve datagram boundaries even with several datagrams
// queued, WriteTo's connected-conn refusal, and a read deadline.
package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"time"
)

func payload(seed byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(int(seed) + i*31 + i>>8)
	}
	return b
}

func main() {
	addr := os.Getenv("UDPECHO_ADDR")
	if addr == "" {
		fmt.Println("UDPECHO_ADDR not set")
		os.Exit(1)
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		fmt.Printf("dial: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()
	fmt.Printf("local=%v remote=%v\n", c.LocalAddr(), c.RemoteAddr())
	if c.RemoteAddr() == nil || c.RemoteAddr().String() != addr {
		fmt.Printf("remote addr %v, want %s\n", c.RemoteAddr(), addr)
		os.Exit(1)
	}

	// Write all datagrams first, then read the echoes: each Read must
	// return exactly one datagram, never a coalesced stream.
	sizes := []int{1, 128, 4096}
	for i, n := range sizes {
		if _, err := c.Write(payload(byte(i+1), n)); err != nil {
			fmt.Printf("write %d: %v\n", i, err)
			os.Exit(1)
		}
	}
	if err := c.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		fmt.Printf("set deadline: %v\n", err)
		os.Exit(1)
	}
	buf := make([]byte, 64<<10)
	for i, n := range sizes {
		rn, err := c.Read(buf)
		if err != nil {
			fmt.Printf("read %d: %v\n", i, err)
			os.Exit(1)
		}
		if rn != n {
			fmt.Printf("read %d returned %d bytes, want exactly %d (datagram boundary lost)\n", i, rn, n)
			os.Exit(1)
		}
		if !bytes.Equal(buf[:rn], payload(byte(i+1), n)) {
			fmt.Printf("read %d payload mismatch\n", i)
			os.Exit(1)
		}
	}
	fmt.Printf("udpconnected: ok msgs=%d\n", len(sizes))

	// WriteTo on a connected conn must be refused by net.
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		fmt.Printf("resolve: %v\n", err)
		os.Exit(1)
	}
	if _, err := c.(*net.UDPConn).WriteToUDP([]byte("x"), ua); err == nil {
		fmt.Println("WriteToUDP on a connected conn unexpectedly succeeded")
		os.Exit(1)
	} else {
		fmt.Printf("writeto-connected error: %v\n", err)
	}

	// Nothing else is in flight: a short read deadline must fire.
	if err := c.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		fmt.Printf("set deadline: %v\n", err)
		os.Exit(1)
	}
	start := time.Now()
	_, rerr := c.Read(buf)
	elapsed := time.Since(start)
	if rerr == nil {
		fmt.Println("deadline read unexpectedly succeeded")
		os.Exit(1)
	}
	ne, ok := rerr.(net.Error)
	fmt.Printf("deadline timeout: %v\n", ok && ne.Timeout())
	fmt.Printf("deadline elapsed: %dms\n", elapsed.Milliseconds())
}

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// echo dials the TCP echo server named by $ECHO_ADDR, round-trips 1MB
// on a single connection with deadlines, then round-trips 8 concurrent
// connections with distinct payloads.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

func payload(seed byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(int(seed) + i*31 + i>>8)
	}
	return b
}

func roundTrip(addr string, seed byte, n int) error {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial: %v", err)
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return fmt.Errorf("set deadline: %v", err)
	}
	want := payload(seed, n)
	errc := make(chan error, 1)
	go func() {
		if _, err := c.Write(want); err != nil {
			errc <- fmt.Errorf("write: %v", err)
			return
		}
		errc <- c.(*net.TCPConn).CloseWrite()
	}()
	got, err := io.ReadAll(c)
	if err != nil {
		return fmt.Errorf("read: %v", err)
	}
	if err := <-errc; err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("got %d bytes, want %d", len(got), len(want))
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("payload mismatch")
	}
	return nil
}

func main() {
	addr := os.Getenv("ECHO_ADDR")
	if addr == "" {
		fmt.Println("ECHO_ADDR not set")
		os.Exit(1)
	}

	const oneMB = 1 << 20
	if err := roundTrip(addr, 7, oneMB); err != nil {
		fmt.Printf("echo1: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("echo1: ok bytes=%d\n", oneMB)

	const conns = 8
	const perConn = 256 << 10
	var wg sync.WaitGroup
	errs := make([]error, conns)
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = roundTrip(addr, byte(i+1), perConn)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			fmt.Printf("echo8: conn %d: %v\n", i, err)
			os.Exit(1)
		}
	}
	fmt.Printf("echo8: ok conns=%d bytes=%d\n", conns, perConn)
}

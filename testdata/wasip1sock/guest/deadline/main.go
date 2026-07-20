// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// deadline connects to $SILENT_ADDR (a server that accepts and then
// stays silent), arms a 300ms read deadline, and verifies the read
// fails with a timeout error.
package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	addr := os.Getenv("SILENT_ADDR")
	if addr == "" {
		fmt.Println("SILENT_ADDR not set")
		os.Exit(1)
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Printf("dial: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()
	if err := c.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		fmt.Printf("set deadline: %v\n", err)
		os.Exit(1)
	}
	start := time.Now()
	buf := make([]byte, 1)
	n, err := c.Read(buf)
	elapsed := time.Since(start)
	if err == nil {
		fmt.Printf("read unexpectedly returned %d bytes\n", n)
		os.Exit(1)
	}
	ne, ok := err.(net.Error)
	fmt.Printf("read error: %v\n", err)
	fmt.Printf("timeout: %v\n", ok && ne.Timeout())
	fmt.Printf("elapsed: %dms\n", elapsed.Milliseconds())
}

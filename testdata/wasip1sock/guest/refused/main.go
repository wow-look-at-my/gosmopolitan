// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// refused dials $REFUSED_ADDR (a closed port) and reports the error
// and how long the dial took.
package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	addr := os.Getenv("REFUSED_ADDR")
	if addr == "" {
		fmt.Println("REFUSED_ADDR not set")
		os.Exit(1)
	}
	start := time.Now()
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	elapsed := time.Since(start)
	if err == nil {
		c.Close()
		fmt.Println("dial unexpectedly succeeded")
		os.Exit(1)
	}
	fmt.Printf("dial error: %v\n", err)
	fmt.Printf("elapsed: %dms\n", elapsed.Milliseconds())
}

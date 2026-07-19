// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// httpserve listens on $SERVE_ADDR, prints the real bound address as
// "LISTENING <addr>", serves /hello, and exits after the first request
// completes.
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := os.Getenv("SERVE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("listen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("LISTENING %s\n", ln.Addr())

	done := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello from wasip1 (remote %s)\n", r.RemoteAddr)
		close(done)
	})
	go http.Serve(ln, mux)

	<-done
	// Give the response time to flush through the connection.
	time.Sleep(200 * time.Millisecond)
	fmt.Println("SERVED")
}

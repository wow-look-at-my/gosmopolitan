// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// httpget fetches $HTTP_URL with net/http over real sockets and prints
// the status and body.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	url := os.Getenv("HTTP_URL")
	if url == "" {
		fmt.Println("HTTP_URL not set")
		os.Exit(1)
	}
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("http get: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("read body: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("status: %s\n", resp.Status)
	fmt.Printf("body: %s\n", body)
}

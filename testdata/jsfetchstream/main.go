// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command jsfetchstream is the js/wasm guest for the streaming fetch
// request-body end-to-end test. run.js starts a node:http server and
// runs this program under Node.js with GODEBUG=jsfetchnode=1 and
// FETCHSTREAM_ADDR pointing at the server.
//
// What it proves:
//
//  1. True upload streaming: a POST with an unknown-length pipe body
//     writes chunk A, then polls GET /got until the server has seen A
//     while the POST body is still open, and only then writes chunk B
//     and closes. A buffered transport can never pass this: it reads
//     the body to EOF before the request even starts, so nothing
//     reaches the server until after B — the poll times out.
//  2. Integrity: the server answers the POST with the SHA-256 and byte
//     count it received; they must match A+B, the request must have no
//     Content-Length, and the upload must have arrived chunked.
//  3. Bounded memory: 8 MiB streamed in 64 KiB writes must not grow
//     the guest's cumulative allocations by anywhere near the body
//     size (a buffered upload allocates the whole body at once).
//  4. Cancellation: a streaming POST to /hang (the server never
//     responds) is cancelled mid-stream. Do must return the context
//     error promptly, the request body must be closed (pipe writes
//     fail afterwards), and the server must observe the abort.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"
)

var base string // http://127.0.0.1:port

// uploadReply is the JSON the server sends when a POST completes.
type uploadReply struct {
	SHA256           string `json:"sha256"`
	Bytes            int64  `json:"bytes"`
	Chunks           int64  `json:"chunks"`
	ContentLength    string `json:"contentLength"`    // "" when absent
	TransferEncoding string `json:"transferEncoding"` // "" when absent
}

func main() {
	log.SetFlags(0)
	addr := os.Getenv("FETCHSTREAM_ADDR")
	if addr == "" {
		log.Fatal("FETCHSTREAM_ADDR not set (run this via run.js)")
	}
	base = "http://" + addr

	testStreamingOrder()
	testKnownLength()
	testLargeBody()
	testCancel()

	fmt.Println("JSFETCHSTREAM: PASS")
}

type doResult struct {
	resp *http.Response
	err  error
}

// startUpload begins a POST of pr to path on its own goroutine and
// returns the channel Do's result arrives on. The pipe gives the
// request an unknown content length (ContentLength 0 with a non-nil
// Body), which is what makes the transport stream it.
func startUpload(ctx context.Context, path string, pr *io.PipeReader) chan doResult {
	req, err := http.NewRequestWithContext(ctx, "POST", base+path, pr)
	if err != nil {
		log.Fatalf("NewRequest: %v", err)
	}
	done := make(chan doResult, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		done <- doResult{resp, err}
	}()
	return done
}

// got asks the server how many body bytes have arrived so far for the
// named upload.
func got(name string) int64 {
	resp, err := http.Get(base + "/got?name=" + name)
	if err != nil {
		log.Fatalf("GET /got: %v", err)
	}
	defer resp.Body.Close()
	var n int64
	if _, err := fmt.Fscan(resp.Body, &n); err != nil {
		log.Fatalf("GET /got: bad body: %v", err)
	}
	return n
}

// waitGot polls /got until the named upload has at least want bytes
// server-side, failing the test if that does not happen in time or if
// the in-flight Do returns early.
func waitGot(name string, want int64, done chan doResult) {
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case r := <-done:
			log.Fatalf("POST finished before its body was closed: resp=%v err=%v", r.resp, r.err)
		default:
		}
		if n := got(name); n >= want {
			return
		}
		if time.Now().After(deadline) {
			log.Fatalf("server saw %d/%d bytes of %q after 15s — upload looks buffered, not streamed", got(name), want, name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func mustWrite(pw *io.PipeWriter, b []byte) {
	if _, err := pw.Write(b); err != nil {
		log.Fatalf("pipe write: %v", err)
	}
}

// finish collects Do's result and decodes the server's reply.
func finish(done chan doResult) uploadReply {
	r := <-done
	if r.err != nil {
		log.Fatalf("Do: %v", r.err)
	}
	defer r.resp.Body.Close()
	if r.resp.StatusCode != 200 {
		log.Fatalf("POST status %s", r.resp.Status)
	}
	var reply uploadReply
	if err := json.NewDecoder(r.resp.Body).Decode(&reply); err != nil {
		log.Fatalf("decoding reply: %v", err)
	}
	return reply
}

func testStreamingOrder() {
	chunkA := bytes.Repeat([]byte("A"), 1024)
	chunkB := bytes.Repeat([]byte("B"), 1024)

	pr, pw := io.Pipe()
	done := startUpload(context.Background(), "/upload?name=order", pr)

	mustWrite(pw, chunkA)
	// The gate that proves streaming: chunk A must reach the server
	// while the request body is still open (B not yet written).
	waitGot("order", int64(len(chunkA)), done)
	fmt.Println("ok: server received chunk A while the request body was still open")

	mustWrite(pw, chunkB)
	pw.Close()

	reply := finish(done)
	sum := sha256.Sum256(append(append([]byte{}, chunkA...), chunkB...))
	if want := hex.EncodeToString(sum[:]); reply.SHA256 != want {
		log.Fatalf("digest mismatch: server %s, want %s", reply.SHA256, want)
	}
	if want := int64(len(chunkA) + len(chunkB)); reply.Bytes != want {
		log.Fatalf("server received %d bytes, want %d", reply.Bytes, want)
	}
	if reply.Chunks < 2 {
		log.Fatalf("server saw %d chunks, want >= 2 (A and B were written on opposite sides of the /got gate)", reply.Chunks)
	}
	if reply.ContentLength != "" {
		log.Fatalf("streamed request carried Content-Length %q, want none", reply.ContentLength)
	}
	if reply.TransferEncoding != "chunked" {
		log.Fatalf("streamed request Transfer-Encoding %q, want %q", reply.TransferEncoding, "chunked")
	}
	fmt.Printf("ok: digest and length match (%d bytes in %d chunks, no Content-Length, chunked)\n", reply.Bytes, reply.Chunks)
}

func testKnownLength() {
	// The flip side of the streaming policy: a known-length body must
	// keep the buffered upload path, so Content-Length stays on the
	// wire exactly as before (fetch drops Content-Length for stream
	// bodies) and nothing is chunked.
	payload := bytes.Repeat([]byte("K"), 4096)
	resp, err := http.Post(base+"/upload?name=known", "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		log.Fatalf("known-length POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("known-length POST status %s", resp.Status)
	}
	var reply uploadReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		log.Fatalf("decoding reply: %v", err)
	}
	sum := sha256.Sum256(payload)
	if want := hex.EncodeToString(sum[:]); reply.SHA256 != want {
		log.Fatalf("known-length digest mismatch: server %s, want %s", reply.SHA256, want)
	}
	if reply.ContentLength != "4096" {
		log.Fatalf("known-length request carried Content-Length %q, want %q", reply.ContentLength, "4096")
	}
	if reply.TransferEncoding != "" {
		log.Fatalf("known-length request Transfer-Encoding %q, want none", reply.TransferEncoding)
	}
	fmt.Println("ok: known-length body kept the buffered path (Content-Length 4096, not chunked)")
}

func testLargeBody() {
	const (
		chunkSize = 64 << 10
		chunks    = 128 // 8 MiB total
	)
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)

	pr, pw := io.Pipe()
	done := startUpload(context.Background(), "/upload?name=large", pr)

	h := sha256.New()
	buf := make([]byte, chunkSize)
	for i := 0; i < chunks; i++ {
		for j := range buf {
			buf[j] = byte(i + j)
		}
		h.Write(buf)
		mustWrite(pw, buf)
	}
	pw.Close()

	reply := finish(done)
	if want := hex.EncodeToString(h.Sum(nil)); reply.SHA256 != want {
		log.Fatalf("large digest mismatch: server %s, want %s", reply.SHA256, want)
	}
	if want := int64(chunkSize * chunks); reply.Bytes != want {
		log.Fatalf("server received %d bytes, want %d", reply.Bytes, want)
	}

	runtime.ReadMemStats(&m1)
	delta := m1.TotalAlloc - m0.TotalAlloc
	fmt.Printf("ok: 8 MiB streamed intact; guest TotalAlloc delta %d bytes\n", delta)
	// A buffered upload allocates the whole 8 MiB body (io.ReadAll's
	// growth pattern makes it ~2x that). Streaming reuses one 64 KiB
	// buffer; leave generous headroom for incidental allocation.
	if delta > 6<<20 {
		log.Fatalf("TotalAlloc grew %d bytes while uploading 8 MiB — the body looks buffered", delta)
	}
}

func testCancel() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	done := startUpload(ctx, "/hang", pr)

	mustWrite(pw, []byte("bytes before cancel"))
	// Make sure the upload really is in flight before cancelling.
	waitGot("hang", 1, done)

	cancel()
	select {
	case r := <-done:
		if r.err == nil {
			log.Fatalf("Do succeeded after cancel: %v", r.resp)
		}
		if !errors.Is(r.err, context.Canceled) {
			log.Fatalf("Do returned %v, want context.Canceled", r.err)
		}
	case <-time.After(10 * time.Second):
		log.Fatal("Do did not return within 10s of cancel")
	}
	fmt.Println("ok: cancel mid-stream returned promptly with context.Canceled")

	// RoundTrip must have closed the request body: pipe writes now fail.
	if _, err := pw.Write([]byte("x")); err == nil {
		log.Fatal("pipe write succeeded after cancel — request body was not closed")
	}
	fmt.Println("ok: request body closed on cancel (pipe write fails)")

	// The server must see the upload abort (connection drop), not a
	// clean end.
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get(base + "/status")
		if err != nil {
			log.Fatalf("GET /status: %v", err)
		}
		var st struct {
			HangAborted bool `json:"hangAborted"`
		}
		err = json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if err != nil {
			log.Fatalf("decoding /status: %v", err)
		}
		if st.HangAborted {
			break
		}
		if time.Now().After(deadline) {
			log.Fatal("server never observed the aborted upload")
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("ok: server observed the aborted upload")
}

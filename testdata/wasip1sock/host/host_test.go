// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// End-to-end tests for GOWASI=wasmedgesock networking: they build the
// guest programs from ../guest with this tree's toolchain (bin/go at
// the repository root, or $WASIP1SOCK_GO) and run them under the
// custom WASI host in this package, against real TCP and UDP servers
// on the host side.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

// forkGo locates this tree's go command.
func forkGo() (string, error) {
	if p := os.Getenv("WASIP1SOCK_GO"); p != "" {
		return p, nil
	}
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "bin", "go"))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("toolchain not found at %s (build it with make.bash or set WASIP1SOCK_GO): %v", p, err)
	}
	return p, nil
}

// buildGuests compiles every guest program once per test run:
// each with GOWASI=wasmedgesock, and the echo guest additionally
// without it (to prove the default stays on the fake network).
func buildGuests() (string, error) {
	buildOnce.Do(func() {
		goTool, err := forkGo()
		if err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "wasip1sock")
		if err != nil {
			buildErr = err
			return
		}
		buildDir = dir
		guestDir, err := filepath.Abs(filepath.Join("..", "guest"))
		if err != nil {
			buildErr = err
			return
		}
		build := func(out, pkg, gowasi string) error {
			cmd := exec.Command(goTool, "build", "-o", filepath.Join(dir, out), "./"+pkg)
			cmd.Dir = guestDir
			cmd.Env = append(os.Environ(),
				"GOOS=wasip1",
				"GOARCH=wasm",
				"GOWASI="+gowasi,
				"GOTOOLCHAIN=local",
				// When the host Go running this test is older than
				// this module's go directive, GOTOOLCHAIN=auto
				// re-execs the test under a downloaded toolchain
				// and exports that toolchain's GOROOT into our
				// environment; goTool would then pick up the
				// downloaded toolchain's compile/link instead of
				// its own ("compile: version ... does not match go
				// tool version ..."). Clear it so goTool derives
				// GOROOT from its own location.
				"GOROOT=",
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("building %s: %v\n%s", pkg, err, out)
			}
			return nil
		}
		for _, pkg := range []string{"echo", "httpget", "httpserve", "refused", "deadline", "udpecho", "udpconnected"} {
			if err := build(pkg+".wasm", pkg, "wasmedgesock"); err != nil {
				buildErr = err
				return
			}
		}
		buildErr = build("echo_default.wasm", "echo", "")
	})
	return buildDir, buildErr
}

func guestModule(t *testing.T, name string) []byte {
	t.Helper()
	dir, err := buildGuests()
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return wasm
}

// runGuest runs a module under the custom host and returns its exit
// code and combined output.
func runGuest(t *testing.T, name string, env []string, stdout io.Writer) (int, string) {
	t.Helper()
	wasm := guestModule(t, name)
	var buf bytes.Buffer
	var out io.Writer = &buf
	if stdout != nil {
		out = io.MultiWriter(&buf, stdout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	code, err := Run(ctx, RunConfig{
		Module: wasm,
		Args:   []string{name},
		Env:    env,
		Stdout: out,
		Stderr: out,
		Trace:  os.Getenv("WASIP1SOCK_TRACE") != "",
	})
	if err != nil {
		t.Fatalf("run %s: %v\noutput:\n%s", name, err, buf.String())
	}
	return code, buf.String()
}

// startEchoServer runs a host-side TCP echo server.
func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(c, c)
				if tc, ok := c.(*net.TCPConn); ok {
					tc.CloseWrite()
				}
				time.Sleep(100 * time.Millisecond)
				c.Close()
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestEchoAndConcurrent(t *testing.T) {
	addr := startEchoServer(t)
	code, out := runGuest(t, "echo.wasm", []string{"ECHO_ADDR=" + addr}, nil)
	t.Logf("guest output:\n%s", out)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "echo1: ok bytes=1048576") {
		t.Errorf("missing 1MB echo confirmation")
	}
	if !strings.Contains(out, "echo8: ok conns=8") {
		t.Errorf("missing concurrent echo confirmation")
	}
}

func TestHTTPGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintln(w, "hello from the host")
	}))
	t.Cleanup(srv.Close)

	code, out := runGuest(t, "httpget.wasm", []string{"HTTP_URL=" + srv.URL + "/hello"}, nil)
	t.Logf("guest output:\n%s", out)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "status: 200 OK") {
		t.Errorf("missing 200 status")
	}
	if !strings.Contains(out, "body: hello from the host") {
		t.Errorf("missing body")
	}
}

// lineWatcher scans a stream for a line with the given prefix.
type lineWatcher struct {
	prefix string
	ch     chan string
	once   sync.Once
	buf    bytes.Buffer
}

func (w *lineWatcher) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// incomplete line: put it back
			var rest bytes.Buffer
			rest.WriteString(line)
			rest.ReadFrom(&w.buf)
			w.buf = rest
			break
		}
		if strings.HasPrefix(line, w.prefix) {
			w.once.Do(func() {
				w.ch <- strings.TrimSpace(strings.TrimPrefix(line, w.prefix))
			})
		}
	}
	return len(p), nil
}

func TestHTTPServe(t *testing.T) {
	watcher := &lineWatcher{prefix: "LISTENING ", ch: make(chan string, 1)}
	type result struct {
		code int
		out  string
	}
	resc := make(chan result, 1)
	go func() {
		code, out := runGuest(t, "httpserve.wasm", []string{"SERVE_ADDR=127.0.0.1:0"}, watcher)
		resc <- result{code, out}
	}()

	var addr string
	select {
	case addr = <-watcher.ch:
	case res := <-resc:
		t.Fatalf("guest exited before listening: code %d\n%s", res.code, res.out)
	// runGuest gives the guest 2 minutes; a shorter budget here fails a guest
	// that is only slow, which is what sibling guests on a 2-core runner make it.
	case <-time.After(2 * time.Minute):
		t.Fatal("timed out waiting for the guest to listen")
	}
	t.Logf("guest listening on %s", addr)

	resp, err := http.Get("http://" + addr + "/hello")
	if err != nil {
		t.Fatalf("GET from host: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("host GET: %s %q", resp.Status, body)
	if resp.StatusCode != 200 {
		t.Errorf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "hello from wasip1") {
		t.Errorf("unexpected body %q", body)
	}

	select {
	case res := <-resc:
		t.Logf("guest output:\n%s", res.out)
		if res.code != 0 {
			t.Errorf("exit code %d", res.code)
		}
		if !strings.Contains(res.out, "SERVED") {
			t.Errorf("guest did not confirm serving")
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("timed out waiting for the guest to exit")
	}
}

func TestDialRefused(t *testing.T) {
	t.Serial() // The host-side clock below measures wall time; a sibling guest run inflates it.
	// Find a port that is closed: bind one, note it, close it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	start := time.Now()
	code, out := runGuest(t, "refused.wasm", []string{"REFUSED_ADDR=" + addr}, nil)
	elapsed := time.Since(start)
	t.Logf("guest output (host-side elapsed %v):\n%s", elapsed, out)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(strings.ToLower(out), "connection refused") {
		t.Errorf("expected a connection-refused error")
	}
	if elapsed > 5*time.Second {
		t.Errorf("refusal took %v, want prompt failure", elapsed)
	}
}

func TestReadDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold the connection silently
		}
	}()

	code, out := runGuest(t, "deadline.wasm", []string{"SILENT_ADDR=" + ln.Addr().String()}, nil)
	t.Logf("guest output:\n%s", out)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "timeout: true") {
		t.Errorf("read did not fail with a timeout error")
	}
}

// startUDPEchoServer runs a host-side UDP echo server that answers
// each datagram to its source.
func startUDPEchoServer(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, addr, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			pc.WriteToUDP(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestUDPEcho(t *testing.T) {
	addr := startUDPEchoServer(t)
	code, out := runGuest(t, "udpecho.wasm", []string{"UDPECHO_ADDR=" + addr}, nil)
	t.Logf("guest output:\n%s", out)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, want := range []string{
		"udpecho-host: ok",
		"udpecho-local: ok msgs=8",
		"deadline timeout: true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestUDPConnected(t *testing.T) {
	addr := startUDPEchoServer(t)
	code, out := runGuest(t, "udpconnected.wasm", []string{"UDPECHO_ADDR=" + addr}, nil)
	t.Logf("guest output:\n%s", out)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, want := range []string{
		"udpconnected: ok msgs=3",
		"writeto-connected error:",
		"deadline timeout: true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(out, "pre-connected") {
		t.Errorf("WriteTo on a connected conn should fail with ErrWriteToConnected")
	}
}

// TestStockWazeroRejects proves the opt-in nature of the extension:
// a GOWASI=wasmedgesock binary cannot even instantiate on a stock
// runtime, because the sock_* imports are unknown there.
func TestStockWazeroRejects(t *testing.T) {
	wasm := guestModule(t, "echo.wasm")
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	compiled, err := r.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions("_start"))
	if err == nil {
		t.Fatal("stock wazero unexpectedly instantiated a wasmedgesock module")
	}
	t.Logf("stock wazero instantiation error (expected):\n%v", err)
	if !strings.Contains(err.Error(), "sock_") {
		t.Errorf("error does not mention the sock_* imports: %v", err)
	}
}

// TestDefaultBuildStaysFake runs the same echo program built WITHOUT
// GOWASI under stock wazero: it must instantiate fine (no WasmEdge
// imports) and fail to reach the very-real echo server, because the
// default build still uses the fake in-memory network.
func TestDefaultBuildStaysFake(t *testing.T) {
	addr := startEchoServer(t)
	wasm := guestModule(t, "echo_default.wasm")

	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	compiled, err := r.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var out bytes.Buffer
	_, err = r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_start").
		WithStdout(&out).
		WithStderr(&out).
		WithEnv("ECHO_ADDR", addr).
		WithArgs("echo_default"))
	t.Logf("default-build output under stock wazero:\n%s", out.String())
	if err == nil {
		t.Fatal("default build unexpectedly dialed the real echo server")
	}
	if exitErr, ok := err.(interface{ ExitCode() uint32 }); !ok || exitErr.ExitCode() == 0 {
		t.Fatalf("want nonzero exit, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out.String()), "connection refused") {
		t.Errorf("expected the fake network to refuse the dial")
	}
}

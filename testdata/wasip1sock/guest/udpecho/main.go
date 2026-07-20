// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// udpecho exercises unconnected UDP: it round-trips datagrams of
// several sizes with the host-side echo server named by $UDPECHO_ADDR
// (checking each reply's source address), then echoes between two of
// its own sockets over the host loopback - the replies only arrive if
// ReadFrom reported the true source port - and finally proves read
// deadlines fire on a quiet socket.
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

var sizes = []int{1, 512, 32 << 10}

// hostEcho round-trips one datagram per size with the host's echo
// server, checking payloads and the reply's source address.
func hostEcho(server string) error {
	saddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return fmt.Errorf("resolve %s: %v", server, err)
	}
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return fmt.Errorf("listen: %v", err)
	}
	defer c.Close()
	buf := make([]byte, 64<<10)
	for _, n := range sizes {
		want := payload(byte(n), n)
		if _, err := c.WriteToUDP(want, saddr); err != nil {
			return fmt.Errorf("writeto size=%d: %v", n, err)
		}
		if err := c.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return fmt.Errorf("set deadline: %v", err)
		}
		rn, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("readfrom size=%d: %v", n, err)
		}
		if from.String() != saddr.String() {
			return fmt.Errorf("size=%d reply source %v, want %v", n, from, saddr)
		}
		if !bytes.Equal(buf[:rn], want) {
			return fmt.Errorf("size=%d payload mismatch: got %d bytes", n, rn)
		}
	}
	return nil
}

// localEcho runs an echo server and a client on two of the guest's
// own sockets. The server replies to whatever address ReadFromUDP
// reports, so a wrong source port would strand every reply.
func localEcho() error {
	srv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return fmt.Errorf("server listen: %v", err)
	}
	defer srv.Close()
	cli, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return fmt.Errorf("client listen: %v", err)
	}
	defer cli.Close()

	const msgs = 8
	caddr := cli.LocalAddr().String()
	errc := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for i := 0; i < msgs; i++ {
			if err := srv.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
				errc <- fmt.Errorf("server deadline %d: %v", i, err)
				return
			}
			n, from, err := srv.ReadFromUDP(buf)
			if err != nil {
				errc <- fmt.Errorf("server read %d: %v", i, err)
				return
			}
			if from.String() != caddr {
				errc <- fmt.Errorf("server read %d from %v, want %v", i, from, caddr)
				return
			}
			if _, err := srv.WriteToUDP(buf[:n], from); err != nil {
				errc <- fmt.Errorf("server reply %d: %v", i, err)
				return
			}
		}
		errc <- nil
	}()

	saddr := srv.LocalAddr().(*net.UDPAddr)
	buf := make([]byte, 4096)
	for i := 0; i < msgs; i++ {
		want := payload(byte(i+3), 100+i*13)
		if _, err := cli.WriteToUDP(want, saddr); err != nil {
			return fmt.Errorf("client write %d: %v", i, err)
		}
		if err := cli.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return fmt.Errorf("client deadline %d: %v", i, err)
		}
		n, from, err := cli.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("client read %d: %v", i, err)
		}
		if from.String() != saddr.String() {
			return fmt.Errorf("echo %d source %v, want %v", i, from, saddr)
		}
		if !bytes.Equal(buf[:n], want) {
			return fmt.Errorf("echo %d payload mismatch", i)
		}
	}
	return <-errc
}

// quietDeadline arms a short read deadline on a socket nobody writes
// to and reports whether the read failed with a timeout.
func quietDeadline() (timedOut bool, elapsed time.Duration, err error) {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return false, 0, fmt.Errorf("listen: %v", err)
	}
	defer c.Close()
	if err := c.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		return false, 0, fmt.Errorf("set deadline: %v", err)
	}
	start := time.Now()
	_, _, rerr := c.ReadFromUDP(make([]byte, 16))
	elapsed = time.Since(start)
	if rerr == nil {
		return false, elapsed, fmt.Errorf("read unexpectedly succeeded")
	}
	ne, ok := rerr.(net.Error)
	return ok && ne.Timeout(), elapsed, nil
}

func main() {
	addr := os.Getenv("UDPECHO_ADDR")
	if addr == "" {
		fmt.Println("UDPECHO_ADDR not set")
		os.Exit(1)
	}
	if err := hostEcho(addr); err != nil {
		fmt.Printf("udpecho-host: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("udpecho-host: ok sizes=%v\n", sizes)
	if err := localEcho(); err != nil {
		fmt.Printf("udpecho-local: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("udpecho-local: ok msgs=8")
	timedOut, elapsed, err := quietDeadline()
	if err != nil {
		fmt.Printf("deadline: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("deadline timeout: %v\n", timedOut)
	fmt.Printf("deadline elapsed: %dms\n", elapsed.Milliseconds())
}

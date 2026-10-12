// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"cmd/go/internal/web/intercept"
)

func TestUserAgent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.UserAgent()))
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal("parse httptest url:", err)
	}
	res, err := Get(Insecure, u)
	if err != nil {
		t.Error("http get:", err)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error("read response body:", err)
	}
	gotUserAgent := string(bytes.TrimSpace(b))
	if gotUserAgent != userAgent {
		t.Errorf("User-Agent: %s, want %s", gotUserAgent, userAgent)
	}
}

func TestBannedHostRefused(t *testing.T) {
	for _, raw := range []string{
		"https://proxy.golang.org/golang.org/x/sync/@v/list",
		"http://PROXY.golang.org:443/x",
		"https://golang.org/x/text?go-get=1",
		"https://sum.golang.org/lookup/golang.org/x/text@v0.1.0",
		"https://google.golang.org/grpc?go-get=1",
		"https://golang.org./x",
		"//golang.org/x/tools?go-get=1",
	} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Get(DefaultSecurity, target); err == nil || !strings.Contains(err.Error(), BannedDomain) {
			t.Errorf("Get(%s) err = %v, want a refusal that names %s", raw, err, BannedDomain)
		}
	}

	for _, target := range []string{"https://proxy.golang.org/x", "https://golang.org/dl/", "https://pkg.go.golang.org/"} {
		server := httptest.NewServer(http.RedirectHandler(target, http.StatusFound))
		start, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Get(Insecure, start); err == nil || !strings.Contains(err.Error(), BannedDomain) {
			t.Errorf("a redirect to %s gave err = %v, want a refusal", target, err)
		}
		server.Close()
	}
}

// A test interceptor sends a host to a local server, so that request reaches
// no host under golang.org and goes through. Without it, the same URL is refused.
func TestInterceptedHostReachesTheTestServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.Write([]byte("local"))
	}))
	defer server.Close()
	target, err := url.Parse("http://vcs-test.golang.org/x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Get(Insecure, target); err == nil || !strings.Contains(err.Error(), BannedDomain) {
		t.Fatalf("Get(%s) with no interceptor err = %v, want a refusal", target, err)
	}
	if err := intercept.EnableTestHooks([]intercept.Interceptor{{Scheme: "http", FromHost: "vcs-test.golang.org", ToHost: server.Listener.Addr().String()}}); err != nil {
		t.Fatal(err)
	}
	defer intercept.DisableTestHooks()
	resp, err := Get(Insecure, target)
	if err != nil {
		t.Fatalf("Get(%s) through the interceptor: %v", target, err)
	}
	resp.Body.Close()
	if IsRefusedHost("vcs-test.golang.org") || !IsRefusedHost("proxy.golang.org") {
		t.Error("IsRefusedHost must refuse every banned host but the intercepted one")
	}
}

func TestIsBannedHost(t *testing.T) {
	for _, host := range []string{"golang.org", "GOLANG.ORG", "golang.org.", "proxy.golang.org", "sum.golang.org", "google.golang.org", "a.b.golang.org"} {
		if !IsBannedHost(host) {
			t.Errorf("IsBannedHost(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"", "go.dev", "github.com", "notgolang.org", "golang.org.example.com", "golang.orgx", "go.googlesource.com"} {
		if IsBannedHost(host) {
			t.Errorf("IsBannedHost(%q) = true, want false", host)
		}
	}
	if err := RefuseBannedRemote("::not a url"); err == nil {
		t.Error("RefuseBannedRemote of an unreadable URL succeeded, want a refusal")
	}
}

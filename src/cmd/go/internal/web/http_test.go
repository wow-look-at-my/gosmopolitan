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
	for _, raw := range []string{"https://proxy.golang.org/golang.org/x/sync/@v/list", "http://PROXY.golang.org:443/x"} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Get(DefaultSecurity, target); err == nil || !strings.Contains(err.Error(), BannedHost) {
			t.Errorf("Get(%s) err = %v, want a refusal that names %s", raw, err, BannedHost)
		}
	}

	server := httptest.NewServer(http.RedirectHandler("https://proxy.golang.org/golang.org/x/sync/@v/list", http.StatusFound))
	defer server.Close()
	start, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Get(Insecure, start); err == nil || !strings.Contains(err.Error(), BannedHost) {
		t.Errorf("a redirect to %s gave err = %v, want a refusal", BannedHost, err)
	}
}

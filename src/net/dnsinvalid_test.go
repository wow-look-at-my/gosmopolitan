// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build unix || windows

package net

import (
	"context"
	"errors"
	"testing"
)

func TestIsInvalidDomain(t *testing.T) {
	cases := map[string]bool{
		"invalid":           true,
		"invalid.":          true,
		"invalid.invalid.":  true,
		"host.INVALID":      true,
		"a.b.invalid":       true,
		"notinvalid":        false,
		"invalid.example":   false,
		"example.com.":      false,
		"x.invalidity.test": false,
	}
	for name, want := range cases {
		if got := isInvalidDomain(name); got != want {
			t.Errorf("isInvalidDomain(%q) = %v, want %v", name, got, want)
		}
	}
}

// The Dial here fails the test, so any query sent is caught.
func TestGoResolverAnswersInvalidWithoutAQuery(t *testing.T) {
	res := &Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (Conn, error) {
		t.Errorf("resolver dialed %s %s for an invalid name", network, address)
		return nil, errors.New("no network in this test")
	}}
	_, err := res.LookupHost(context.Background(), "invalid.invalid.")
	var dnsErr *DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound || dnsErr.Err != errNoSuchHost.Error() {
		t.Fatalf("LookupHost(invalid.invalid.) = %v, want a not-found DNSError", err)
	}
}

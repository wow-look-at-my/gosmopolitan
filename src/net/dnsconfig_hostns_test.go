// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows

package net

import (
	"reflect"
	"testing"
)

// A host that keeps its resolvers out of reach of open() answers here
// instead, and the answer reaches the dialer, so it has to arrive in
// the same shape resolv.conf's own nameserver lines produce.
func TestNameserversFromHost(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "nothing to report leaves the caller on its default",
			in:   nil,
			want: nil,
		},
		{
			name: "addresses gain the DNS port",
			in:   []string{"10.0.0.1", "8.8.8.8"},
			want: []string{"10.0.0.1:53", "8.8.8.8:53"},
		},
		{
			name: "a v6 address is bracketed",
			in:   []string{"2001:4860:4860::8888"},
			want: []string{"[2001:4860:4860::8888]:53"},
		},
		{
			name: "a name is dropped: resolving it would need the resolver being configured",
			in:   []string{"dns.example.com", "10.0.0.1"},
			want: []string{"10.0.0.1:53"},
		},
		{
			name: "no more than the standard limit",
			in:   []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"},
			want: []string{"10.0.0.1:53", "10.0.0.2:53", "10.0.0.3:53"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := nameserversFromHost(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("nameserversFromHost(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// An empty answer must leave dnsReadConfig on defaultNS by identity,
// because isDefaultNS compares the backing array: a copy carrying the
// same strings would report false and change what the resolver does
// with it.
func TestUnreadableConfServersKeepsDefaultIdentity(t *testing.T) {
	if len(hostNameservers()) != 0 {
		t.Skip("this host publishes nameservers out of band")
	}
	got := unreadableConfServers()
	if len(got) != len(defaultNS) || &got[0] != &defaultNS[0] {
		t.Errorf("unreadableConfServers() = %q, want defaultNS itself", got)
	}
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Arturo (AutoHost)

package dispatcher

import (
	"reflect"
	"testing"

	"github.com/docker/docker/api/types"
)

func TestPublishedPorts(t *testing.T) {
	got := publishedPorts([]types.Port{
		{IP: "0.0.0.0", PublicPort: 8053, PrivatePort: 80, Type: "tcp"},
		{IP: "::", PublicPort: 8053, PrivatePort: 80, Type: "tcp"}, // IPv6 duplicate
		{IP: "0.0.0.0", PublicPort: 53, PrivatePort: 53, Type: "udp"},
		{IP: "0.0.0.0", PublicPort: 53, PrivatePort: 53, Type: "tcp"}, // same port, other protocol
		{PrivatePort: 9000, Type: "tcp"},                              // exposed but not published
	})
	want := []ContainerPort{
		{PublicPort: 53, PrivatePort: 53, Protocol: "tcp"},
		{PublicPort: 53, PrivatePort: 53, Protocol: "udp"},
		{PublicPort: 8053, PrivatePort: 80, Protocol: "tcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("publishedPorts = %+v, want %+v", got, want)
	}
	if publishedPorts(nil) != nil {
		t.Fatal("no ports must give nil, so the JSON field is omitted")
	}
}

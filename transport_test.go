// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"testing"
)

func TestParseAddressesUnix(t *testing.T) {
	addrs, err := parseAddresses("unix:path=/run/user/1000/bus")
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0].transport != "unix" || addrs[0].keys["path"] != "/run/user/1000/bus" {
		t.Errorf("parsed %#v", addrs)
	}
}

func TestParseAddressesMultiAndAbstract(t *testing.T) {
	addrs, err := parseAddresses("unix:abstract=/tmp/dbus-XyZ,guid=abc;unix:path=/fallback")
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 2 {
		t.Fatalf("want 2 addresses, got %d", len(addrs))
	}
	if addrs[0].keys["abstract"] != "/tmp/dbus-XyZ" || addrs[0].keys["guid"] != "abc" {
		t.Errorf("first: %#v", addrs[0])
	}
	na, err := unixNetAddr(addrs[0])
	if err != nil || na != "@/tmp/dbus-XyZ" {
		t.Errorf("abstract net addr = %q, %v", na, err)
	}
	np, _ := unixNetAddr(addrs[1])
	if np != "/fallback" {
		t.Errorf("path net addr = %q", np)
	}
}

func TestParseAddressesPercentEscape(t *testing.T) {
	addrs, err := parseAddresses("unix:path=/tmp/a%20b")
	if err != nil {
		t.Fatal(err)
	}
	if addrs[0].keys["path"] != "/tmp/a b" {
		t.Errorf("unescaped path = %q", addrs[0].keys["path"])
	}
}

func TestParseAddressesErrors(t *testing.T) {
	for _, s := range []string{
		"",              // empty
		";",             // only separators -> empty
		"notransport",   // no colon
		"unix:novalue",  // parameter without '='
		"unix:path=%2",  // truncated escape
		"unix:path=%zz", // bad escape hex
	} {
		if _, err := parseAddresses(s); err == nil {
			t.Errorf("parseAddresses(%q) expected error", s)
		}
	}
}

func TestUnixNetAddrError(t *testing.T) {
	if _, err := unixNetAddr(busAddress{transport: "unix", keys: map[string]string{}}); err == nil {
		t.Error("expected error for unix address with neither path nor abstract")
	}
}

func TestDialAddressErrors(t *testing.T) {
	// Unsupported transport.
	if _, _, err := dialAddress([]busAddress{{transport: "tcp", keys: map[string]string{}}}); err == nil {
		t.Error("expected unsupported-transport error")
	}
	// unix without path/abstract.
	if _, _, err := dialAddress([]busAddress{{transport: "unix", keys: map[string]string{}}}); err == nil {
		t.Error("expected missing path/abstract error")
	}
	// unix path that cannot be dialled.
	if _, _, err := dialAddress([]busAddress{{transport: "unix", keys: map[string]string{"path": "/nonexistent/socket/really"}}}); err == nil {
		t.Error("expected dial error for missing socket")
	}
}

func TestSystemBusAddressDefault(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "")
	if got := systemBusAddress(); got != defaultSystemBusAddress {
		t.Errorf("default system bus = %q", got)
	}
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path=/custom")
	if got := systemBusAddress(); got != "unix:path=/custom" {
		t.Errorf("env system bus = %q", got)
	}
}

func TestSessionBusAddressEnv(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	if _, err := sessionBusAddress(); err == nil {
		t.Error("expected error when session address unset")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/s")
	if s, err := sessionBusAddress(); err != nil || s != "unix:path=/s" {
		t.Errorf("session bus = %q, %v", s, err)
	}
}

func TestConnectAndDialAddressErrors(t *testing.T) {
	// Dial with an unparseable address.
	if _, err := Dial("bogus"); err == nil {
		t.Error("expected Dial parse error")
	}
	// Dial with a valid-but-undialable address.
	if _, err := Dial("unix:path=/nonexistent/socket/really"); err == nil {
		t.Error("expected Dial connect error")
	}
	// ConnectSessionBus with the env cleared.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	if _, err := ConnectSessionBus(); err == nil {
		t.Error("expected ConnectSessionBus error with no address")
	}
	// ConnectSystemBus against the (absent) default socket.
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path=/nonexistent/socket/really")
	if _, err := ConnectSystemBus(); err == nil {
		t.Error("expected ConnectSystemBus error")
	}
}

func TestCurrentUIDString(t *testing.T) {
	if currentUIDString(1000) != "1000" {
		t.Errorf("currentUIDString(1000) = %q", currentUIDString(1000))
	}
}

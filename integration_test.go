// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

// This end-to-end test talks to a real dbus-daemon and is therefore gated: it
// runs only when DBUS_GOFD_INTEGRATION=1 (set it under `dbus-run-session`). It
// proves interoperability with the reference implementation — connect, request
// a name, export an object, call it from a second connection, emit and receive
// a signal, and introspect — exercising the public API exactly as a consumer
// (for example go-freedesktop/notifications) would.
package dbus_test

import (
	"os"
	"testing"
	"time"

	"github.com/go-freedesktop/dbus"
)

const (
	svcName   = "org.gofd.dbus.Test"
	svcPath   = dbus.ObjectPath("/org/gofd/dbus/Test")
	svcIface  = "org.gofd.dbus.Test1"
	greetVerb = svcIface + ".Greet"
	pingSig   = svcIface + ".Pinged"
)

// greeter is the object exported on the real bus.
type greeter struct{}

func (greeter) Greet(name string) (string, *dbus.Error) {
	return "Hello " + name, nil
}

func (greeter) Boom() (string, *dbus.Error) {
	return "", &dbus.Error{Name: "org.gofd.dbus.Test1.Boom", Body: []interface{}{"kaboom"}}
}

func TestIntegrationRealBus(t *testing.T) {
	if os.Getenv("DBUS_GOFD_INTEGRATION") != "1" {
		t.Skip("set DBUS_GOFD_INTEGRATION=1 (under dbus-run-session) to run the real-bus test")
	}

	// --- server connection: claim a name and export an object ---
	srv, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatalf("ConnectSessionBus (server): %v", err)
	}
	defer srv.Close()
	t.Logf("server unique name: %v", srv.Names())

	if err := srv.Export(greeter{}, svcPath, svcIface); err != nil {
		t.Fatalf("Export: %v", err)
	}
	code, err := srv.RequestName(svcName, dbus.NameFlagDoNotQueue)
	if err != nil {
		t.Fatalf("RequestName: %v", err)
	}
	if code != dbus.NameReplyPrimaryOwner {
		t.Fatalf("RequestName code = %d, want primary owner", code)
	}

	// --- client connection: call the exported method ---
	cli, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatalf("ConnectSessionBus (client): %v", err)
	}
	defer cli.Close()

	obj := cli.Object(svcName, svcPath)
	var greeting string
	if err := obj.Call(greetVerb, 0, "world").Store(&greeting); err != nil {
		t.Fatalf("Greet call: %v", err)
	}
	if greeting != "Hello world" {
		t.Fatalf("Greet = %q, want %q", greeting, "Hello world")
	}
	t.Logf("Greet returned: %q", greeting)

	// Remote error propagation.
	if err := obj.Call(svcIface+".Boom", 0).Err; err == nil {
		t.Fatal("expected remote error from Boom")
	} else {
		t.Logf("Boom returned expected error: %v", err)
	}

	// --- introspection via the real bus ---
	var xml string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if !contains(xml, svcIface) || !contains(xml, "Greet") {
		t.Fatalf("introspection missing exported interface/method:\n%s", xml)
	}
	t.Logf("introspection length: %d bytes", len(xml))

	// --- signal: subscribe, emit, receive ---
	if err := cli.AddMatch("type='signal',interface='" + svcIface + "'"); err != nil {
		t.Fatalf("AddMatch: %v", err)
	}
	sigCh := make(chan *dbus.Signal, 4)
	cli.Signal(sigCh)

	if err := srv.Emit(svcPath, pingSig, int32(7), "greetings"); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case sig := <-sigCh:
			if sig.Name != pingSig {
				continue // ignore unrelated bus signals (e.g. NameAcquired)
			}
			if len(sig.Body) != 2 || sig.Body[0].(int32) != 7 || sig.Body[1].(string) != "greetings" {
				t.Fatalf("signal body = %#v", sig.Body)
			}
			t.Logf("received signal %s from %s: %#v", sig.Name, sig.Sender, sig.Body)
			return // success
		case <-deadline:
			t.Fatal("timed out waiting for the emitted signal")
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

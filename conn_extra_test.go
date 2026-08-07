// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestObjectPathAndDestination(t *testing.T) {
	c, _, _ := pipePair(t)
	obj := c.Object("org.dest", testPath)
	if obj.Path() != testPath || obj.Destination() != "org.dest" {
		t.Errorf("Path/Destination = %q %q", obj.Path(), obj.Destination())
	}
}

func TestDialHandshakeError(t *testing.T) {
	// A server that accepts then immediately closes fails the client SASL
	// handshake, and Dial must return the error (not hang).
	sock := filepath.Join(t.TempDir(), "bus")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if conn, err := ln.Accept(); err == nil {
			conn.Close()
		}
	}()
	if _, err := Dial("unix:path=" + sock); err == nil {
		t.Error("expected handshake error from a peer that closes immediately")
	}
}

// blockConn is a net.Conn whose reads block until Close and whose writes always
// fail, used to exercise the write-error path in callWithContext.
type blockConn struct {
	ch   chan struct{}
	once sync.Once
}

func newBlockConn() *blockConn { return &blockConn{ch: make(chan struct{})} }

func (b *blockConn) Read([]byte) (int, error) {
	<-b.ch
	return 0, errors.New("closed")
}
func (b *blockConn) Write([]byte) (int, error)        { return 0, errors.New("write failed") }
func (b *blockConn) Close() error                     { b.once.Do(func() { close(b.ch) }); return nil }
func (b *blockConn) LocalAddr() net.Addr              { return fakeAddr{} }
func (b *blockConn) RemoteAddr() net.Addr             { return fakeAddr{} }
func (b *blockConn) SetDeadline(time.Time) error      { return nil }
func (b *blockConn) SetReadDeadline(time.Time) error  { return nil }
func (b *blockConn) SetWriteDeadline(time.Time) error { return nil }

func TestCallWriteError(t *testing.T) {
	c := NewConn(newBlockConn())
	defer c.Close()
	err := c.Object("org.test", testPath).Call(testIface+".Echo", 0, "x").Err
	if err == nil {
		t.Error("expected write error from a failing transport")
	}
}

func TestDeliverSignalSenderAndDrop(t *testing.T) {
	c, _, _ := pipePair(t)
	ch := make(chan *Signal) // unbuffered, never read -> delivery is dropped
	c.Signal(ch)
	// A signal carrying a SENDER header; the drop keeps the read loop alive.
	c.deliverSignal(&Message{Type: TypeSignal, Headers: map[HeaderField]Variant{
		FieldSender: MakeVariant(":1.5"),
		FieldPath:   MakeVariant(testPath),
	}})
	// No assertion beyond not blocking: reaching here proves the non-ready
	// channel was skipped via the default branch.
}

func TestReleaseNameError(t *testing.T) {
	// A bus-less peer answers ReleaseName with an UnknownObject error reply,
	// which ReleaseName must surface.
	ca, cb := net.Pipe()
	c := NewConn(ca)
	s := NewConn(cb)
	t.Cleanup(func() { c.Close(); s.Close() })
	if _, err := c.ReleaseName("org.test.app"); err == nil {
		t.Error("expected ReleaseName error against a bus-less peer")
	}
}

func TestMachineID(t *testing.T) {
	old := machineIDPaths
	defer func() { machineIDPaths = old }()

	f := filepath.Join(t.TempDir(), "machine-id")
	if err := os.WriteFile(f, []byte("abc123def456\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	machineIDPaths = []string{f}
	if got := machineID(); got != "abc123def456" {
		t.Errorf("machineID = %q", got)
	}
	machineIDPaths = []string{filepath.Join(t.TempDir(), "does-not-exist")}
	if got := machineID(); got != "" {
		t.Errorf("machineID with no file = %q", got)
	}
}

func TestHeaderStringNonString(t *testing.T) {
	// A header field whose variant is neither string nor ObjectPath yields
	// (,false).
	m := &Message{Headers: map[HeaderField]Variant{FieldUnixFDs: MakeVariant(uint32(3))}}
	if _, ok := headerString(m, FieldUnixFDs); ok {
		t.Error("expected non-string header to report ok=false")
	}
}

func TestIntrospectPropertyOnlyInterface(t *testing.T) {
	c, server, _ := pipePair(t)
	// Properties on an interface that exports no methods: its name must still
	// appear in the introspection document.
	if err := server.ExportProperties(testPath, "org.only.Props", map[string]*Prop{
		"Flag": {Value: true, Writable: false},
	}); err != nil {
		t.Fatal(err)
	}
	var xml string
	if err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `<interface name="org.only.Props">`) ||
		!strings.Contains(xml, `<property name="Flag" type="b" access="read"/>`) {
		t.Errorf("property-only interface missing:\n%s", xml)
	}
}

func TestIntrospectGrandchildNode(t *testing.T) {
	c, server, _ := pipePair(t)
	// A grandchild path exercises the multi-level child-name trimming.
	if err := server.Export(&testService{invoked: make(chan string, 1)}, testPath+"/child/grand", testIface); err != nil {
		t.Fatal(err)
	}
	var xml string
	if err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `<node name="child"/>`) {
		t.Errorf("expected immediate child node from a grandchild path:\n%s", xml)
	}
}

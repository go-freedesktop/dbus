// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"bytes"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readLineRaw reads a CRLF-terminated line one byte at a time so the server
// never buffers past the BEGIN line into the binary D-Bus stream that follows.
func readLineRaw(c net.Conn) (string, error) {
	var b []byte
	one := make([]byte, 1)
	for {
		if _, err := io.ReadFull(c, one); err != nil {
			return "", err
		}
		if one[0] == '\n' {
			return strings.TrimRight(string(b), "\r"), nil
		}
		b = append(b, one[0])
	}
}

// serverAuth performs the server half of the SASL handshake: consume the
// leading NUL, accept EXTERNAL, and wait for BEGIN.
func serverAuth(c net.Conn) error {
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	one := make([]byte, 1)
	if _, err := io.ReadFull(c, one); err != nil { // leading NUL
		return err
	}
	line, err := readLineRaw(c)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "AUTH") {
		return errors.New("expected AUTH")
	}
	if _, err := io.WriteString(c, "OK 0123456789abcdef0123456789abcdef\r\n"); err != nil {
		return err
	}
	if _, err := readLineRaw(c); err != nil { // BEGIN
		return err
	}
	return c.SetDeadline(time.Time{})
}

// startFakeBus listens on a unix socket, performs the server SASL handshake for
// one client and (optionally) serves a mock bus object, so the full dial +
// auth + Hello path can be exercised in-process without a real dbus-daemon.
func startFakeBus(t *testing.T, exportBus bool) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "bus")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	served := make(chan *Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if err := serverAuth(conn); err != nil {
			conn.Close()
			return
		}
		s := NewConn(conn)
		if exportBus {
			_ = s.Export(mockBus{}, busPath, busIface)
		}
		served <- s
	}()
	t.Cleanup(func() {
		ln.Close()
		select {
		case s := <-served:
			s.Close()
		default:
		}
	})
	return "unix:path=" + sock
}

func TestDialAndConnectSessionBus(t *testing.T) {
	addr := startFakeBus(t, true)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)

	c, err := ConnectSessionBus()
	if err != nil {
		t.Fatalf("ConnectSessionBus: %v", err)
	}
	defer c.Close()
	if got := c.Names(); len(got) != 1 || got[0] != ":1.99" {
		t.Errorf("unique name = %v", got)
	}
}

func TestDialRaw(t *testing.T) {
	addr := startFakeBus(t, true)
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	// Dial does not send Hello, so there is no unique name yet.
	if len(c.Names()) != 0 {
		t.Errorf("Dial should not assign a name: %v", c.Names())
	}
}

func TestConnectSessionBusHelloError(t *testing.T) {
	// A bus that authenticates but serves no bus object: Hello fails, and
	// connectAndHello must close and return the error rather than hang.
	addr := startFakeBus(t, false)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	old := DefaultCallTimeout
	DefaultCallTimeout = 500 * time.Millisecond
	defer func() { DefaultCallTimeout = old }()
	if _, err := ConnectSessionBus(); err == nil {
		t.Error("expected Hello error from a bus-less peer")
	}
}

// fakeConn is a net.Conn backed by in-memory buffers, with injectable failures
// for the clientHandshake error branches that a real socket cannot deterministically hit.
type fakeConn struct {
	r                *bytes.Reader
	w                bytes.Buffer
	failWrite        bool
	failZeroDeadline bool
}

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake" }

func (f *fakeConn) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *fakeConn) Write(p []byte) (int, error) {
	if f.failWrite {
		return 0, errors.New("write failed")
	}
	return f.w.Write(p)
}
func (f *fakeConn) Close() error         { return nil }
func (f *fakeConn) LocalAddr() net.Addr  { return fakeAddr{} }
func (f *fakeConn) RemoteAddr() net.Addr { return fakeAddr{} }
func (f *fakeConn) SetDeadline(tm time.Time) error {
	if f.failZeroDeadline && tm.IsZero() {
		return errors.New("clear-deadline failed")
	}
	return nil
}
func (f *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(time.Time) error { return nil }

func TestClientHandshakeSuccess(t *testing.T) {
	f := &fakeConn{r: bytes.NewReader([]byte("OK guid\r\n"))}
	if err := clientHandshake(f); err != nil {
		t.Fatalf("clientHandshake: %v", err)
	}
	// The leading NUL and an AUTH line must have been written.
	if f.w.Len() == 0 || f.w.Bytes()[0] != 0 {
		t.Errorf("handshake did not write leading NUL: %v", f.w.Bytes())
	}
	if !strings.Contains(f.w.String(), "AUTH EXTERNAL") {
		t.Errorf("no AUTH EXTERNAL line: %q", f.w.String())
	}
}

func TestClientHandshakeWriteNULError(t *testing.T) {
	f := &fakeConn{r: bytes.NewReader(nil), failWrite: true}
	if err := clientHandshake(f); err == nil {
		t.Error("expected write error for leading NUL")
	}
}

func TestClientHandshakeAuthError(t *testing.T) {
	// Both mechanisms rejected -> authenticate fails inside clientHandshake.
	f := &fakeConn{r: bytes.NewReader([]byte("REJECTED EXTERNAL\r\nREJECTED ANONYMOUS\r\n"))}
	if err := clientHandshake(f); err == nil {
		t.Error("expected auth failure")
	}
}

func TestClientHandshakeClearDeadlineError(t *testing.T) {
	f := &fakeConn{r: bytes.NewReader([]byte("OK guid\r\n")), failZeroDeadline: true}
	if err := clientHandshake(f); err == nil {
		t.Error("expected clear-deadline error")
	}
}

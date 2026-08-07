// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// authScript runs a scripted SASL server on one end of a net.Pipe. For each
// client line it reads, it writes the next reply from replies. The exchange is
// deadline-bounded so a desync fails fast instead of hanging.
func authScript(t *testing.T, replies []string) (client net.Conn, done chan struct{}) {
	t.Helper()
	c, s := net.Pipe()
	deadline := time.Now().Add(2 * time.Second)
	_ = c.SetDeadline(deadline)
	_ = s.SetDeadline(deadline)
	done = make(chan struct{})
	go func() {
		defer close(done)
		defer s.Close()
		r := bufio.NewReader(s)
		for _, reply := range replies {
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
			if _, err := io.WriteString(s, reply); err != nil {
				return
			}
		}
		// Drain any trailing BEGIN.
		_, _ = r.ReadString('\n')
	}()
	return c, done
}

func TestAuthExternalOK(t *testing.T) {
	c, done := authScript(t, []string{"OK 1234abcd\r\n"})
	defer c.Close()
	res, err := authenticate(c, "1000", false)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if res.guid != "1234abcd" {
		t.Errorf("guid = %q", res.guid)
	}
	<-done
}

func TestAuthExternalRejectedThenAnonymous(t *testing.T) {
	c, done := authScript(t, []string{"REJECTED EXTERNAL\r\n", "OK deadbeef\r\n"})
	defer c.Close()
	res, err := authenticate(c, "1000", false)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if res.guid != "deadbeef" {
		t.Errorf("guid = %q", res.guid)
	}
	<-done
}

func TestAuthAllRejected(t *testing.T) {
	c, done := authScript(t, []string{"REJECTED EXTERNAL\r\n", "REJECTED ANONYMOUS\r\n"})
	defer c.Close()
	if _, err := authenticate(c, "1000", false); err == nil {
		t.Error("expected all-rejected error")
	}
	<-done
}

func TestAuthErrorFinal(t *testing.T) {
	c, done := authScript(t, []string{"REJECTED EXTERNAL\r\n", "ERROR nope\r\n"})
	defer c.Close()
	if _, err := authenticate(c, "1000", false); err == nil {
		t.Error("expected auth ERROR to fail after fallback")
	}
	<-done
}

func TestAuthUnexpectedReply(t *testing.T) {
	c, done := authScript(t, []string{"WEIRD stuff\r\n"})
	defer c.Close()
	if _, err := authenticate(c, "1000", false); err == nil {
		t.Error("expected unexpected-reply error")
	}
	<-done
}

func TestAuthNegotiateUnixFDAgree(t *testing.T) {
	c, done := authScript(t, []string{"OK guid\r\n", "AGREE_UNIX_FD\r\n"})
	defer c.Close()
	res, err := authenticate(c, "1000", true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.unixFDOK {
		t.Error("expected AGREE_UNIX_FD to set unixFDOK")
	}
	<-done
}

func TestAuthNegotiateUnixFDError(t *testing.T) {
	c, done := authScript(t, []string{"OK guid\r\n", "ERROR not supported\r\n"})
	defer c.Close()
	res, err := authenticate(c, "1000", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.unixFDOK {
		t.Error("expected declined unix-fd negotiation")
	}
	<-done
}

func TestAuthNegotiateUnixFDUnexpected(t *testing.T) {
	c, done := authScript(t, []string{"OK guid\r\n", "HUH\r\n"})
	defer c.Close()
	if _, err := authenticate(c, "1000", true); err == nil {
		t.Error("expected unexpected NEGOTIATE_UNIX_FD reply error")
	}
	<-done
}

// closedRW returns io errors on read and write to exercise the transport error
// branches in authenticate.
type closedRW struct{}

func (closedRW) Read([]byte) (int, error)  { return 0, io.ErrClosedPipe }
func (closedRW) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestAuthWriteError(t *testing.T) {
	if _, err := authenticate(closedRW{}, "1000", false); err == nil {
		t.Error("expected write error from authenticate")
	}
}

// readErrRW writes fine but fails reads, so authenticate fails awaiting the
// first reply.
type readErrRW struct{ w io.Writer }

func (r readErrRW) Read([]byte) (int, error)    { return 0, io.EOF }
func (r readErrRW) Write(p []byte) (int, error) { return r.w.Write(p) }

func TestAuthReadError(t *testing.T) {
	if _, err := authenticate(readErrRW{w: io.Discard}, "1000", false); err == nil {
		t.Error("expected read error from authenticate")
	}
}

func TestAuthErrorThenFallbackOK(t *testing.T) {
	// EXTERNAL returns ERROR (not final) so the client continues to ANONYMOUS.
	c, done := authScript(t, []string{"ERROR bad\r\n", "OK guid2\r\n"})
	defer c.Close()
	res, err := authenticate(c, "1000", false)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if res.guid != "guid2" {
		t.Errorf("guid = %q", res.guid)
	}
	<-done
}

func TestFinishAuthBeginWriteError(t *testing.T) {
	// No FD negotiation: the first write is BEGIN, which fails.
	r := bufio.NewReader(strings.NewReader(""))
	if _, err := finishAuth(closedRW{}, r, false, authResult{}); err == nil {
		t.Error("expected BEGIN write error")
	}
}

func TestFinishAuthNegotiateWriteError(t *testing.T) {
	// With FD negotiation, the first write is NEGOTIATE_UNIX_FD, which fails.
	r := bufio.NewReader(strings.NewReader(""))
	if _, err := finishAuth(closedRW{}, r, true, authResult{}); err == nil {
		t.Error("expected NEGOTIATE_UNIX_FD write error")
	}
}

func TestFinishAuthNegotiateReadError(t *testing.T) {
	// NEGOTIATE write succeeds but the reply cannot be read.
	r := bufio.NewReader(strings.NewReader(""))
	if _, err := finishAuth(io.Discard, r, true, authResult{}); err == nil {
		t.Error("expected NEGOTIATE_UNIX_FD read error")
	}
}

func TestReadLineNoSpace(t *testing.T) {
	cmd, args, err := readLine(bufio.NewReader(strings.NewReader("BEGIN\r\n")))
	if err != nil || cmd != "BEGIN" || args != "" {
		t.Errorf("readLine = %q %q %v", cmd, args, err)
	}
}

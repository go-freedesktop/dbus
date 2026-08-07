// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// authResult reports what the SASL exchange negotiated.
type authResult struct {
	guid     string // server GUID from the OK line (may be empty)
	unixFDOK bool   // whether NEGOTIATE_UNIX_FD was agreed
}

// authenticate runs the D-Bus SASL handshake as a client over rw. The caller
// must already have written the mandatory leading NUL byte. It tries the
// EXTERNAL mechanism (credentials-passing, the common case for local sockets)
// and falls back to ANONYMOUS if EXTERNAL is rejected. When negotiateUnixFD is
// true it additionally requests UNIX-FD passing. It finishes with BEGIN, after
// which the connection carries the binary message stream.
//
// The exchange is line-oriented: commands and replies are CRLF-terminated ASCII
// as defined by the specification's "Authentication Protocol" section.
func authenticate(rw io.ReadWriter, uid string, negotiateUnixFD bool) (authResult, error) {
	r := bufio.NewReader(rw)
	send := func(format string, args ...interface{}) error {
		_, err := fmt.Fprintf(rw, format+"\r\n", args...)
		return err
	}

	// Mechanisms to attempt, in order of preference.
	extToken := hex.EncodeToString([]byte(uid))
	mechs := []struct {
		name  string
		token string
	}{
		{"EXTERNAL", extToken},
		{"ANONYMOUS", hex.EncodeToString([]byte("go-freedesktop"))},
	}

	var res authResult
	var last string
	for _, m := range mechs {
		if err := send("AUTH %s %s", m.name, m.token); err != nil {
			return res, err
		}
		cmd, args, err := readLine(r)
		if err != nil {
			return res, err
		}
		switch cmd {
		case "OK":
			res.guid = args
			return finishAuth(rw, r, negotiateUnixFD, res)
		case "REJECTED", "ERROR":
			last = strings.TrimSpace(cmd + " " + args) // try the next mechanism
			continue
		default:
			return res, fmt.Errorf("dbus: unexpected auth reply %q", cmd)
		}
	}
	return res, fmt.Errorf("dbus: authentication failed (last server reply: %q)", last)
}

// finishAuth performs the optional UNIX-FD negotiation and the terminating
// BEGIN after an OK line has been received.
func finishAuth(w io.Writer, r *bufio.Reader, negotiateUnixFD bool, res authResult) (authResult, error) {
	send := func(s string) error {
		_, err := io.WriteString(w, s+"\r\n")
		return err
	}
	if negotiateUnixFD {
		if err := send("NEGOTIATE_UNIX_FD"); err != nil {
			return res, err
		}
		cmd, args, err := readLine(r)
		if err != nil {
			return res, err
		}
		switch cmd {
		case "AGREE_UNIX_FD":
			res.unixFDOK = true
		case "ERROR":
			res.unixFDOK = false // server declined; continue without FD passing
			_ = args
		default:
			return res, fmt.Errorf("dbus: unexpected reply to NEGOTIATE_UNIX_FD: %q", cmd)
		}
	}
	if err := send("BEGIN"); err != nil {
		return res, err
	}
	return res, nil
}

// readLine reads one CRLF-terminated auth line and splits it into its command
// word and the remainder (arguments).
func readLine(r *bufio.Reader) (cmd, args string, err error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return line[:i], line[i+1:], nil
	}
	return line, "", nil
}

// currentUIDString returns the caller's numeric UID as a decimal string, used
// as the EXTERNAL mechanism's identity token.
func currentUIDString(uid int) string {
	return strconv.Itoa(uid)
}

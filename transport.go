// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// defaultSystemBusAddress is the well-known system bus socket used when
// DBUS_SYSTEM_BUS_ADDRESS is unset.
const defaultSystemBusAddress = "unix:path=/var/run/dbus/system_bus_socket"

// busAddress is one parsed entry from a (possibly multi-entry) D-Bus address
// string: a transport name and its key=value parameters.
type busAddress struct {
	transport string
	keys      map[string]string
}

// parseAddresses parses a D-Bus address string, which is one or more
// ";"-separated entries of the form "transport:key=value,key=value". Values use
// percent-encoding for reserved characters; this implementation decodes it.
func parseAddresses(s string) ([]busAddress, error) {
	var out []busAddress
	for _, entry := range strings.Split(s, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		colon := strings.IndexByte(entry, ':')
		if colon < 0 {
			return nil, fmt.Errorf("dbus: address %q has no transport", entry)
		}
		a := busAddress{transport: entry[:colon], keys: map[string]string{}}
		params := entry[colon+1:]
		if params != "" {
			for _, kv := range strings.Split(params, ",") {
				eq := strings.IndexByte(kv, '=')
				if eq < 0 {
					return nil, fmt.Errorf("dbus: address parameter %q is not key=value", kv)
				}
				val, err := unescapeAddressValue(kv[eq+1:])
				if err != nil {
					return nil, err
				}
				a.keys[kv[:eq]] = val
			}
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("dbus: empty address")
	}
	return out, nil
}

// unescapeAddressValue decodes the "%xx" percent-encoding used in address
// values.
func unescapeAddressValue(s string) (string, error) {
	if !strings.ContainsRune(s, '%') {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", fmt.Errorf("dbus: truncated %%-escape in address value %q", s)
		}
		var v int
		if _, err := fmt.Sscanf(s[i+1:i+3], "%02x", &v); err != nil {
			return "", fmt.Errorf("dbus: bad %%-escape in address value %q", s)
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String(), nil
}

// dialAddress connects the first usable entry of a parsed address list, returns
// the underlying net.Conn and the entry that succeeded. Only the "unix"
// transport is supported (path= and abstract= forms).
func dialAddress(addrs []busAddress) (net.Conn, busAddress, error) {
	var lastErr error
	for _, a := range addrs {
		if a.transport != "unix" {
			lastErr = fmt.Errorf("dbus: unsupported transport %q", a.transport)
			continue
		}
		netAddr, err := unixNetAddr(a)
		if err != nil {
			lastErr = err
			continue
		}
		conn, err := net.Dial("unix", netAddr)
		if err != nil {
			lastErr = err
			continue
		}
		return conn, a, nil
	}
	return nil, busAddress{}, fmt.Errorf("dbus: no usable address: %w", lastErr)
}

// unixNetAddr converts a unix busAddress into the string net.Dial expects. An
// abstract socket is expressed with a leading "@" (Go's abstract-namespace
// convention), matching the wire's leading-NUL abstract address.
func unixNetAddr(a busAddress) (string, error) {
	if p, ok := a.keys["path"]; ok {
		return p, nil
	}
	if abs, ok := a.keys["abstract"]; ok {
		return "@" + abs, nil
	}
	return "", fmt.Errorf("dbus: unix address has neither path= nor abstract=")
}

// sessionBusAddress returns the session bus address from the environment.
func sessionBusAddress() (string, error) {
	if s := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("dbus: DBUS_SESSION_BUS_ADDRESS is not set")
}

// systemBusAddress returns the system bus address from the environment, falling
// back to the well-known socket path.
func systemBusAddress() string {
	if s := os.Getenv("DBUS_SYSTEM_BUS_ADDRESS"); s != "" {
		return s
	}
	return defaultSystemBusAddress
}

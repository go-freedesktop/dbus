# dbus — go-freedesktop

[![ci](https://github.com/go-freedesktop/dbus/actions/workflows/ci.yml/badge.svg)](https://github.com/go-freedesktop/dbus/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-freedesktop/dbus.svg)](https://pkg.go.dev/github.com/go-freedesktop/dbus)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

A from-scratch, pure-Go implementation of the **[D-Bus](https://dbus.freedesktop.org/doc/dbus-specification.html)**
message protocol — the freedesktop IPC bus every Linux desktop service speaks.
**CGO-free, zero non-standard dependencies**, built directly against the D-Bus
specification. It is the go-freedesktop family's own D-Bus layer, a sovereign
replacement for `github.com/godbus/dbus/v5` whose public surface is kept
deliberately close so migrating off the third-party/cgo-adjacent dependency is
near-mechanical.

`dbus` is the open freedesktop protocol name — a spec, like the shared MIME-info
database or the notifications interface — not a borrowed project name.

## Scope — the whole stack, phased

The library implements the four layers a D-Bus client *and* service needs:

- **Wire codec** — the complete type system (`y b n q i u x t d h`, the
  string-likes `s o g`, arrays `a`, structs `(...)`, variants `v`, and dict
  arrays `a{..}`), with correct alignment/padding and **both byte orders**
  (little `l` and big `B`, validated on real s390x/ppc64le hardware models in
  CI). Messages carry the standard header (`a(yv)` fields, endian flag, type,
  flags, protocol version, body length, serial). Go values map naturally, with
  `Variant`, `ObjectPath`, `Signature`, `MakeVariant`, and the `Marshaler` /
  `Unmarshaler` escape hatches.
- **Auth & transport** — the unix-socket transport (leading `\0`, line-based
  SASL: `AUTH EXTERNAL <uid>` with `AUTH ANONYMOUS` fallback, optional
  `NEGOTIATE_UNIX_FD`, `BEGIN`, and `REJECTED`/`OK`/`ERROR` handling), plus
  address parsing for `unix:path=` / `unix:abstract=` and the session/system
  bus environment variables.
- **Connection** — a serial counter, method-call→reply matching that is
  **context-cancellable and always bounded by a hard timeout** (a silent or
  misbehaving peer can never hang a caller), the `Hello` handshake, an incoming
  dispatch loop, and signal subscription (`AddMatch`/`RemoveMatch` + a
  `Signal(chan)` fan-out).
- **Server / export** — publish a Go value's methods on an object path and
  interface with reflection dispatch (a trailing `*Error` return becomes an
  error reply), `RequestName`, `Emit`, and the built-in
  `org.freedesktop.DBus.Introspectable`, `.Properties`, and `.Peer`
  interfaces.

## Install

```sh
go get github.com/go-freedesktop/dbus
```

## Quickstart

```go
package main

import (
	"fmt"

	"github.com/go-freedesktop/dbus"
)

func main() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	// Call a method: list the names currently on the bus.
	var names []string
	err = conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").
		Call("org.freedesktop.DBus.ListNames", 0).Store(&names)
	if err != nil {
		panic(err)
	}
	fmt.Println(names)
}
```

Exporting an object is symmetric:

```go
type Greeter struct{}

func (Greeter) Greet(name string) (string, *dbus.Error) {
	return "Hello " + name, nil
}

conn.Export(Greeter{}, "/com/example/Greeter", "com.example.Greeter1")
conn.RequestName("com.example.Greeter", dbus.NameFlagDoNotQueue)
```

## Public API (server/export surface)

| Symbol | Purpose |
| --- | --- |
| `ConnectSessionBus() / ConnectSystemBus()` | dial a bus and perform `Hello` |
| `Dial(address) / NewConn(net.Conn)` | connect (with SASL) / wrap an authenticated transport |
| `(*Conn).Object(dest, path) BusObject` | handle for a remote object |
| `BusObject.Call / CallWithContext(...) *Call` | invoke a method (hard-timeout bounded) |
| `(*Call).Store(&dst...)` | decode a reply, or return its error |
| `(*Conn).Export(v, path, iface)` | publish a Go value's methods (reflection dispatch) |
| `(*Conn).RequestName / ReleaseName(name, flags)` | own / release a well-known name |
| `(*Conn).Emit(path, "iface.member", args...)` | emit a signal |
| `(*Conn).Signal(ch) / AddMatch / RemoveMatch` | subscribe to signals |
| `(*Conn).ExportProperties(path, iface, props)` | serve `org.freedesktop.DBus.Properties` |
| `(*Conn).EmitPropertiesChanged(...)` | emit `PropertiesChanged` |
| `Variant / MakeVariant / MakeVariantWithSignature` | self-describing values |
| `ObjectPath / Signature / ParseSignature` | validated wire identifiers |
| `Error / NewError / MakeFailedError` | D-Bus error replies (implements `error`) |
| `Marshal / Unmarshal / Store` | the low-level codec |

The names and shapes (`Export`, `RequestName`, `Emit`, `Variant`, `*Error`,
`MakeVariant`, `ObjectPath`, `Signature`) mirror `godbus/dbus/v5` so a consumer
such as **[go-freedesktop/notifications](https://github.com/go-freedesktop/notifications)**
migrates with near-mechanical edits.

## Never hangs

Every method call is bounded twice over: by the caller's `context.Context` and,
when that carries no deadline, by the package-level `DefaultCallTimeout`. The
read loop fails every in-flight call when the connection drops, and socket
writes carry their own deadline. A peer that stops responding produces a
`context.DeadlineExceeded`, never a stuck goroutine.

## Tests & coverage

`CGO_ENABLED=0 go test ./...` — **100% statement coverage**, including every
error branch. The codec is exercised by table round-trips under both byte
orders plus targeted truncation/misalignment/bad-signature cases; the
connection, auth, and export layers run over in-process `net.Pipe` pairs and
real unix sockets, **every one bounded by a test timeout and context deadline**
so CI can never hang. CI also runs the suite on the six supported 64-bit
targets (amd64/arm64 natively, riscv64/loong64/ppc64le/s390x under qemu-user);
the big-endian s390x run validates the `B` wire order end to end. The `-race`
coverage gate is the only step that enables cgo.

### Real-dbus-daemon interoperability

A gated end-to-end test (`integration_test.go`, `//go:build linux`) proves
interop with the reference `dbus-daemon`. Run it under a private session bus:

```sh
DBUS_GOFD_INTEGRATION=1 dbus-run-session -- go test -run TestIntegrationRealBus -v
```

It connects, requests a name, exports an object, calls it (and a
deliberately-failing method) from a second connection, introspects it, and
emits/receives a signal — all against the real daemon.

## License

BSD-3-Clause. Copyright (c) the go-freedesktop/dbus authors.

---

> **Note:** the `go-freedesktop` org landing page and MkDocs site are deferred
> to the Wave-2 documentation sweep; this repo ships the README and `.github`
> workflow for now.

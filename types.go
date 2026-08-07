// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

// Package dbus is a pure-Go, CGO-free implementation of the D-Bus wire
// protocol, authentication, transport, connection and object-export layers,
// built from scratch against the D-Bus specification with zero non-standard
// dependencies.
//
// The public surface deliberately mirrors the shape of the widely used
// github.com/godbus/dbus/v5 API (Conn, Object, Variant, ObjectPath, Signature,
// Error, Export, RequestName, Emit, MakeVariant) so existing consumers can
// migrate off cgo/third-party code with near-mechanical edits.
package dbus

import (
	"encoding/binary"
	"fmt"
)

// ByteOrder selects the wire byte order. D-Bus messages are either
// little-endian ('l') or big-endian ('B'); both are supported and negotiated
// per message via the header's first byte.
type ByteOrder = binary.ByteOrder

// D-Bus type codes (single-character signature tokens). See the "Summary of
// types" table in the specification.
const (
	typeByte        = 'y'
	typeBool        = 'b'
	typeInt16       = 'n'
	typeUint16      = 'q'
	typeInt32       = 'i'
	typeUint32      = 'u'
	typeInt64       = 'x'
	typeUint64      = 't'
	typeDouble      = 'd'
	typeUnixFD      = 'h'
	typeString      = 's'
	typeObjectPath  = 'o'
	typeSignature   = 'g'
	typeArray       = 'a'
	typeVariant     = 'v'
	typeStructBegin = '('
	typeStructEnd   = ')'
	typeDictBegin   = '{'
	typeDictEnd     = '}'
)

// ObjectPath is a D-Bus object path such as "/org/freedesktop/DBus". It marshals
// with the 'o' type code.
type ObjectPath string

// IsValid reports whether p is a syntactically valid object path: a non-empty
// sequence of "/"-separated elements of [A-Za-z0-9_], with a single leading
// slash, no trailing slash (except the root "/") and no empty elements.
func (p ObjectPath) IsValid() bool {
	s := string(p)
	if len(s) == 0 || s[0] != '/' {
		return false
	}
	if s == "/" {
		return true
	}
	if s[len(s)-1] == '/' {
		return false
	}
	// Elements between slashes must be non-empty and use only [A-Za-z0-9_].
	elemStart := true
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '/' {
			if elemStart { // empty element ("//")
				return false
			}
			elemStart = true
			continue
		}
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
		elemStart = false
	}
	return true
}

// UnixFD is the Go representation of the 'h' (UNIX file descriptor) type. This
// implementation encodes and decodes the wire index; out-of-band descriptor
// passing over the socket is not performed.
type UnixFD int32

// UnixFDIndex is the on-wire index form of a UNIX file descriptor.
type UnixFDIndex uint32

// Variant is a D-Bus variant: a value carrying its own single complete type
// signature. It marshals with the 'v' type code.
type Variant struct {
	sig   Signature
	value interface{}
}

// MakeVariant wraps value in a Variant, inferring its signature from the Go
// type of value.
func MakeVariant(value interface{}) Variant {
	return MakeVariantWithSignature(value, SignatureOf(value))
}

// MakeVariantWithSignature wraps value in a Variant with the explicit
// signature sig (which must be a single complete type).
func MakeVariantWithSignature(value interface{}, sig Signature) Variant {
	return Variant{sig: sig, value: value}
}

// Value returns the Go value carried by the variant.
func (v Variant) Value() interface{} { return v.value }

// Signature returns the variant's value signature.
func (v Variant) Signature() Signature { return v.sig }

// String renders the variant as an annotated literal, e.g. `@i 42` style is
// avoided in favour of a compact "<sig> value" form useful in diagnostics.
func (v Variant) String() string {
	return fmt.Sprintf("%s %v", v.sig.str, v.value)
}

// Error is a D-Bus error reply. It carries the error name (a valid interface
// name such as "org.freedesktop.DBus.Error.Failed") and an optional body,
// whose first element is conventionally a human-readable message string.
type Error struct {
	Name string
	Body []interface{}
}

// Error implements the error interface.
func (e Error) Error() string {
	if len(e.Body) >= 1 {
		if s, ok := e.Body[0].(string); ok {
			return s
		}
	}
	return e.Name
}

// NewError builds an *Error with name and an optional body.
func NewError(name string, body []interface{}) *Error {
	return &Error{Name: name, Body: body}
}

// MakeFailedError wraps an arbitrary Go error as a generic
// org.freedesktop.DBus.Error.Failed reply.
func MakeFailedError(err error) *Error {
	return &Error{
		Name: "org.freedesktop.DBus.Error.Failed",
		Body: []interface{}{err.Error()},
	}
}

// Marshaler is implemented by types that encode themselves to the D-Bus wire
// format. MarshalDBus must append to and return dst, honouring the alignment
// implied by having already written pos bytes since the message start, and
// report the single complete signature it produced.
type Marshaler interface {
	MarshalDBus(dst []byte, order ByteOrder, pos int) ([]byte, Signature, error)
}

// Unmarshaler is implemented by types that decode themselves from the D-Bus
// wire format, returning the number of bytes consumed from src.
type Unmarshaler interface {
	UnmarshalDBus(src []byte, order ByteOrder, pos int) (int, error)
}

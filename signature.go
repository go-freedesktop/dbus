// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"fmt"
	"reflect"
	"strings"
)

// maxSignatureLen is the spec limit on a signature's textual length.
const maxSignatureLen = 255

// maxTypeDepth is the spec limit on nesting of container types (arrays and
// structs each count toward this bound).
const maxTypeDepth = 32

// Signature is a validated D-Bus type signature: a possibly-empty concatenation
// of complete single types (e.g. "a{sv}", "(ii)", "s"). It marshals with the
// 'g' type code.
type Signature struct {
	str string
}

// ParseSignature validates s and returns it as a Signature. It reports an error
// for over-length input, unknown type codes, or malformed containers.
func ParseSignature(s string) (Signature, error) {
	if len(s) > maxSignatureLen {
		return Signature{}, fmt.Errorf("dbus: signature exceeds %d bytes", maxSignatureLen)
	}
	rest := s
	for len(rest) > 0 {
		n, err := validateSingle(rest, 0)
		if err != nil {
			return Signature{}, err
		}
		rest = rest[n:]
	}
	return Signature{str: s}, nil
}

// String returns the textual signature.
func (s Signature) String() string { return s.str }

// Empty reports whether the signature carries no types.
func (s Signature) Empty() bool { return s.str == "" }

// single reports whether the signature is exactly one complete type.
func (s Signature) single() bool {
	if s.str == "" {
		return false
	}
	n, err := validateSingle(s.str, 0)
	return err == nil && n == len(s.str)
}

// validateSingle validates the single complete type that begins s and returns
// the number of bytes it spans. depth guards against runaway nesting.
func validateSingle(s string, depth int) (int, error) {
	if depth > maxTypeDepth {
		return 0, fmt.Errorf("dbus: type nested deeper than %d", maxTypeDepth)
	}
	if len(s) == 0 {
		return 0, fmt.Errorf("dbus: empty type")
	}
	switch s[0] {
	case typeByte, typeBool, typeInt16, typeUint16, typeInt32, typeUint32,
		typeInt64, typeUint64, typeDouble, typeUnixFD, typeString,
		typeObjectPath, typeSignature, typeVariant:
		return 1, nil
	case typeArray:
		elem, err := validateSingle(s[1:], depth+1)
		if err != nil {
			return 0, err
		}
		return 1 + elem, nil
	case typeStructBegin:
		return validateStruct(s, depth)
	case typeDictBegin:
		return validateDict(s, depth)
	default:
		return 0, fmt.Errorf("dbus: unknown type code %q", s[0])
	}
}

// validateStruct validates a "(...)" struct type beginning at s.
func validateStruct(s string, depth int) (int, error) {
	i := 1 // past '('
	count := 0
	for {
		if i >= len(s) {
			return 0, fmt.Errorf("dbus: unterminated struct")
		}
		if s[i] == typeStructEnd {
			if count == 0 {
				return 0, fmt.Errorf("dbus: empty struct")
			}
			return i + 1, nil
		}
		n, err := validateSingle(s[i:], depth+1)
		if err != nil {
			return 0, err
		}
		i += n
		count++
	}
}

// validateDict validates a "{kv}" dict-entry type. A dict entry is only legal as
// the element type of an array, but validateSingle admits it directly and the
// container rules (single basic key, single value) are enforced here.
func validateDict(s string, depth int) (int, error) {
	// s[0] == '{'
	if len(s) < 2 {
		return 0, fmt.Errorf("dbus: unterminated dict entry")
	}
	if !isBasicType(s[1]) {
		return 0, fmt.Errorf("dbus: dict key %q is not a basic type", s[1])
	}
	// value type
	val, err := validateSingle(s[2:], depth+1)
	if err != nil {
		return 0, err
	}
	i := 2 + val
	if i >= len(s) || s[i] != typeDictEnd {
		return 0, fmt.Errorf("dbus: dict entry not closed with '}'")
	}
	return i + 1, nil
}

// isBasicType reports whether c is a fixed or string-like basic type code (a
// legal dict key).
func isBasicType(c byte) bool {
	switch c {
	case typeByte, typeBool, typeInt16, typeUint16, typeInt32, typeUint32,
		typeInt64, typeUint64, typeDouble, typeUnixFD, typeString,
		typeObjectPath, typeSignature:
		return true
	}
	return false
}

// SignatureOf returns the concatenated signature of the Go values vs, inferring
// each type. It panics if any value has no D-Bus representation; callers that
// cannot guarantee representable inputs should use SignatureOfType.
func SignatureOf(vs ...interface{}) Signature {
	var b strings.Builder
	for _, v := range vs {
		s, err := signatureOfType(reflect.TypeOf(v))
		if err != nil {
			panic(err)
		}
		b.WriteString(s)
	}
	return Signature{str: b.String()}
}

// SignatureOfType returns the signature for a single Go type, or an error if the
// type cannot be represented on the wire.
func SignatureOfType(t reflect.Type) (Signature, error) {
	s, err := signatureOfType(t)
	if err != nil {
		return Signature{}, err
	}
	return Signature{str: s}, nil
}

var (
	objectPathType = reflect.TypeOf(ObjectPath(""))
	signatureType  = reflect.TypeOf(Signature{})
	variantType    = reflect.TypeOf(Variant{})
	unixFDType     = reflect.TypeOf(UnixFD(0))
	unixFDIdxType  = reflect.TypeOf(UnixFDIndex(0))
	byteSliceType  = reflect.TypeOf([]byte(nil))
)

// signatureOfType computes the wire signature string for t.
func signatureOfType(t reflect.Type) (string, error) {
	if t == nil {
		return "", fmt.Errorf("dbus: cannot take signature of nil")
	}
	switch t {
	case objectPathType:
		return "o", nil
	case signatureType:
		return "g", nil
	case variantType:
		return "v", nil
	case unixFDType, unixFDIdxType:
		return "h", nil
	}
	switch t.Kind() {
	case reflect.Uint8:
		return "y", nil
	case reflect.Bool:
		return "b", nil
	case reflect.Int16:
		return "n", nil
	case reflect.Uint16:
		return "q", nil
	case reflect.Int32:
		return "i", nil
	case reflect.Uint32:
		return "u", nil
	case reflect.Int64:
		return "x", nil
	case reflect.Uint64:
		return "t", nil
	case reflect.Float64:
		return "d", nil
	case reflect.String:
		return "s", nil
	case reflect.Slice, reflect.Array:
		elem, err := signatureOfType(t.Elem())
		if err != nil {
			return "", err
		}
		return "a" + elem, nil
	case reflect.Map:
		key, err := signatureOfType(t.Key())
		if err != nil {
			return "", err
		}
		if len(key) != 1 || !isBasicType(key[0]) {
			return "", fmt.Errorf("dbus: map key type %s is not a basic type", t.Key())
		}
		val, err := signatureOfType(t.Elem())
		if err != nil {
			return "", err
		}
		return "a{" + key + val + "}", nil
	case reflect.Struct:
		var b strings.Builder
		b.WriteByte('(')
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" { // unexported field
				continue
			}
			fs, err := signatureOfType(f.Type)
			if err != nil {
				return "", err
			}
			b.WriteString(fs)
		}
		b.WriteByte(')')
		return b.String(), nil
	case reflect.Interface:
		// A bare interface (e.g. an element of []interface{}) has no static
		// signature; it must be wrapped in a Variant to be representable.
		return "", fmt.Errorf("dbus: cannot take signature of bare interface %s; wrap in a Variant", t)
	default:
		return "", fmt.Errorf("dbus: type %s has no D-Bus representation", t)
	}
}

// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"fmt"
	"math"
	"reflect"
)

// Marshal encodes vs into the D-Bus wire format using byte order, returning the
// encoded bytes. Alignment is computed as if the encoding begins at an
// 8-aligned position (as a message body does), so the result may be embedded
// directly after an 8-padded header.
func Marshal(order ByteOrder, vs ...interface{}) ([]byte, error) {
	e := &encoder{order: order}
	for _, v := range vs {
		if err := e.encode(reflect.ValueOf(v)); err != nil {
			return nil, err
		}
	}
	return e.buf, nil
}

// encoder accumulates wire bytes. Alignment padding is measured from the start
// of buf, which the caller guarantees corresponds to an 8-aligned message
// offset.
type encoder struct {
	order ByteOrder
	buf   []byte
}

// align pads buf with zero bytes until its length is a multiple of n.
func (e *encoder) align(n int) {
	for len(e.buf)%n != 0 {
		e.buf = append(e.buf, 0)
	}
}

func (e *encoder) putByte(x byte) { e.buf = append(e.buf, x) }

func (e *encoder) putUint16(x uint16) {
	e.align(2)
	var b [2]byte
	e.order.PutUint16(b[:], x)
	e.buf = append(e.buf, b[:]...)
}

func (e *encoder) putUint32(x uint32) {
	e.align(4)
	var b [4]byte
	e.order.PutUint32(b[:], x)
	e.buf = append(e.buf, b[:]...)
}

func (e *encoder) putUint64(x uint64) {
	e.align(8)
	var b [8]byte
	e.order.PutUint64(b[:], x)
	e.buf = append(e.buf, b[:]...)
}

// putString writes a length-prefixed, NUL-terminated string. lenSize is 4 for
// 's'/'o' (uint32 length) and 1 for 'g' (single-byte length).
func (e *encoder) putString(lenSize int, s string) {
	if lenSize == 1 {
		e.putByte(byte(len(s)))
	} else {
		e.putUint32(uint32(len(s)))
	}
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, 0) // trailing NUL, not counted in the length
}

// encode writes a single Go value.
func (e *encoder) encode(v reflect.Value) error {
	// Unwrap interfaces and pointers to their concrete value.
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return fmt.Errorf("dbus: cannot marshal nil %s", v.Kind())
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return fmt.Errorf("dbus: cannot marshal invalid value")
	}
	t := v.Type()

	// Library types that need bespoke encodings.
	switch t {
	case signatureType:
		e.putString(1, v.Interface().(Signature).str)
		return nil
	case variantType:
		return e.encodeVariant(v.Interface().(Variant))
	case objectPathType:
		e.putString(4, v.String())
		return nil
	case unixFDType:
		e.putUint32(uint32(v.Int()))
		return nil
	case unixFDIdxType:
		e.putUint32(uint32(v.Uint()))
		return nil
	}

	// User-supplied Marshaler.
	if m, ok := v.Interface().(Marshaler); ok {
		out, _, err := m.MarshalDBus(e.buf, e.order, len(e.buf))
		if err != nil {
			return err
		}
		e.buf = out
		return nil
	}

	switch t.Kind() {
	case reflect.Uint8:
		e.putByte(byte(v.Uint()))
	case reflect.Bool:
		var x uint32
		if v.Bool() {
			x = 1
		}
		e.putUint32(x)
	case reflect.Int16:
		e.putUint16(uint16(v.Int()))
	case reflect.Uint16:
		e.putUint16(uint16(v.Uint()))
	case reflect.Int32:
		e.putUint32(uint32(v.Int()))
	case reflect.Uint32:
		e.putUint32(uint32(v.Uint()))
	case reflect.Int64:
		e.putUint64(uint64(v.Int()))
	case reflect.Uint64:
		e.putUint64(v.Uint())
	case reflect.Float64:
		e.putUint64(math.Float64bits(v.Float()))
	case reflect.String:
		e.putString(4, v.String())
	case reflect.Slice, reflect.Array:
		return e.encodeArray(v)
	case reflect.Map:
		return e.encodeMap(v)
	case reflect.Struct:
		return e.encodeStruct(v)
	default:
		return fmt.Errorf("dbus: type %s has no D-Bus representation", t)
	}
	return nil
}

// encodeVariant writes the variant's signature then its value aligned to the
// value's alignment.
func (e *encoder) encodeVariant(v Variant) error {
	sig := v.sig
	if !sig.single() {
		return fmt.Errorf("dbus: variant signature %q is not a single complete type", sig.str)
	}
	e.putString(1, sig.str)
	return e.encode(reflect.ValueOf(v.value))
}

// encodeArray writes a length-prefixed array. The 4-byte length counts only the
// element bytes (including inter-element padding), never the padding inserted
// between the length and the first element.
func (e *encoder) encodeArray(v reflect.Value) error {
	et := v.Type().Elem()
	var elemAlign int
	var err error
	if et.Kind() == reflect.Interface {
		// Element type is statically unknown; a homogeneous array needs a
		// concrete element to derive its alignment from.
		if v.Len() == 0 {
			return fmt.Errorf("dbus: cannot marshal empty []interface{}: element type unknown")
		}
		ev := reflect.ValueOf(v.Index(0).Interface())
		if !ev.IsValid() {
			return fmt.Errorf("dbus: cannot marshal array with a nil element")
		}
		elemAlign, err = alignmentOfType(ev.Type())
	} else {
		elemAlign, err = alignmentOfType(et)
	}
	if err != nil {
		return err
	}
	e.align(4)
	lenIdx := len(e.buf)
	e.buf = append(e.buf, 0, 0, 0, 0)
	e.align(elemAlign)
	start := len(e.buf)
	for i := 0; i < v.Len(); i++ {
		if err := e.encode(v.Index(i)); err != nil {
			return err
		}
	}
	e.order.PutUint32(e.buf[lenIdx:lenIdx+4], uint32(len(e.buf)-start))
	return nil
}

// encodeMap writes a map as an array of dict entries (element alignment 8).
func (e *encoder) encodeMap(v reflect.Value) error {
	e.align(4)
	lenIdx := len(e.buf)
	e.buf = append(e.buf, 0, 0, 0, 0)
	e.align(8) // dict-entry alignment
	start := len(e.buf)
	iter := v.MapRange()
	for iter.Next() {
		e.align(8) // each dict entry is 8-aligned
		if err := e.encode(iter.Key()); err != nil {
			return err
		}
		if err := e.encode(iter.Value()); err != nil {
			return err
		}
	}
	e.order.PutUint32(e.buf[lenIdx:lenIdx+4], uint32(len(e.buf)-start))
	return nil
}

// encodeStruct writes a struct, 8-aligned, encoding exported fields in order.
func (e *encoder) encodeStruct(v reflect.Value) error {
	e.align(8)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).PkgPath != "" { // skip unexported
			continue
		}
		if err := e.encode(v.Field(i)); err != nil {
			return err
		}
	}
	return nil
}

// alignmentOfType returns the wire alignment (1, 2, 4 or 8) of the D-Bus type
// corresponding to Go type t.
func alignmentOfType(t reflect.Type) (int, error) {
	switch t {
	case signatureType, variantType:
		return 1, nil
	case objectPathType, unixFDType, unixFDIdxType:
		return 4, nil
	}
	switch t.Kind() {
	case reflect.Uint8:
		return 1, nil
	case reflect.Int16, reflect.Uint16:
		return 2, nil
	case reflect.Bool, reflect.Int32, reflect.Uint32, reflect.String,
		reflect.Slice, reflect.Array, reflect.Map:
		return 4, nil
	case reflect.Int64, reflect.Uint64, reflect.Float64, reflect.Struct:
		return 8, nil
	case reflect.Pointer:
		return alignmentOfType(t.Elem())
	default:
		return 0, fmt.Errorf("dbus: type %s has no D-Bus alignment", t)
	}
}

// alignmentOfSig returns the wire alignment of the first type in signature sig.
func alignmentOfSig(sig string) int {
	switch sig[0] {
	case typeByte, typeSignature, typeVariant:
		return 1
	case typeInt16, typeUint16:
		return 2
	case typeInt64, typeUint64, typeDouble, typeStructBegin, typeDictBegin:
		return 8
	default:
		// b i u s o g(handled above) h a
		return 4
	}
}

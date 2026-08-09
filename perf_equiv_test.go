// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

// TestEncodeHeaderFieldsMatchesReflect proves that the reflection-free
// encodeHeaderFields writes byte-identical output to routing the same
// []headerFieldStruct through the generic reflect-based encoder, for both byte
// orders. This guards the header-encoding optimization against any wire drift.
func TestEncodeHeaderFieldsMatchesReflect(t *testing.T) {
	fields := []headerFieldStruct{
		{Code: byte(FieldPath), Value: MakeVariant(ObjectPath("/org/freedesktop/Notifications"))},
		{Code: byte(FieldInterface), Value: MakeVariant("org.freedesktop.Notifications")},
		{Code: byte(FieldMember), Value: MakeVariant("Notify")},
		{Code: byte(FieldDestination), Value: MakeVariant("org.freedesktop.Notifications")},
		{Code: byte(FieldReplySerial), Value: MakeVariant(uint32(7))},
		{Code: byte(FieldSignature), Value: MakeVariant(ParseSignatureMust("susssasa{sv}i"))},
		{Code: byte(FieldUnixFDs), Value: MakeVariant(uint32(0))},
	}
	for _, order := range []ByteOrder{binary.LittleEndian, binary.BigEndian} {
		// Optimized path.
		opt := &encoder{order: order}
		if err := opt.encodeHeaderFields(fields); err != nil {
			t.Fatalf("encodeHeaderFields: %v", err)
		}
		// Generic reflect path (what the code did before the optimization).
		ref := &encoder{order: order}
		if err := ref.encode(reflect.ValueOf(fields)); err != nil {
			t.Fatalf("reflect encode: %v", err)
		}
		if !bytes.Equal(opt.buf, ref.buf) {
			t.Fatalf("order %T: header encoding differs\n opt=%v\n ref=%v", order, opt.buf, ref.buf)
		}
	}
}

// TestEncodeHeaderFieldsEmpty covers the empty-array framing (length 0).
func TestEncodeHeaderFieldsEmpty(t *testing.T) {
	for _, order := range []ByteOrder{binary.LittleEndian, binary.BigEndian} {
		opt := &encoder{order: order}
		if err := opt.encodeHeaderFields(nil); err != nil {
			t.Fatalf("encodeHeaderFields(nil): %v", err)
		}
		ref := &encoder{order: order}
		if err := ref.encode(reflect.ValueOf([]headerFieldStruct{})); err != nil {
			t.Fatalf("reflect encode empty: %v", err)
		}
		if !bytes.Equal(opt.buf, ref.buf) {
			t.Fatalf("order %T: empty header encoding differs\n opt=%v\n ref=%v", order, opt.buf, ref.buf)
		}
	}
}

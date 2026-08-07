// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"encoding/binary"
	"testing"
)

func TestBodySignatureFallbacks(t *testing.T) {
	// No SIGNATURE header present -> empty signature.
	m := &Message{Headers: map[HeaderField]Variant{}}
	if !m.bodySignature().Empty() {
		t.Error("expected empty signature when no SIGNATURE header")
	}
	// SIGNATURE header present but carrying a non-Signature value -> empty.
	m.Headers[FieldSignature] = MakeVariant("not a signature")
	if !m.bodySignature().Empty() {
		t.Error("expected empty signature when SIGNATURE value is not a Signature")
	}
}

func TestMarshalNilHeadersWithBody(t *testing.T) {
	// A message with nil Headers but a body must lazily allocate the map (to
	// install the SIGNATURE header) before failing required-field validation.
	m := &Message{Type: TypeSignal, Body: []interface{}{"x"}}
	if _, err := m.Marshal(binary.LittleEndian); err == nil {
		t.Error("expected required-field validation error")
	}
	if m.Headers == nil {
		t.Error("nil Headers map was not lazily allocated")
	}
}

func TestMarshalHeaderFieldEncodeError(t *testing.T) {
	// A valid method_call plus one extra header field whose variant carries an
	// unencodable value forces the header-fields encode to fail.
	m := &Message{
		Type: TypeMethodCall,
		Headers: map[HeaderField]Variant{
			FieldPath:   MakeVariant(ObjectPath("/p")),
			FieldMember: MakeVariant("M"),
			FieldSender: MakeVariantWithSignature(make(chan int), mustSig(t, "s")),
		},
	}
	if _, err := m.Marshal(binary.LittleEndian); err == nil {
		t.Error("expected header-field encode error")
	}
}

func TestDecodeMessageBadPadding(t *testing.T) {
	// A bodyless method_call with PATH="/" and MEMBER="M" yields a header-fields
	// array whose length is not a multiple of 8, so DecodeMessage must skip
	// padding to reach the 8-aligned body offset. Corrupting the first pad byte
	// exercises the non-zero-padding rejection in that align(8) step.
	m := &Message{
		Type: TypeMethodCall,
		Headers: map[HeaderField]Variant{
			FieldPath:   MakeVariant(ObjectPath("/")),
			FieldMember: MakeVariant("M"),
		},
	}
	data, err := m.Marshal(binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	fieldsLen := int(binary.LittleEndian.Uint32(data[12:16]))
	pad := (8 - (fieldsLen % 8)) % 8
	if pad == 0 {
		t.Fatalf("test fixture has no inter-body padding (fieldsLen=%d)", fieldsLen)
	}
	data[16+fieldsLen] = 0xFF // corrupt the first pad byte after the fields array
	if _, _, err := DecodeMessage(data); err == nil {
		t.Error("expected non-zero-padding align error")
	}
}

func TestDecodeMessageBadBody(t *testing.T) {
	// Corrupt the NUL terminator of the first body string so the body Unmarshal
	// fails after the header decodes cleanly.
	m := sampleCall() // body: "arg1"(s), int32
	data, err := m.Marshal(binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	bodyLen := int(binary.LittleEndian.Uint32(data[4:8]))
	bodyStart := len(data) - bodyLen
	// body: [len u32][a r g 1][NUL]... -> NUL is at bodyStart+4+4
	data[bodyStart+8] = 0xFF
	if _, _, err := DecodeMessage(data); err == nil {
		t.Error("expected body Unmarshal error")
	}
}

// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

// okMarshaler is a value-receiver Marshaler that appends a single fixed byte.
type okMarshaler struct{ b byte }

func (m okMarshaler) MarshalDBus(dst []byte, order ByteOrder, pos int) ([]byte, Signature, error) {
	return append(dst, m.b), Signature{str: "y"}, nil
}

// errMarshaler is a value-receiver Marshaler that always fails.
type errMarshaler struct{}

func (errMarshaler) MarshalDBus(dst []byte, order ByteOrder, pos int) ([]byte, Signature, error) {
	return nil, Signature{}, errors.New("marshal boom")
}

func TestMarshalCustomMarshaler(t *testing.T) {
	data, err := Marshal(binary.LittleEndian, okMarshaler{0x7E})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 || data[0] != 0x7E {
		t.Errorf("custom marshaler output = %#v", data)
	}
}

func TestMarshalCustomMarshalerError(t *testing.T) {
	if _, err := Marshal(binary.LittleEndian, errMarshaler{}); err == nil {
		t.Error("expected error from failing Marshaler")
	}
}

func TestMarshalUnixFDIndex(t *testing.T) {
	data, err := Marshal(binary.LittleEndian, UnixFDIndex(5))
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := Unmarshal(data, binary.LittleEndian, mustSig(t, "h"))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].(UnixFD) != 5 {
		t.Errorf("UnixFDIndex round-trip got %#v", got[0])
	}
}

func TestMarshalArrayElementError(t *testing.T) {
	// First element fixes the element alignment; the second fails to encode.
	if _, err := Marshal(binary.LittleEndian, []interface{}{int32(1), make(chan int)}); err == nil {
		t.Error("expected array element encode error")
	}
}

func TestMarshalMapKeyError(t *testing.T) {
	// An interface-keyed map whose key is an unencodable concrete type.
	m := map[interface{}]int32{make(chan int): 1}
	if _, err := Marshal(binary.LittleEndian, m); err == nil {
		t.Error("expected map key encode error")
	}
}

func TestMarshalMapValueError(t *testing.T) {
	m := map[string]interface{}{"k": make(chan int)}
	if _, err := Marshal(binary.LittleEndian, m); err == nil {
		t.Error("expected map value encode error")
	}
}

func TestMarshalStructUnexportedSkipped(t *testing.T) {
	type mixed struct {
		A int32
		b int32 // unexported, skipped by the encoder
	}
	m := mixed{A: 3}
	m.b = 9
	data, err := Marshal(binary.LittleEndian, m)
	if err != nil {
		t.Fatal(err)
	}
	// Only the exported int32 (4 bytes) is encoded; the struct is 8-aligned but
	// starts at 0 so there is no leading pad.
	if len(data) != 4 {
		t.Errorf("unexported field not skipped: len=%d", len(data))
	}
}

func TestMarshalStructFieldError(t *testing.T) {
	type bad struct{ C chan int }
	if _, err := Marshal(binary.LittleEndian, bad{}); err == nil {
		t.Error("expected struct field encode error")
	}
}

// Array-element alignment coverage for the special element types.
func TestMarshalArrayElementAlignments(t *testing.T) {
	x, y := int32(1), int32(2)
	cases := []struct {
		sig string
		val interface{}
	}{
		{"av", []Variant{MakeVariant(int32(1)), MakeVariant("s")}},
		{"ag", []Signature{mustSig(t, "ii"), mustSig(t, "s")}},
		{"ao", []ObjectPath{"/a", "/b"}},
		{"an", []int16{1, 2, 3}},
		{"ai", []*int32{&x, &y}},
	}
	for _, c := range cases {
		data, err := Marshal(binary.LittleEndian, c.val)
		if err != nil {
			t.Errorf("Marshal %s: %v", c.sig, err)
			continue
		}
		if _, _, err := Unmarshal(data, binary.LittleEndian, mustSig(t, c.sig)); err != nil {
			t.Errorf("Unmarshal %s: %v", c.sig, err)
		}
	}
}

// Round-trip an int16 array to exercise alignmentOfSig's 2-byte branch on the
// decode side.
func TestRoundTripInt16Array(t *testing.T) {
	data, err := Marshal(binary.LittleEndian, []int16{-1, 2, -3})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := Unmarshal(data, binary.LittleEndian, mustSig(t, "an"))
	if err != nil {
		t.Fatal(err)
	}
	want := []interface{}{int16(-1), int16(2), int16(-3)}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("int16 array got %#v", got[0])
	}
}

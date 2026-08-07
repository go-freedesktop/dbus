// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"encoding/binary"
	"reflect"
	"testing"
)

var orders = []struct {
	name  string
	order ByteOrder
}{
	{"little", binary.LittleEndian},
	{"big", binary.BigEndian},
}

// roundTrip marshals val, unmarshals it back with sig, and checks the decoded
// value equals want, under both byte orders.
func roundTrip(t *testing.T, sig string, val, want interface{}) {
	t.Helper()
	s, err := ParseSignature(sig)
	if err != nil {
		t.Fatalf("bad test signature %q: %v", sig, err)
	}
	for _, o := range orders {
		data, err := Marshal(o.order, val)
		if err != nil {
			t.Fatalf("%s: Marshal(%v): %v", o.name, val, err)
		}
		got, n, err := Unmarshal(data, o.order, s)
		if err != nil {
			t.Fatalf("%s: Unmarshal: %v", o.name, err)
		}
		if n != len(data) {
			t.Errorf("%s: consumed %d of %d bytes", o.name, n, len(data))
		}
		if len(got) != 1 {
			t.Fatalf("%s: got %d values", o.name, len(got))
		}
		if !reflect.DeepEqual(got[0], want) {
			t.Errorf("%s: sig %q round-trip = %#v (%T), want %#v (%T)",
				o.name, sig, got[0], got[0], want, want)
		}
	}
}

func TestRoundTripBasics(t *testing.T) {
	roundTrip(t, "y", byte(0xAB), byte(0xAB))
	roundTrip(t, "b", true, true)
	roundTrip(t, "b", false, false)
	roundTrip(t, "n", int16(-12345), int16(-12345))
	roundTrip(t, "q", uint16(54321), uint16(54321))
	roundTrip(t, "i", int32(-2000000000), int32(-2000000000))
	roundTrip(t, "u", uint32(4000000000), uint32(4000000000))
	roundTrip(t, "x", int64(-9000000000000000000), int64(-9000000000000000000))
	roundTrip(t, "t", uint64(18000000000000000000), uint64(18000000000000000000))
	roundTrip(t, "d", float64(3.141592653589793), float64(3.141592653589793))
	roundTrip(t, "s", "hello, D-Bus", "hello, D-Bus")
	roundTrip(t, "s", "", "")
	roundTrip(t, "o", ObjectPath("/org/example/Obj"), ObjectPath("/org/example/Obj"))
	roundTrip(t, "h", UnixFD(7), UnixFD(7))
}

func TestRoundTripSignatureValue(t *testing.T) {
	sig, _ := ParseSignature("a{sv}")
	roundTrip(t, "g", sig, sig)
	empty, _ := ParseSignature("")
	roundTrip(t, "g", empty, empty)
}

func TestRoundTripArrays(t *testing.T) {
	roundTrip(t, "ai", []int32{1, 2, 3}, []interface{}{int32(1), int32(2), int32(3)})
	roundTrip(t, "as", []string{"a", "bb", "ccc"},
		[]interface{}{"a", "bb", "ccc"})
	roundTrip(t, "ay", []byte{1, 2, 3, 4}, []byte{1, 2, 3, 4})
	roundTrip(t, "ai", []int32{}, []interface{}{})
	// array of arrays (nested alignment)
	roundTrip(t, "aai", [][]int32{{1}, {2, 3}},
		[]interface{}{[]interface{}{int32(1)}, []interface{}{int32(2), int32(3)}})
	// array of int64 to exercise 8-alignment padding after the length word
	roundTrip(t, "ax", []int64{1, 2}, []interface{}{int64(1), int64(2)})
}

func TestRoundTripStruct(t *testing.T) {
	type inner struct {
		X int32
		Y string
	}
	roundTrip(t, "(is)", inner{42, "hi"}, []interface{}{int32(42), "hi"})
	// The freedesktop image-data shape (iiibiiay).
	type imgData struct {
		W, H, Stride int32
		HasAlpha     bool
		Bits, Ch     int32
		Pix          []byte
	}
	roundTrip(t, "(iiibiiay)",
		imgData{2, 1, 8, true, 8, 4, []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		[]interface{}{int32(2), int32(1), int32(8), true, int32(8), int32(4),
			[]byte{1, 2, 3, 4, 5, 6, 7, 8}})
}

func TestRoundTripVariant(t *testing.T) {
	v := MakeVariant(int32(99))
	roundTrip(t, "v", v, v)
	vs := MakeVariant("nested")
	roundTrip(t, "v", vs, vs)
	// variant carrying an explicit-signature struct decodes to []interface{}
	structVar := MakeVariantWithSignature([]int32{1, 2}, mustSig(t, "ai"))
	roundTrip(t, "v", structVar,
		MakeVariantWithSignature([]interface{}{int32(1), int32(2)}, mustSig(t, "ai")))
}

func TestRoundTripDict(t *testing.T) {
	m := map[string]int32{"one": 1}
	roundTrip(t, "a{si}", m, map[interface{}]interface{}{"one": int32(1)})
	// a{sv} hints map, single entry for determinism
	hints := map[string]Variant{"urgency": MakeVariant(byte(2))}
	roundTrip(t, "a{sv}", hints,
		map[interface{}]interface{}{"urgency": MakeVariant(byte(2))})
}

func TestMarshalErrors(t *testing.T) {
	// nil (invalid) top-level value
	if _, err := Marshal(binary.LittleEndian, nil); err == nil {
		t.Error("expected error marshalling nil")
	}
	// nil pointer
	var p *int32
	if _, err := Marshal(binary.LittleEndian, p); err == nil {
		t.Error("expected error marshalling nil pointer")
	}
	// unrepresentable type
	if _, err := Marshal(binary.LittleEndian, make(chan int)); err == nil {
		t.Error("expected error marshalling chan")
	}
	// empty []interface{} array (unknown element type)
	if _, err := Marshal(binary.LittleEndian, []interface{}{}); err == nil {
		t.Error("expected error marshalling empty []interface{}")
	}
	// []interface{} whose first element is nil
	if _, err := Marshal(binary.LittleEndian, []interface{}{nil}); err == nil {
		t.Error("expected error marshalling []interface{}{nil}")
	}
	// variant with a non-single signature
	bad := MakeVariantWithSignature(int32(0), Signature{str: "ii"})
	if _, err := Marshal(binary.LittleEndian, bad); err == nil {
		t.Error("expected error marshalling variant with multi-type signature")
	}
	// array whose element type has no representation
	if _, err := Marshal(binary.LittleEndian, []chan int{make(chan int)}); err == nil {
		t.Error("expected error marshalling []chan")
	}
	// map with unrepresentable value type element
	if _, err := Marshal(binary.LittleEndian, map[string]chan int{"a": make(chan int)}); err == nil {
		t.Error("expected error marshalling map of chan")
	}
}

func TestMarshalPointerAndInterfaceUnwrap(t *testing.T) {
	x := int32(5)
	data, err := Marshal(binary.LittleEndian, &x)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := Unmarshal(data, binary.LittleEndian, mustSig(t, "i"))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].(int32) != 5 {
		t.Errorf("pointer unwrap: got %v", got[0])
	}
	// []interface{} with concrete element unwraps
	data, err = Marshal(binary.LittleEndian, []interface{}{int32(1), int32(2)})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err = Unmarshal(data, binary.LittleEndian, mustSig(t, "ai"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got[0], []interface{}{int32(1), int32(2)}) {
		t.Errorf("[]interface{} array: got %#v", got[0])
	}
}

func TestUnmarshalErrors(t *testing.T) {
	le := binary.LittleEndian
	// truncated fixed-width value
	if _, _, err := Unmarshal([]byte{0x01}, le, mustSig(t, "i")); err == nil {
		t.Error("expected truncation error for int32")
	}
	// truncated int16
	if _, _, err := Unmarshal([]byte{0x01}, le, mustSig(t, "n")); err == nil {
		t.Error("expected truncation error for int16")
	}
	// truncated uint64
	if _, _, err := Unmarshal([]byte{1, 2, 3, 4}, le, mustSig(t, "x")); err == nil {
		t.Error("expected truncation error for int64")
	}
	// truncated byte
	if _, _, err := Unmarshal([]byte{}, le, mustSig(t, "y")); err == nil {
		t.Error("expected truncation error for byte")
	}
	// invalid boolean (value 2)
	if _, _, err := Unmarshal([]byte{2, 0, 0, 0}, le, mustSig(t, "b")); err == nil {
		t.Error("expected invalid-boolean error")
	}
	// string length too large
	if _, _, err := Unmarshal([]byte{0xFF, 0xFF, 0xFF, 0xFF}, le, mustSig(t, "s")); err == nil {
		t.Error("expected truncation error for oversize string length")
	}
	// string not NUL-terminated
	if _, _, err := Unmarshal([]byte{3, 0, 0, 0, 'a', 'b', 'c', '!'}, le, mustSig(t, "s")); err == nil {
		t.Error("expected NUL-termination error")
	}
	// signature length byte then truncated
	if _, _, err := Unmarshal([]byte{}, le, mustSig(t, "g")); err == nil {
		t.Error("expected truncation for signature length")
	}
	// array length exceeds buffer
	if _, _, err := Unmarshal([]byte{0xFF, 0xFF, 0xFF, 0xFF}, le, mustSig(t, "ai")); err == nil {
		t.Error("expected array-length overflow error")
	}
}

func TestUnmarshalAlignmentPadding(t *testing.T) {
	le := binary.LittleEndian
	// "yx": byte then int64 -> 7 padding bytes must be zero. Provide a
	// non-zero padding byte to trigger the alignment error.
	bad := []byte{0x01, 0xFF, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0}
	if _, _, err := Unmarshal(bad, le, mustSig(t, "yx")); err == nil {
		t.Error("expected non-zero-padding alignment error")
	}
	// truncated while aligning
	if _, _, err := Unmarshal([]byte{0x01, 0x00}, le, mustSig(t, "yx")); err == nil {
		t.Error("expected truncation-during-align error")
	}
	// good padding round-trips
	good := []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0}
	got, _, err := Unmarshal(good, le, mustSig(t, "yx"))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].(byte) != 1 || got[1].(int64) != 9 {
		t.Errorf("got %#v", got)
	}
}

func TestUnmarshalVariantErrors(t *testing.T) {
	le := binary.LittleEndian
	// variant with a bad embedded signature
	if _, _, err := Unmarshal([]byte{1, 'z', 0}, le, mustSig(t, "v")); err == nil {
		t.Error("expected bad-signature error in variant")
	}
	// variant whose embedded signature is multi-type ("ii")
	if _, _, err := Unmarshal([]byte{2, 'i', 'i', 0}, le, mustSig(t, "v")); err == nil {
		t.Error("expected non-single-type variant error")
	}
	// variant value truncated
	if _, _, err := Unmarshal([]byte{1, 'i', 0}, le, mustSig(t, "v")); err == nil {
		t.Error("expected truncated variant value error")
	}
}

func TestUnmarshalDictErrors(t *testing.T) {
	le := binary.LittleEndian
	// a{si}: declare length 100 but no data
	if _, _, err := Unmarshal([]byte{100, 0, 0, 0}, le, mustSig(t, "a{si}")); err == nil {
		t.Error("expected dict array length error")
	}
}

func TestUnmarshalUnknownTypeCode(t *testing.T) {
	// decodeSingle is only reached through validated signatures, so drive it
	// directly with a bogus code to cover the default branch.
	d := &decoder{order: binary.LittleEndian, data: []byte{0}}
	if _, err := d.decodeSingle("z"); err == nil {
		t.Error("expected error for unknown type code in decodeSingle")
	}
}

func TestSplitTypeError(t *testing.T) {
	if _, _, err := splitType("z"); err == nil {
		t.Error("expected splitType error")
	}
	if _, _, err := Unmarshal(nil, binary.LittleEndian, Signature{str: "z"}); err == nil {
		t.Error("expected Unmarshal error on bad signature")
	}
}

func mustSig(t *testing.T, s string) Signature {
	t.Helper()
	sig, err := ParseSignature(s)
	if err != nil {
		t.Fatalf("bad signature %q: %v", s, err)
	}
	return sig
}

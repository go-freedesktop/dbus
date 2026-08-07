// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"encoding/binary"
	"testing"
)

var le = binary.LittleEndian

func TestReadUint16AlignError(t *testing.T) {
	// "yq": a byte then a uint16 needs one padding byte at offset 1. Truncating
	// there exercises the align(2) truncation branch inside readUint16.
	if _, _, err := Unmarshal([]byte{0x01}, le, mustSig(t, "yq")); err == nil {
		t.Error("expected align-truncation error for uint16")
	}
	// Non-zero padding byte before the uint16.
	if _, _, err := Unmarshal([]byte{0x01, 0xFF, 0, 0}, le, mustSig(t, "yq")); err == nil {
		t.Error("expected non-zero-padding error for uint16")
	}
}

func TestDecodeObjectPathTruncated(t *testing.T) {
	// Object path with a length prefix but a truncated body.
	if _, _, err := Unmarshal([]byte{5, 0, 0, 0, '/'}, le, mustSig(t, "o")); err == nil {
		t.Error("expected truncated object-path error")
	}
}

func TestDecodeSignatureBadParse(t *testing.T) {
	// A 'g' value whose bytes form an invalid signature string ("z").
	if _, _, err := Unmarshal([]byte{1, 'z', 0}, le, mustSig(t, "g")); err == nil {
		t.Error("expected bad-signature parse error for 'g'")
	}
}

func TestDecodeVariantTruncatedSignature(t *testing.T) {
	// Variant whose embedded signature length byte cannot be read.
	if _, _, err := Unmarshal([]byte{}, le, mustSig(t, "v")); err == nil {
		t.Error("expected truncated variant-signature error")
	}
}

func TestDecodeArrayAlignError(t *testing.T) {
	// "ax": array of int64. After the 4-byte length there is 4 bytes of padding
	// to reach 8-alignment before the first element; a non-zero pad byte is an
	// error.
	bad := []byte{8, 0, 0, 0, 0, 0, 0, 0xFF} // length 8, bad pad
	if _, _, err := Unmarshal(bad, le, mustSig(t, "ax")); err == nil {
		t.Error("expected array alignment-padding error")
	}
}

func TestDecodeArrayOverrun(t *testing.T) {
	// "as": declare an array byte-length that stops mid-string, so the string's
	// own NUL-terminated body overruns the declared array end.
	data := []byte{4, 0, 0, 0, 3, 0, 0, 0, 'a', 'b', 'c', 0}
	if _, _, err := Unmarshal(data, le, mustSig(t, "as")); err == nil {
		t.Error("expected array-overrun error")
	}
}

func TestDecodeArrayElementError(t *testing.T) {
	// "ab": array of bool with an invalid boolean (2) inside.
	data := []byte{4, 0, 0, 0, 2, 0, 0, 0}
	if _, _, err := Unmarshal(data, le, mustSig(t, "ab")); err == nil {
		t.Error("expected array element decode error")
	}
}

func TestDecodeDictAlignError(t *testing.T) {
	// "a{si}": declared length forces reading a dict entry but the 8-alignment
	// padding after the length word is truncated.
	if _, _, err := Unmarshal([]byte{1, 0, 0, 0, 0}, le, mustSig(t, "a{si}")); err == nil {
		t.Error("expected dict-entry alignment error")
	}
}

func TestDecodeDictKeyError(t *testing.T) {
	// a{si} with length large enough to enter the loop but a truncated key.
	// length=1, then pad to 8, then only 1 byte of a 4-byte string length.
	data := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0xFF}
	if _, _, err := Unmarshal(data, le, mustSig(t, "a{si}")); err == nil {
		t.Error("expected dict key decode error")
	}
}

func TestDecodeDictValueError(t *testing.T) {
	// a{sy}: valid key string then a value that is truncated.
	// dict length covers the key ("a\0") but not the trailing byte value.
	// entry: s="a" -> [1,0,0,0,'a',0], then y value missing.
	data := []byte{6, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 'a', 0}
	if _, _, err := Unmarshal(data, le, mustSig(t, "a{sy}")); err == nil {
		t.Error("expected dict value decode error")
	}
}

func TestDecodeDictOverrun(t *testing.T) {
	// a{sy}: declared length stops before the key string's NUL, so the entry
	// overruns the declared array end.
	// length = 5 (covers the 4-byte string length word + 1), key string len=1
	// with body 'a'+NUL extends past end.
	data := []byte{5, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 'a', 0, 7}
	if _, _, err := Unmarshal(data, le, mustSig(t, "a{sy}")); err == nil {
		t.Error("expected dict-overrun error")
	}
}

func TestDecodeBoolTruncated(t *testing.T) {
	// A boolean whose 4-byte word is truncated (readUint32 error inside the
	// bool case of decodeSingle).
	if _, _, err := Unmarshal([]byte{1, 0}, le, mustSig(t, "b")); err == nil {
		t.Error("expected truncated boolean error")
	}
}

func TestDecodeArrayLengthTruncated(t *testing.T) {
	// The array's 4-byte length word itself is truncated.
	if _, _, err := Unmarshal([]byte{1}, le, mustSig(t, "ai")); err == nil {
		t.Error("expected truncated array-length error")
	}
}

func TestDecodeDictEntryBadPadding(t *testing.T) {
	// a{si} with two entries where the second entry's 8-alignment padding byte
	// is non-zero, exercising the per-entry align(8) rejection in
	// decodeDictArray.
	data := []byte{
		16, 0, 0, 0, // array length = 16 (bytes from first element)
		0, 0, 0, 0, // pad to 8
		1, 0, 0, 0, // entry1 key string length = 1
		'a', 0, // "a" + NUL
		0, 0, // pad to align int32 to 4
		9, 0, 0, 0, // entry1 value int32 = 9
		0xFF, 0, 0, 0, // entry2 region: first pad byte non-zero
	}
	if _, _, err := Unmarshal(data, le, mustSig(t, "a{si}")); err == nil {
		t.Error("expected dict-entry non-zero-padding error")
	}
}

func TestDecodeStructFieldError(t *testing.T) {
	// "(i)": struct whose single int32 field is truncated.
	if _, _, err := Unmarshal([]byte{1, 0}, le, mustSig(t, "(i)")); err == nil {
		t.Error("expected struct field decode error")
	}
}

func TestDecodeStructAlignError(t *testing.T) {
	// "y(i)": a byte then a struct. The struct is 8-aligned; truncating in the
	// padding region exercises the struct align error.
	if _, _, err := Unmarshal([]byte{1, 0, 0}, le, mustSig(t, "y(i)")); err == nil {
		t.Error("expected struct alignment error")
	}
}

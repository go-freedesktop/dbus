// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSignatureValid(t *testing.T) {
	for _, s := range []string{
		"", "y", "b", "n", "q", "i", "u", "x", "t", "d", "h", "s", "o", "g", "v",
		"as", "a{sv}", "(ii)", "(s(ii)as)", "a(yv)", "aai", "a{s(ii)}",
	} {
		if _, err := ParseSignature(s); err != nil {
			t.Errorf("ParseSignature(%q) unexpected error: %v", s, err)
		}
	}
}

func TestParseSignatureInvalid(t *testing.T) {
	long := strings.Repeat("y", maxSignatureLen+1)
	deep := strings.Repeat("a", maxTypeDepth+2) + "y"
	for _, s := range []string{
		long,    // over length
		"z",     // unknown code
		"a",     // array with no element
		"(",     // unterminated struct
		"()",    // empty struct
		"(iz)",  // bad element in struct
		"{",     // unterminated dict
		"{vs}",  // non-basic key
		"{s",    // dict value missing / unterminated
		"{si",   // dict not closed
		"{sz}",  // bad dict value
		"a{sz}", // bad dict value in array
		deep,    // too deep
		"a(z)",  // nested error under array
	} {
		if _, err := ParseSignature(s); err == nil {
			t.Errorf("ParseSignature(%q) expected error, got nil", s)
		}
	}
}

func TestParseSignatureMust(t *testing.T) {
	// A valid signature is returned verbatim.
	if got := ParseSignatureMust("(iiibiiay)").String(); got != "(iiibiiay)" {
		t.Errorf("ParseSignatureMust valid = %q, want %q", got, "(iiibiiay)")
	}
	// A malformed signature panics.
	defer func() {
		if r := recover(); r == nil {
			t.Error("ParseSignatureMust(invalid) did not panic")
		}
	}()
	_ = ParseSignatureMust("z") // unknown type code -> panic
}

func TestSignatureAccessors(t *testing.T) {
	var empty Signature
	if !empty.Empty() {
		t.Error("zero Signature should be Empty")
	}
	if empty.single() {
		t.Error("empty signature is not a single type")
	}
	s, _ := ParseSignature("a{sv}")
	if s.Empty() {
		t.Error("a{sv} should not be Empty")
	}
	if s.String() != "a{sv}" {
		t.Errorf("String() = %q", s.String())
	}
	if !s.single() {
		t.Error("a{sv} should be a single complete type")
	}
	multi, _ := ParseSignature("ii")
	if multi.single() {
		t.Error("ii is two types, not single")
	}
}

func TestSignatureOf(t *testing.T) {
	cases := []struct {
		v    interface{}
		want string
	}{
		{byte(0), "y"},
		{true, "b"},
		{int16(0), "n"},
		{uint16(0), "q"},
		{int32(0), "i"},
		{uint32(0), "u"},
		{int64(0), "x"},
		{uint64(0), "t"},
		{float64(0), "d"},
		{"", "s"},
		{ObjectPath("/"), "o"},
		{Signature{}, "g"},
		{Variant{}, "v"},
		{UnixFD(0), "h"},
		{UnixFDIndex(0), "h"},
		{[]string{}, "as"},
		{[]byte{}, "ay"},
		{map[string]int32{}, "a{si}"},
		{struct {
			A int32
			B string
		}{}, "(is)"},
	}
	for _, c := range cases {
		if got := SignatureOf(c.v).String(); got != c.want {
			t.Errorf("SignatureOf(%T) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestSignatureOfMultiple(t *testing.T) {
	if got := SignatureOf("s", int32(0), true).String(); got != "sib" {
		t.Errorf("got %q", got)
	}
}

func TestSignatureOfPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic for unrepresentable type")
		}
	}()
	SignatureOf(make(chan int))
}

func TestSignatureOfType(t *testing.T) {
	if _, err := SignatureOfType(reflect.TypeOf(int32(0))); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := SignatureOfType(reflect.TypeOf(make(chan int))); err == nil {
		t.Error("expected error for chan")
	}
	if _, err := SignatureOfType(nil); err == nil {
		t.Error("expected error for nil type")
	}
}

func TestSignatureOfTypeErrors(t *testing.T) {
	// map with non-basic key type (struct key)
	type k struct{ A int32 }
	if _, err := signatureOfType(reflect.TypeOf(map[k]int32{})); err == nil {
		t.Error("expected error: struct map key")
	}
	// map with unrepresentable value
	if _, err := signatureOfType(reflect.TypeOf(map[string]chan int{})); err == nil {
		t.Error("expected error: chan map value")
	}
	// slice of unrepresentable
	if _, err := signatureOfType(reflect.TypeOf([]chan int{})); err == nil {
		t.Error("expected error: chan slice")
	}
	// struct with unrepresentable field
	type bad struct{ C chan int }
	if _, err := signatureOfType(reflect.TypeOf(bad{})); err == nil {
		t.Error("expected error: chan struct field")
	}
	// bare interface
	if _, err := signatureOfType(reflect.TypeOf([]interface{}{}).Elem()); err == nil {
		t.Error("expected error: bare interface")
	}
	// map key computes but is not basic (map key is a struct handled above);
	// also cover a map whose key signature errors: map[chan int]int
	if _, err := signatureOfType(reflect.TypeOf(map[[1]chan int]int32{})); err == nil {
		t.Error("expected error: array-of-chan map key")
	}
}

func TestSignatureOfStructUnexported(t *testing.T) {
	type mixed struct {
		A int32
		b string // unexported, skipped
	}
	_ = mixed{}.b
	if got := SignatureOf(mixed{}).String(); got != "(i)" {
		t.Errorf("unexported field not skipped: got %q", got)
	}
}

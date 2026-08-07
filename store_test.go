// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestStoreScalars(t *testing.T) {
	var (
		s  string
		i  int32
		u  uint32
		b  bool
		by byte
		f  float64
		op ObjectPath
	)
	src := []interface{}{"hi", int32(-5), uint32(7), true, byte(9), 2.5, ObjectPath("/p")}
	if err := Store(src, &s, &i, &u, &b, &by, &f, &op); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if s != "hi" || i != -5 || u != 7 || !b || by != 9 || f != 2.5 || op != "/p" {
		t.Errorf("stored wrong values: %q %d %d %v %d %v %q", s, i, u, b, by, f, op)
	}
}

func TestStoreInterfaceTarget(t *testing.T) {
	var any interface{}
	if err := Store([]interface{}{int32(3)}, &any); err != nil {
		t.Fatal(err)
	}
	if any.(int32) != 3 {
		t.Errorf("interface target got %v", any)
	}
}

func TestStoreConvertibleScalar(t *testing.T) {
	// A named integer destination receives a plain int32 via conversion.
	type myInt int32
	var m myInt
	if err := Store([]interface{}{int32(11)}, &m); err != nil {
		t.Fatal(err)
	}
	if m != 11 {
		t.Errorf("convertible scalar got %v", m)
	}
}

func TestStoreSlice(t *testing.T) {
	var out []int32
	src := []interface{}{[]interface{}{int32(1), int32(2), int32(3)}}
	if err := Store(src, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, []int32{1, 2, 3}) {
		t.Errorf("slice got %#v", out)
	}
}

func TestStoreNamedByteSlice(t *testing.T) {
	type blob []byte
	var out blob
	if err := Store([]interface{}{[]byte{1, 2, 3}}, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, blob{1, 2, 3}) {
		t.Errorf("named []byte got %#v", out)
	}
}

func TestStoreMap(t *testing.T) {
	var out map[string]int32
	src := []interface{}{map[interface{}]interface{}{"a": int32(1), "b": int32(2)}}
	if err := Store(src, &out); err != nil {
		t.Fatal(err)
	}
	if out["a"] != 1 || out["b"] != 2 {
		t.Errorf("map got %#v", out)
	}
}

func TestStoreStruct(t *testing.T) {
	type point struct {
		X int32
		Y string
		z int32 // unexported, skipped
	}
	_ = point{}.z
	var p point
	src := []interface{}{[]interface{}{int32(4), "yes"}}
	if err := Store(src, &p); err != nil {
		t.Fatal(err)
	}
	if p.X != 4 || p.Y != "yes" {
		t.Errorf("struct got %#v", p)
	}
}

func TestStoreNested(t *testing.T) {
	// Decode a real wire value and Store it into typed destinations.
	type inner struct {
		A int32
		B []string
	}
	data, err := Marshal(binary.LittleEndian, inner{7, []string{"x", "y"}})
	if err != nil {
		t.Fatal(err)
	}
	vals, _, err := Unmarshal(data, binary.LittleEndian, mustSig(t, "(ias)"))
	if err != nil {
		t.Fatal(err)
	}
	var got inner
	if err := Store(vals, &got); err != nil {
		t.Fatal(err)
	}
	if got.A != 7 || !reflect.DeepEqual(got.B, []string{"x", "y"}) {
		t.Errorf("nested store got %#v", got)
	}
}

func TestStoreErrors(t *testing.T) {
	// count mismatch
	if err := Store([]interface{}{int32(1)}); err == nil {
		t.Error("expected count-mismatch error")
	}
	// non-pointer destination
	var x int32
	if err := Store([]interface{}{int32(1)}, x); err == nil {
		t.Error("expected non-pointer error")
	}
	// nil pointer destination
	var p *int32
	if err := Store([]interface{}{int32(1)}, p); err == nil {
		t.Error("expected nil-pointer error")
	}
	// incompatible scalar (string into int)
	var i int32
	if err := Store([]interface{}{"nope"}, &i); err == nil {
		t.Error("expected incompatible-assignment error")
	}
	// slice source not a slice
	var sl []int32
	if err := Store([]interface{}{int32(1)}, &sl); err == nil {
		t.Error("expected slice source-type error")
	}
	// slice element conversion error
	var sl2 []int32
	if err := Store([]interface{}{[]interface{}{"bad"}}, &sl2); err == nil {
		t.Error("expected slice element error")
	}
	// map source not a map
	var mp map[string]int32
	if err := Store([]interface{}{int32(1)}, &mp); err == nil {
		t.Error("expected map source-type error")
	}
	// map key conversion error
	var mp2 map[int32]int32
	if err := Store([]interface{}{map[interface{}]interface{}{"x": int32(1)}}, &mp2); err == nil {
		t.Error("expected map key error")
	}
	// map value conversion error
	var mp3 map[string]int32
	if err := Store([]interface{}{map[interface{}]interface{}{"x": "bad"}}, &mp3); err == nil {
		t.Error("expected map value error")
	}
	// struct source not a struct field list
	type s struct{ A int32 }
	var sv s
	if err := Store([]interface{}{int32(1)}, &sv); err == nil {
		t.Error("expected struct source-type error")
	}
	// struct field-count mismatch
	var sv2 s
	if err := Store([]interface{}{[]interface{}{int32(1), int32(2)}}, &sv2); err == nil {
		t.Error("expected struct field-count error")
	}
	// struct field conversion error
	var sv3 s
	if err := Store([]interface{}{[]interface{}{"bad"}}, &sv3); err == nil {
		t.Error("expected struct field-conversion error")
	}
}

func TestStoreDefinedByteSlice(t *testing.T) {
	// A []byte source stored into a slice whose element is a *defined* byte
	// type is neither assignable nor convertible, so it exercises the
	// element-wise byte-slice path in storeSlice.
	type myByte byte
	var out []myByte
	if err := Store([]interface{}{[]byte{1, 2, 3}}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0] != 1 || out[2] != 3 {
		t.Errorf("defined byte slice got %#v", out)
	}
}

func TestStoreCrossKindRejected(t *testing.T) {
	// string is Go-convertible to []byte, but the kinds differ (String vs
	// Slice), so the same-kind convert guard rejects it and the assignment
	// ultimately fails rather than silently coercing.
	var out []byte
	if err := Store([]interface{}{"abc"}, &out); err == nil {
		t.Error("expected error storing string into []byte across kinds")
	}
	// A named integer receiving a plain int of a different kind is likewise
	// rejected (int32 source, int64-kind destination).
	type i64 int64
	var v i64
	if err := Store([]interface{}{int32(1)}, &v); err == nil {
		t.Error("expected error storing int32 into int64-kind destination")
	}
}

func TestStoreVariantDirect(t *testing.T) {
	// A Variant destination receives a Variant verbatim (AssignableTo path).
	var v Variant
	src := MakeVariant(int32(5))
	if err := Store([]interface{}{src}, &v); err != nil {
		t.Fatal(err)
	}
	if v.Value().(int32) != 5 {
		t.Errorf("variant store got %#v", v)
	}
}

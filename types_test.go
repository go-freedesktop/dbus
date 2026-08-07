// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"errors"
	"testing"
)

func TestObjectPathIsValid(t *testing.T) {
	valid := []ObjectPath{"/", "/a", "/org/freedesktop/DBus", "/a_b/c9"}
	for _, p := range valid {
		if !p.IsValid() {
			t.Errorf("%q should be valid", p)
		}
	}
	invalid := []ObjectPath{"", "a", "/a/", "//a", "/a//b", "/a-b", "/a.b", "/€"}
	for _, p := range invalid {
		if p.IsValid() {
			t.Errorf("%q should be invalid", p)
		}
	}
}

func TestVariantAccessors(t *testing.T) {
	v := MakeVariant(int32(42))
	if v.Value().(int32) != 42 {
		t.Errorf("Value() = %v", v.Value())
	}
	if v.Signature().String() != "i" {
		t.Errorf("Signature() = %q", v.Signature())
	}
	if got := v.String(); got != "i 42" {
		t.Errorf("String() = %q", got)
	}
}

func TestError(t *testing.T) {
	e := NewError("org.example.Boom", []interface{}{"kaboom"})
	if e.Error() != "kaboom" {
		t.Errorf("Error() = %q", e.Error())
	}
	// no body -> falls back to the name
	e2 := NewError("org.example.Empty", nil)
	if e2.Error() != "org.example.Empty" {
		t.Errorf("Error() = %q", e2.Error())
	}
	// non-string first body element -> falls back to name
	e3 := NewError("org.example.NonString", []interface{}{int32(1)})
	if e3.Error() != "org.example.NonString" {
		t.Errorf("Error() = %q", e3.Error())
	}
	// as an error value
	var err error = e
	if err.Error() != "kaboom" {
		t.Errorf("error interface: %q", err.Error())
	}
}

func TestMakeFailedError(t *testing.T) {
	e := MakeFailedError(errors.New("underlying"))
	if e.Name != "org.freedesktop.DBus.Error.Failed" {
		t.Errorf("Name = %q", e.Name)
	}
	if e.Error() != "underlying" {
		t.Errorf("Error() = %q", e.Error())
	}
}

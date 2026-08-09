// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"fmt"
	"math"
	"reflect"
)

// decoder consumes wire bytes starting at pos, which the caller guarantees is
// an 8-aligned message offset (so type alignment computed from pos is correct).
type decoder struct {
	order ByteOrder
	data  []byte
	pos   int
}

// align advances pos to the next multiple of n, verifying that every skipped
// padding byte is zero (as the spec requires) and that the buffer is long
// enough.
func (d *decoder) align(n int) error {
	for d.pos%n != 0 {
		if d.pos >= len(d.data) {
			return fmt.Errorf("dbus: truncated buffer while aligning to %d", n)
		}
		if d.data[d.pos] != 0 {
			return fmt.Errorf("dbus: non-zero padding byte at offset %d", d.pos)
		}
		d.pos++
	}
	return nil
}

func (d *decoder) need(n int) error {
	if d.pos+n > len(d.data) {
		return fmt.Errorf("dbus: truncated buffer: need %d bytes at offset %d, have %d",
			n, d.pos, len(d.data)-d.pos)
	}
	return nil
}

func (d *decoder) readByte() (byte, error) {
	if err := d.need(1); err != nil {
		return 0, err
	}
	b := d.data[d.pos]
	d.pos++
	return b, nil
}

func (d *decoder) readUint16() (uint16, error) {
	if err := d.align(2); err != nil {
		return 0, err
	}
	if err := d.need(2); err != nil {
		return 0, err
	}
	x := d.order.Uint16(d.data[d.pos:])
	d.pos += 2
	return x, nil
}

func (d *decoder) readUint32() (uint32, error) {
	if err := d.align(4); err != nil {
		return 0, err
	}
	if err := d.need(4); err != nil {
		return 0, err
	}
	x := d.order.Uint32(d.data[d.pos:])
	d.pos += 4
	return x, nil
}

func (d *decoder) readUint64() (uint64, error) {
	if err := d.align(8); err != nil {
		return 0, err
	}
	if err := d.need(8); err != nil {
		return 0, err
	}
	x := d.order.Uint64(d.data[d.pos:])
	d.pos += 8
	return x, nil
}

// readStringN reads a length-prefixed, NUL-terminated string. lenSize is 4 for
// 's'/'o' and 1 for 'g'.
func (d *decoder) readStringN(lenSize int) (string, error) {
	var n int
	if lenSize == 1 {
		b, err := d.readByte()
		if err != nil {
			return "", err
		}
		n = int(b)
	} else {
		u, err := d.readUint32()
		if err != nil {
			return "", err
		}
		n = int(u)
	}
	if err := d.need(n + 1); err != nil {
		return "", err
	}
	s := string(d.data[d.pos : d.pos+n])
	if d.data[d.pos+n] != 0 {
		return "", fmt.Errorf("dbus: string not NUL-terminated at offset %d", d.pos+n)
	}
	d.pos += n + 1
	return s, nil
}

// Unmarshal decodes every top-level type in sig from data (interpreted with
// byte order) into natural Go values, returning them and the number of bytes
// consumed. The decode begins at an 8-aligned offset, matching Marshal.
func Unmarshal(data []byte, order ByteOrder, sig Signature) ([]interface{}, int, error) {
	d := &decoder{order: order, data: data}
	out := make([]interface{}, 0, countTypes(sig.str))
	rest := sig.str
	for len(rest) > 0 {
		head, tail, err := splitType(rest)
		if err != nil {
			return nil, 0, err
		}
		v, err := d.decodeSingle(head)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
		rest = tail
	}
	return out, d.pos, nil
}

// countTypes returns the number of complete top-level types in sig, which must
// already be a validated signature. It lets callers size their result slices
// up-front instead of regrowing them element by element.
func countTypes(sig string) int {
	n := 0
	for len(sig) > 0 {
		c, err := validateSingle(sig, 0)
		if err != nil || c == 0 {
			return n
		}
		sig = sig[c:]
		n++
	}
	return n
}

// splitType splits sig into its first complete type and the remainder.
func splitType(sig string) (head, tail string, err error) {
	n, err := validateSingle(sig, 0)
	if err != nil {
		return "", "", err
	}
	return sig[:n], sig[n:], nil
}

// decodeSingle decodes exactly one complete type (given by sig) into a natural
// Go value.
func (d *decoder) decodeSingle(sig string) (interface{}, error) {
	switch sig[0] {
	case typeByte:
		return d.readByte()
	case typeBool:
		x, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		if x > 1 {
			return nil, fmt.Errorf("dbus: invalid boolean value %d", x)
		}
		return x == 1, nil
	case typeInt16:
		x, err := d.readUint16()
		return int16(x), err
	case typeUint16:
		return d.readUint16()
	case typeInt32:
		x, err := d.readUint32()
		return int32(x), err
	case typeUint32:
		return d.readUint32()
	case typeInt64:
		x, err := d.readUint64()
		return int64(x), err
	case typeUint64:
		return d.readUint64()
	case typeDouble:
		x, err := d.readUint64()
		return math.Float64frombits(x), err
	case typeUnixFD:
		x, err := d.readUint32()
		return UnixFD(int32(x)), err
	case typeString:
		return d.readStringN(4)
	case typeObjectPath:
		s, err := d.readStringN(4)
		return ObjectPath(s), err
	case typeSignature:
		s, err := d.readStringN(1)
		if err != nil {
			return nil, err
		}
		return ParseSignature(s)
	case typeVariant:
		return d.decodeVariant()
	case typeArray:
		return d.decodeArray(sig)
	case typeStructBegin:
		return d.decodeStruct(sig)
	default:
		return nil, fmt.Errorf("dbus: cannot decode type code %q", sig[0])
	}
}

// decodeVariant reads the embedded signature then the value it describes.
func (d *decoder) decodeVariant() (interface{}, error) {
	sigStr, err := d.readStringN(1)
	if err != nil {
		return nil, err
	}
	sig, err := ParseSignature(sigStr)
	if err != nil {
		return nil, err
	}
	if !sig.single() {
		return nil, fmt.Errorf("dbus: variant signature %q is not a single complete type", sigStr)
	}
	val, err := d.decodeSingle(sigStr)
	if err != nil {
		return nil, err
	}
	return Variant{sig: sig, value: val}, nil
}

// decodeArray decodes an array type (sig begins with 'a'). Byte arrays decode to
// []byte, dict arrays to map[interface{}]interface{}, all others to
// []interface{}.
func (d *decoder) decodeArray(sig string) (interface{}, error) {
	elemSig := sig[1:]
	n, err := d.readUint32()
	if err != nil {
		return nil, err
	}
	// After the length, pad to the element's alignment before the first
	// element; this padding is not counted in n.
	if err := d.align(alignmentOfSig(elemSig)); err != nil {
		return nil, err
	}
	if int(n) > len(d.data)-d.pos {
		return nil, fmt.Errorf("dbus: array length %d exceeds remaining %d bytes", n, len(d.data)-d.pos)
	}
	end := d.pos + int(n)

	// Byte array fast path.
	if elemSig[0] == typeByte {
		b := make([]byte, n)
		copy(b, d.data[d.pos:end])
		d.pos = end
		return b, nil
	}
	// Dict-entry array -> map.
	if elemSig[0] == typeDictBegin {
		return d.decodeDictArray(elemSig, end)
	}
	out := []interface{}{}
	for d.pos < end {
		v, err := d.decodeSingle(elemSig)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if d.pos != end {
		return nil, fmt.Errorf("dbus: array elements overran declared length")
	}
	return out, nil
}

// decodeDictArray decodes the dict entries of an "a{kv}" up to offset end into a
// map. elemSig is the "{kv}" element signature.
func (d *decoder) decodeDictArray(elemSig string, end int) (interface{}, error) {
	keySig := elemSig[1:2]
	valSig := elemSig[2 : len(elemSig)-1]
	m := map[interface{}]interface{}{}
	for d.pos < end {
		if err := d.align(8); err != nil { // each dict entry is 8-aligned
			return nil, err
		}
		k, err := d.decodeSingle(keySig)
		if err != nil {
			return nil, err
		}
		v, err := d.decodeSingle(valSig)
		if err != nil {
			return nil, err
		}
		m[k] = v
	}
	if d.pos != end {
		return nil, fmt.Errorf("dbus: dict entries overran declared length")
	}
	return m, nil
}

// decodeStruct decodes a "(...)" struct into a []interface{} of its fields.
func (d *decoder) decodeStruct(sig string) (interface{}, error) {
	if err := d.align(8); err != nil {
		return nil, err
	}
	inner := sig[1 : len(sig)-1] // strip parens
	out := make([]interface{}, 0, countTypes(inner))
	// inner is a substring of an already-validated signature, so each leading
	// complete type is well-formed; validateSingle cannot fail here.
	for rest := inner; len(rest) > 0; {
		n, _ := validateSingle(rest, 0)
		v, err := d.decodeSingle(rest[:n])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		rest = rest[n:]
	}
	return out, nil
}

// Store coerces the dynamically-decoded values in src into the pointer targets
// dsts (as returned by Unmarshal), mirroring github.com/godbus/dbus's Store: a
// *T receives a T, a *[]X a homogeneous array, a *map a dict, a *struct the
// fields of a struct, and an interface{} the value verbatim.
func Store(src []interface{}, dsts ...interface{}) error {
	if len(src) != len(dsts) {
		return fmt.Errorf("dbus: Store: %d source values, %d destinations", len(src), len(dsts))
	}
	for i := range src {
		rv := reflect.ValueOf(dsts[i])
		if rv.Kind() != reflect.Pointer || rv.IsNil() {
			return fmt.Errorf("dbus: Store: destination %d is not a non-nil pointer", i)
		}
		if err := storeValue(src[i], rv.Elem()); err != nil {
			return err
		}
	}
	return nil
}

// storeValue assigns src into the settable destination dst, converting between
// the natural decoded representation and the destination's concrete type.
func storeValue(src interface{}, dst reflect.Value) error {
	// interface{} destination: assign verbatim.
	if dst.Kind() == reflect.Interface && dst.NumMethod() == 0 {
		dst.Set(reflect.ValueOf(src))
		return nil
	}
	sv := reflect.ValueOf(src)
	dt := dst.Type()

	// Directly assignable (same type, e.g. Variant, ObjectPath, string...).
	if sv.IsValid() && sv.Type().AssignableTo(dt) {
		dst.Set(sv)
		return nil
	}
	// Same-kind convertible (e.g. a named integer type receiving its base
	// type). Requiring identical kinds excludes surprising cross-kind Go
	// conversions such as int-to-string.
	if sv.IsValid() && sv.Kind() == dt.Kind() && sv.Type().ConvertibleTo(dt) {
		dst.Set(sv.Convert(dt))
		return nil
	}

	switch dt.Kind() {
	case reflect.Slice:
		return storeSlice(src, dst)
	case reflect.Map:
		return storeMap(src, dst)
	case reflect.Struct:
		return storeStruct(src, dst)
	}
	return fmt.Errorf("dbus: Store: cannot assign %T to %s", src, dt)
}

func storeSlice(src interface{}, dst reflect.Value) error {
	dt := dst.Type()
	// []byte decodes as []byte already; handled by AssignableTo above, but a
	// named []byte destination reaches here.
	if b, ok := src.([]byte); ok && dt.Elem().Kind() == reflect.Uint8 {
		// A byte array decoded as []byte, stored into a defined byte-slice type
		// (e.g. type Blob []MyByte) that is not directly assignable.
		out := reflect.MakeSlice(dt, len(b), len(b))
		for i, x := range b {
			out.Index(i).SetUint(uint64(x))
		}
		dst.Set(out)
		return nil
	}
	items, ok := src.([]interface{})
	if !ok {
		return fmt.Errorf("dbus: Store: cannot assign %T to slice %s", src, dt)
	}
	out := reflect.MakeSlice(dt, len(items), len(items))
	for i, it := range items {
		if err := storeValue(it, out.Index(i)); err != nil {
			return err
		}
	}
	dst.Set(out)
	return nil
}

func storeMap(src interface{}, dst reflect.Value) error {
	dt := dst.Type()
	m, ok := src.(map[interface{}]interface{})
	if !ok {
		return fmt.Errorf("dbus: Store: cannot assign %T to map %s", src, dt)
	}
	out := reflect.MakeMapWithSize(dt, len(m))
	for k, v := range m {
		kv := reflect.New(dt.Key()).Elem()
		if err := storeValue(k, kv); err != nil {
			return err
		}
		vv := reflect.New(dt.Elem()).Elem()
		if err := storeValue(v, vv); err != nil {
			return err
		}
		out.SetMapIndex(kv, vv)
	}
	dst.Set(out)
	return nil
}

func storeStruct(src interface{}, dst reflect.Value) error {
	fields, ok := src.([]interface{})
	if !ok {
		return fmt.Errorf("dbus: Store: cannot assign %T to struct %s", src, dst.Type())
	}
	dt := dst.Type()
	// Collect exported field indices.
	var exported []int
	for i := 0; i < dt.NumField(); i++ {
		if dt.Field(i).PkgPath == "" {
			exported = append(exported, i)
		}
	}
	if len(exported) != len(fields) {
		return fmt.Errorf("dbus: Store: struct %s has %d exported fields, got %d values",
			dt, len(exported), len(fields))
	}
	for j, fi := range exported {
		if err := storeValue(fields[j], dst.Field(fi)); err != nil {
			return err
		}
	}
	return nil
}

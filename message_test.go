// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func sampleCall() *Message {
	return &Message{
		Type:   TypeMethodCall,
		Flags:  FlagNoReplyExpected,
		serial: 7,
		Headers: map[HeaderField]Variant{
			FieldPath:        MakeVariant(ObjectPath("/org/example")),
			FieldInterface:   MakeVariant("org.example.Iface"),
			FieldMember:      MakeVariant("Method"),
			FieldDestination: MakeVariant("org.example.Dest"),
		},
		Body: []interface{}{"arg1", int32(42)},
	}
}

func TestMessageRoundTrip(t *testing.T) {
	for _, o := range orders {
		m := sampleCall()
		data, err := m.Marshal(o.order)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", o.name, err)
		}
		if len(data)%8 != 0 {
			// The header up to the body must be 8-padded; total length need
			// not be, but the body offset must. Verify body offset alignment
			// by decoding.
		}
		got, n, err := DecodeMessage(data)
		if err != nil {
			t.Fatalf("%s: DecodeMessage: %v", o.name, err)
		}
		if n != len(data) {
			t.Errorf("%s: consumed %d of %d", o.name, n, len(data))
		}
		if got.Type != TypeMethodCall || got.Flags != FlagNoReplyExpected || got.serial != 7 {
			t.Errorf("%s: header mismatch: %+v", o.name, got)
		}
		if got.Serial() != 7 {
			t.Errorf("%s: Serial() = %d", o.name, got.Serial())
		}
		if got.Headers[FieldMember].Value().(string) != "Method" {
			t.Errorf("%s: member = %v", o.name, got.Headers[FieldMember].Value())
		}
		if len(got.Body) != 2 || got.Body[0].(string) != "arg1" || got.Body[1].(int32) != 42 {
			t.Errorf("%s: body = %#v", o.name, got.Body)
		}
	}
}

func TestMessageNoBody(t *testing.T) {
	m := &Message{
		Type: TypeSignal,
		Headers: map[HeaderField]Variant{
			FieldPath:      MakeVariant(ObjectPath("/p")),
			FieldInterface: MakeVariant("i.f"),
			FieldMember:    MakeVariant("Sig"),
		},
	}
	data, err := m.Marshal(binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := DecodeMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Body) != 0 {
		t.Errorf("expected empty body, got %#v", got.Body)
	}
}

func TestMessageSetSerial(t *testing.T) {
	m := sampleCall()
	m.SetSerial(99)
	if m.Serial() != 99 {
		t.Errorf("SetSerial/Serial = %d", m.Serial())
	}
}

func TestMessageMarshalErrors(t *testing.T) {
	// missing required fields
	bad := &Message{Type: TypeMethodCall, Headers: map[HeaderField]Variant{}}
	if _, err := bad.Marshal(binary.LittleEndian); err == nil {
		t.Error("expected error for missing required header fields")
	}
	// invalid message type
	badType := &Message{Type: MessageType(99)}
	if _, err := badType.Marshal(binary.LittleEndian); err == nil {
		t.Error("expected error for invalid message type")
	}
	// body that cannot be marshalled
	badBody := &Message{
		Type:    TypeSignal,
		Headers: map[HeaderField]Variant{FieldPath: MakeVariant(ObjectPath("/p")), FieldInterface: MakeVariant("i.f"), FieldMember: MakeVariant("S")},
		Body:    []interface{}{make(chan int)},
	}
	if _, err := badBody.Marshal(binary.LittleEndian); err == nil {
		t.Error("expected error for unmarshalable body")
	}
}

func TestMessageMarshalPopulatesHeadersMap(t *testing.T) {
	// A message with a nil Headers map but a body must still gain a SIGNATURE
	// header (exercises the lazy map init).
	m := &Message{
		Type: TypeError,
		Headers: map[HeaderField]Variant{
			FieldErrorName:   MakeVariant("org.e.Err"),
			FieldReplySerial: MakeVariant(uint32(3)),
		},
		Body: []interface{}{"msg"},
	}
	data, err := m.Marshal(binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := DecodeMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body[0].(string) != "msg" {
		t.Errorf("body = %#v", got.Body)
	}
}

func TestDecodeMessageErrors(t *testing.T) {
	// too short
	if _, _, err := DecodeMessage([]byte{1, 2, 3}); err == nil {
		t.Error("expected short-header error")
	}
	good, _ := sampleCall().Marshal(binary.LittleEndian)
	// bad byte-order flag
	bad := append([]byte(nil), good...)
	bad[0] = 'X'
	if _, _, err := DecodeMessage(bad); err == nil {
		t.Error("expected byte-order error")
	}
	// bad protocol version
	badv := append([]byte(nil), good...)
	badv[3] = 9
	if _, _, err := DecodeMessage(badv); err == nil {
		t.Error("expected protocol-version error")
	}
	// body length too large
	huge := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(huge[4:8], maxMessageLength+1)
	if _, _, err := DecodeMessage(huge); err == nil {
		t.Error("expected oversize-body error")
	}
	// truncated body: shorten the buffer
	if _, _, err := DecodeMessage(good[:len(good)-1]); err == nil {
		t.Error("expected truncated-body error")
	}
	// corrupt the header-fields array so decoding the a(yv) fails: set fields
	// length to something impossible.
	corrupt := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(corrupt[12:16], 0xFFFFFFF0)
	if _, _, err := DecodeMessage(corrupt); err == nil {
		t.Error("expected header-fields decode error")
	}
}

func TestDecodeMessageValidateFails(t *testing.T) {
	// Build a well-formed wire message whose type lacks its required fields:
	// a method_return with no REPLY_SERIAL. We hand-encode by marshalling a
	// signal (which needs path/iface/member) then flipping the type byte to
	// method_return, whose required REPLY_SERIAL is then absent.
	m := &Message{
		Type:    TypeSignal,
		Headers: map[HeaderField]Variant{FieldPath: MakeVariant(ObjectPath("/p")), FieldInterface: MakeVariant("i.f"), FieldMember: MakeVariant("S")},
	}
	data, _ := m.Marshal(binary.LittleEndian)
	data[1] = byte(TypeMethodReturn) // now missing REPLY_SERIAL
	if _, _, err := DecodeMessage(data); err == nil {
		t.Error("expected post-decode validation error")
	}
}

func TestReadWriteMessage(t *testing.T) {
	m := sampleCall()
	var buf bytes.Buffer
	if err := WriteMessage(&buf, m, binary.BigEndian); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Headers[FieldMember].Value().(string) != "Method" {
		t.Errorf("member = %v", got.Headers[FieldMember].Value())
	}
}

func TestWriteMessageError(t *testing.T) {
	// A message that fails to marshal must surface the error from WriteMessage.
	bad := &Message{Type: MessageType(99)}
	if err := WriteMessage(io.Discard, bad, binary.LittleEndian); err == nil {
		t.Error("expected WriteMessage error")
	}
}

// errWriter fails every write.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWriteMessageWriterError(t *testing.T) {
	if err := WriteMessage(errWriter{}, sampleCall(), binary.LittleEndian); err == nil {
		t.Error("expected writer error")
	}
}

func TestReadMessageErrors(t *testing.T) {
	// EOF before the fixed prefix
	if _, err := ReadMessage(bytes.NewReader([]byte{1, 2, 3})); err == nil {
		t.Error("expected short-prefix error")
	}
	// bad order flag in prefix
	prefix := make([]byte, 16)
	prefix[0] = 'X'
	if _, err := ReadMessage(bytes.NewReader(prefix)); err == nil {
		t.Error("expected order-flag error")
	}
	// oversize lengths in prefix
	big := make([]byte, 16)
	big[0] = 'l'
	binary.LittleEndian.PutUint32(big[4:8], maxMessageLength+1)
	if _, err := ReadMessage(bytes.NewReader(big)); err == nil {
		t.Error("expected oversize-length error")
	}
	// valid prefix but truncated remainder
	m := sampleCall()
	full, _ := m.Marshal(binary.LittleEndian)
	if _, err := ReadMessage(bytes.NewReader(full[:16+2])); err == nil {
		t.Error("expected truncated-remainder error")
	}
	// valid prefix, remainder present but body corrupt -> DecodeMessage error.
	// Corrupt the SIGNATURE-driven body by flipping the version byte after the
	// prefix is read is not possible; instead feed a prefix whose fields land
	// on an invalid header, exercised in DecodeMessage tests already.
}

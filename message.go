// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"encoding/binary"
	"fmt"
	"io"
	"reflect"
)

// protoVersion is the D-Bus major protocol version this implementation speaks.
const protoVersion = 1

// maxMessageLength is the spec's absolute ceiling on a single message
// (2^27 bytes). It bounds allocations when reading from an untrusted peer.
const maxMessageLength = 1 << 27

// MessageType identifies the kind of a D-Bus message.
type MessageType byte

// Message types.
const (
	TypeInvalid MessageType = iota
	TypeMethodCall
	TypeMethodReturn
	TypeError
	TypeSignal
)

// Flags is the message flags bitfield.
type Flags byte

// Message flags.
const (
	// FlagNoReplyExpected suppresses the method_return/error reply.
	FlagNoReplyExpected Flags = 1 << iota
	// FlagNoAutoStart asks the bus not to auto-start a service.
	FlagNoAutoStart
	// FlagAllowInteractiveAuthorization permits interactive authorization.
	FlagAllowInteractiveAuthorization
)

// HeaderField is a message header field code.
type HeaderField byte

// Header field codes.
const (
	FieldPath HeaderField = iota + 1
	FieldInterface
	FieldMember
	FieldErrorName
	FieldReplySerial
	FieldDestination
	FieldSender
	FieldSignature
	FieldUnixFDs
)

// Message is a decoded D-Bus message: its type, flags, serial, header fields and
// body values.
type Message struct {
	Type    MessageType
	Flags   Flags
	serial  uint32
	Headers map[HeaderField]Variant
	Body    []interface{}
}

// Serial returns the message serial (0 until assigned by a connection).
func (m *Message) Serial() uint32 { return m.serial }

// SetSerial sets the message serial.
func (m *Message) SetSerial(s uint32) { m.serial = s }

// bodySignature returns the signature stored in the SIGNATURE header, or the
// empty signature if none is present.
func (m *Message) bodySignature() Signature {
	if v, ok := m.Headers[FieldSignature]; ok {
		if s, ok := v.value.(Signature); ok {
			return s
		}
	}
	return Signature{}
}

// requiredFields lists the header fields each message type must carry.
var requiredFields = map[MessageType][]HeaderField{
	TypeMethodCall:   {FieldPath, FieldMember},
	TypeMethodReturn: {FieldReplySerial},
	TypeError:        {FieldErrorName, FieldReplySerial},
	TypeSignal:       {FieldPath, FieldInterface, FieldMember},
}

// validate checks the message's type and that its required header fields are
// present.
func (m *Message) validate() error {
	req, ok := requiredFields[m.Type]
	if !ok {
		return fmt.Errorf("dbus: invalid message type %d", m.Type)
	}
	for _, f := range req {
		if _, ok := m.Headers[f]; !ok {
			return fmt.Errorf("dbus: message type %d missing required header field %d", m.Type, f)
		}
	}
	return nil
}

// Marshal encodes the message into wire bytes using the given byte order. It
// derives and installs the SIGNATURE header from Body when the body is
// non-empty, then validates the required header fields.
func (m *Message) Marshal(order ByteOrder) ([]byte, error) {
	// Encode the body first so we know its length and signature.
	var body []byte
	if len(m.Body) > 0 {
		var err error
		body, err = Marshal(order, m.Body...)
		if err != nil {
			return nil, err
		}
		if m.Headers == nil {
			m.Headers = map[HeaderField]Variant{}
		}
		m.Headers[FieldSignature] = MakeVariant(SignatureOf(m.Body...))
	}
	if err := m.validate(); err != nil {
		return nil, err
	}

	e := &encoder{order: order}
	var flag byte = 'l'
	if order == binary.BigEndian {
		flag = 'B'
	}
	e.putByte(flag)
	e.putByte(byte(m.Type))
	e.putByte(byte(m.Flags))
	e.putByte(protoVersion)
	e.putUint32(uint32(len(body)))
	e.putUint32(m.serial)

	// Header fields: array of (yv) structs, in ascending field-code order for
	// determinism.
	fields := make([]headerFieldStruct, 0, len(m.Headers))
	for code := HeaderField(1); code <= FieldUnixFDs; code++ {
		if v, ok := m.Headers[code]; ok {
			fields = append(fields, headerFieldStruct{Code: byte(code), Value: v})
		}
	}
	if err := e.encode(reflect.ValueOf(fields)); err != nil {
		return nil, err
	}

	// Pad to 8 before the body.
	e.align(8)
	e.buf = append(e.buf, body...)
	return e.buf, nil
}

// headerFieldStruct is the (yv) wire shape of a single header field.
type headerFieldStruct struct {
	Code  byte
	Value Variant
}

// DecodeMessage decodes exactly one message from the front of data, returning
// the message and the total number of bytes it occupied.
func DecodeMessage(data []byte) (*Message, int, error) {
	if len(data) < 16 {
		return nil, 0, fmt.Errorf("dbus: short message header (%d bytes)", len(data))
	}
	order, err := orderFromFlag(data[0])
	if err != nil {
		return nil, 0, err
	}
	d := &decoder{order: order, data: data}
	d.pos = 1 // past order flag
	typeByteVal, _ := d.readByte()
	flagsByte, _ := d.readByte()
	ver, _ := d.readByte()
	if ver != protoVersion {
		return nil, 0, fmt.Errorf("dbus: unsupported protocol version %d", ver)
	}
	bodyLen, _ := d.readUint32()
	serial, _ := d.readUint32()
	if bodyLen > maxMessageLength {
		return nil, 0, fmt.Errorf("dbus: body length %d exceeds maximum", bodyLen)
	}

	m := &Message{
		Type:    MessageType(typeByteVal),
		Flags:   Flags(flagsByte),
		serial:  serial,
		Headers: map[HeaderField]Variant{},
	}

	// Header fields array a(yv).
	fieldsRaw, err := d.decodeSingle("a(yv)")
	if err != nil {
		return nil, 0, err
	}
	for _, entry := range fieldsRaw.([]interface{}) {
		fs := entry.([]interface{})
		code := HeaderField(fs[0].(byte))
		m.Headers[code] = fs[1].(Variant)
	}

	// Pad to 8 before the body.
	if err := d.align(8); err != nil {
		return nil, 0, err
	}
	if d.pos+int(bodyLen) > len(data) {
		return nil, 0, fmt.Errorf("dbus: truncated body: need %d bytes, have %d",
			bodyLen, len(data)-d.pos)
	}
	if bodyLen > 0 {
		bodyVals, _, err := Unmarshal(data[d.pos:d.pos+int(bodyLen)], order, m.bodySignature())
		if err != nil {
			return nil, 0, err
		}
		m.Body = bodyVals
	}
	total := d.pos + int(bodyLen)
	if err := m.validate(); err != nil {
		return nil, 0, err
	}
	return m, total, nil
}

// orderFromFlag maps the leading endianness byte to a ByteOrder.
func orderFromFlag(b byte) (ByteOrder, error) {
	switch b {
	case 'l':
		return binary.LittleEndian, nil
	case 'B':
		return binary.BigEndian, nil
	default:
		return nil, fmt.Errorf("dbus: invalid byte-order flag %q", b)
	}
}

// WriteMessage encodes m with order and writes it to w.
func WriteMessage(w io.Writer, m *Message, order ByteOrder) error {
	b, err := m.Marshal(order)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadMessage reads exactly one message from r. It first reads the 16-byte
// fixed prefix (through the header-array length), then the remaining header,
// padding and body.
func ReadMessage(r io.Reader) (*Message, error) {
	prefix := make([]byte, 16)
	if _, err := io.ReadFull(r, prefix); err != nil {
		return nil, err
	}
	order, err := orderFromFlag(prefix[0])
	if err != nil {
		return nil, err
	}
	bodyLen := order.Uint32(prefix[4:8])
	fieldsLen := order.Uint32(prefix[12:16])
	if bodyLen > maxMessageLength || fieldsLen > maxMessageLength {
		return nil, fmt.Errorf("dbus: message length out of range (fields=%d body=%d)", fieldsLen, bodyLen)
	}
	// Padding between the header fields and the body aligns the body to 8.
	pad := (8 - (int(fieldsLen) % 8)) % 8
	remaining := int(fieldsLen) + pad + int(bodyLen)
	buf := make([]byte, 16+remaining)
	copy(buf, prefix)
	if _, err := io.ReadFull(r, buf[16:]); err != nil {
		return nil, err
	}
	m, _, err := DecodeMessage(buf)
	return m, err
}

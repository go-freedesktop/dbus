// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"encoding/binary"
	"testing"
)

// notifyBody builds a representative body for the freedesktop Notifications
// Notify method, whose signature is "susssasa{sv}i":
//
//	app_name s, replaces_id u, app_icon s, summary s, body s,
//	actions as, hints a{sv}, expire_timeout i
func notifyBody() []interface{} {
	return []interface{}{
		"my-application",     // app_name  s
		uint32(0),            // replaces_id u
		"dialog-information", // app_icon  s
		"Build finished",     // summary   s
		"The release pipeline completed successfully in 4m32s.", // body s
		[]string{"default", "Open", "dismiss", "Dismiss"},       // actions as
		map[string]Variant{ // hints a{sv}
			"urgency":       MakeVariant(byte(1)),
			"category":      MakeVariant("device.added"),
			"desktop-entry": MakeVariant("my-application"),
			"resident":      MakeVariant(true),
		},
		int32(-1), // expire_timeout i
	}
}

// notifyMessage builds a full method_call Message carrying the Notify body.
func notifyMessage() *Message {
	m := &Message{
		Type:  TypeMethodCall,
		Flags: 0,
		Headers: map[HeaderField]Variant{
			FieldPath:        MakeVariant(ObjectPath("/org/freedesktop/Notifications")),
			FieldInterface:   MakeVariant("org.freedesktop.Notifications"),
			FieldMember:      MakeVariant("Notify"),
			FieldDestination: MakeVariant("org.freedesktop.Notifications"),
		},
		Body: notifyBody(),
	}
	m.SetSerial(42)
	return m
}

// signalMessage builds a representative signal Message with mixed args.
func signalMessage() *Message {
	m := &Message{
		Type: TypeSignal,
		Headers: map[HeaderField]Variant{
			FieldPath:      MakeVariant(ObjectPath("/org/freedesktop/Notifications")),
			FieldInterface: MakeVariant("org.freedesktop.Notifications"),
			FieldMember:    MakeVariant("NotificationClosed"),
		},
		Body: []interface{}{uint32(7), uint32(2)},
	}
	m.SetSerial(99)
	return m
}

func BenchmarkMarshalNotifyBody(b *testing.B) {
	body := notifyBody()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Marshal(binary.LittleEndian, body...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalNotifyBody(b *testing.B) {
	sig := SignatureOf(notifyBody()...)
	data, err := Marshal(binary.LittleEndian, notifyBody()...)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Unmarshal(data, binary.LittleEndian, sig); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalNotifyMessage(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := notifyMessage()
		if _, err := m.Marshal(binary.LittleEndian); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeNotifyMessage(b *testing.B) {
	data, err := notifyMessage().Marshal(binary.LittleEndian)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := DecodeMessage(data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNotifyRoundTrip is the headline metric: encode a fresh Notify
// message then decode it back, the full path every notification travels.
func BenchmarkNotifyRoundTrip(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := notifyMessage().Marshal(binary.LittleEndian)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := DecodeMessage(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSignalRoundTrip(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := signalMessage().Marshal(binary.LittleEndian)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := DecodeMessage(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNotifyRoundTripBE(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := notifyMessage().Marshal(binary.BigEndian)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := DecodeMessage(data); err != nil {
			b.Fatal(err)
		}
	}
}

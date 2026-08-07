// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"context"
	"strings"
)

// BusObject is a handle to a remote object at a destination and path, on which
// methods can be called.
type BusObject interface {
	Call(method string, flags Flags, args ...interface{}) *Call
	CallWithContext(ctx context.Context, method string, flags Flags, args ...interface{}) *Call
	GetProperty(name string) (Variant, error)
	SetProperty(name string, value interface{}) error
	Path() ObjectPath
	Destination() string
}

// Object returns a handle to the object at path on the peer named dest.
func (c *Conn) Object(dest string, path ObjectPath) BusObject {
	return &object{conn: c, dest: dest, path: path}
}

// object is the concrete BusObject.
type object struct {
	conn *Conn
	dest string
	path ObjectPath
}

func (o *object) Path() ObjectPath    { return o.path }
func (o *object) Destination() string { return o.dest }

// Call is a completed (or failed) method call: Body holds the reply arguments,
// Err any error (including a remote *Error).
type Call struct {
	Destination string
	Path        ObjectPath
	Method      string // "interface.member"
	Args        []interface{}
	Body        []interface{}
	Err         error
}

// Store decodes the reply body into the provided pointers, mirroring
// (*Conn).Store semantics. It returns the call's error if it failed.
func (c *Call) Store(dsts ...interface{}) error {
	if c.Err != nil {
		return c.Err
	}
	return Store(c.Body, dsts...)
}

// Call invokes method (given as "interface.member" or a bare "member") with the
// default hard timeout.
func (o *object) Call(method string, flags Flags, args ...interface{}) *Call {
	return o.CallWithContext(context.Background(), method, flags, args...)
}

// CallWithContext invokes method, bounding the wait by ctx (and the default
// hard timeout when ctx has none).
func (o *object) CallWithContext(ctx context.Context, method string, flags Flags, args ...interface{}) *Call {
	iface, member := splitMethod(method)
	call := &Call{
		Destination: o.dest,
		Path:        o.path,
		Method:      method,
		Args:        args,
	}
	msg := &Message{
		Type:  TypeMethodCall,
		Flags: flags,
		Headers: map[HeaderField]Variant{
			FieldPath:   MakeVariant(o.path),
			FieldMember: MakeVariant(member),
		},
		Body: args,
	}
	if o.dest != "" {
		msg.Headers[FieldDestination] = MakeVariant(o.dest)
	}
	if iface != "" {
		msg.Headers[FieldInterface] = MakeVariant(iface)
	}
	body, err := o.conn.callWithContext(ctx, msg)
	call.Body = body
	call.Err = err
	return call
}

// GetProperty reads a single property via org.freedesktop.DBus.Properties.Get.
// The property name is "interface.property".
func (o *object) GetProperty(name string) (Variant, error) {
	iface, prop := splitMethod(name)
	var v Variant
	err := o.Call("org.freedesktop.DBus.Properties.Get", 0, iface, prop).Store(&v)
	return v, err
}

// SetProperty writes a single property via org.freedesktop.DBus.Properties.Set.
func (o *object) SetProperty(name string, value interface{}) error {
	iface, prop := splitMethod(name)
	return o.Call("org.freedesktop.DBus.Properties.Set", 0, iface, prop, MakeVariant(value)).Err
}

// splitMethod separates "interface.member" into its interface and member. A
// bare member (no dot) yields an empty interface.
func splitMethod(s string) (iface, member string) {
	i := strings.LastIndexByte(s, '.')
	if i < 0 {
		return "", s
	}
	return s[:i], s[i+1:]
}

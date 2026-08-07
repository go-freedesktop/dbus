// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"fmt"
	"os"
	"reflect"
	"strings"
)

// Standard interface names handled internally by every connection.
const (
	ifacePeer          = "org.freedesktop.DBus.Peer"
	ifaceIntrospchable = "org.freedesktop.DBus.Introspectable"
	ifaceProperties    = "org.freedesktop.DBus.Properties"
)

// RequestName flags and reply codes (subset mirroring the bus API).
const (
	NameFlagAllowReplacement uint32 = 1 << 0
	NameFlagReplaceExisting  uint32 = 1 << 1
	NameFlagDoNotQueue       uint32 = 1 << 2

	NameReplyPrimaryOwner uint32 = 1
	NameReplyInQueue      uint32 = 2
	NameReplyExists       uint32 = 3
	NameReplyAlreadyOwner uint32 = 4
)

// errType is the reflect.Type of *Error, the conventional last return value of
// an exported method.
var errType = reflect.TypeOf((*Error)(nil))

// exportedObject is one interface's method table exported on one object path.
type exportedObject struct {
	iface   string
	methods map[string]reflect.Value // member name -> bound method
}

// Export publishes the exported methods of v under the given object path and
// interface name. Incoming method calls addressed to that path and interface
// are dispatched to the matching method by reflection. A method may return any
// number of D-Bus-representable values optionally followed by a *Error, which
// (when non-nil) is sent back as an error reply.
func (c *Conn) Export(v interface{}, path ObjectPath, iface string) error {
	if !path.IsValid() {
		return fmt.Errorf("dbus: Export: invalid object path %q", path)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v == nil {
		// Un-export: drop the interface (and the path if it becomes empty).
		if m := c.handlers[path]; m != nil {
			delete(m, iface)
			if len(m) == 0 {
				delete(c.handlers, path)
			}
		}
		return nil
	}
	eo := &exportedObject{iface: iface, methods: map[string]reflect.Value{}}
	rv := reflect.ValueOf(v)
	rt := rv.Type()
	// reflect exposes only exported methods for a concrete type, which are
	// exactly the ones eligible to be called over the bus.
	for i := 0; i < rt.NumMethod(); i++ {
		eo.methods[rt.Method(i).Name] = rv.Method(i)
	}
	if c.handlers[path] == nil {
		c.handlers[path] = map[string]*exportedObject{}
	}
	c.handlers[path][iface] = eo
	return nil
}

// RequestName asks the bus to assign the well-known name to this connection.
// On success (primary owner or already owner) the name is recorded.
func (c *Conn) RequestName(name string, flags uint32) (uint32, error) {
	var code uint32
	err := c.Object(busName, busPath).
		Call(busIface+".RequestName", 0, name, flags).Store(&code)
	if err != nil {
		return 0, err
	}
	if code == NameReplyPrimaryOwner || code == NameReplyAlreadyOwner {
		c.mu.Lock()
		c.names = append(c.names, name)
		c.mu.Unlock()
	}
	return code, nil
}

// ReleaseName asks the bus to release a previously requested well-known name.
func (c *Conn) ReleaseName(name string) (uint32, error) {
	var code uint32
	err := c.Object(busName, busPath).
		Call(busIface+".ReleaseName", 0, name).Store(&code)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	for i, n := range c.names {
		if n == name {
			c.names = append(c.names[:i], c.names[i+1:]...)
			break
		}
	}
	c.mu.Unlock()
	return code, nil
}

// Emit sends a signal from the local object at path. name is
// "interface.member".
func (c *Conn) Emit(path ObjectPath, name string, args ...interface{}) error {
	iface, member := splitMethod(name)
	if iface == "" || member == "" {
		return fmt.Errorf("dbus: Emit: name %q must be interface.member", name)
	}
	msg := &Message{
		Type: TypeSignal,
		Headers: map[HeaderField]Variant{
			FieldPath:      MakeVariant(path),
			FieldInterface: MakeVariant(iface),
			FieldMember:    MakeVariant(member),
		},
		Body: args,
	}
	return c.sendMessage(msg)
}

// sendMessage assigns a fresh serial and writes msg.
func (c *Conn) sendMessage(msg *Message) error {
	serial := c.nextSerial()
	msg.SetSerial(serial)
	return c.writeAssigned(msg)
}

// handleMethodCall dispatches an incoming method call: the standard interfaces
// first, then a user-exported method. It always replies (unless the caller set
// NO_REPLY_EXPECTED) — with a return, or an error naming the failure.
func (c *Conn) handleMethodCall(m *Message) {
	path, _ := headerString(m, FieldPath)
	iface, _ := headerString(m, FieldInterface)
	member, _ := headerString(m, FieldMember)

	body, dbusErr := c.invoke(ObjectPath(path), iface, member, m.Body)
	if m.Flags&FlagNoReplyExpected != 0 {
		return
	}

	var reply *Message
	if dbusErr != nil {
		reply = &Message{
			Type: TypeError,
			Headers: map[HeaderField]Variant{
				FieldErrorName:   MakeVariant(dbusErr.Name),
				FieldReplySerial: MakeVariant(m.serial),
			},
			Body: dbusErr.Body,
		}
	} else {
		reply = &Message{
			Type: TypeMethodReturn,
			Headers: map[HeaderField]Variant{
				FieldReplySerial: MakeVariant(m.serial),
			},
			Body: body,
		}
	}
	if sender, ok := headerString(m, FieldSender); ok && sender != "" {
		reply.Headers[FieldDestination] = MakeVariant(sender)
	}
	_ = c.sendMessage(reply)
}

// invoke resolves and calls the target method, returning its reply body or a
// *Error describing the failure.
func (c *Conn) invoke(path ObjectPath, iface, member string, body []interface{}) ([]interface{}, *Error) {
	// Standard interfaces handled by the library itself.
	switch iface {
	case ifacePeer:
		return c.handlePeer(member)
	case ifaceIntrospchable:
		if member == "Introspect" {
			return []interface{}{c.introspectXML(path)}, nil
		}
	case ifaceProperties:
		return c.handleProperties(path, member, body)
	}

	c.mu.Lock()
	ifaces := c.handlers[path]
	var eo *exportedObject
	if iface != "" {
		eo = ifaces[iface]
	} else {
		// No interface given: search for a unique member across interfaces.
		for _, cand := range ifaces {
			if _, ok := cand.methods[member]; ok {
				eo = cand
				break
			}
		}
	}
	c.mu.Unlock()

	if eo == nil {
		return nil, &Error{
			Name: "org.freedesktop.DBus.Error.UnknownObject",
			Body: []interface{}{fmt.Sprintf("no object %q exporting interface %q", path, iface)},
		}
	}
	method, ok := eo.methods[member]
	if !ok {
		return nil, &Error{
			Name: "org.freedesktop.DBus.Error.UnknownMethod",
			Body: []interface{}{fmt.Sprintf("no method %q on interface %q", member, eo.iface)},
		}
	}
	return callMethod(method, body)
}

// callMethod converts body to the method's parameters, calls it and splits the
// results into a reply body and an optional trailing *Error.
func callMethod(method reflect.Value, body []interface{}) ([]interface{}, *Error) {
	mt := method.Type()
	if mt.NumIn() != len(body) {
		return nil, &Error{
			Name: "org.freedesktop.DBus.Error.InvalidArgs",
			Body: []interface{}{fmt.Sprintf("expected %d arguments, got %d", mt.NumIn(), len(body))},
		}
	}
	args := make([]reflect.Value, mt.NumIn())
	for i := 0; i < mt.NumIn(); i++ {
		av := reflect.New(mt.In(i)).Elem()
		if err := storeValue(body[i], av); err != nil {
			return nil, &Error{
				Name: "org.freedesktop.DBus.Error.InvalidArgs",
				Body: []interface{}{err.Error()},
			}
		}
		args[i] = av
	}
	rets := method.Call(args)
	// A trailing *Error return is the method's error channel.
	if n := len(rets); n > 0 && mt.Out(n-1) == errType {
		if !rets[n-1].IsNil() {
			return nil, rets[n-1].Interface().(*Error)
		}
		rets = rets[:n-1]
	}
	out := make([]interface{}, len(rets))
	for i, r := range rets {
		out[i] = r.Interface()
	}
	return out, nil
}

// handlePeer implements org.freedesktop.DBus.Peer.
func (c *Conn) handlePeer(member string) ([]interface{}, *Error) {
	switch member {
	case "Ping":
		return nil, nil
	case "GetMachineId":
		return []interface{}{machineID()}, nil
	}
	return nil, &Error{
		Name: "org.freedesktop.DBus.Error.UnknownMethod",
		Body: []interface{}{fmt.Sprintf("no Peer method %q", member)},
	}
}

// machineIDPaths are the files consulted, in order, for the D-Bus machine id.
var machineIDPaths = []string{"/var/lib/dbus/machine-id", "/etc/machine-id"}

// machineID returns the D-Bus machine id, or an empty string if unreadable.
func machineID() string {
	for _, p := range machineIDPaths {
		if b, err := os.ReadFile(p); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// headerString extracts a string-valued header field.
func headerString(m *Message, f HeaderField) (string, bool) {
	v, ok := m.Headers[f]
	if !ok {
		return "", false
	}
	switch s := v.value.(type) {
	case string:
		return s, true
	case ObjectPath:
		return string(s), true
	}
	return "", false
}

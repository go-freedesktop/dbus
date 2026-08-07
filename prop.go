// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import "fmt"

// Prop is a single exported property: its current value and whether remote
// peers may write it via org.freedesktop.DBus.Properties.Set.
type Prop struct {
	Value    interface{}
	Writable bool
}

// ExportProperties publishes a set of properties for one interface on one
// object path, served through org.freedesktop.DBus.Properties. props maps
// property name to its definition.
func (c *Conn) ExportProperties(path ObjectPath, iface string, props map[string]*Prop) error {
	if !path.IsValid() {
		return fmt.Errorf("dbus: ExportProperties: invalid object path %q", path)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.props[path] == nil {
		c.props[path] = map[string]map[string]*Prop{}
	}
	dup := make(map[string]*Prop, len(props))
	for name, p := range props {
		dup[name] = p
	}
	c.props[path][iface] = dup
	return nil
}

// handleProperties implements org.freedesktop.DBus.Properties (Get, Set,
// GetAll).
func (c *Conn) handleProperties(path ObjectPath, member string, body []interface{}) ([]interface{}, *Error) {
	switch member {
	case "Get":
		iface, prop, err := twoStrings(body)
		if err != nil {
			return nil, err
		}
		p, ferr := c.lookupProp(path, iface, prop)
		if ferr != nil {
			return nil, ferr
		}
		return []interface{}{MakeVariant(p.Value)}, nil

	case "Set":
		if len(body) != 3 {
			return nil, invalidArgs("Set expects interface, property, value")
		}
		iface, _ := body[0].(string)
		prop, _ := body[1].(string)
		val, ok := body[2].(Variant)
		if !ok {
			return nil, invalidArgs("Set value must be a variant")
		}
		p, ferr := c.lookupProp(path, iface, prop)
		if ferr != nil {
			return nil, ferr
		}
		if !p.Writable {
			return nil, &Error{
				Name: "org.freedesktop.DBus.Error.PropertyReadOnly",
				Body: []interface{}{fmt.Sprintf("property %q is read-only", prop)},
			}
		}
		c.mu.Lock()
		p.Value = val.Value()
		c.mu.Unlock()
		return nil, nil

	case "GetAll":
		if len(body) != 1 {
			return nil, invalidArgs("GetAll expects an interface name")
		}
		iface, _ := body[0].(string)
		c.mu.Lock()
		set := c.props[path][iface]
		out := map[string]Variant{}
		for name, p := range set {
			out[name] = MakeVariant(p.Value)
		}
		c.mu.Unlock()
		return []interface{}{out}, nil
	}
	return nil, &Error{
		Name: "org.freedesktop.DBus.Error.UnknownMethod",
		Body: []interface{}{fmt.Sprintf("no Properties method %q", member)},
	}
}

// lookupProp finds a property or returns a descriptive error.
func (c *Conn) lookupProp(path ObjectPath, iface, name string) (*Prop, *Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.props[path][iface][name]
	if p == nil {
		return nil, &Error{
			Name: "org.freedesktop.DBus.Error.UnknownProperty",
			Body: []interface{}{fmt.Sprintf("no property %q on interface %q", name, iface)},
		}
	}
	return p, nil
}

// EmitPropertiesChanged emits org.freedesktop.DBus.Properties.PropertiesChanged
// for the named interface, carrying the changed name/value pairs.
func (c *Conn) EmitPropertiesChanged(path ObjectPath, iface string, changed map[string]interface{}) error {
	vch := map[string]Variant{}
	for k, v := range changed {
		vch[k] = MakeVariant(v)
	}
	return c.Emit(path, ifaceProperties+".PropertiesChanged", iface, vch, []string{})
}

// twoStrings extracts a leading (interface, property) string pair.
func twoStrings(body []interface{}) (string, string, *Error) {
	if len(body) != 2 {
		return "", "", invalidArgs("expected interface and property names")
	}
	a, _ := body[0].(string)
	b, _ := body[1].(string)
	return a, b, nil
}

func invalidArgs(msg string) *Error {
	return &Error{Name: "org.freedesktop.DBus.Error.InvalidArgs", Body: []interface{}{msg}}
}

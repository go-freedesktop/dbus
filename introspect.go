// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"reflect"
	"sort"
	"strings"
)

// introspectHeader is the standard D-Bus introspection DOCTYPE.
const introspectHeader = `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">
`

// introspectXML renders the org.freedesktop.DBus.Introspectable document for
// the object at path: its user-exported interfaces (methods and properties),
// the standard interfaces every object implements, and immediate child nodes.
func (c *Conn) introspectXML(path ObjectPath) string {
	c.mu.Lock()
	ifaces := c.handlers[path]
	propSets := c.props[path]
	children := c.childNodes(path)
	// Snapshot interface names for deterministic output.
	names := make([]string, 0, len(ifaces))
	for name := range ifaces {
		names = append(names, name)
	}
	for name := range propSets {
		if _, ok := ifaces[name]; !ok {
			names = append(names, name)
		}
	}
	c.mu.Unlock()
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(introspectHeader)
	b.WriteString("<node>\n")

	for _, name := range names {
		b.WriteString(`  <interface name="` + name + "\">\n")
		if eo := ifaces[name]; eo != nil {
			writeMethods(&b, eo)
		}
		writeProperties(&b, propSets[name])
		b.WriteString("  </interface>\n")
	}

	// Standard interfaces.
	b.WriteString(stdIntrospectInterfaces)

	for _, child := range children {
		b.WriteString(`  <node name="` + child + "\"/>\n")
	}
	b.WriteString("</node>\n")
	return b.String()
}

// writeMethods renders <method> elements (with in/out args) in name order.
func writeMethods(b *strings.Builder, eo *exportedObject) {
	names := make([]string, 0, len(eo.methods))
	for name := range eo.methods {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mt := eo.methods[name].Type()
		b.WriteString(`    <method name="` + name + "\">\n")
		for i := 0; i < mt.NumIn(); i++ {
			if sig, err := signatureOfType(mt.In(i)); err == nil {
				b.WriteString(`      <arg direction="in" type="` + sig + "\"/>\n")
			}
		}
		nOut := mt.NumOut()
		if nOut > 0 && mt.Out(nOut-1) == errType {
			nOut-- // the trailing *Error is not a wire argument
		}
		for i := 0; i < nOut; i++ {
			if sig, err := signatureOfType(mt.Out(i)); err == nil {
				b.WriteString(`      <arg direction="out" type="` + sig + "\"/>\n")
			}
		}
		b.WriteString("    </method>\n")
	}
}

// writeProperties renders <property> elements in name order.
func writeProperties(b *strings.Builder, props map[string]*Prop) {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := props[name]
		access := "read"
		if p.Writable {
			access = "readwrite"
		}
		sig := "v"
		if s, err := SignatureOfType(reflect.TypeOf(p.Value)); err == nil {
			sig = s.str
		}
		b.WriteString(`    <property name="` + name + `" type="` + sig + `" access="` + access + "\"/>\n")
	}
}

// childNodes returns the immediate child element names of path among the
// exported paths (both method handlers and property sets).
func (c *Conn) childNodes(path ObjectPath) []string {
	prefix := string(path)
	if prefix != "/" {
		prefix += "/"
	}
	seen := map[string]struct{}{}
	consider := func(p ObjectPath) {
		s := string(p)
		if s == string(path) || !strings.HasPrefix(s, prefix) {
			return
		}
		rest := s[len(prefix):]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[:i]
		}
		if rest != "" {
			seen[rest] = struct{}{}
		}
	}
	for p := range c.handlers {
		consider(p)
	}
	for p := range c.props {
		consider(p)
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// stdIntrospectInterfaces is the fixed introspection block for the standard
// interfaces implemented by every object on the connection.
const stdIntrospectInterfaces = `  <interface name="org.freedesktop.DBus.Introspectable">
    <method name="Introspect">
      <arg direction="out" type="s"/>
    </method>
  </interface>
  <interface name="org.freedesktop.DBus.Peer">
    <method name="Ping"/>
    <method name="GetMachineId">
      <arg direction="out" type="s"/>
    </method>
  </interface>
  <interface name="org.freedesktop.DBus.Properties">
    <method name="Get">
      <arg direction="in" type="s"/>
      <arg direction="in" type="s"/>
      <arg direction="out" type="v"/>
    </method>
    <method name="Set">
      <arg direction="in" type="s"/>
      <arg direction="in" type="s"/>
      <arg direction="in" type="v"/>
    </method>
    <method name="GetAll">
      <arg direction="in" type="s"/>
      <arg direction="out" type="a{sv}"/>
    </method>
    <signal name="PropertiesChanged">
      <arg type="s"/>
      <arg type="a{sv}"/>
      <arg type="as"/>
    </signal>
  </interface>
`

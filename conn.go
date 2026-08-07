// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Well-known bus name, path and interface of the message bus itself.
const (
	busName  = "org.freedesktop.DBus"
	busPath  = ObjectPath("/org/freedesktop/DBus")
	busIface = "org.freedesktop.DBus"
)

// DefaultCallTimeout is the hard ceiling applied to every method call whose
// context carries no earlier deadline. It guarantees a misbehaving or silent
// peer can never make a call block forever.
var DefaultCallTimeout = 30 * time.Second

// writeTimeout bounds a single blocking socket write.
var writeTimeout = 30 * time.Second

// ErrClosed is returned for operations on a closed connection.
var ErrClosed = errors.New("dbus: connection closed")

// Signal is a received D-Bus signal, delivered to channels registered with
// (*Conn).Signal.
type Signal struct {
	Sender string
	Path   ObjectPath
	Name   string // "interface.member"
	Body   []interface{}
}

// pendingCall is an in-flight method call awaiting its reply.
type pendingCall struct {
	done chan struct{}
	body []interface{}
	err  error
}

// Conn is a connection to a D-Bus message bus.
type Conn struct {
	conn  net.Conn
	order ByteOrder

	serial atomic.Uint32

	wmu sync.Mutex // serialises writes to conn

	mu       sync.Mutex
	calls    map[uint32]*pendingCall
	signals  []chan<- *Signal
	handlers map[ObjectPath]map[string]*exportedObject  // path -> iface -> object
	props    map[ObjectPath]map[string]map[string]*Prop // path -> iface -> name -> prop
	names    []string                                   // acquired well-known names
	unique   string
	closed   bool
	closeErr error

	done chan struct{} // closed when the read loop exits
}

// newConn wraps an authenticated net.Conn and starts the read loop. Messages
// this side sends use little-endian order, which the specification permits;
// both orders are accepted on decode.
func newConn(c net.Conn) *Conn {
	conn := &Conn{
		conn:     c,
		order:    binary.LittleEndian,
		calls:    map[uint32]*pendingCall{},
		handlers: map[ObjectPath]map[string]*exportedObject{},
		props:    map[ObjectPath]map[string]map[string]*Prop{},
		done:     make(chan struct{}),
	}
	go conn.readLoop()
	return conn
}

// nextSerial returns a fresh non-zero serial.
func (c *Conn) nextSerial() uint32 {
	for {
		s := c.serial.Add(1)
		if s != 0 {
			return s
		}
	}
}

// Dial connects to the bus at address, authenticates (EXTERNAL, then
// ANONYMOUS) and returns a ready connection. It does not send Hello; callers
// wanting a unique name should use ConnectSessionBus/ConnectSystemBus or call
// Hello themselves.
func Dial(address string) (*Conn, error) {
	addrs, err := parseAddresses(address)
	if err != nil {
		return nil, err
	}
	nc, _, err := dialAddress(addrs)
	if err != nil {
		return nil, err
	}
	if err := clientHandshake(nc); err != nil {
		nc.Close()
		return nil, err
	}
	return newConn(nc), nil
}

// NewConn wraps an already-connected, already-authenticated transport (for
// example one half of a net.Pipe in tests) and starts serving it.
func NewConn(transport net.Conn) *Conn { return newConn(transport) }

// clientHandshake writes the leading NUL and runs SASL, bounded by a deadline
// so a stalled peer cannot hang the dial.
func clientHandshake(nc net.Conn) error {
	_ = nc.SetDeadline(time.Now().Add(DefaultCallTimeout))
	if _, err := nc.Write([]byte{0}); err != nil {
		return err
	}
	if _, err := authenticate(nc, currentUIDString(os.Getuid()), false); err != nil {
		return err
	}
	return nc.SetDeadline(time.Time{}) // clear the deadline for normal operation
}

// ConnectSessionBus dials the session bus (DBUS_SESSION_BUS_ADDRESS) and sends
// Hello, returning a connection with an assigned unique name.
func ConnectSessionBus() (*Conn, error) {
	addr, err := sessionBusAddress()
	if err != nil {
		return nil, err
	}
	return connectAndHello(addr)
}

// ConnectSystemBus dials the system bus and sends Hello.
func ConnectSystemBus() (*Conn, error) {
	return connectAndHello(systemBusAddress())
}

func connectAndHello(addr string) (*Conn, error) {
	c, err := Dial(addr)
	if err != nil {
		return nil, err
	}
	if _, err := c.Hello(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Hello performs the org.freedesktop.DBus.Hello handshake and records the
// unique name the bus assigns.
func (c *Conn) Hello() (string, error) {
	var name string
	if err := c.Object(busName, busPath).Call(busIface+".Hello", 0).Store(&name); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.unique = name
	c.mu.Unlock()
	return name, nil
}

// Names returns the unique name followed by any acquired well-known names.
func (c *Conn) Names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, 1+len(c.names))
	if c.unique != "" {
		out = append(out, c.unique)
	}
	out = append(out, c.names...)
	return out
}

// Close shuts the connection down, failing every pending call.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		<-c.done
		return nil
	}
	c.closed = true
	if c.closeErr == nil {
		c.closeErr = ErrClosed
	}
	c.mu.Unlock()
	err := c.conn.Close()
	<-c.done // wait for the read loop to drain and fail pending calls
	return err
}

// callWithContext sends a method call and waits for its reply. The wait is
// bounded by ctx and, if ctx carries no deadline, by DefaultCallTimeout, so a
// lost or silent reply can never block indefinitely.
func (c *Conn) callWithContext(ctx context.Context, msg *Message) ([]interface{}, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultCallTimeout)
		defer cancel()
	}

	noReply := msg.Flags&FlagNoReplyExpected != 0

	// Assign the serial and, for reply-expecting calls, register the waiter
	// before writing so a fast reply is never missed.
	serial := c.nextSerial()
	msg.SetSerial(serial)

	var pc *pendingCall
	if !noReply {
		pc = &pendingCall{done: make(chan struct{})}
		c.mu.Lock()
		if c.closed {
			err := c.closeErr
			c.mu.Unlock()
			return nil, err
		}
		c.calls[serial] = pc
		c.mu.Unlock()
	}

	if err := c.writeAssigned(msg); err != nil {
		if pc != nil {
			c.mu.Lock()
			delete(c.calls, serial)
			c.mu.Unlock()
		}
		return nil, err
	}

	if noReply {
		return nil, nil
	}

	// A registered call's pc.done is always closed on connection failure (the
	// read loop fails every pending call on exit), so this select needs only
	// the reply and the context deadline; neither branch can block forever.
	select {
	case <-pc.done:
		return pc.body, pc.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.calls, serial)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// writeAssigned writes a message whose serial is already set, under the write
// lock and with a bounded write deadline.
func (c *Conn) writeAssigned(msg *Message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	defer c.conn.SetWriteDeadline(time.Time{})
	return WriteMessage(c.conn, msg, c.order)
}

// readLoop reads and dispatches incoming messages until the connection fails or
// is closed. On exit it fails every pending call.
func (c *Conn) readLoop() {
	defer close(c.done)
	for {
		m, err := ReadMessage(c.conn)
		if err != nil {
			c.mu.Lock()
			if c.closeErr == nil {
				c.closeErr = err
			}
			c.closed = true
			pending := c.calls
			c.calls = map[uint32]*pendingCall{}
			c.mu.Unlock()
			for _, pc := range pending {
				pc.err = c.closeErr
				close(pc.done)
			}
			return
		}
		c.dispatch(m)
	}
}

// dispatch routes one decoded message.
func (c *Conn) dispatch(m *Message) {
	switch m.Type {
	case TypeMethodReturn, TypeError:
		c.deliverReply(m)
	case TypeSignal:
		c.deliverSignal(m)
	case TypeMethodCall:
		// Serve in its own goroutine so a slow handler cannot stall the read
		// loop (and thus block replies to other in-flight calls).
		go c.handleMethodCall(m)
	}
}

// deliverReply matches a reply to its waiting call by REPLY_SERIAL.
func (c *Conn) deliverReply(m *Message) {
	rs, ok := m.Headers[FieldReplySerial]
	if !ok {
		return
	}
	serial, ok := rs.value.(uint32)
	if !ok {
		return
	}
	c.mu.Lock()
	pc := c.calls[serial]
	delete(c.calls, serial)
	c.mu.Unlock()
	if pc == nil {
		return
	}
	if m.Type == TypeError {
		name := ""
		if v, ok := m.Headers[FieldErrorName]; ok {
			name, _ = v.value.(string)
		}
		pc.err = &Error{Name: name, Body: m.Body}
	} else {
		pc.body = m.Body
	}
	close(pc.done)
}

// deliverSignal fans a signal out to every registered channel, dropping it for
// any channel that is not ready so one slow consumer cannot stall the loop.
func (c *Conn) deliverSignal(m *Message) {
	sig := &Signal{Body: m.Body}
	if v, ok := m.Headers[FieldSender]; ok {
		sig.Sender, _ = v.value.(string)
	}
	if v, ok := m.Headers[FieldPath]; ok {
		sig.Path, _ = v.value.(ObjectPath)
	}
	iface, member := "", ""
	if v, ok := m.Headers[FieldInterface]; ok {
		iface, _ = v.value.(string)
	}
	if v, ok := m.Headers[FieldMember]; ok {
		member, _ = v.value.(string)
	}
	sig.Name = iface + "." + member

	c.mu.Lock()
	chans := append([]chan<- *Signal(nil), c.signals...)
	c.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- sig:
		default:
		}
	}
}

// Signal registers ch to receive every signal the connection observes. Combine
// with AddMatch to have the bus actually route signals to this connection.
func (c *Conn) Signal(ch chan<- *Signal) {
	c.mu.Lock()
	c.signals = append(c.signals, ch)
	c.mu.Unlock()
}

// RemoveSignal removes a previously registered signal channel.
func (c *Conn) RemoveSignal(ch chan<- *Signal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, existing := range c.signals {
		if existing == ch {
			c.signals = append(c.signals[:i], c.signals[i+1:]...)
			return
		}
	}
}

// AddMatch installs a match rule on the bus so matching signals are routed to
// this connection.
func (c *Conn) AddMatch(rule string) error {
	return c.Object(busName, busPath).Call(busIface+".AddMatch", 0, rule).Err
}

// RemoveMatch removes a previously installed match rule.
func (c *Conn) RemoveMatch(rule string) error {
	return c.Object(busName, busPath).Call(busIface+".RemoveMatch", 0, rule).Err
}

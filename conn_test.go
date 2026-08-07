// Copyright (c) the go-freedesktop/dbus authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package dbus

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

const testPath = ObjectPath("/org/test/Obj")
const testIface = "org.test.Svc"

// testService is the object exported by the server side of the pipe pair.
type testService struct {
	invoked chan string // records member names of invoked methods
}

func (s *testService) Echo(in string) (string, *Error) {
	s.note("Echo")
	return in, nil
}

func (s *testService) Add(a, b int32) (int32, *Error) {
	s.note("Add")
	return a + b, nil
}

// Double has no trailing *Error return, exercising the no-error method shape.
func (s *testService) Double(x int32) int32 {
	s.note("Double")
	return x * 2
}

func (s *testService) Fail() *Error {
	s.note("Fail")
	return &Error{Name: "org.test.Boom", Body: []interface{}{"boom"}}
}

func (s *testService) Slow() (string, *Error) {
	time.Sleep(200 * time.Millisecond)
	return "late", nil
}

func (s *testService) note(name string) {
	select {
	case s.invoked <- name:
	default:
	}
}

// mockBus impersonates the message bus for client-wrapper coverage.
type mockBus struct{}

func (mockBus) Hello() (string, *Error) { return ":1.99", nil }
func (mockBus) AddMatch(rule string) *Error {
	if rule == "bad" {
		return &Error{Name: "org.freedesktop.DBus.Error.MatchRuleInvalid", Body: []interface{}{"bad"}}
	}
	return nil
}
func (mockBus) RemoveMatch(string) *Error { return nil }
func (mockBus) RequestName(name string, flags uint32) (uint32, *Error) {
	switch name {
	case "org.test.fail":
		return 0, &Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{"no"}}
	case "org.test.queue":
		return NameReplyInQueue, nil
	}
	return NameReplyPrimaryOwner, nil
}
func (mockBus) ReleaseName(string) (uint32, *Error) { return 1, nil }

// pipePair wires two connections back-to-back over an in-process net.Pipe and
// exports a testService and mock bus on the server side. Both are torn down at
// test end so no goroutine leaks.
func pipePair(t *testing.T) (client, server *Conn, svc *testService) {
	t.Helper()
	ca, cb := net.Pipe()
	client = NewConn(ca)
	server = NewConn(cb)
	svc = &testService{invoked: make(chan string, 8)}
	if err := server.Export(svc, testPath, testIface); err != nil {
		t.Fatal(err)
	}
	if err := server.Export(mockBus{}, busPath, busIface); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return
}

// ctxT returns a short-deadline context so no call can hang the suite.
func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCallEcho(t *testing.T) {
	c, _, _ := pipePair(t)
	var out string
	err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), testIface+".Echo", 0, "hello").Store(&out)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Errorf("Echo = %q", out)
	}
}

func TestCallAdd(t *testing.T) {
	c, _, _ := pipePair(t)
	var sum int32
	if err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), testIface+".Add", 0, int32(2), int32(40)).Store(&sum); err != nil {
		t.Fatal(err)
	}
	if sum != 42 {
		t.Errorf("Add = %d", sum)
	}
}

func TestCallNoErrorReturn(t *testing.T) {
	c, _, _ := pipePair(t)
	var got int32
	if err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), testIface+".Double", 0, int32(21)).Store(&got); err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Errorf("Double = %d", got)
	}
}

func TestCallRemoteError(t *testing.T) {
	c, _, _ := pipePair(t)
	call := c.Object("org.test", testPath).CallWithContext(ctxT(t), testIface+".Fail", 0)
	if call.Err == nil {
		t.Fatal("expected remote error")
	}
	de, ok := call.Err.(*Error)
	if !ok || de.Name != "org.test.Boom" || de.Error() != "boom" {
		t.Errorf("error = %#v", call.Err)
	}
	// Store must surface the call error too.
	var x string
	if err := call.Store(&x); err == nil {
		t.Error("Store should return the call error")
	}
}

func TestCallBareMemberLookup(t *testing.T) {
	c, _, _ := pipePair(t)
	var out string
	// No interface component: the server matches the member across interfaces.
	if err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), "Echo", 0, "bare").Store(&out); err != nil {
		t.Fatal(err)
	}
	if out != "bare" {
		t.Errorf("bare Echo = %q", out)
	}
}

func TestCallUnknownObjectMethodAndArgs(t *testing.T) {
	c, _, _ := pipePair(t)
	obj := c.Object("org.test", testPath)
	// Unknown object path.
	if err := c.Object("org.test", "/no/such").CallWithContext(ctxT(t), testIface+".Echo", 0, "x").Err; err == nil {
		t.Error("expected UnknownObject")
	}
	// Unknown method on a known interface.
	if err := obj.CallWithContext(ctxT(t), testIface+".Nope", 0).Err; err == nil {
		t.Error("expected UnknownMethod")
	}
	// Wrong argument count.
	if err := obj.CallWithContext(ctxT(t), testIface+".Echo", 0).Err; err == nil {
		t.Error("expected InvalidArgs (count)")
	}
	// Wrong argument type (int where string expected).
	if err := obj.CallWithContext(ctxT(t), testIface+".Echo", 0, int32(1)).Err; err == nil {
		t.Error("expected InvalidArgs (type)")
	}
}

func TestSignalEmitReceive(t *testing.T) {
	c, server, _ := pipePair(t)
	ch := make(chan *Signal, 1)
	c.Signal(ch)
	if err := server.Emit(testPath, testIface+".Pinged", int32(7), "note"); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-ch:
		if sig.Path != testPath || sig.Name != testIface+".Pinged" {
			t.Errorf("signal = %+v", sig)
		}
		if len(sig.Body) != 2 || sig.Body[0].(int32) != 7 || sig.Body[1].(string) != "note" {
			t.Errorf("signal body = %#v", sig.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
	// Removing the channel stops delivery.
	c.RemoveSignal(ch)
	if err := server.Emit(testPath, testIface+".Pinged"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Error("signal delivered after RemoveSignal")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHelloAndNames(t *testing.T) {
	c, _, _ := pipePair(t)
	name, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	if name != ":1.99" {
		t.Errorf("Hello = %q", name)
	}
	names := c.Names()
	if len(names) != 1 || names[0] != ":1.99" {
		t.Errorf("Names = %v", names)
	}
}

func TestHelloError(t *testing.T) {
	// A pipe pair whose server exports NO bus object: Hello must return an
	// error rather than hang.
	ca, cb := net.Pipe()
	c := NewConn(ca)
	s := NewConn(cb)
	t.Cleanup(func() { c.Close(); s.Close() })
	if _, err := c.Hello(); err == nil {
		t.Error("expected Hello error against a bus-less peer")
	}
}

func TestRequestReleaseName(t *testing.T) {
	c, _, _ := pipePair(t)
	code, err := c.RequestName("org.test.app", 0)
	if err != nil || code != NameReplyPrimaryOwner {
		t.Fatalf("RequestName = %d, %v", code, err)
	}
	if got := c.Names(); len(got) != 1 || got[0] != "org.test.app" {
		t.Errorf("Names after request = %v", got)
	}
	// Queued name is not recorded as owned.
	if code, _ := c.RequestName("org.test.queue", 0); code != NameReplyInQueue {
		t.Errorf("queue code = %d", code)
	}
	if len(c.Names()) != 1 {
		t.Errorf("queued name should not be recorded: %v", c.Names())
	}
	// Failed request surfaces the error.
	if _, err := c.RequestName("org.test.fail", 0); err == nil {
		t.Error("expected RequestName error")
	}
	// Release.
	if _, err := c.ReleaseName("org.test.app"); err != nil {
		t.Fatal(err)
	}
	if len(c.Names()) != 0 {
		t.Errorf("Names after release = %v", c.Names())
	}
}

func TestAddRemoveMatch(t *testing.T) {
	c, _, _ := pipePair(t)
	if err := c.AddMatch("type='signal'"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveMatch("type='signal'"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddMatch("bad"); err == nil {
		t.Error("expected AddMatch error for a bad rule")
	}
}

func TestIntrospect(t *testing.T) {
	c, _, _ := pipePair(t)
	var xml string
	if err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<!DOCTYPE node",
		`<interface name="org.test.Svc">`,
		`<method name="Echo">`,
		`<arg direction="in" type="s"/>`,
		`<arg direction="out" type="s"/>`,
		`<interface name="org.freedesktop.DBus.Introspectable">`,
		`<interface name="org.freedesktop.DBus.Properties">`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("introspection missing %q\n%s", want, xml)
		}
	}
}

func TestIntrospectChildNodes(t *testing.T) {
	c, server, _ := pipePair(t)
	// Export a deeper object so the parent lists it as a child node.
	if err := server.Export(&testService{invoked: make(chan string, 1)}, testPath+"/child", testIface); err != nil {
		t.Fatal(err)
	}
	var xml string
	if err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `<node name="child"/>`) {
		t.Errorf("expected child node in introspection:\n%s", xml)
	}
}

func TestPeerInterface(t *testing.T) {
	c, _, _ := pipePair(t)
	obj := c.Object("org.test", testPath)
	// Ping returns no body and no error.
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Peer.Ping", 0).Err; err != nil {
		t.Errorf("Ping: %v", err)
	}
	// GetMachineId returns a string (possibly empty on this host).
	var id string
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Peer.GetMachineId", 0).Store(&id); err != nil {
		t.Errorf("GetMachineId: %v", err)
	}
	// Unknown Peer method errors.
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Peer.Nope", 0).Err; err == nil {
		t.Error("expected unknown Peer method error")
	}
}

func TestProperties(t *testing.T) {
	c, server, _ := pipePair(t)
	if err := server.ExportProperties(testPath, testIface, map[string]*Prop{
		"Name": {Value: "widget", Writable: true},
		"Size": {Value: int32(5), Writable: false},
	}); err != nil {
		t.Fatal(err)
	}
	obj := c.Object("org.test", testPath)

	// Get.
	v, err := obj.GetProperty(testIface + ".Name")
	if err != nil || v.Value().(string) != "widget" {
		t.Fatalf("GetProperty Name = %v, %v", v, err)
	}
	// Set writable.
	if err := obj.SetProperty(testIface+".Name", "gadget"); err != nil {
		t.Fatal(err)
	}
	v, _ = obj.GetProperty(testIface + ".Name")
	if v.Value().(string) != "gadget" {
		t.Errorf("after Set, Name = %v", v.Value())
	}
	// Set read-only fails.
	if err := obj.SetProperty(testIface+".Size", int32(9)); err == nil {
		t.Error("expected read-only error")
	}
	// GetAll.
	var all map[string]Variant
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Properties.GetAll", 0, testIface).Store(&all); err != nil {
		t.Fatal(err)
	}
	if all["Name"].Value().(string) != "gadget" || all["Size"].Value().(int32) != 5 {
		t.Errorf("GetAll = %#v", all)
	}
	// Unknown property.
	if _, err := obj.GetProperty(testIface + ".Nope"); err == nil {
		t.Error("expected unknown-property error")
	}
	// Introspection reflects properties.
	var xml string
	_ = obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml)
	if !strings.Contains(xml, `<property name="Name" type="s" access="readwrite"/>`) ||
		!strings.Contains(xml, `<property name="Size" type="i" access="read"/>`) {
		t.Errorf("introspection properties missing:\n%s", xml)
	}
}

func TestPropertiesErrors(t *testing.T) {
	c, server, _ := pipePair(t)
	_ = server.ExportProperties(testPath, testIface, map[string]*Prop{"P": {Value: int32(1)}})
	obj := c.Object("org.test", testPath)
	// Get with wrong arg count.
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Properties.Get", 0, testIface).Err; err == nil {
		t.Error("expected Get arg-count error")
	}
	// Set with wrong arg count.
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Properties.Set", 0, testIface).Err; err == nil {
		t.Error("expected Set arg-count error")
	}
	// Set with a non-variant value.
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Properties.Set", 0, testIface, "P", "novariant").Err; err == nil {
		t.Error("expected Set non-variant error")
	}
	// GetAll with wrong arg count.
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Properties.GetAll", 0).Err; err == nil {
		t.Error("expected GetAll arg-count error")
	}
	// Unknown Properties method.
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Properties.Nope", 0).Err; err == nil {
		t.Error("expected unknown Properties method error")
	}
	// Set unknown property.
	if err := obj.CallWithContext(ctxT(t), "org.freedesktop.DBus.Properties.Set", 0, testIface, "Nope", MakeVariant(int32(1))).Err; err == nil {
		t.Error("expected Set unknown-property error")
	}
}

func TestEmitPropertiesChanged(t *testing.T) {
	c, server, _ := pipePair(t)
	ch := make(chan *Signal, 1)
	c.Signal(ch)
	if err := server.EmitPropertiesChanged(testPath, testIface, map[string]interface{}{"Name": "z"}); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-ch:
		if !strings.HasSuffix(sig.Name, "PropertiesChanged") {
			t.Errorf("signal name = %q", sig.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no PropertiesChanged signal")
	}
}

func TestNoReplyExpected(t *testing.T) {
	c, _, svc := pipePair(t)
	call := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), testIface+".Echo", FlagNoReplyExpected, "fire")
	if call.Err != nil || call.Body != nil {
		t.Errorf("no-reply call = %#v", call)
	}
	// The server still ran the method.
	select {
	case name := <-svc.invoked:
		if name != "Echo" {
			t.Errorf("invoked %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("method not invoked for no-reply call")
	}
}

func TestExportErrorsAndUnexport(t *testing.T) {
	c, server, _ := pipePair(t)
	if err := server.Export(&testService{invoked: make(chan string, 1)}, "bad-path", testIface); err == nil {
		t.Error("expected invalid-path Export error")
	}
	if err := server.ExportProperties("bad-path", testIface, nil); err == nil {
		t.Error("expected invalid-path ExportProperties error")
	}
	// Unexport the interface, after which calls fail.
	if err := server.Export(nil, testPath, testIface); err != nil {
		t.Fatal(err)
	}
	if err := c.Object("org.test", testPath).
		CallWithContext(ctxT(t), testIface+".Echo", 0, "x").Err; err == nil {
		t.Error("expected error after unexport")
	}
	// Unexporting a non-existent path/iface is a no-op.
	if err := server.Export(nil, "/never/exported", "no.iface"); err != nil {
		t.Errorf("unexport of missing path: %v", err)
	}
}

func TestEmitBadName(t *testing.T) {
	c, _, _ := pipePair(t)
	if err := c.Emit(testPath, "nomember"); err == nil {
		t.Error("expected Emit bad-name error")
	}
}

func TestCallTimeoutContext(t *testing.T) {
	// A live pipe whose far end never answers: the call must hit the context
	// deadline, never hang.
	ca, cb := net.Pipe()
	c := NewConn(ca)
	drainSilently(cb)
	t.Cleanup(func() { c.Close(); cb.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := c.Object("org.test", testPath).CallWithContext(ctx, testIface+".Echo", 0, "x").Err
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestDefaultCallTimeout(t *testing.T) {
	ca, cb := net.Pipe()
	c := NewConn(ca)
	drainSilently(cb)
	t.Cleanup(func() { c.Close(); cb.Close() })

	old := DefaultCallTimeout
	DefaultCallTimeout = 100 * time.Millisecond
	defer func() { DefaultCallTimeout = old }()

	// context.Background() carries no deadline, so the hard default applies.
	err := c.Object("org.test", testPath).Call(testIface+".Echo", 0, "x").Err
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded from default timeout, got %v", err)
	}
}

func TestCloseFailsPendingCall(t *testing.T) {
	ca, cb := net.Pipe()
	c := NewConn(ca)
	drainSilently(cb)
	defer cb.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Object("org.test", testPath).Call(testIface+".Echo", 0, "x").Err
	}()
	time.Sleep(50 * time.Millisecond) // let the call register and block
	c.Close()
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected pending call to fail on Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call did not unblock after Close")
	}
}

func TestCallAfterClose(t *testing.T) {
	c, _, _ := pipePair(t)
	c.Close()
	// Reply-expecting call after close.
	if err := c.Object("org.test", testPath).Call(testIface+".Echo", 0, "x").Err; err == nil {
		t.Error("expected error calling on a closed connection")
	}
	// No-reply call after close.
	if err := c.Object("org.test", testPath).Call(testIface+".Echo", FlagNoReplyExpected, "x").Err; err == nil {
		t.Error("expected error on no-reply call after close")
	}
	// Double close is a no-op.
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestLateReplyDropped(t *testing.T) {
	c, _, _ := pipePair(t)
	// Call Slow with a short deadline so the waiter is removed before the reply
	// arrives; the late reply must be dropped without panicking.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Object("org.test", testPath).CallWithContext(ctx, testIface+".Slow", 0).Err; err != context.DeadlineExceeded {
		t.Errorf("expected deadline, got %v", err)
	}
	time.Sleep(300 * time.Millisecond) // allow the late reply to arrive and be dropped
}

// drainSilently reads and discards everything from c without ever replying.
func drainSilently(c net.Conn) {
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()
}

func TestDeliverReplyEdgeCases(t *testing.T) {
	c, _, _ := pipePair(t)
	// Reply with no REPLY_SERIAL header -> ignored.
	c.deliverReply(&Message{Type: TypeMethodReturn, Headers: map[HeaderField]Variant{}})
	// REPLY_SERIAL not a uint32 -> ignored.
	c.deliverReply(&Message{Type: TypeMethodReturn, Headers: map[HeaderField]Variant{
		FieldReplySerial: MakeVariant("nope"),
	}})
	// Unknown serial -> ignored.
	c.deliverReply(&Message{Type: TypeMethodReturn, Headers: map[HeaderField]Variant{
		FieldReplySerial: MakeVariant(uint32(999999)),
	}})
	// dispatch with an out-of-range type is a no-op.
	c.dispatch(&Message{Type: TypeInvalid})
}

func TestDeliverSignalMinimalHeaders(t *testing.T) {
	c, _, _ := pipePair(t)
	ch := make(chan *Signal, 1)
	c.Signal(ch)
	// A signal with no interface/member/path/sender still delivers.
	c.deliverSignal(&Message{Type: TypeSignal, Headers: map[HeaderField]Variant{}})
	select {
	case sig := <-ch:
		if sig.Name != "." {
			t.Errorf("empty signal name = %q", sig.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("minimal signal not delivered")
	}
}

func TestHandleMethodCallSenderDestination(t *testing.T) {
	// Feed a method_call carrying a SENDER so the reply gets a matching
	// DESTINATION, and read the reply off the raw peer end.
	ca, cb := net.Pipe()
	server := NewConn(ca)
	_ = server.Export(&testService{invoked: make(chan string, 1)}, testPath, testIface)
	t.Cleanup(func() { server.Close(); cb.Close() })
	_ = cb.SetDeadline(time.Now().Add(3 * time.Second))

	call := &Message{
		Type:   TypeMethodCall,
		serial: 55,
		Headers: map[HeaderField]Variant{
			FieldPath:   MakeVariant(testPath),
			FieldMember: MakeVariant("Echo"),
			FieldSender: MakeVariant(":1.7"),
		},
		Body: []interface{}{"hi"},
	}
	// Write the call from the raw side into the server.
	if err := WriteMessage(cb, call, binary.LittleEndian); err != nil {
		t.Fatal(err)
	}
	reply, err := ReadMessage(cb)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Type != TypeMethodReturn {
		t.Fatalf("reply type = %v", reply.Type)
	}
	dest, _ := headerString(reply, FieldDestination)
	if dest != ":1.7" {
		t.Errorf("reply destination = %q, want :1.7", dest)
	}
}

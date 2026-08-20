package wlcore

import (
	"encoding/binary"
	"testing"
)

func TestGeneratedDescriptorFactoryClearsListener(t *testing.T) {
	c := newConn(nil)
	callback := CallbackInterface.New(NewProxyBase(2, 1, c))
	c.Register(callback)

	called := false
	callback.SetListener(CallbackListener{Done: func(uint32) { called = true }})
	c.Destroy(callback)
	if err := callback.Dispatch(opEvtCallbackDone, c.newDecoder(NewEncoder().Uint32(1).Bytes())); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if called {
		t.Fatal("descriptor-created callback listener should be cleared on destroy")
	}
}

func TestGeneratedRequestReturnClearsListener(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	surface := newSurface(2, 1, c)
	c.Register(surface)

	callback, err := surface.Frame()
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}
	called := false
	callback.SetListener(CallbackListener{Done: func(uint32) { called = true }})
	c.Destroy(callback)
	if err := callback.Dispatch(opEvtCallbackDone, c.newDecoder(NewEncoder().Uint32(1).Bytes())); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if called {
		t.Fatal("request-created callback listener should be cleared on destroy")
	}
}

func TestGeneratedEventNewIDClearsListener(t *testing.T) {
	c := newConn(nil)
	device := newDataDevice(2, 1, c)
	c.Register(device)

	var offer *DataOffer
	device.SetListener(DataDeviceListener{DataOffer: func(got *DataOffer) { offer = got }})
	if err := device.Dispatch(opEvtDataDeviceDataOffer, c.newDecoder(NewEncoder().ID(serverIDBase+1).Bytes())); err != nil {
		t.Fatalf("Dispatch data_offer: %v", err)
	}
	if offer == nil {
		t.Fatal("data_offer listener did not receive the generated proxy")
	}

	called := false
	offer.SetListener(DataOfferListener{Offer: func(string) { called = true }})
	c.Destroy(offer)
	if err := offer.Dispatch(opEvtDataOfferOffer, c.newDecoder(NewEncoder().String("text/plain").Bytes())); err != nil {
		t.Fatalf("Dispatch offer: %v", err)
	}
	if called {
		t.Fatal("event-created data offer listener should be cleared on destroy")
	}
}

func TestGeneratedNullableObjectRequestEncodesZero(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	surface := newSurface(2, 1, c)
	c.Register(surface)

	if err := surface.Attach(nil, 7, 9); err != nil {
		t.Fatalf("Surface.Attach(nil, 7, 9): %v", err)
	}

	buf := make([]byte, 64)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatalf("read Surface.Attach request: %v", err)
	}
	if n != 20 {
		t.Fatalf("Surface.Attach request bytes = %d, want 20", n)
	}
	body := buf[8:n]
	if got := binary.NativeEndian.Uint32(body[0:4]); got != 0 {
		t.Errorf("Surface.Attach buffer object ID = %d, want 0", got)
	}
	if got := int32(binary.NativeEndian.Uint32(body[4:8])); got != 7 {
		t.Errorf("Surface.Attach x = %d, want 7", got)
	}
	if got := int32(binary.NativeEndian.Uint32(body[8:12])); got != 9 {
		t.Errorf("Surface.Attach y = %d, want 9", got)
	}
}

func TestGeneratedDestructorEventDestroysProxy(t *testing.T) {
	client, server := newSocketpairConns(t)
	c := newConn(client)
	callback := newCallback(serverIDBase+1, 1, c)
	c.Register(callback)

	called := false
	callback.SetListener(CallbackListener{Done: func(uint32) { called = true }})

	if _, err := server.Write(rawMessage(serverIDBase+1, opEvtCallbackDone, NewEncoder().Uint32(1).Bytes())); err != nil {
		t.Fatalf("write wl_callback.done: %v", err)
	}
	if err := c.Dispatch(); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !called {
		t.Fatal("Done listener was not invoked")
	}
	if c.Lookup(serverIDBase+1) != nil {
		t.Fatal("wl_callback.done (a destructor event) should have destroyed the proxy -- Conn.Lookup still finds it")
	}
}

func TestGeneratedVersionedDestructorRejectsBeforeClearingListener(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	keyboard := newKeyboard(2, 1, c)
	c.Register(keyboard)
	keyboard.SetListener(KeyboardListener{Key: func(uint32, uint32, uint32, KeyboardKeyState) {}})

	if err := keyboard.Release(); err == nil {
		t.Fatal("Keyboard.Release() at version 1 = nil, want version error")
	}
	if keyboard.listener.Key == nil {
		t.Fatal("Keyboard.Release() below since version cleared listener")
	}
}

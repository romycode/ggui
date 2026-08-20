package wlcore

import "testing"

func TestGeneratedDescriptorFactoryClearsListener(t *testing.T) {
	c := newConn(nil)
	callback := CallbackInterface.New(NewProxyBase(2, 1, c))
	c.Register(callback)

	called := false
	callback.SetListener(CallbackListener{Done: func(uint32) { called = true }})
	c.destroy(callback)
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
	c.destroy(callback)
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
	c.destroy(offer)
	if err := offer.Dispatch(opEvtDataOfferOffer, c.newDecoder(NewEncoder().String("text/plain").Bytes())); err != nil {
		t.Fatalf("Dispatch offer: %v", err)
	}
	if called {
		t.Fatal("event-created data offer listener should be cleared on destroy")
	}
}

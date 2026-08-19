package wlcore

import "testing"

func TestCallbackDispatchDone(t *testing.T) {
	c := newConn(nil)
	cb := newCallback(2, 1, c)
	var got uint32
	called := false
	cb.SetListener(CallbackListener{Done: func(data uint32) {
		called = true
		got = data
	}})

	body := NewEncoder().Uint32(1234).Bytes()
	if err := cb.Dispatch(opEvtCallbackDone, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !called {
		t.Fatal("el listener Done no se llamó")
	}
	if got != 1234 {
		t.Fatalf("callback_data = %d, want 1234", got)
	}
}

func TestCallbackDispatchWithoutListenerDoesNotPanic(t *testing.T) {
	c := newConn(nil)
	cb := newCallback(2, 1, c)
	body := NewEncoder().Uint32(1).Bytes()
	if err := cb.Dispatch(opEvtCallbackDone, c.newDecoder(body)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
}

func TestCallbackDispatchUnknownOpcode(t *testing.T) {
	c := newConn(nil)
	cb := newCallback(2, 1, c)
	if err := cb.Dispatch(99, c.newDecoder(nil)); err == nil {
		t.Fatal("opcode desconocido debería devolver error")
	}
}

func TestCallbackClearListener(t *testing.T) {
	c := newConn(nil)
	cb := newCallback(2, 1, c)
	called := false
	cb.SetListener(CallbackListener{Done: func(uint32) { called = true }})
	cb.clearListener()

	body := NewEncoder().Uint32(1).Bytes()
	cb.Dispatch(opEvtCallbackDone, c.newDecoder(body))
	if called {
		t.Fatal("tras clearListener, Done no debería llamarse")
	}
}

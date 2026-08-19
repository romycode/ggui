package wlcore

import (
	"errors"
	"testing"
)

func TestCloseIsIdempotentAndSetsErrClosed(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)

	c.Close()
	c.Close() // no debe hacer panic ni sobreescribir el error

	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("Err() = %v, want ErrClosed", c.Err())
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done() debería estar cerrado tras Close()")
	}
}

func TestCloseDoesNotMaskEarlierFatalError(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)

	sentinel := errors.New("boom")
	c.fatal(sentinel)
	c.Close()

	if !errors.Is(c.Err(), sentinel) {
		t.Fatalf("Err() = %v, want el primer error (%v), no ErrClosed", c.Err(), sentinel)
	}
}

func TestOnErrorSetsCallback(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	called := false
	c.OnError(func(objectID, code uint32, msg string) { called = true })
	if c.onError == nil {
		t.Fatal("OnError() no fijó c.onError")
	}
	c.onError(1, 2, "x")
	if !called {
		t.Fatal("el callback fijado por OnError no se invocó")
	}
}

package wlcore

import (
	"errors"
	"testing"
)

func TestCloseIsIdempotentAndSetsErrClosed(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)

	c.Close()
	c.Close() // must not panic or overwrite the error

	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("Err() = %v, want ErrClosed", c.Err())
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done() should be closed after Close()")
	}
}

func TestCloseDoesNotMaskEarlierFatalError(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)

	sentinel := errors.New("boom")
	c.fatal(sentinel)
	c.Close()

	if !errors.Is(c.Err(), sentinel) {
		t.Fatalf("Err() = %v, want the first error (%v), not ErrClosed", c.Err(), sentinel)
	}
}

func TestOnErrorSetsCallback(t *testing.T) {
	client, _ := newSocketpairConns(t)
	c := newConn(client)
	called := false
	c.OnError(func(objectID, code uint32, msg string) { called = true })
	if c.onError == nil {
		t.Fatal("OnError() did not set c.onError")
	}
	c.onError(1, 2, "x")
	if !called {
		t.Fatal("the callback set by OnError was not invoked")
	}
}

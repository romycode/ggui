package wlcore

import (
	"errors"
	"sync"
	"testing"
	"time"
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

// Close is how another goroutine stops a loop that is parked in a read, so it
// has to be safe against that read. The connection's terminal error is
// written by Close and read by the goroutine that was dispatching; without
// synchronization that is a data race the detector reports, and a torn read
// of an interface value in the worst case.
func TestCloseFromAnotherGoroutineWhileDispatching(t *testing.T) {
	c, _ := newDispatchTestConn(t)

	got := make(chan error, 1)
	go func() { got <- c.Dispatch() }()
	time.Sleep(30 * time.Millisecond) // let it park in the read

	c.Close()

	select {
	case err := <-got:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("Dispatch returned %v, want ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch never returned after Close")
	}
	if !errors.Is(c.Err(), ErrClosed) {
		t.Errorf("Err() = %v, want ErrClosed", c.Err())
	}
}

// Err may be read while the connection is still up and while another
// goroutine ends it: before Done it is nil, after it is the error, and it is
// never a race.
func TestErrIsSafeToReadWhileTheConnectionEnds(t *testing.T) {
	c, _ := newDispatchTestConn(t)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = c.Err()
			}
		}
	}()

	time.Sleep(10 * time.Millisecond)
	c.Close()
	close(stop)
	wg.Wait()

	if !errors.Is(c.Err(), ErrClosed) {
		t.Errorf("Err() = %v, want ErrClosed", c.Err())
	}
}

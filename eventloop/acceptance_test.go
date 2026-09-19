package eventloop

import (
	"encoding/binary"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/internal/wltest"
	"github.com/romycode/ggui/wayland/xdgshell"
)

// opWmBasePong is xdg_wm_base.pong, the request that answers a ping.
const opWmBasePong = 3

// These two tests are the reason the package exists: neither goroutine may
// hold the other up. Each freezes one side and checks that the other keeps
// going.

// A UI stuck painting must not stop the socket from being served. The proof
// that matters is xdg_wm_base.ping: the compositor marks a client that does
// not pong in time as unresponsive, and painting is exactly what could make
// it late.
func TestAcceptanceStuckUIDoesNotBlockTheSocket(t *testing.T) {
	const uiBusyFor = 500 * time.Millisecond
	const pings = 200

	conn, comp := wltest.NewConn(t)
	inbox := NewInbox()
	l, _ := startLoop(t, conn, nil)

	// What a real client does: bind xdg_wm_base and answer its pings from
	// the listener, on the Wayland goroutine, while telling the UI.
	l.Post(func() {
		reg, err := conn.Display().GetRegistry()
		if err != nil {
			t.Errorf("GetRegistry: %v", err)
			return
		}
		wm, err := reg.Bind(1, 1, xdgshell.WmBaseInterface)
		if err != nil {
			t.Errorf("Bind: %v", err)
			return
		}
		wm.SetListener(xdgshell.WmBaseListener{Ping: func(serial uint32) {
			if err := wm.Pong(serial); err != nil {
				t.Errorf("Pong: %v", err)
			}
			inbox.Push(Event{Kind: EvFrameDone, Time: serial})
		}})
	})
	comp.ReadRequest() // wl_display.get_registry
	_, _, bind := comp.ReadRequest()
	wmID := binary.NativeEndian.Uint32(bind[len(bind)-4:]) // the new_id is last

	// The UI: woken by the first event, then busy for half a second.
	drained := make(chan int, 1)
	go func() {
		<-inbox.Signal()
		time.Sleep(uiBusyFor)
		drained <- len(inbox.Drain(nil))
	}()

	start := time.Now()
	var worst time.Duration
	for serial := uint32(1); serial <= pings; serial++ {
		sent := time.Now()
		comp.Send(wmID, 0 /* xdg_wm_base.ping */, serial)

		objectID, opcode, body := comp.ReadRequest()
		if objectID != wmID || opcode != opWmBasePong || binary.NativeEndian.Uint32(body) != serial {
			t.Fatalf("ping %d: got object %d opcode %d, want pong on %d", serial, objectID, opcode, wmID)
		}
		if d := time.Since(sent); d > worst {
			worst = d
		}
	}

	if elapsed := time.Since(start); elapsed >= uiBusyFor {
		t.Fatalf("the pings took %v, so the UI was no longer stuck: the test proves nothing", elapsed)
	}
	if worst > 50*time.Millisecond {
		t.Errorf("slowest pong took %v while the UI was stuck, want under 50ms", worst)
	}

	// Nothing was dropped on the way: every ping told the UI, and the UI
	// finds all of them when it finally looks.
	select {
	case n := <-drained:
		if n != pings {
			t.Errorf("the UI found %d events, want %d", n, pings)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the UI never woke")
	}
}

// A socket that stops accepting writes must not stop the UI. The Wayland
// goroutine may sit in a blocked write for as long as the compositor
// stalls, but everything the UI does has to keep returning: Post queues and
// carries on, and what was queued runs, in order, once the socket drains.
func TestAcceptanceStuckSocketDoesNotBlockTheUI(t *testing.T) {
	const queued = 10_000

	conn, comp := wltest.NewConn(t)

	// A small send buffer, so a compositor that stops reading fills it after
	// a handful of messages instead of a few hundred kilobytes.
	rc, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	rc.Control(func(fd uintptr) {
		if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF, 4096); err != nil {
			t.Errorf("SO_SNDBUF: %v", err)
		}
	})

	l, _ := startLoop(t, conn, nil)

	// The Wayland goroutine writes until the socket is full and blocks. The
	// compositor is not reading yet.
	var started, finished atomic.Bool
	l.Post(func() {
		started.Store(true)
		for i := 0; i < 5000; i++ {
			if _, err := conn.Display().Sync(); err != nil {
				return
			}
		}
		finished.Store(true)
	})
	for !started.Load() {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if finished.Load() {
		t.Fatal("the writes never blocked, so the test proves nothing")
	}

	// The UI goes on regardless.
	var ran atomic.Int64
	order := make([]int, 0, queued) // appended on the Wayland goroutine only
	start := time.Now()
	for i := 0; i < queued; i++ {
		l.Post(func() {
			order = append(order, i)
			ran.Add(1)
		})
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("queueing %d closures took %v with the socket stuck, want it not to wait", queued, d)
	}
	if n := ran.Load(); n != 0 {
		t.Fatalf("%d closures ran while the loop was blocked in a write", n)
	}

	// The compositor wakes up and reads everything.
	go io.Copy(io.Discard, comp.Conn())

	deadline := time.Now().Add(10 * time.Second)
	for ran.Load() < queued {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d closures ran after the socket drained", ran.Load(), queued)
		}
		time.Sleep(time.Millisecond)
	}
	if !finished.Load() {
		t.Error("the blocked write never completed")
	}
	for i, got := range order {
		if got != i {
			t.Fatalf("closure %d ran at position %d: order was lost", got, i)
		}
	}
}

// The same guarantee with the real UI in it. The Wayland goroutine is stuck
// in a write, so no frame callback can come back; the UI still paints its
// first frame, hands it to the loop, and goes on running background results
// and invalidations, none of which wait for the socket. What it handed over
// is presented once the socket drains.
func TestAcceptanceUIKeepsRunningWhileTheSocketIsStuck(t *testing.T) {
	conn, comp := wltest.NewConn(t)
	rc, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	rc.Control(func(fd uintptr) {
		unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF, 4096)
	})
	l, _ := startLoop(t, conn, nil)

	var started, finished atomic.Bool
	l.Post(func() {
		started.Store(true)
		for i := 0; i < 5000; i++ {
			if _, err := conn.Display().Sync(); err != nil {
				return
			}
		}
		finished.Store(true)
	})
	for !started.Load() {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if finished.Load() {
		t.Fatal("the writes never blocked, so the test proves nothing")
	}

	var painted, presented atomic.Int64
	ui := NewUI()
	startUI(t, ui, Handler{Paint: func(uint32) (bool, bool) {
		painted.Add(1)
		l.Post(func() { presented.Add(1) }) // stands for attach, damage, frame, commit
		return true, true
	}})
	ui.Do(ui.Invalidate)
	ui.Push(Event{Kind: EvConfigure, Width: 100, Height: 100})

	deadline := time.Now().Add(2 * time.Second)
	for painted.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the UI did not paint while the socket was stuck")
		}
		time.Sleep(time.Millisecond)
	}

	// Background results keep landing on the UI goroutine.
	var results atomic.Int64
	start := time.Now()
	for i := 0; i < 1000; i++ {
		ui.Do(func() {
			results.Add(1)
			ui.Invalidate()
		})
	}
	for results.Load() < 1000 {
		if time.Since(start) > time.Second {
			t.Fatalf("only %d of 1000 results were applied while the socket was stuck", results.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if n := presented.Load(); n != 0 {
		t.Fatalf("%d frames were presented while the loop was blocked in a write", n)
	}
	if n := painted.Load(); n != 1 {
		t.Errorf("the UI painted %d frames with no frame callback able to arrive, want 1", n)
	}

	go io.Copy(io.Discard, comp.Conn())
	deadline = time.Now().Add(5 * time.Second)
	for presented.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the frame the UI handed over was never presented after the socket drained")
		}
		time.Sleep(time.Millisecond)
	}
}

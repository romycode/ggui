package window

import (
	"fmt"
	"log"
	"math"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/romycode/ggui/canvas"
	"github.com/romycode/ggui/eventloop"
	"github.com/romycode/ggui/wayland/wlcore"
)

const (
	// frameCount is the depth of the pool. Two is enough for input-driven
	// redraws: the compositor releases the previous buffer at its next
	// composite. Each frame is painted whole, so the contents a buffer had
	// two frames ago are never relied on and nothing is preserved.
	frameCount = 2

	// bytesPerPixel is the size of one ARGB8888 pixel.
	bytesPerPixel = 4

	// defaultScale is physical pixels per logical unit. It stays at 1 until
	// HiDPI is wired in; pool.scale is the one place that has to change, and
	// the pool's users never see the physical size.
	defaultScale = 1
)

// frame is one pooled shm buffer and everything that has to stay alive for
// as long as the compositor may read it. It straddles the two goroutines: the
// UI maps the memory and owns the canvas, the Wayland goroutine creates the
// wl_buffer. Every field belongs to the UI goroutine.
type frame struct {
	// buf is set, on the UI goroutine, once the Wayland goroutine has created
	// the wl_buffer. Until then the frame cannot be handed out: there would
	// be nothing to give the compositor.
	buf *wlcore.Buffer
	// data is the mapping, kept for Munmap. It must stay mapped for the whole
	// life of cv, which borrows it and never copies it. It is nil once the
	// frame is dead.
	data []byte
	// cv draws into data as ARGB8888 words. Built once per frame. It is nil
	// once the frame is dead, so a stale pointer fails as a nil dereference
	// and not as a fault on unmapped memory.
	cv *canvas.Canvas
	// busy means the frame is attached and the compositor has not sent
	// wl_buffer.release for it yet. Drawing into it now would tear. The
	// presenter sets it; released clears it.
	busy bool
	// dead means a resize (or close) replaced this frame, possibly while its
	// wl_buffer was still being created, so a buffer that arrives for it must
	// be destroyed and not adopted.
	dead bool
	// failed means the Wayland goroutine could not make this frame's buffer.
	// It will never have one, so it is never free.
	failed bool
}

// pool is the double-buffered shm pool of one window, split across the two
// goroutines like everything else in this package. Its methods run on the UI
// goroutine and only reserve memory and touch bookkeeping; every request goes
// out as a closure through post, and what the Wayland goroutine has to report
// comes back through do (state changes) and push (release events).
//
// A resize replaces the pool entirely, because a wl_buffer cannot change
// size. It is not safe for concurrent use.
type pool struct {
	// shm belongs to the Wayland goroutine: the UI goroutine never calls it,
	// and only createBuffer, which runs there, uses it.
	shm *wlcore.Shm

	// post queues a closure for the Wayland goroutine (Loop.Post). do queues
	// one for the UI goroutine (UI.Do). push sends the UI an event
	// (UI.Push). The pool takes them as plain funcs so a test needs no
	// connection.
	post func(func())
	do   func(func())
	push func(eventloop.Event)

	// scale is physical pixels per logical unit. The canvas draws in logical
	// units and the wl_buffer has the physical size.
	scale float32

	// frames is the current pool, nil until ensure and after close.
	frames [frameCount]*frame

	// create makes the wl_buffer over a frame's memory. It runs on the
	// Wayland goroutine, takes ownership of the descriptor (it must close
	// it), and reports back through do. It is createBuffer in production and
	// a stub in tests.
	create func(f *frame, fd, size int, width, height int32)
	// destroy destroys a wl_buffer. It runs on the Wayland goroutine.
	destroy func(*wlcore.Buffer)
	// adopted is called on the UI goroutine each time a frame gets its
	// buffer, so the owner can tell the frame clock one is free again. It is
	// never nil.
	adopted func()
	// failed is called on the UI goroutine when the Wayland goroutine could not
	// make a frame's buffer, so the owner can fail the window instead of
	// waiting for a buffer that will never come. It is never nil.
	failed func(error)
}

// newPool returns an empty pool that creates its buffers over shm. shm is
// only ever used on the Wayland goroutine, through post. It builds nothing
// until ensure.
func newPool(shm *wlcore.Shm, post, do func(func()), push func(eventloop.Event)) *pool {
	p := &pool{
		shm:     shm,
		post:    post,
		do:      do,
		push:    push,
		scale:   defaultScale,
		adopted: func() {},
	}
	p.create = p.createBuffer
	p.destroy = func(buf *wlcore.Buffer) {
		if err := buf.Destroy(); err != nil {
			log.Printf("window: destroy buffer: %v", err)
		}
	}
	return p
}

// ensure makes the pool match logical size width by height, and does nothing
// when it already does. A change replaces the pool: the old frames die, the
// buffers the Wayland goroutine had already made for them are destroyed, and
// the ones still being made are destroyed when they arrive. Each new frame's
// buffer is asked for through post and is not usable until adopt has run.
//
// It fails, leaving the current pool untouched and posting nothing, when the
// size cannot be built or memory cannot be reserved.
func (p *pool) ensure(width, height int32) error {
	pixelWidth, pixelHeight, size, err := p.geometry(width, height)
	if err != nil {
		return err
	}
	if f := p.frames[0]; f != nil && !f.dead &&
		f.cv.Width() == int(width) && f.cv.Height() == int(height) &&
		f.cv.PixelWidth() == int(pixelWidth) && f.cv.PixelHeight() == int(pixelHeight) {
		return nil
	}

	var next [frameCount]*frame
	fds := [frameCount]int{-1, -1}
	for i := range next {
		f, fd, err := newFrame(width, height, pixelWidth, pixelHeight, size, p.scale)
		if err != nil {
			for j, made := range next {
				if fds[j] >= 0 {
					_ = unix.Close(fds[j])
				}
				if made != nil {
					_ = unix.Munmap(made.data)
				}
			}
			return err
		}
		next[i], fds[i] = f, fd
	}

	p.retire(true)
	p.frames = next
	for i, f := range next {
		fd := fds[i]
		p.post(func() { p.create(f, fd, size, pixelWidth, pixelHeight) })
	}
	return nil
}

// geometry validates a logical size and returns the physical pixel size and
// the byte size of one buffer, rounding a fractional scale up so the buffer
// always covers the surface.
func (p *pool) geometry(width, height int32) (pixelWidth, pixelHeight int32, size int, err error) {
	if width <= 0 || height <= 0 || math.IsNaN(float64(p.scale)) || math.IsInf(float64(p.scale), 0) || p.scale <= 0 {
		return 0, 0, 0, fmt.Errorf("invalid buffer geometry %dx%d at scale %v", width, height, p.scale)
	}
	pw64 := int64(math.Ceil(float64(width) * float64(p.scale)))
	ph64 := int64(math.Ceil(float64(height) * float64(p.scale)))
	if pw64 <= 0 || ph64 <= 0 || pw64 > math.MaxInt32 || ph64 > math.MaxInt32 || pw64 > math.MaxInt32/(ph64*bytesPerPixel) {
		return 0, 0, 0, fmt.Errorf("buffer geometry %dx%d at scale %v is too large", width, height, p.scale)
	}
	return int32(pw64), int32(ph64), int(pw64 * ph64 * bytesPerPixel), nil
}

// newFrame reserves one frame's memory: a sealed memfd, a mapping that
// outlives the call, and a Canvas over that mapping. The returned descriptor
// belongs to the caller, which hands it to create.
func newFrame(width, height, pixelWidth, pixelHeight int32, size int, scale float32) (*frame, int, error) {
	fd, err := unix.MemfdCreate("ggui-window", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, -1, fmt.Errorf("create buffer memory: %w", err)
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		_ = unix.Close(fd)
		return nil, -1, fmt.Errorf("size buffer memory: %w", err)
	}
	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = unix.Close(fd)
		return nil, -1, fmt.Errorf("map buffer memory: %w", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK); err != nil {
		_ = unix.Munmap(data)
		_ = unix.Close(fd)
		return nil, -1, fmt.Errorf("seal buffer memory: %w", err)
	}

	// canvas takes []uint32 and mmap returns []byte, and Go has no safe
	// conversion between slice element types, so they are bridged here once
	// per buffer. The mapping is page aligned, so the view is aligned too.
	// This reinterprets in host byte order while wl_shm defines argb8888 as
	// little endian; they agree on every architecture Wayland runs on.
	pixels := unsafe.Slice((*uint32)(unsafe.Pointer(&data[0])), size/bytesPerPixel)
	cv, err := canvas.New(canvas.Buffer{
		Pixels: pixels,
		Width:  int(pixelWidth),
		Height: int(pixelHeight),
		Stride: int(pixelWidth),
	}, int(width), int(height), scale)
	if err != nil {
		_ = unix.Munmap(data)
		_ = unix.Close(fd)
		return nil, -1, fmt.Errorf("create canvas: %w", err)
	}
	return &frame{data: data, cv: cv}, fd, nil
}

// createBuffer makes the wl_buffer over f's memory. It runs on the Wayland
// goroutine, called through post, and reports back to the UI through do, the
// only way a frame's state may change. It owns fd and closes it: wl_shm keeps
// its own reference through create_pool, and the mapping keeps ours.
func (p *pool) createBuffer(f *frame, fd, size int, width, height int32) {
	defer unix.Close(fd)

	shmPool, err := p.shm.CreatePool(fd, int32(size))
	if err != nil {
		p.createFailedOn(f, fmt.Errorf("create shm pool: %w", err))
		return
	}
	// The wl_shm_pool has served its purpose once the buffer exists; the
	// buffer keeps the storage alive on its own.
	defer func() {
		if err := shmPool.Destroy(); err != nil {
			log.Printf("window: destroy shm pool: %v", err)
		}
	}()

	buf, err := shmPool.CreateBuffer(0, width, height, width*bytesPerPixel, wlcore.ShmFormatArgb8888)
	if err != nil {
		p.createFailedOn(f, fmt.Errorf("create buffer: %w", err))
		return
	}
	buf.SetListener(wlcore.BufferListener{Release: func() {
		p.push(eventloop.Event{Kind: eventloop.EvBufferRelease, Buffer: buf})
	}})
	p.do(func() { p.adopt(f, buf) })
}

// createFailedOn reports, from the Wayland goroutine, that f's buffer could not
// be made, unless the connection is gone. Only the UI goroutine may change a
// frame, so the report travels through do like every other one.
func (p *pool) createFailedOn(f *frame, err error) {
	if p.shm.Conn().Err() != nil {
		// The connection ended first, by a close from either side, and that is
		// why the request could not be made. It is not a failure of the pool,
		// and Run reports how the connection ended on its own.
		return
	}
	log.Printf("window: %v", err)
	p.do(func() { p.createFailure(f, err) })
}

// createFailure records, on the UI goroutine, that f will never have a
// buffer, and tells the owner. A frame of a pool that was replaced meanwhile is
// nobody's business any more: the new pool has its own.
func (p *pool) createFailure(f *frame, err error) {
	if f.dead {
		return
	}
	f.failed = true
	p.failed(err)
}

// usable reports whether the pool has a frame that is, or will be, able to
// hold a picture: it is not dead and its buffer did not fail. It is false for
// an empty pool, which is what a window whose first size could not be built
// has.
func (p *pool) usable() bool {
	for _, f := range p.frames {
		if f != nil && !f.dead && !f.failed {
			return true
		}
	}
	return false
}

// adopt gives f the buffer the Wayland goroutine made for it, on the UI
// goroutine. A buffer for a dead frame, that is one whose pool was replaced
// while the buffer was being made, is destroyed and never adopted, and so is a
// second buffer for a frame that already has one: a frame has at most one
// buffer and the compositor may be reading it.
func (p *pool) adopt(f *frame, buf *wlcore.Buffer) {
	if f.dead || f.buf != nil {
		p.post(func() { p.destroy(buf) })
		return
	}
	f.buf = buf
	p.adopted()
}

// free returns a frame that can be painted and presented: it has its
// wl_buffer, the compositor is not reading it, and it is not dead. It returns
// nil when there is none.
func (p *pool) free() *frame {
	for _, f := range p.frames {
		if f != nil && f.buf != nil && !f.busy && !f.dead {
			return f
		}
	}
	return nil
}

// released handles wl_buffer.release: the compositor is done reading buf, so
// the frame that owns it may be painted again. A buffer the pool does not own,
// such as one of a replaced pool, is ignored.
func (p *pool) released(buf *wlcore.Buffer) {
	if buf == nil {
		return // frames still waiting for their buffer have a nil buf too
	}
	for _, f := range p.frames {
		if f != nil && f.buf == buf {
			f.busy = false
			return
		}
	}
}

// close tears the pool down: every frame dies and is unmapped. destroy asks
// for the buffers that exist to be destroyed through post, and is false when
// the connection is already gone and there is nothing to destroy them with.
// Destroying a wl_buffer the compositor still holds is allowed, and so is
// unmapping, since the compositor maps the memory itself. It is safe to call
// twice.
func (p *pool) close(destroy bool) {
	p.retire(destroy)
}

// retire kills every frame of the current pool and empties it.
func (p *pool) retire(destroy bool) {
	for i, f := range p.frames {
		if f == nil {
			continue
		}
		f.dead = true
		if destroy && f.buf != nil {
			buf := f.buf
			p.post(func() { p.destroy(buf) })
		}
		if f.data != nil {
			if err := unix.Munmap(f.data); err != nil {
				log.Printf("window: unmap buffer: %v", err)
			}
			f.data = nil
		}
		f.cv = nil
		p.frames[i] = nil
	}
}

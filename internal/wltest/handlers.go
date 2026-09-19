package wltest

import (
	"encoding/binary"
	"time"

	"github.com/romycode/ggui/wayland/wlcore"
)

// handle applies one request. Nothing the client can send may panic here:
// a malformed message is recorded and the loop carries on.
func (s *Server) handle(r *wireReader, id uint32, opcode uint16, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}

	iface, ok := s.objects[id]
	if !ok {
		s.errorf("request opcode %d on unknown object %d", opcode, id)
		return
	}
	s.requests = append(s.requests, iface+"."+requestName(iface, opcode))

	a := newArgs(body)
	switch iface {
	case "wl_display":
		s.handleDisplay(a, opcode)
	case "wl_registry":
		s.handleRegistry(a, opcode)
	case "wl_compositor":
		s.handleCompositor(a, opcode)
	case "wl_shm":
		s.handleShm(r, a, opcode, id)
	case "wl_shm_pool":
		s.handleShmPool(a, opcode, id)
	case "wl_buffer":
		s.handleBuffer(opcode, id)
	case "wl_surface":
		s.handleSurface(a, opcode, id)
	case "wl_seat":
		s.handleSeat(a, opcode, id)
	case "wl_keyboard":
		if opcode == reqKeyboardRelease {
			s.releaseKeyboard(id)
		}
	case "wl_pointer":
		if opcode == reqPointerRelease {
			s.releasePointer(id)
		}
	case "xdg_wm_base":
		s.handleWmBase(a, opcode, id)
	case "xdg_surface":
		s.handleXdgSurface(a, opcode, id)
	case "xdg_toplevel":
		s.handleToplevel(a, opcode, id)
	case "wl_region":
		if opcode == 0 {
			s.deleteID(id)
		}
	}

	if a.err != nil {
		s.errorf("%s.%s: malformed request: %v", iface, requestName(iface, opcode), a.err)
	}
}

func (s *Server) handleDisplay(a *args, opcode uint16) {
	switch opcode {
	case reqDisplaySync:
		cb := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[cb] = "wl_callback"
		s.send(cb, evtCallbackDone, s.nowMS())
		s.deleteID(cb)
	case reqDisplayGetRegistry:
		reg := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[reg] = "wl_registry"
		for _, g := range s.globals {
			e := wlcore.NewEncoder().Uint32(g.name).String(g.iface).Uint32(g.version)
			s.sendEncoded(reg, evtRegistryGlobal, e, -1)
		}
	}
}

func (s *Server) handleRegistry(a *args, opcode uint16) {
	if opcode != reqRegistryBind {
		return
	}
	name := a.uint32()
	iface := a.string()
	version := a.uint32()
	newID := a.uint32()
	if a.err != nil {
		return
	}

	var found *globalEntry
	for i := range s.globals {
		if s.globals[i].name == name {
			found = &s.globals[i]
		}
	}
	if found == nil {
		s.errorf("wl_registry.bind of global %d (%s), which was never announced", name, iface)
		return
	}
	if found.iface != iface {
		s.errorf("wl_registry.bind of global %d as %s, announced as %s", name, iface, found.iface)
		return
	}
	if version > found.version {
		s.errorf("wl_registry.bind of %s at version %d, announced at %d", iface, version, found.version)
		return
	}
	s.objects[newID] = iface

	switch iface {
	case "xdg_wm_base":
		if s.wmBase == 0 {
			s.wmBase = newID
		}
	case "wl_shm":
		s.send(newID, evtShmFormat, shmFormatArgb8888)
		s.send(newID, evtShmFormat, shmFormatXrgb8888)
	case "wl_seat":
		s.seat, s.seatVersion = newID, version
		s.send(newID, evtSeatCapabilities, seatCapabilityPointer|seatCapabilityKeyboard)
		if version >= 2 {
			s.sendEncoded(newID, evtSeatName, wlcore.NewEncoder().String("wltest-seat"), -1)
		}
	}
}

func (s *Server) handleCompositor(a *args, opcode uint16) {
	switch opcode {
	case reqCompositorCreateSurface:
		id := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[id] = "wl_surface"
		if s.surface == 0 {
			s.surface = id
		}
	case reqCompositorCreateRegion:
		id := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[id] = "wl_region"
	}
}

func (s *Server) handleSurface(a *args, opcode uint16, id uint32) {
	switch opcode {
	case reqSurfaceDestroy:
		if s.surface == id {
			s.surface = 0
		}
		s.deleteID(id)
	case reqSurfaceAttach:
		buffer := a.uint32()
		a.int32() // x
		a.int32() // y
		if a.err != nil {
			return
		}
		s.attach(buffer)
	case reqSurfaceFrame:
		cb := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[cb] = "wl_callback"
		s.pendingFrames = append(s.pendingFrames, cb)
	case reqSurfaceCommit:
		s.commit()
	case reqSurfaceDamage, reqSurfaceDamageBuffer:
		a.int32()
		a.int32()
		a.int32()
		a.int32()
	}
}

// attach records the pending buffer and checks the one rule a window layer
// gets wrong: an xdg_surface may not be given a buffer before its first
// configure has been acked.
func (s *Server) attach(buffer uint32) {
	if s.xdgSurface != 0 && !s.acked {
		s.errorf("wl_surface.attach before the first xdg_surface.ack_configure")
	}
	if buffer != 0 {
		if b := s.buffers[buffer]; b == nil || !b.live {
			s.errorf("wl_surface.attach of object %d, which is not a live wl_buffer", buffer)
		}
	}
	s.pendingAttach, s.pendingBuffer = true, buffer
}

// commit applies the pending surface state, presents whatever buffer is
// current and answers the frame callbacks the client asked for.
func (s *Server) commit() {
	if s.pendingAttach {
		s.currentBuffer, s.pendingAttach = s.pendingBuffer, false
	}
	frames := s.pendingFrames
	s.pendingFrames = nil

	switch {
	case s.currentBuffer != 0:
		s.present(s.currentBuffer)
	case !s.configureSent && s.xdgSurface != 0 && !s.opts.ManualConfigure:
		// The commit with no buffer that opens the window: a real
		// compositor answers it with the first configure sequence.
		s.configureToplevel(s.opts.Width, s.opts.Height)
		s.configureSurface()
	}

	for _, cb := range frames {
		s.scheduleFrame(cb)
	}
}

// present snapshots the committed buffer and applies the release policy.
func (s *Server) present(id uint32) {
	b := s.buffers[id]
	if b == nil || !b.live {
		s.errorf("wl_surface.commit of object %d, which is not a live wl_buffer", id)
		return
	}
	s.deliver(Commit{Width: b.width, Height: b.height, Pixels: b.snapshot(), At: time.Now()})

	switch s.opts.ReleaseMode {
	case ReleaseImmediately:
		s.send(id, evtBufferRelease)
	case ReleaseOnNextCommit:
		if s.heldBuffer != 0 && s.heldBuffer != id {
			if prev := s.buffers[s.heldBuffer]; prev != nil && prev.live {
				s.send(s.heldBuffer, evtBufferRelease)
			}
		}
		s.heldBuffer = id
	}
}

func (s *Server) handleWmBase(a *args, opcode uint16, id uint32) {
	switch opcode {
	case reqWmBaseDestroy:
		if s.wmBase == id {
			s.wmBase = 0
		}
		s.deleteID(id)
	case reqWmBaseGetXdgSurface:
		newID := a.uint32()
		surface := a.uint32()
		if a.err != nil {
			return
		}
		if s.objects[surface] != "wl_surface" {
			s.errorf("xdg_wm_base.get_xdg_surface on object %d, which is not a wl_surface", surface)
		}
		s.objects[newID] = "xdg_surface"
		if s.xdgSurface == 0 {
			s.xdgSurface = newID
		}
	case reqWmBasePong:
		a.uint32()
	}
}

func (s *Server) handleXdgSurface(a *args, opcode uint16, id uint32) {
	switch opcode {
	case reqXdgSurfaceDestroy:
		if s.xdgSurface == id {
			s.xdgSurface, s.acked, s.configureSent = 0, false, false
		}
		s.deleteID(id)
	case reqXdgSurfaceGetToplevel:
		newID := a.uint32()
		if a.err != nil {
			return
		}
		s.objects[newID] = "xdg_toplevel"
		if s.toplevel == 0 {
			s.toplevel = newID
		}
	case reqXdgSurfaceSetWindowGeometry:
		a.int32()
		a.int32()
		a.int32()
		a.int32()
	case reqXdgSurfaceAckConfigure:
		serial := a.uint32()
		if a.err != nil {
			return
		}
		if !s.liveSerials[serial] {
			s.errorf("xdg_surface.ack_configure with serial %d, which was never sent or was already acked", serial)
			return
		}
		delete(s.liveSerials, serial)
		s.acked = true
	}
}

func (s *Server) handleToplevel(a *args, opcode uint16, id uint32) {
	switch opcode {
	case reqToplevelDestroy:
		if s.toplevel == id {
			s.toplevel = 0
		}
		s.deleteID(id)
	case reqToplevelSetTitle:
		s.title = a.string()
	case reqToplevelSetAppID:
		s.appID = a.string()
	}
}

// configureToplevel sends xdg_toplevel.configure. The caller holds the
// mutex.
func (s *Server) configureToplevel(width, height int32, states ...uint32) {
	if s.toplevel == 0 {
		s.errorf("xdg_toplevel.configure with no xdg_toplevel")
		return
	}
	arr := make([]byte, 4*len(states))
	for i, st := range states {
		binary.NativeEndian.PutUint32(arr[4*i:], st)
	}
	e := wlcore.NewEncoder().Int32(width).Int32(height).Array(arr)
	s.sendEncoded(s.toplevel, evtToplevelConfigure, e, -1)
}

// configureSurface ends a configure sequence and returns its serial. The
// caller holds the mutex.
func (s *Server) configureSurface() uint32 {
	if s.xdgSurface == 0 {
		s.errorf("xdg_surface.configure with no xdg_surface")
		return 0
	}
	serial := s.nextSerial()
	s.liveSerials[serial] = true
	s.configureSent = true
	s.send(s.xdgSurface, evtXdgSurfaceConfigure, serial)
	return serial
}

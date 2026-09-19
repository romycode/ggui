package wltest

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// shmPool is one wl_shm_pool: the descriptor the client sent over
// SCM_RIGHTS and the read-only shared mapping of it. The mapping outlives
// wl_shm_pool.destroy, exactly as the protocol says — the memory stays
// while buffers carved out of it are alive — and is only released when the
// test ends.
type shmPool struct {
	id        uint32
	fd        int
	size      int
	data      []byte
	destroyed bool
}

func (p *shmPool) close() {
	if p.data != nil {
		unix.Munmap(p.data)
		p.data = nil
	}
	if p.fd >= 0 {
		unix.Close(p.fd)
		p.fd = -1
	}
}

// remap replaces the mapping after wl_shm_pool.resize.
func (p *shmPool) remap(size int) error {
	data, err := unix.Mmap(p.fd, 0, size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return err
	}
	if p.data != nil {
		unix.Munmap(p.data)
	}
	p.data, p.size = data, size
	return nil
}

// bufferState is one wl_buffer carved out of a pool.
type bufferState struct {
	id                    uint32
	pool                  *shmPool
	offset                int32
	width, height, stride int32
	format                uint32
	live                  bool
}

// snapshot copies the buffer out of the mapping as a tight width*height
// slice, dropping whatever padding the stride leaves at the end of a row.
// A buffer whose pool went away reads as zeros rather than panicking.
func (b *bufferState) snapshot() []uint32 {
	px := make([]uint32, int(b.width)*int(b.height))
	if b.pool == nil || b.pool.data == nil {
		return px
	}
	for y := 0; y < int(b.height); y++ {
		row := int(b.offset) + y*int(b.stride)
		for x := 0; x < int(b.width); x++ {
			off := row + 4*x
			if off < 0 || off+4 > len(b.pool.data) {
				return px
			}
			px[y*int(b.width)+x] = binary.NativeEndian.Uint32(b.pool.data[off:])
		}
	}
	return px
}

func (s *Server) handleShm(r *wireReader, a *args, opcode uint16, id uint32) {
	switch opcode {
	case reqShmCreatePool:
		newID := a.uint32()
		size := a.int32()
		if a.err != nil {
			return
		}
		fd, ok := r.popFD()
		if !ok {
			s.errorf("wl_shm.create_pool arrived without a file descriptor")
			return
		}
		if size <= 0 {
			s.errorf("wl_shm.create_pool with size %d", size)
			unix.Close(fd)
			return
		}
		data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			s.errorf("wl_shm.create_pool: mapping the client's fd: %v", err)
			unix.Close(fd)
			return
		}
		s.objects[newID] = "wl_shm_pool"
		s.pools[newID] = &shmPool{id: newID, fd: fd, size: int(size), data: data}
	case reqShmRelease:
		s.deleteID(id)
	}
}

func (s *Server) handleShmPool(a *args, opcode uint16, id uint32) {
	pool := s.pools[id]
	switch opcode {
	case reqShmPoolCreateBuffer:
		newID := a.uint32()
		offset := a.int32()
		width := a.int32()
		height := a.int32()
		stride := a.int32()
		format := a.uint32()
		if a.err != nil {
			return
		}
		if pool == nil {
			s.errorf("wl_shm_pool.create_buffer on pool %d, which the fake never mapped", id)
			return
		}
		if width <= 0 || height <= 0 || stride < width*4 {
			s.errorf("wl_shm_pool.create_buffer of %dx%d with stride %d", width, height, stride)
			return
		}
		if end := int(offset) + int(stride)*int(height); offset < 0 || end > pool.size {
			s.errorf("wl_shm_pool.create_buffer needs %d bytes of a %d byte pool", end, pool.size)
			return
		}
		s.objects[newID] = "wl_buffer"
		s.buffers[newID] = &bufferState{
			id: newID, pool: pool, offset: offset,
			width: width, height: height, stride: stride,
			format: format, live: true,
		}
		s.bufferOrder = append(s.bufferOrder, newID)
	case reqShmPoolDestroy:
		if pool != nil {
			// The mapping stays: the buffers carved out of it are still
			// the compositor's to read.
			pool.destroyed = true
		}
		s.deleteID(id)
	case reqShmPoolResize:
		size := a.int32()
		if a.err != nil {
			return
		}
		if pool == nil {
			return
		}
		if int(size) < pool.size {
			s.errorf("wl_shm_pool.resize from %d to %d, which shrinks the pool", pool.size, size)
			return
		}
		if err := pool.remap(int(size)); err != nil {
			s.errorf("wl_shm_pool.resize: %v", err)
		}
	}
}

func (s *Server) handleBuffer(opcode uint16, id uint32) {
	if opcode != reqBufferDestroy {
		return
	}
	if b := s.buffers[id]; b != nil {
		b.live = false
	}
	if s.currentBuffer == id {
		s.currentBuffer = 0
	}
	if s.heldBuffer == id {
		s.heldBuffer = 0
	}
	s.deleteID(id)
}

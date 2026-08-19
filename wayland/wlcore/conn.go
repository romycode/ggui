package wlcore

import (
	"net"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	// El id 1 es wl_display: no lo asigna NewID, lo construye Connect().
	displayID = 1
	// Reparto del espacio de ids: abajo el cliente, arriba el servidor.
	maxClientID  = 0xFEFFFFFF
	serverIDBase = 0xFF000000
)

type Conn struct {
	sock *net.UnixConn

	// objects, nextID y freeIDs, igual que in/fds/oob: sin lock, porque
	// toda la API de Conn se usa desde una única goroutine (ver "Quién
	// bombea" en wlcore.md).
	objects map[uint32]Proxy
	nextID  uint32
	freeIDs []uint32 // ids devueltos por wl_display.delete_id

	display *Display // objeto 1, construido en Connect()
	onError func(objectID, code uint32, msg string)

	errOnce sync.Once
	done    chan struct{}
	err     error

	in  readBuf // bytes leídos, sin procesar
	fds fdQueue // fds recibidos, aún no consumidos por Proxy.Dispatch
	oob []byte  // buffer de ancillary data, reutilizado en cada recvmsg
}

func newConn(sock *net.UnixConn) *Conn {
	return &Conn{
		sock:    sock,
		objects: make(map[uint32]Proxy),
		nextID:  displayID, // el primer NewID() devuelve 2
		in:      readBuf{data: make([]byte, readBufSize)},
		oob:     make([]byte, unix.CmsgSpace(4*maxFDsPerRead)),
		done:    make(chan struct{}),
	}
}

func (c *Conn) Register(p Proxy) {
	c.objects[p.ID()] = p
}

func (c *Conn) Lookup(id uint32) Proxy {
	return c.objects[id]
}

// NewID recicla antes de crecer: sin esto, una sesión larga con frame
// callbacks agota el espacio de ids en unas horas.
func (c *Conn) NewID() uint32 {
	if n := len(c.freeIDs); n > 0 {
		id := c.freeIDs[n-1]
		c.freeIDs = c.freeIDs[:n-1]
		return id
	}
	c.nextID++
	return c.nextID
}

// Display devuelve el objeto 1, ya registrado por Connect().
func (c *Conn) Display() *Display { return c.display }

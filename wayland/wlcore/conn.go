package wlcore

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

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

// fatal fija el error terminal la primera vez, cierra done y el socket —
// eso desbloquea a quien esté parado en ReadMsgUnix.
func (c *Conn) fatal(err error) {
	if err == nil {
		return
	}
	c.errOnce.Do(func() {
		c.err = err
		close(c.done)
		c.sock.Close()
	})
}

func (c *Conn) Done() <-chan struct{} { return c.done }
func (c *Conn) Err() error            { return c.err } // válido tras Done()

// destroy es el runtime que hay detrás del Destroy() generado: siempre
// limpia el listener, y libera el id ya mismo si era del servidor — el de
// cliente no se libera hasta que llegue delete_id.
func (c *Conn) destroy(p Proxy) {
	p.clearListener()
	if id := p.ID(); id >= serverIDBase {
		delete(c.objects, id)
	}
}

// release libera un id de cliente. Solo lo llama el handler interno de
// wl_display.delete_id.
func (c *Conn) release(id uint32) {
	delete(c.objects, id)
	c.freeIDs = append(c.freeIDs, id)
}

type ProtocolError struct {
	ObjectID uint32
	Code     uint32
	Message  string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("wayland: objeto %d: %s (code %d)", e.ObjectID, e.Message, e.Code)
}

func dial() (*net.UnixConn, error) {
	if s, ok := os.LookupEnv("WAYLAND_SOCKET"); ok {
		fd, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("wlcore: WAYLAND_SOCKET inválido: %q", s)
		}
		// Quitarla del entorno SIEMPRE: si no, cualquier proceso hijo que
		// lancemos hereda la variable y comparte el stream con nosotros.
		os.Unsetenv("WAYLAND_SOCKET")
		syscall.CloseOnExec(fd)

		f := os.NewFile(uintptr(fd), "wayland")
		defer f.Close() // FileConn duplica el fd y pone el suyo en no-bloqueante
		nc, err := net.FileConn(f)
		if err != nil {
			return nil, err
		}
		uc, ok := nc.(*net.UnixConn)
		if !ok {
			nc.Close()
			return nil, fmt.Errorf("wlcore: WAYLAND_SOCKET no es un socket unix")
		}
		return uc, nil
	}

	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	path := name
	if !filepath.IsAbs(name) {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			return nil, errors.New("wlcore: ni WAYLAND_SOCKET ni XDG_RUNTIME_DIR")
		}
		path = filepath.Join(dir, name)
	}
	return net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
}

// Connect monta el objeto 1 a mano — es el único que no pasa por NewID() —
// y engancha el listener interno antes de arrancar el loop, para no perder
// un error temprano.
func Connect() (*Conn, error) {
	sock, err := dial()
	if err != nil {
		return nil, err
	}
	c := newConn(sock)

	c.display = &Display{ProxyBase: NewProxyBase(displayID, 1, c)}
	c.Register(c.display)
	c.display.SetListener(DisplayListener{
		Error: func(objectID, code uint32, msg string) {
			if c.onError != nil {
				c.onError(objectID, code, msg)
			}
			c.fatal(&ProtocolError{ObjectID: objectID, Code: code, Message: msg})
		},
		DeleteID: c.release,
	})

	return c, nil
}

var ErrClosed = errors.New("wlcore: conexión cerrada por el cliente")

// Close cierra la conexión. Idempotente, y seguro llamarlo con la conexión
// ya caída: el errOnce se queda con el primer error, así que un Close() de
// defer no enmascara el fallo real.
func (c *Conn) Close() error {
	c.fatal(ErrClosed)
	return nil
}

// OnError registra el callback que se invoca cuando el compositor manda
// wl_display.error. Sustituye por completo al listener de Display, que el
// usuario no debe tocar. Como cualquier SetListener, hay que llamarlo
// antes del primer Dispatch()/Run() para no perderse un error temprano.
func (c *Conn) OnError(f func(objectID, code uint32, msg string)) { c.onError = f }

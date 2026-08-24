# waygenerator — generador de bindings desde los XML de protocolo
 
Qué emite el generador y con qué convenciones, leyendo `wayland.xml`,
`wayland-protocols` y `wlr-layer-shell`. El runtime sobre el que se apoya
está en `wlcore.md`.
 
## Contrato con el runtime
 
Esto es **todo** lo que el código generado puede usar de `wlcore`. Si una
plantilla necesita algo que no está en esta lista, o falta algo en el
contrato o la plantilla está metiéndose donde no le toca. El generador no
sabe que existen `readBuf`, `fdQueue` ni el bombeo del socket.
 
| Pieza | Firma | Para qué |
|---|---|---|
| `Proxy` | `ID() uint32`, `Dispatch(uint16, *Decoder) error`, `clearListener()` | lo que satisface cada tipo generado; el tipo generado implementa `ID` y `clearListener` **por embebido**, solo escribe `Dispatch` |
| `ProxyBase` | embebido; `ID()`, `Version()`, `Conn()`, campo `OnClear func()` | estado común de todo proxy; `clearListener()` (no exportada, la usa `Conn.destroy`) vive aquí y ejecuta `OnClear` |
| listener | `OnClear`, que fija el constructor generado con `func() { x.listener = XListener{} }` | poner el listener a su cero al destruir; es la única pieza de `clearListener` específica de cada tipo |
| constructor | `NewProxyBase(id, version uint32, c *Conn) ProxyBase` | crear el proxy hijo heredando versión |
| ids | `Conn.NewID() uint32` | id de cliente, ya reciclado |
| registro | `Conn.Register(Proxy)`, `Conn.Lookup(uint32) Proxy` | alta y resolución de object ids |
| destrucción | `Conn.destroy(Proxy)` | lo que emite `Destroy()` para un request `type="destructor"`; limpia el listener y libera ya el id si es de servidor (ver ciclo de vida en `wlcore.md`) |
| envío | `Conn.Send(objectID uint32, opcode uint16, *Encoder, fds ...int) error` | un request |
| tipo | `Fixed int32`, `.Float64()`, `FixedFromFloat64(float64)` | punto fijo 24.8; firma de cualquier arg `fixed` |
| serialización | `NewEncoder()`, `.ID/.Uint32/.Int32/.Fixed/.String/.StringOpt/.Array` | argumentos de request; `.StringOpt(*string)` solo para `allow-null="true"` |
| deserialización | `.ID/.Uint32/.Int32/.Fixed/.String/.StringOpt/.Array/.FD` + `.Err()` | argumentos de evento; `.StringOpt() *string` solo para `allow-null="true"` |
| descriptor | `Interface[T]{Name, MaxVersion, New func(ProxyBase) T}` | metadatos + factory de cada interfaz; lo consumen `Bind` y el `new_id` en eventos |
| fds huérfanos | `DropFD(int)` | cerrar un fd decodificado que no se entrega a ningún listener |
 
**Las dos costuras**, que son contrato de verdad y no viven limpiamente a un
lado ni al otro:
 
1. **Versionado** — negociar la versión es del runtime; emitir las guardas
   `since=` y propagar la versión al hijo es del generador.
2. **`new_id` en eventos** — el `Dispatch` generado llama a `Register` con un
   id que asignó el servidor.
Cada una está descrita entera en un solo sitio, nunca duplicada: (1) abajo en
Versionado, (2) abajo en casos especiales.
 
Hubo una tercera, "fds por opcode": una tabla generada por interfaz y opcode
para que un objeto zombi supiera cuántos fds consumir de un evento sin
listener. Sobraba. El zombi sigue en `objects`, así que su `Dispatch`
generado se ejecuta igual y ya sabe leer los fds de ese evento — es
literalmente la misma información que llevaba la tabla, sacada del mismo XML.
Lo que sí queda como regla del generador es cerrarlos cuando no hay a quién
entregárselos: ver la plantilla de `Dispatch`.
 
## Estructura del proyecto
 
Módulo: **`github.com/romycode/ggui`** (`go.mod` en el root del repo).
 
```
ggui/
├── go.mod
├── wayland/
│   ├── wlcore/              # infra a mano + protocolo core (wayland.xml)
│   │   ├── conn.go
│   │   ├── proxy.go
│   │   ├── fixed.go
│   │   ├── wire.go
│   │   ├── registry.go      # Bind genérico + Interface[T], a mano
│   │   ├── compositor.gen.go
│   │   ├── surface.gen.go
│   │   └── ...
│   ├── xdgshell/             # extensión: xdg-shell.xml
│   │   └── *.gen.go
│   └── wlrlayershell/        # extensión: wlr-layer-shell
│       └── *.gen.go
├── cmd/
│   └── waygenerator/         # CLI del generador
│       └── main.go
└── protocols/                # XML fuente que lee waygenerator
    ├── wayland.xml
    ├── xdg-shell.xml
    └── wlr-layer-shell.xml
```
 
`wayland/` es solo un directorio organizativo que agrupa `wlcore` y las
extensiones — no es un paquete Go, no tiene ficheros propios.
`cmd/waygenerator` es la CLI que parsea todo lo que hay en `protocols/` y
escribe los `*.gen.go` correspondientes en cada paquete de `wayland/`.
 
**Dependencias**: de momento, solo extended stdlib (`golang.org/x/...`),
concretamente `x/sys/unix` (para `memfd_create`, `mmap`, `SCM_RIGHTS`). Sin
librerías de terceros.

**Rutas de import**, las que van a aparecer en los `import` de los
`*.gen.go` de las extensiones:

```
github.com/romycode/ggui/wayland/wlcore
github.com/romycode/ggui/wayland/xdgshell
github.com/romycode/ggui/wayland/wlrlayershell
```
 
## Naming conventions
 
- **`wlcore` contiene tanto la infraestructura a mano como los objetos
  generados del protocolo core (`wayland.xml`).** No son cosas separadas:
  `wl_compositor`, `wl_surface`, `wl_shm`, etc. son parte del núcleo, no una
  extensión, así que viven en el mismo paquete que `Conn`/`Proxy`/`Fixed`.
  Consecuencia directa: **el código generado del core no se cualifica a sí
  mismo**. Dentro de `wlcore` se escribe `ProxyBase`, `NewEncoder()`,
  `*Output`; el prefijo `wlcore.` solo aparece en `xdgshell` y
  `wlrlayershell`.
  **Extensiones** (`xdg-shell.xml`, `wlr-layer-shell`) sí van en su propio
  paquete cada una (`xdgshell`, `wlrlayershell`), porque conceptualmente no
  son el núcleo — importan `wlcore` para la infra y para referenciar tipos
  del core (p. ej. `xdg_wm_base.get_xdg_surface` recibe un `wlcore.Surface`).
- **Separación real dentro de `wlcore`: por fichero, no por paquete.**
  Riesgo evitado: que el generador machaque código escrito a mano al
  regenerar. Convención:
  - Escrito a mano, el generador nunca lo toca: `wlcore/conn.go`,
    `wlcore/proxy.go`, `wlcore/fixed.go`, `wlcore/wire.go`,
    `wlcore/registry.go`.
  - Generado desde `wayland.xml`, el generador lo sobreescribe sin
    problema: `wlcore/compositor.gen.go`, `wlcore/surface.gen.go`, etc.
    (un fichero `*.gen.go` por interfaz, o agrupados — a decidir).
    Primera línea `// Code generated by waygenerator. DO NOT EDIT.`
    (convención estándar de Go, reconocida por tooling).
  - **Bootstrap circular, asumido:** `conn.go` referencia `Display` y
    `DisplayListener` (`Connect()`, `destroy`/`release`), y `registry.go`
    referencia `Registry` y `bindRaw` (`Bind[T]`) — los cuatro son símbolos
    generados. `wlcore` no compila en un checkout limpio sin los `*.gen.go`
    ya escritos; se comitean al repo como cualquier otro fichero generado
    en Go (no hay `go:generate` implícito en el build). Regenerarlos exige
    tener ya un `waygenerator` compilable, lo cual no depende de `wlcore` —
    no hay ciclo de verdad, solo una dependencia de orden en el primer
    `git clone`.
- **Nombres de interfaz: Go idiomático, sin el prefijo `wl_`/`xdg_`.**
  - `wl_compositor` → `wlcore.Compositor`
  - `wl_surface` → `wlcore.Surface`
  - `wl_shm` → `wlcore.Shm`
  - `xdg_wm_base` → `xdgshell.WmBase`
  - `xdg_surface` → `xdgshell.Surface`
  - `xdg_toplevel` → `xdgshell.Toplevel`
- **Listeners: nombre de la interfaz + `Listener`.**
  `wl_surface` → `wlcore.SurfaceListener`, `xdg_toplevel` →
  `xdgshell.ToplevelListener`.
- **Descriptor de interfaz: nombre del tipo + `Interface`.**
  `wlcore.CompositorInterface`, `xdgshell.WmBaseInterface`. Es el único
  símbolo generado que lleva horneados el nombre de interfaz del XML y la
  versión máxima, y por eso el call site de `Bind` no tiene que escribir
  ninguno de los dos (ver casos especiales).
- **Métodos de request: PascalCase directo del nombre XML, sin prefijo.**
  `wl_compositor.create_surface` → `(*Compositor).CreateSurface()`.
- **Palabras reservadas de Go.** El XML de Wayland ya evita sus propias
  colisiones en otros lenguajes (p. ej. `class_` para C++), pero no las de
  Go — `interface` es un arg real (`wl_registry.bind`) y es keyword aquí.
  Un nombre de parámetro/campo (`goname.Camel`) que coincida con una
  palabra reservada de Go se renombra vía la tabla `keywordAliases`:
  `interface` → `iface`, que es como Go lo escribe por convención y como
  `codegen` ya nombra el parámetro de `bindRaw`. El resto de keywords —
  ninguna aparece hoy como `<arg>` en `protocols/` — cae al fallback
  genérico de un `_` final (`type` → `type_`). No aplica a nombres de tipo
  (`goname.Pascal`): ningún identificador de Wayland colisiona en mayúscula.
- **`id` es un initialism.** Siguiendo la convención de Go
  ([Initialisms](https://go.dev/wiki/CodeReviewComments#initialisms)),
  cualquier componente `id` que no sea el primero de un nombre se renderiza
  en mayúsculas: `delete_id` → `DeleteID` (Pascal), `object_id` → `objectID`
  (Camel). Como primer componente de un Camel va en minúscula normal
  (`id` → `id`, nunca `Id`).
- **Consts de opcode: no exportadas.** El opcode es un detalle de wire
  format, no algo que el usuario del binding deba tocar — exponerlo público
  invita a usarlo mal. Un bloque `const (...)` por fichero generado, patrón
  `opReq<Interfaz><Request>` / `opEvt<Interfaz><Event>` en minúscula inicial.
  El prefijo `Req`/`Evt` no es decoración: requests y events se numeran por
  separado desde 0 (ver pasada 1), así que sin distinguirlos un request y un
  evento con el mismo nombre en la misma interfaz colisionarían en el mismo
  identificador con dos valores distintos — la pasada 2 aborta la generación
  si eso pasa (ver invariantes):
  ```go
  const (
      opReqCompositorCreateSurface = 0
  )
  ```
 
## Mapeo de tipos XML → Go
 
Directos, sin ambigüedad:
 
| XML | Go |
|---|---|
| `int` | `int32` |
| `uint` | `uint32` |
| `string` (sin `allow-null`) | `string` |
| `fd` | `int` |
 
**`fixed`** — punto fijo 24.8 con signo empaquetado en `int32`. No mapear a
`float64` en la firma (pierde la representación exacta del wire). Tipo
propio en `wlcore`:
```go
type Fixed int32
 
func (f Fixed) Float64() float64 { return float64(f) / 256.0 }
func FixedFromFloat64(v float64) Fixed { return Fixed(math.Round(v * 256.0)) }
```
(`math.Round`, no truncado: truncar sesga sistemáticamente hacia cero y se
nota acumulando deltas de puntero.)
 
**`array`** — el XML no dice qué hay dentro. Sin metadata adicional, el
generador solo puede emitir `[]byte` genérico. Arrays tipados para casos
concretos (p. ej. keycodes de `wl_keyboard.enter`, estados de
`xdg_toplevel.configure`) se resuelven a mano en post-proceso para esa
interfaz, no de forma automática.
 
**`string` con `allow-null="true"`** — `*string` en vez de `string`, servido
por el par `Encoder.StringOpt`/`Decoder.StringOpt` de `wlcore` (el wire format
del string nulo es longitud `0` sin nul ni datos, y ni `Encoder.String` ni
`Decoder.String` saben representarlo — ver `wlcore.md`). Caso real en ambos
sentidos: `wl_data_offer.accept(mime_type)` en request,
`wl_data_source.target` en evento.

**`object`** — depende de si `<arg>` trae el atributo `interface`:
- Con `interface="wl_surface"` → tipo concreto (`*wlcore.Surface`,
  `*xdgshell.Surface`, según paquete). Con `allow-null="true"` el puntero
  puede ser `nil` — chequear id `0` al deserializar eventos, aceptar `nil`
  al serializar requests.
- Sin `interface` (raro) → no tipable en compile-time. Cae como `uint32`
  crudo con comentario `// object id, interface dinámica`.
**`new_id`** — puede aparecer en un request (retorno del método) o **en un
evento** (lo asigna el servidor y lo construye el `Dispatch`; ver casos
especiales). Interfaz estática → puntero tipado devuelto por el método;
interfaz dinámica (`wl_registry.bind`) → `Bind[T]` genérico (ver casos
especiales).
 
**`enum` (atributo, no tipo)** — cuando un `int`/`uint` lleva
`enum="xdg_toplevel.state"`, la firma usa el tipo enum generado en vez del
entero crudo. Si el enum referenciado vive en otro paquete, hay que resolver
el import cruzado — el generador necesita parsear **todos** los XML en una
pasada antes de emitir código, no fichero por fichero.
 
**Orden de argumentos** — los args del XML mapean 1:1 y en orden a los
parámetros de la función (requests) o a los campos parseados en `Dispatch`
(events), excluyendo el `new_id` de un request, que no es parámetro sino el
valor de retorno.
 
## Requests → métodos
 
Un request que crea un objeto (`new_id`) devuelve el puntero directamente,
no un id suelto. Forma canónica, idéntica dentro de `wlcore` y en las
extensiones — siempre vía `Conn()`, nunca tocando el campo no exportado:
 
```go
func (c *Compositor) CreateSurface() (*Surface, error) {
    id := c.Conn().NewID()
    surf := &Surface{ProxyBase: NewProxyBase(id, c.Version(), c.Conn())} // hereda versión
    c.Conn().Register(surf)
 
    e := NewEncoder().ID(id)
    if err := c.Conn().Send(c.ID(), opReqCompositorCreateSurface, e); err != nil {
        return nil, err
    }
    return surf, nil
}
```
 
Nota de orden: `Register` va **antes** del `Send`. El servidor puede empezar a
mandar eventos al id nuevo en cuanto procese el request, y `processMessages`
los descartaría si el objeto aún no estuviera en el mapa.
 
El `objectID` de la cabecera es el del **receptor** (`c.ID()`), no el del
objeto creado: `surf` todavía no existe en el servidor cuando se manda el
mensaje.
 
El opcode sale directo del orden de `<request>` en el XML (índice en la
lista) — el generador lo saca gratis, y lo emite como const no exportada
(ver naming), nunca como literal en la llamada.

Si `Send` falla, `surf` se queda registrado en `objects` con un id que nunca
va a llegar a liberarse (no habrá `delete_id` para algo que el servidor nunca
vio). Se acepta la fuga a propósito, sin revertir el `Register`: un fallo de
`Send` es un error de socket, y con el contrato de un solo hilo (ver
`wlcore.md`, "Quién bombea") eso dice que la conexión entera está rota — el
siguiente `Dispatch()` va a fallar igual y la aplicación va a tirar el `Conn`
completo. Revertir un registro para una conexión que se está muriendo no
compra nada.

Un request con arg `fd` (`wl_shm.create_pool(fd, size) -> wl_shm_pool`) es la
misma forma canónica, con el fd pasado a `Send` en vez de al `Encoder` — el
arg `fd` **no** se serializa en el body (ver wire protocol en `wlcore.md`):

```go
func (s *Shm) CreatePool(fd int, size int32) (*ShmPool, error) {
    id := s.Conn().NewID()
    pool := &ShmPool{ProxyBase: NewProxyBase(id, s.Version(), s.Conn())}
    s.Conn().Register(pool)

    e := NewEncoder().ID(id).Int32(size)
    if err := s.Conn().Send(s.ID(), opReqShmCreatePool, e, fd); err != nil {
        return nil, err
    }
    return pool, nil
}
```

El orden de args en el `Encoder` sigue siendo el orden del XML **saltándose**
los args `fd`, que van todos al final como variádico de `Send`, en el mismo
orden en que aparecen en el XML — no hay más que uno en la práctica, pero la
regla es general.
 
## Eventos → struct de funcs, no channels
 
Decisión deliberada: el dispatch de Wayland es estrictamente secuencial y lo
hace la goroutine que bombea, una sola. Meter cada evento en un channel
obliga a decidir buffering, y si nadie lee ese channel se bloquea el bombeo
entero — es decir, se paran los eventos de *todos* los objetos, no solo los
de ese. El patrón de listener con struct de funcs evita ese acoplamiento, y
además, al ser todo la misma goroutine, `SetListener` no necesita
sincronización ninguna:
 
```go
type SurfaceListener struct {
    Enter func(output *Output)
    Leave func(output *Output)
}
 
type Surface struct {
    ProxyBase
    listener SurfaceListener
}
 
// El constructor engancha OnClear: es lo que ejecuta el clearListener() que
// Surface hereda de ProxyBase cuando Conn.destroy limpia el objeto. El tipo
// generado no implementa clearListener, solo aporta esta closure.
func newSurface(id, version uint32, c *Conn) *Surface {
    s := &Surface{ProxyBase: NewProxyBase(id, version, c)}
    s.OnClear = func() { s.listener = SurfaceListener{} }
    return s
}
 
func (s *Surface) SetListener(l SurfaceListener) { s.listener = l }
```
 
Forma canónica del `Dispatch` generado — **decodificar siempre, entregar
solo si hay listener**, en ese orden:
 
```go
func (s *Surface) Dispatch(opcode uint16, dec *Decoder) error {
    switch opcode {
    case opEvtSurfaceEnter:
        outputID := dec.ID()
        if err := dec.Err(); err != nil {
            return err
        }
        if s.listener.Enter != nil {
            out, _ := s.Conn().Lookup(outputID).(*Output)
            s.listener.Enter(out)
        }
    case opEvtSurfaceLeave:
        outputID := dec.ID()
        if err := dec.Err(); err != nil {
            return err
        }
        if s.listener.Leave != nil {
            out, _ := s.Conn().Lookup(outputID).(*Output)
            s.listener.Leave(out)
        }
    default:
        return fmt.Errorf("wlcore: opcode %d desconocido en wl_surface", opcode)
    }
    return nil
}
```

El parámetro se llama siempre `dec`, nunca `d`: el generador usa un único
molde para las 23 interfaces de `wayland.xml`, y `Display`, `DataDevice`,
`DataDeviceManager`, `DataOffer` y `DataSource` — cualquier interfaz cuyo
tipo Go empiece por `D` — tienen receptor `d`. `d *Decoder` chocaría con
`d *Display` ("d redeclared in this block"); `dec` lo evita sin tener que
dar un caso especial a esas cinco interfaces.
 
El orden importa: si se salta la decodificación cuando el listener es `nil`,
los fds de ese evento se quedan en la cola y desincronizan todos los
siguientes. El nil-check sigue siendo obligatorio — un evento puede llegar
antes de que nadie haya llamado a `SetListener`.
 
**Eventos con `fd`: quien decodifica se queda con la propiedad.** Decodificar
saca el fd de la cola, y si nadie se lo lleva se filtra hasta que muera el
proceso. Para esos eventos —y solo para esos, el generador sabe cuáles son
por los args del XML— la plantilla cambia:
 
```go
case opEvtKeyboardKeymap:
    format := dec.Uint32()
    fd := dec.FD()
    size := dec.Uint32()
    if err := dec.Err(); err != nil {
        DropFD(fd) // el error puede venir de un arg posterior, con el fd ya sacado
        return err
    }
    if k.listener.Keymap == nil {
        DropFD(fd) // zombi, o listener aún sin poner
        break
    }
    k.listener.Keymap(KeymapFormat(format), fd, size)
```
 
`DropFD(-1)` es no-op, así que la rama de error no necesita distinguir si el
`FD()` llegó a hacer `pop` o falló antes. A partir de la llamada al listener,
cerrarlo es cosa del usuario.
 
Opcional, en modo estricto (build tag de debug): comprobar al final que
`d.off == len(d.buf)`. Si sobran bytes, el generador y el XML no coinciden —
es un bug del generador, no del compositor, y así sale a la primera.
 
Si hace falta un modelo por channels para la capa de aplicación (desktopd),
se construye **encima** de esto, no dentro del generador — el generador se
queda fiel al protocolo.
 
## Enums → constantes tipadas
 
Naming: sin prefijo `Wl`/`Xdg` (ya lo da el paquete), con el nombre del tipo
repetido en cada valor.
 
```go
// xdgshell, generado desde <enum name="state"> de xdg_toplevel
type ToplevelState uint32
 
const (
    ToplevelStateMaximized  ToplevelState = 1
    ToplevelStateFullscreen ToplevelState = 2
    ToplevelStateResizing   ToplevelState = 3
    ToplevelStateActivated  ToplevelState = 4
)
```
 
Ojo: `xdg_toplevel.state` **no** es un bitfield. Es un enum plano y el
`configure` manda un `array` de estados, no una máscara — hay que decodificar
el array como una lista de u32. Bitfields de verdad (`bitfield="true"` en el
XML): `wl_seat.capability`, `wl_output.mode`,
`xdg_positioner.constraint_adjustment`. Para esos, y solo para esos, el
generador emite además:
 
```go
func (c SeatCapability) Has(flag SeatCapability) bool { return c&flag != 0 }
```
 
## Casos especiales que rompen la generación uniforme
 
- **`wl_display`**: se genera como cualquier otra interfaz. Lo único a mano
  es que `Connect()` lo construya con `NewProxyBase(displayID, 1, conn)` en
  vez de pedirle un id a `NewID()`, y lo registre.
  Pero **su listener lo consume el runtime, no el usuario**: sus dos eventos
  (`error` y `delete_id`) son maquinaria de `Conn`. Si `Display.SetListener`
  queda público, el primer usuario que lo llame desmonta el reciclado de ids
  sin enterarse. El runtime engancha el listener real en `Connect()` y expone
  `Conn.OnError(func(objectID, code uint32, msg string))` para la parte que
  sí interesa fuera.
- **`wl_callback`** (roundtrip, frame callbacks): **ya no es caso especial.**
  Se genera como cualquier interfaz, con su `Listener`. Llevaba un
  `chan struct{}` interno para que `Roundtrip` pudiera bloquearse; con el
  bombeo en el hilo de la aplicación, quien espera el `done` es el mismo que
  lo despacha, así que un `bool` puesto por el listener basta y el bucle de
  `Roundtrip` lo mira entre `Dispatch()` y `Dispatch()`. Lo único suyo sigue
  siendo que se autodestruye tras `done`.
- **`wl_registry` y `bind`**: `bind` es el request 0 de `wl_registry`, que
  vive en `wayland.xml` → **todo esto es `wlcore`**, sin cualificar. En
  `xdgshell`/`wlrlayershell` los mismos tipos sí llevan el prefijo.
  Regla de wire que solo dispara aquí: un `new_id` **sin** atributo
  `interface` no se serializa como un u32, sino como **tres** valores —
  `string` (nombre de interfaz), `uint` (versión) y `uint` (el id). El
  generador tiene que implementar ese caso aunque en la práctica solo lo use
  `bind`.
  Reparto generado / a mano: el generador emite el request crudo, no
  exportado, porque no puede tipar el retorno; el genérico va a mano en
  `wlcore/registry.go`:
  ```go
  // generado en wlcore/registry.gen.go
  func (r *Registry) bindRaw(name uint32, iface string, version, newID uint32) error
 
  // a mano en wlcore/registry.go
  type Interface[T Proxy] struct {
      Name       string
      MaxVersion uint32
      New        func(ProxyBase) T
  }
 
  func (r *Registry) Bind[T Proxy](name, version uint32, iface Interface[T]) (T, error)
  ```
 
  Método genérico (Go 1.27 en adelante): antes tenía que ser función libre
  porque Go no admitía parámetros de tipo en métodos; con métodos genéricos
  disponibles, `Registry` sí puede llevar el parámetro de tipo directamente.
  Una sola forma de descriptor para los dos usos. El generador emite uno por
  interfaz de ese XML, y ahí dentro va la factory `func(ProxyBase) T` que
  también usa el `new_id` en eventos:
  ```go
  // generado, uno por interfaz
  var CompositorInterface = Interface[*Compositor]{
      Name:       "wl_compositor",
      MaxVersion: 6, // <interface version="6"> del XML
      // wl_compositor no tiene eventos: no hay listener y OnClear se queda
      // nil. Si la interfaz tuviera eventos, la factory lo engancharía
      // igual que el constructor de más arriba.
      New:        func(b ProxyBase) *Compositor { return &Compositor{ProxyBase: b} },
  }
  ```
 
  No hay mapa `map[string]func(ProxyBase) Proxy` de por medio: `Bind` recibe
  el descriptor tipado y el `Dispatch` de un `new_id` en evento conoce el
  tipo concreto en tiempo de compilación. Una tabla indexada por nombre de
  interfaz solo haría falta para resolver interfaces en runtime, que es un
  caso que no existe.
  **Tres formas de resolver esto; se queda el genérico.**
  1. `Registry.Bind` devolviendo `Proxy` + type assertion en el call site.
     Cero maquinaria, pero cambia un error de compilación por un
     `.(*Compositor)` que revienta en runtime. Descartada.
  2. El genérico de arriba. **Elegida.**
  3. Un binder generado por interfaz, sin genéricos:
     ```go
     func BindWmBase(r *wlcore.Registry, name, version uint32) (*WmBase, error)
     ```
     Su ventaja era hornear nombre e interfaz y versión máxima en el generado
     para que el call site no pudiera escribirlos mal — pero eso ya lo da el
     descriptor, así que hoy solo queda el coste: un símbolo exportado por
     interfaz, `bindRaw` tendría que pasar a exportado para que los paquetes
     de extensión lo alcancen, y se pierde poder bindear una interfaz que no
     esté en los XML con los que generaste (un `wayland-info` genérico).
     Prácticamente descartada.
  Detalle que aplica a las tres: `wl_output` y `wl_seat` se anuncian **varias
  veces** (un global por monitor, uno por asiento). El evento `global` no dice
  "existe la interfaz X", dice "existe esta instancia concreta de X", y el
  `name` es cómo se elige cuál. Cualquier API que reciba el string de la
  interfaz en vez del `name` funciona para los singleton y falla en silencio
  con dos monitores.
  **El runtime no guarda los globals.** El listener de `wl_registry` es del
  usuario, no del runtime: quien use la librería decide qué bindear desde
  dentro del callback, que es donde ya tiene `name`, `iface` y `version` en la
  mano. Por eso `Bind` los recibe sueltos y no hay tipo `Global` ni tabla:
  ```go
  reg.SetListener(wlcore.RegistryListener{
      Global: func(name uint32, iface string, version uint32) {
          if iface == wlcore.CompositorInterface.Name {
              comp, _ = reg.Bind(name, version, wlcore.CompositorInterface)
          }
      },
  })
  ```
 
  Consecuencia asumida: `global_remove` solo trae el `name`, así que para
  saber qué se ha ido hay que haberse guardado el anuncio. Eso queda en la
  capa de aplicación (desktopd ya va a llevar su propia tabla de monitores),
  no en `wlcore`.
- **`new_id` en eventos** (`wl_data_device.data_offer`, `wl_data_device.enter`
  con su offer, etc.): el `new_id` no siempre es el retorno de un request. El
  `Dispatch` generado tiene que **construir y registrar** el proxy, con la
  misma factory `New` del descriptor de arriba — no hace falta mecanismo
  nuevo. Cuatro consecuencias:
  - El id llega por el wire y lo asigna el **servidor** (≥ `serverIDBase`).
    `NewID()` nunca puede producir uno de esos.
  - Ese objeto **no genera `delete_id`**: para ids del servidor, el id lo
    libera el cliente localmente al destruirlo. Son dos caminos de
    liberación distintos, no uno (ver ciclo de vida en `wlcore.md`).
  - El registro va **antes** de invocar al listener del padre: los eventos
    del objeto nuevo (`data_offer.offer`, uno por MIME type) vienen pegados
    detrás en el mismo read.
  - Corolario de API: el listener del hijo se pone **dentro** del callback
    del padre, síncronamente. Si vuelves y lo pones después, ya te has
    perdido eventos.
  La interfaz sí está en el XML (`interface="wl_data_offer"`), así que se
  tipa. El único caso dinámico de verdad sigue siendo `bind`.
## Versionado
 
El generador tiene que leer `since=`. Ignorarlo mata de dos formas: mandas un
request que la versión bindeada no soporta y el compositor te cierra con
`wl_display.error`; o recibes un evento nuevo, tu `default:` lo declara opcode
desconocido y cierras tú.
 
Cuatro piezas:
 
1. `version` en `ProxyBase` — lo aporta el runtime (`wlcore.md`).
2. `Bind` negocia `min(versión anunciada en el global, versión que soporta el
   binding generado)`.
3. **Herencia**: el objeto hijo hereda la versión del padre, no se
   renegocia. `get_xdg_surface` propaga la de `WmBase`; `create_surface` la
   de `Compositor`.
4. Guarda en los métodos generados con `since > 1`:
   ```go
   func (t *Toplevel) SetParent(parent *Toplevel) error {
       if t.Version() < 2 {
           return fmt.Errorf("xdgshell: set_parent requiere versión >= 2, hay %d", t.Version())
       }
       // ...
   }
   ```
 
## Pasadas de parseo y resolución de referencias
 
**Todo el `protocols/` se parsea antes de emitir una sola línea.** No es una
preferencia: `enum="xdg_toplevel.state"` e `interface="wl_surface"` cruzan
ficheros, así que fichero-a-fichero no puede resolverse.
 
**Pasada 0 — leer.** `encoding/xml` a un AST fiel al XML: `<protocol>`,
`<interface name version>`, `<request name type since>`, `<event name since>`,
`<arg name type interface enum allow-null summary>`, `<enum name bitfield>`,
`<entry name value since>`, `<description summary>`. Sin interpretar nada
todavía; los errores de esta pasada son "el XML no es XML".
 
**Pasada 1 — tabla global de símbolos.** `nombre de interfaz XML` →
`{fichero, paquete Go, tipo Go, versión máxima, opcodes de request, opcodes de
event, enums}`. Los opcodes salen aquí, del índice en la lista: requests y
events se numeran **por separado**, cada uno desde 0.
 
El mapa fichero → paquete es configuración, no deducción. Un manifiesto corto
en el propio generador:
 
```
wayland.xml           → wlcore
xdg-shell.xml         → xdgshell
wlr-layer-shell-*.xml → wlrlayershell
```
 
**Pasada 2 — resolver.** Cada referencia se convierte en un símbolo real:
 
- `interface="wl_surface"` → busca en la tabla; si no está, error con el
  fichero y la línea, nunca emitir `interface{}` y seguir.
- `enum="state"` (sin punto) → enum **de la misma interfaz** del arg.
- `enum="xdg_toplevel.state"` (con punto) → el cualificador es
  `interfaz.enum`, no `protocolo.interfaz.enum`. Es la confusión típica.
- Mismo trato de "error con fichero y línea, nunca seguir" para los otros
  dos huecos que puede dejar un XML mal formado: un `enum=` que no existe
  en la interfaz referenciada (o en la propia, sin punto), y un `type=` de
  `<arg>` que el switch de tipos no reconoce (ver mapeo de tipos). Los tres
  casos comparten la misma forma de mensaje: `fichero:línea: interfaz
  "X": request/event "Y": arg "Z": <qué faltó>`.
- Los imports de cada fichero de salida salen de aquí, como efecto
  secundario: el conjunto de paquetes de los símbolos referenciados.
Tres invariantes que se comprueban en esta pasada y abortan la generación:
 
1. **El grafo de dependencias entre paquetes es un DAG con `wlcore` en la
   raíz.** El core nunca referencia extensiones. Si un XML nuevo lo rompe,
   sale un import cíclico en Go y el mensaje del compilador no dice nada
   útil; mejor fallar aquí, diciendo qué interfaz de qué fichero lo causó.
2. **Sin colisiones de nombre dentro de un paquete.** Tras quitar prefijos y
   sufijos, dos interfaces del mismo XML pueden acabar en el mismo tipo Go.
   La misma comprobación aplica a los identificadores de const de opcode
   (`opReq…`/`opEvt…`, ver naming): con requests y events numerados por
   separado desde 0, dos miembros homónimos de la misma interfaz —un request
   y un event con el mismo nombre— colisionarían en el mismo identificador
   con dos valores de opcode distintos. El prefijo `Req`/`Evt` ya lo evita en
   la práctica; esta es la comprobación que lo hace seguro por construcción y
   no por convención.
3. **Ninguna interfaz alcanzable como `new_id` en un evento lleva un arg
   `fd` en ninguno de sus eventos.** El id de esos objetos lo asigna el
   servidor y `Destroy()` los borra de `objects` en el acto, sin `delete_id`
   ni zombi (ver ciclo de vida en `wlcore.md`); un evento con fd en tránsito
   hacia un id ya borrado se ignoraría sin hacer `pop`, desincronizando la
   cola de fds de toda la conexión. Hoy ningún XML del manifiesto la rompe
   (`wl_data_offer` y el offer de primary-selection no llevan fd en sus
   eventos), pero es la invariante, no la casualidad del XML actual, la que
   lo mantiene seguro.
Sobre los nombres de `wayland-protocols`: las extensiones no usan `wl_`, usan
`z` + prefijo + `_vN` (`zwlr_layer_surface_v1`, `zwp_pointer_gestures_v1`).
La regla es quitar la `z` inicial, quitar el prefijo del protocolo y quitar el
`_vN` final → `LayerSurface`. La versión de la extensión (`v1` vs `v2`) es
parte del **nombre del paquete**, no del tipo: si algún día conviven las dos,
son dos paquetes.
 
**Pasada 3 — emitir.** Tres cosas que se olvidan y duelen:
 
- **Orden determinista.** Nada de iterar mapas directamente: ordenar por
  nombre antes de emitir. Si no, cada regeneración produce un diff distinto
  sin que haya cambiado nada, y el `git diff` deja de servir para ver qué
  cambió de verdad en el protocolo.
- **Pasar la salida por `go/format`** (`format.Source`) antes de escribir, y
  **fallar si no parsea**. Es el test más barato que existe contra un bug de
  plantilla: si el generador emite Go inválido, te enteras en el generador y
  no cincuenta líneas más abajo en el compilador.
- **`<description>` → comentarios de doc**, empezando por el nombre del
  símbolo Go para que `go doc` los muestre bien. El `summary` del `<arg>`
  vale para el comentario del parámetro. Formato exacto:
  ```go
  // GoSymbolName: summary del <description>
  //
  // cuerpo del <description>, con la indentación cruda del XML limpiada y
  // las líneas en blanco conservadas como separador de párrafo
  //
  // Parameters:
  //   - argName: summary del <arg> (solo si al menos un arg lo trae)
  ```
  Va encima del tipo de la interfaz, de cada método de request exportado
  (`bindRaw` no cuenta: no es público), de cada campo de listener de evento
  y de cada tipo/entry de enum documentados. Sin `summary` en el
  `<description>` no se emite nada — no hay comentario vacío ni placeholder.

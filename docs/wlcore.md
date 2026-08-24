# wlcore — runtime Wayland en Go

> **Documento vivo.** Refleja el estado actual del código y se actualiza con él.
> El diseño original, congelado y con fecha, está en `docs/archive/`.

 
Runtime escrito a mano del cliente Wayland de **ggui**: conexión, wire
format, despacho de eventos y ciclo de vida de objetos. Nada de aquí lo
genera `waygenerator`; el generador se apoya en esta API y solo en ella (el
contrato está en `waygenerator.md`).
 
Contexto: hablar el protocolo Wayland en Go puro, sin cgo contra
`libwayland-client`.
 
## Qué hace falta para abrir una ventana (resumen del protocolo)
 
1. **Conectar** al socket Unix en `$XDG_RUNTIME_DIR/$WAYLAND_DISPLAY`.
2. **Registry**: `wl_display.get_registry`, escuchar evento `global`, hacer
   bind de `wl_compositor`, `wl_shm`, `xdg_wm_base`. Forzar con un roundtrip:
   `wl_display.sync` devuelve un `wl_callback` nuevo, y es *ese callback* el
   que emite `done` — no `wl_display`.
3. **Superficie**: `wl_compositor.create_surface` → `wl_surface` (rectángulo
   sin rol todavía).
4. **Rol de ventana**: `xdg_wm_base.get_xdg_surface` → `xdg_surface.get_toplevel`
   → `xdg_toplevel`. Responder `ping`/`pong` de `xdg_wm_base` o el compositor
   mata el cliente.
5. **Buffer**: memoria compartida vía `memfd_create` + `mmap`, pintar píxeles
   a mano (no hay canvas gratis en Wayland), envolver en `wl_shm_pool` →
   `wl_buffer`, `wl_surface.attach`.
6. **Commit**: nada se muestra hasta `wl_surface.commit` (doble buffer por
   diseño).
7. **Bombeo de eventos**: `Conn.Dispatch()` / `Conn.Run()` (ver más abajo).
   El equivalente en C es `wl_display_dispatch`, pero eso es API de
   libwayland, no una request del protocolo — aquí no existe, lo
   implementamos nosotros. Sin bombear no llegan los `configure` ni nada
   más.
Detalle importante: el compositor manda `configure` en `xdg_surface`, hay que
hacer `ack_configure()` y **solo entonces** dibujar y adjuntar el buffer. Es
protocolo negociado, no "pinta y ya".
 
## Wire protocol — lo que el scanner tiene que generar bien
 
Cada mensaje: `object_id (u32) | size<<16|opcode (u32) | argumentos`.
El campo `size` es el tamaño **total** del mensaje, cabecera incluida.
 
Serialización por tipo de argumento:
- `int` / `uint` / `fixed` → 4 bytes
- `string` → longitud (u32, **incluye el byte nul**) + datos + nul + padding
  a múltiplo de 4
- `array` → longitud (u32) + datos + padding a múltiplo de 4
- `new_id` → u32, asignado por el cliente. **Empieza en 2**: el id 1 es
  `wl_display`, implícito y nunca asignado. Rango del cliente:
  `1..0xFEFFFFFF`; de `0xFF000000` para arriba es rango del servidor.
- `fd` → **no va en el payload**. Viaja como ancillary data (`SCM_RIGHTS`)
  fuera del mensaje. En Go requiere castear a `*net.UnixConn` y usar
  `WriteMsgUnix`/`ReadMsgUnix` con `unix.UnixRights()` — un `net.Conn`
  normal no da acceso a esto.
Buffer compartido en Go (mejor que el `shm_open` + nombre aleatorio de C):
 
```go
fd, _ := unix.MemfdCreate("buffer", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
unix.Ftruncate(fd, int64(size))
data, _ := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK)
```
 
(`golang.org/x/sys/unix`). `MFD_CLOEXEC` para no heredar el fd en un exec, y
`F_SEAL_SHRINK` para que un truncate posterior no le provoque un SIGBUS al
compositor cuando lea el pool. El fd se manda al compositor por `SCM_RIGHTS`
al crear el `wl_shm_pool`.
 
 
## Runtime compartido (escrito a mano, no generado)
 
`Conn` se define **una sola vez**, aquí, con todos sus campos. Las secciones
posteriores solo añaden métodos.
 
 
El generador solo produce structs que embeben `ProxyBase` y satisfacen
`Proxy`.
 
**Por qué `clearListener` no la implementa cada tipo.** La intención no
cambia: un llamante normal no puede quitarle el listener a un objeto, porque
el método sigue sin exportarse; solo `Conn.destroy` lo llama. Lo que cambia
es quién lo implementa. Si cada tipo generado tuviera que escribir su propio
`clearListener()`, `Proxy` sería una interfaz sellada: un método no exportado
solo lo puede implementar código del propio paquete `wlcore`, así que
`xdgshell`, `wlrlayershell` y cualquier otra extensión —que viven en su
paquete, ver `waygenerator.md`— no podrían satisfacer `Proxy` ni pasar por
`Register`, `Bind` o `destroy`. Implementándola una sola vez en `*ProxyBase`,
la promoción por embebido la lleva a todos, dentro y fuera de `wlcore`, y la
parte que sí es específica de cada tipo —dejar su `listener` a cero— viaja en
`OnClear`, un campo que su constructor rellena:
 
```go
func newCallback(id, version uint32, conn *Conn) *Callback {
    return newCallbackFromProxyBase(NewProxyBase(id, version, conn))
}

func newCallbackFromProxyBase(base ProxyBase) *Callback {
    cb := &Callback{ProxyBase: base}
    cb.OnClear = func() { cb.listener = CallbackListener{} }
    return cb
}
```
 
Dos constructores, no uno: `newCallback` arma su propio `ProxyBase` a partir
de `(id, version, conn)`, para el caso normal (una request que crea el
objeto). `newCallbackFromProxyBase` recibe el `ProxyBase` ya construido, y es
la que de verdad hace el trabajo — la usan tanto `newCallback` como el
`New` del descriptor (`Interface[T].New`, ver "Firma de `Bind`" más abajo),
que necesita una factory `func(ProxyBase) T` sin más argumentos que decidir.
 
`OnClear` está exportado por necesidad: el constructor generado tiene que
poder fijarlo desde otro paquete y salir idéntico dentro y fuera de `wlcore`.
Es superficie pública, sí, pero no una que sirva para limpiarle el listener a
nadie: fijarlo es cosa del constructor, y quien lo cambie solo se lo cambia a
su propio objeto.
 
## Lectura y escritura de mensajes — Encoder / Decoder
 
**Byte order: nativo del host, no fijo.** La spec de Wayland dice
explícitamente que los valores van en el orden de bytes del host — en la
práctica va a ser little-endian en x86/ARM, pero no hay que asumirlo como
constante del protocolo. En Go, usar `binary.NativeEndian`
(`encoding/binary`, estable desde Go 1.21) en vez de `binary.LittleEndian`
a pelo.
 
Patrón elegido: `Encoder`/`Decoder`, simétrico al de la propia stdlib
(`encoding/json.Encoder`/`Decoder`). Ninguno de los dos sabe nada de
mensajes Wayland (objectID, opcode, header) — solo serializan/deserializan
primitivas del wire format. El ensamblado del header es responsabilidad de
`Conn.Send` / `processMessages`.
 
### Encoder

Invariante que sostiene todo el padding: **`e.buf` mide un múltiplo
de 4 al entrar a cada método.** `Uint32`/`ID`/`Int32`/`Fixed` escriben
siempre 4 bytes exactos, así que la mantienen sola; `String`/`Array`/
`StringOpt` la restauran ellos mismos rellenando hasta el siguiente múltiplo
de 4 antes de devolver. Ningún método parte de esa invariante ni la
comprueba — se sostiene por construcción mientras todo el código que toca
`e.buf` pase por estos métodos.

 
Todos los métodos devuelven `*Encoder`, así que el uso es encadenable:
 
```go
e := wlcore.NewEncoder().ID(1).String("super")
```
 
`Object` nullable (arg XML con `allow-null="true"`) no es un método propio
del encoder — el código generado resuelve el id antes de llamar:
 
```go
var parentID uint32
if parent != nil {
    parentID = parent.ID()
}
e.ID(parentID)
```
 
### Envío — header y guard de tamaño en `Conn.Send`
 
El campo `size` del header ocupa 16 bits (`size<<16 | opcode` en un
`uint32`). Un mensaje que supere **65535 bytes** desborda esos bits en
silencio y corrompe el opcode al otro lado — bug que solo aparece con
payloads grandes (arrays largos, strings largas) y muy difícil de depurar
porque se manifiesta como "opcode raro" en el receptor. El guard vive aquí,
no en el `Encoder`, así el generador nunca tiene que acordarse de
comprobarlo.
 
`Send` no lleva mutex: es parte de la misma API de un solo hilo que
`Register`/`SetListener`/`Dispatch` (ver "Quién bombea") — un request
normalmente sale desde dentro de un listener o justo después de un
`Roundtrip`, en la goroutine que bombea. Si dos writes concurrentes llegaran a
intercalarse a nivel de socket, el compositor recibiría basura; evitarlo es
responsabilidad de quien viole el contrato, no de `Send`.

 
No trocear el envío si el mensaje lleva fds: tienen que ir pegados
exactamente a ese write, porque el kernel asocia el `SCM_RIGHTS` al
segmento de datos con el que viajó, no al mensaje Wayland lógico.
 
**El `objectID` del header es siempre el id del receptor del request, no
de ningún objeto que el request cree.** En `create_surface`, el receptor
es el `Compositor` (`c.ID()`), aunque el request devuelva un `*Surface`
nuevo — `surf` todavía no existe en el servidor cuando se manda el
mensaje.
 
 
La plantilla del request generado que usa esto vive en `waygenerator.md`.
 
### Decoder
 
Dos reglas de diseño:
 
1. **No hace panic nunca.** El body viene del otro lado del socket: es input
   no confiable, aunque el otro lado sea un compositor "amigo". Un
   `d.buf[d.off:]` sin comprobar convierte un bug del compositor en un crash
   del cliente.
2. **Error pegajoso, se comprueba una vez.** Mismo patrón que
   `bufio.Writer`/`sql.Rows`: cada método devuelve el cero de su tipo si ya
   hay error, y el código generado hace un solo `d.Err()` después de leer
   todos los argumentos. Si cada lectura devolviera `(T, error)`, un evento
   de 6 args serían 6 bloques de error en cada `case` generado.
Los fds no se le copian: hace `pop` sobre la cola de la conexión (ver más
abajo por qué no se pueden repartir antes).
 
 
Los métodos no encadenan: devuelven el valor leído, que es lo que necesita
`Dispatch`.
 
La plantilla del `Dispatch` generado que consume este `Decoder` vive en
`waygenerator.md`.
 
### Lectura del socket — un stream no preserva límites de mensaje
 
Puede llegar `Read` con 3 mensajes Wayland pegados, o medio mensaje. Hace
falta un buffer propio de reensamblado en la conexión, separado del
`Decoder` (el `Decoder` opera sobre un mensaje ya completo y delimitado).
 
**Buffer de capacidad fija que se compacta, no un slice que crece.** El
patrón fácil (`inBuf = append(inBuf, ...)` para meter, `inBuf = inBuf[size:]`
para sacar) no recicla nada: la ventana avanza, la capacidad de delante se
agota y cada `append` acaba realocando y copiando. Con reensamblado continuo
eso es basura constante en el camino caliente.
 
Tamaño: `maxMessageSize + 1` = 64 KiB, el máximo que cabe en el campo `size`
del header. Con eso, **cualquier mensaje legal entra entero tras compactar**,
así que no existe el caso "no cabe" ni la rama de crecer. 64 KiB por conexión
es ruido para un cliente de escritorio.
 
Un ring buffer también evitaría la realocación, pero un mensaje que dé la
vuelta al final del anillo queda partido en dos trozos, y entonces o el
`Decoder` sabe leer trozos discontinuos o hay que copiar igual en el wrap.
Compactar da una vista contigua gratis y la copia es amortizada: como mucho
un `memmove` de lo pendiente por buffer lleno.
 
 
`free()` nunca devuelve un hueco de longitud 0: para que eso pasara el buffer
entero tendría que estar lleno de un mensaje incompleto, y un mensaje completo
mide como mucho 65535 < 65536, así que `processMessages` siempre consume algo
antes de volver a leer.
 
Misma idea para los fds, con índice de cabeza en vez de reslice:
 
 
 
`Conn.Dispatch` y `Proxy.Dispatch` comparten nombre y no son lo mismo: el
primero lee del socket, el segundo decodifica un mensaje ya delimitado. Se
llaman igual porque son los dos nombres correctos (`wl_display_dispatch` y el
dispatch del proxy en libwayland), y los receptores son distintos.
 
 
Cualquier error de aquí para abajo es terminal: el stream queda desalineado y
no hay forma de resincronizar (no hay marcadores de trama). `Dispatch`
devuelve el error, se cierra el socket y la aplicación se entera por el
retorno, sin ir a buscarlo a ningún sitio.
 
**Fds y su cola de consumo**: no se sabe cuántos fds pertenecen a un
mensaje concreto hasta decodificar el body y encontrar un argumento tipo
`fd` en el spec de ese evento — el wire format no lo indica en el header.
Por eso **no** se le puede pasar un `[]int` ya troceado a `Dispatch`: la cola
vive en `Conn` y el `Decoder` hace `pop` sobre ella (`Decoder.FD()`).
Funciona porque el orden de fds en la cola siempre coincide con el orden de
los mensajes que los llevan, al ir por el mismo flujo secuencial del socket.
 
Esto obliga a que el objeto destruido, **si su id es del cliente**, siga en
`objects` como zombi hasta el `delete_id` (ver ciclo de vida): si un evento
con fd llega para un id ya destruido y se ignora sin hacer `pop`, la cola se
desincroniza y además se filtra el fd. El zombi no necesita maquinaria propia
— es un proxy normal que sigue en el mapa, así que su `Dispatch` generado
decodifica el evento entero como siempre y sus `FD()` hacen `pop`. **No hace
falta ninguna tabla de fds-por-opcode**: esa información ya está dentro del
`Dispatch`, que es código generado a partir del mismo XML del que saldría la
tabla. Con esto el `Lookup` de `processMessages` (`wayland/wlcore/wire.go`), para
ids de cliente,
solo falla de verdad para ids que nunca existieron — y eso ya es error de
protocolo, no caso legítimo.

**Para ids del servidor la historia es la otra**, la que `processMessages`
anticipa al tratar un `Lookup` fallido como caso legítimo: no hay zombi,
`Destroy()`
los borra de `objects` en el acto (ver ciclo de vida), así que un `Lookup`
fallido para uno de esos sí es el caso normal — el servidor puede tener
eventos en vuelo hacia un id que el cliente ya destruyó localmente, y por eso
la invariante que cierra ese hueco no vive aquí sino en el generador: ninguna
interfaz que nazca de un `new_id` en evento puede tener eventos con `fd` (ver
`waygenerator.md`, invariante de la pasada 2). Sin esa garantía, el mismo
"se ignora sin pop" de la frase anterior desincronizaría la cola también para
estos ids.
 
Aún no muerde (ningún evento que consumimos lleva fd), pero
`wl_keyboard.keymap` lleva uno en cuanto entre input.
 
### Propiedad de los fds
 
`SCM_RIGHTS` crea un fd nuevo en el proceso receptor y **nadie lo cierra por
ti**. El compilador no ayuda aquí (un fd es un `int`), así que la regla va
escrita:
 
**Al enviar**: `Send` no dupea ni cierra nada. El fd sigue siendo del
llamante, que lo cierra cuando quiera una vez `Send` ha devuelto sin error.
Es seguro porque el write es síncrono: cuando `WriteMsgUnix` vuelve, el
kernel ya tiene su propia referencia. libwayland sí dupea, pero porque encola
los mensajes y los flushea más tarde, y para entonces el original puede estar
cerrado; al no tener buffer de salida, aquí ese problema no existe. Caso
típico: `shm.CreatePool(fd, size)` y `unix.Close(fd)` justo detrás — el pool
sigue vivo con la referencia del compositor.
 
**Al recibir**: `Decoder.FD()` transfiere la propiedad a quien decodifica. El
`Dispatch` generado se la pasa al listener y a partir de ahí es del usuario
(`wl_keyboard.keymap`: mmap, parsear, cerrar).
 
**Si no hay listener** — zombi, o evento que llega antes del `SetListener` —
el `Dispatch` ha decodificado igual y se queda con un fd que nadie va a
cerrar. Tiene que cerrarlo él. De ahí este helper, que existe para que el
código generado no importe `x/sys/unix`:
 
```go
// DropFD cierra un fd recibido que no se va a entregar a nadie.
func DropFD(fd int) {
    if fd >= 0 {
        unix.Close(fd)
    }
}
```
 
**Al morir la conexión**: lo que quedara en la cola sin consumir se cierra en
`fdQueue.drain()`, desde la misma goroutine que bombeaba, al salir de
`Run()`.
 
### Quién bombea
 
**No hay goroutine de fondo.** El que lee y despacha es el hilo de la
aplicación, cuando llama a `Dispatch()` o se queda dentro de `Run()`. Es el
modelo de libwayland (`wl_display_dispatch` lo llama tu bucle, no un hilo
oculto), y aquí compra bastante más que allí:
 
- **El race de `SetListener` desaparece.** No es que se mitigue: quien
  escribe el listener y quien lo lee son la misma goroutine, así que no hay
  dos accesos que ordenar. Deja de importar *cuándo* se pone el listener, y
  con ello se cae la necesidad de arrancar el bombeo en un punto concreto —
  `Connect()` no arranca nada porque no hay nada que arrancar.
- **`in` y `fds` sin lock** siguen siendo correctos, y por un motivo más
  fuerte: solo los toca quien bombea, y bombea uno.
- **`Conn` entero es de un solo hilo, no solo `in`/`fds`.** Requests
  (`Send`, y con ellos `Register`/`NewID`/`objects`/`freeIDs`) y
  `SetListener` se llaman desde la misma goroutine que bombea — normalmente
  desde dentro de un listener, o entre dos `Dispatch()` del propio bucle. Por
  eso ni `Send` ni `Register`/`Lookup`/`NewID` llevan mutex: no hay un `mu`
  aparte protegiendo `objects`/`nextID`/`freeIDs`, protegerlos sería tapar un
  uso del contrato que ya está mal. Si la aplicación necesita mandar un request
  desde otra goroutine, no hay atajo: pasa por el mismo channel de la capa de
  aplicación que se usa para reaccionar a eventos (ver más abajo), y quien
  bombea es quien de verdad llama a `Send`.
Lo que se paga, sin adornos:
 
- **Nada se despacha mientras la aplicación hace otra cosa.** Si se va
  200 ms a trabajar en esa goroutine, los eventos esperan en el socket.
  Es lo normal en un cliente Wayland, pero implica que tarde o temprano hará
  falta exponer el fd del socket para meterlo en un `epoll` propio junto a
  timers y demás, en vez de bloquear en `Run()`. No hasta que haga falta.
- **Solo puede bombear uno.** No hay lock que lo imponga: si otra goroutine
  llama a `Dispatch()` mientras `Run()` está parado en el `read`, se queda
  esperando su turno indefinidamente. Contrato documentado, no comprobado.
- Si la aplicación necesita reaccionar a eventos desde otras goroutines, se
  resuelve dentro de los listeners (p. ej. mandando a un channel de la capa
  de aplicación), no paralelizando el bombeo.
`Conn.Done()` sobrevive a este cambio aunque `Roundtrip` ya no lo use: es la
única forma que tiene una goroutine que *no* bombea de enterarse de que la
conexión ha muerto sin ponerse a preguntar por `Err()`.
 
## Ciclo de vida de objetos
 
Esto es lo que separa "hablo el wire format" de "hablo el protocolo". Sin
ello, un cliente real se cae en minutos.
 
**`wl_display.delete_id(id)`** es la única fuente de verdad para reciclar un
id de cliente. `nextID++` monótono no vale: con un frame callback por frame,
a 144 Hz son ~500k ids/hora.
 
**Destructores.** El generador detecta `type="destructor"` en el request y
emite `Destroy()`. Mandar el destructor le quita siempre el listener al
proxy, para no entregar eventos de un objeto que el usuario ya dio por
muerto — pero lo que pasa con el id **depende de quién lo asignó**:

- **Id de cliente** (`< serverIDBase`): el destructor **no** libera el id
  todavía — el servidor puede tener eventos en vuelo hacia ese objeto. El
  proxy pasa a **zombi**: sale del alcance del usuario pero **sigue en
  `objects`** hasta que llegue el `delete_id`. El zombi no es solo
  contabilidad: sigue en `objects`, así que su `Dispatch` decodifica los
  eventos en tránsito y **consume y cierra sus fds aunque ya no haya nadie
  escuchando**. Es exactamente el agujero de la cola de fds que quedaba
  pendiente en la sección de lectura — mismo problema, misma solución, es lo
  que hace libwayland con las entradas zombie de su `wl_map`.
- **Id de servidor** (`≥ serverIDBase`, viene de un `new_id` en evento):
  `Destroy()` lo borra de `objects` en el acto. No hay zombi porque no hay
  `delete_id` que lo cierre, y no hace falta: el servidor puede reutilizar ese
  id en cuanto procese nuestro `destroy`, así que dejarlo en el mapa solo
  serviría para que el `Register` del objeto siguiente lo pisara en silencio.
  El hueco de eventos ya en vuelo antes de que el servidor viera el `destroy`
  se cierra en el generador, no aquí: ninguna interfaz alcanzable por `new_id`
  en evento puede llevar un `fd` en sus eventos (invariante de la pasada 2 en
  `waygenerator.md`), así que ignorar el mensaje para un id ya borrado nunca
  desincroniza la cola de fds.

**Contrastado contra la implementación de referencia.** El spec solo dice
que `delete_id` es para "an object **that it had created**" — deliberadamente
mudo sobre qué pasa con los ids de servidor. libwayland sí lo implementa, en
`proxy_destroy()` (`wayland-client.c`): id de cliente → entra un
`wl_zombie` en el mapa (`WL_MAP_ENTRY_ZOMBIE`) hasta el `delete_id`; id de
servidor (`>= WL_SERVER_ID_START`) → `wl_map_insert_at(..., 0, id, NULL)`,
fuera en el acto, sin zombi. Es exactamente la ruta doble que implementa
`Conn.destroy` (`wayland/wlcore/conn.go`), no una invención de este runtime.

Una diferencia real, ya asumida: el `wl_zombie` de libwayland no es el proxy
entero, es una tabla ligera (`event_count` + `fd_count[]` por opcode) — el
equivalente en C de la "tabla de fds por opcode" que `waygenerator.md`
describe y descarta a propósito (nuestro `Dispatch` generado ya trae esa
información, no hace falta duplicarla en una tabla aparte). Confirma que el
problema es real — la resuelve incluso la referencia — y que mantener vivo
el proxy completo es una alternativa válida, más simple en Go, no una
sobre-ingeniería.

Un límite que sí es nuestro y no de libwayland: la referencia no se defiende
de un evento con `fd` llegando a un id de servidor ya puesto a `NULL` —
simplemente asume que no pasa. La invariante de la pasada 2 lo hace imposible
por construcción en vez de por convención; es más estricta que la propia
implementación de referencia, no una réplica de ella.

 
Resumen de las dos rutas de liberación:
 
| Quién asignó el id | Cuándo se libera |
|---|---|
| Cliente (`NewID`, < `serverIDBase`) | al llegar `wl_display.delete_id` |
| Servidor (evento con `new_id`, ≥ `serverIDBase`) | localmente al destruir; el servidor no manda `delete_id` |
 
Sin destructor explícito: `wl_callback` se autodestruye tras `done` y el
servidor manda su `delete_id` igualmente.
 
## Roundtrip
 
Dos capas distintas, a propósito:
 
- **`Display.Sync() (*Callback, error)`** — generado. Es la request del
  protocolo, no hay nada que decidir.
- **`Conn.Roundtrip() error`** — a mano. Es política: crea el sync y bombea
  hasta que llegue su `done`.
```go
func (c *Conn) Roundtrip() error {
    cb, err := c.display.Sync()
    if err != nil {
        return err
    }
    done := false
    cb.SetListener(CallbackListener{Done: func(uint32) { done = true }})
    for !done {
        if err := c.Dispatch(); err != nil {
            return err
        }
    }
    return nil
}
```
 
El `bool` no vive en el proxy — `Callback` es una interfaz generada normal,
`ProxyBase` + `listener`, sin ningún campo propio — sino en el stack de
`Roundtrip`, capturado por el listener que se le pone antes de bombear. Un
`bool` basta donde antes hacía falta un channel: el bucle que lo mira es el
mismo que despacha el evento que lo pone. Por eso `wl_callback` deja de ser
caso especial en el generador y pasa a ser una interfaz normal con su
listener; el `SetListener` va antes del primer `Dispatch()` de este bucle,
igual que en cualquier otro arranque.
 
**Contrato: `Roundtrip()` sigue sin poder llamarse desde dentro de un
listener**, pero cambia el motivo. Ya no es autobloqueo — bombearía
perfectamente — sino dispatch reentrante: lees más mensajes desde dentro de
un evento y te llegan eventos de otros objetos anidados en medio de ese
callback. Un cuelgue se depura; esto no. Desde un listener, la salida
correcta sigue siendo la forma asíncrona: `Sync()` y reaccionar en el `done`.
 
Segundo contrato, nuevo: **`Roundtrip()` lo llama quien bombea**, y solo si
no está ya dentro de `Run()`. Encaja con el uso real (roundtrips en el
arranque, `Run()` al final), pero es una libertad que se pierde: antes lo
podía llamar cualquier goroutine.
 
libwayland resuelve esto de verdad con colas de eventos (`wl_event_queue` +
`wl_display_dispatch_queue`), que permiten dispatch anidado. Es bastante
máquina; no meterlo hasta tener un caso real que lo pida.
 
## Connect — arranque de la conexión
 
Dos caminos, y el primero se olvida siempre:
 
1. **`WAYLAND_SOCKET`**: el compositor nos ha lanzado él y nos pasa un fd ya
   conectado, heredado. Si existe, manda sobre todo lo demás.
2. **`WAYLAND_DISPLAY`** (por defecto `wayland-0`), relativo a
   `XDG_RUNTIME_DIR`. Desde 1.15 puede venir como ruta absoluta, y entonces
   se usa tal cual sin tocar `XDG_RUNTIME_DIR`.
 
El `defer f.Close()` no es cosmético: `net.FileConn` **duplica** el fd para
quedarse uno suyo en modo no bloqueante y registrado en el netpoller. Si no
cierras el original te queda un fd colgando para toda la vida del proceso, y
además uno en modo bloqueante apuntando al mismo socket.
 
`Connect` monta el objeto 1 a mano — es el único que no pasa por `NewID()` —
y engancha el listener interno antes de arrancar el loop, para no perder un
`error` temprano:
 
 
`Connect` no lee nada del socket: deja la conexión montada y vuelve. Un
`wl_display.error` temprano se queda esperando en el buffer del socket hasta
el primer `Dispatch()`, no se pierde. Y el listener interno del display queda
enganchado antes de que nadie pueda bombear, así que no hay ventana en la que
el usuario pueda pisarlo.
 
`Display` no genera `SetListener`: el resolver fija
`PublicListener: iface.Name != "wl_display"` (ver `waygenerator.md`), así
que el generador se salta ese método concretamente para `wl_display` — no
hay un `// Deprecated:` que ignorar, no hay método público que llamar y
pisar el reciclado de ids. `Connect`, que vive en el mismo paquete que el
campo no exportado `listener`, se lo asigna directamente. La API buena para
el usuario es `Conn.OnError` (`wayland/wlcore/conn.go`).

 
### Error terminal
 
Cualquier fallo del loop de lectura, y cualquier `wl_display.error`, dejan la
conexión inservible: el stream está desalineado o el compositor ya nos ha
dado por muertos. Se fija una vez y se cierra:
 
```go
type ProtocolError struct {
    ObjectID uint32
    Code     uint32
    Message  string
}
 
func (e *ProtocolError) Error() string {
    return fmt.Sprintf("wlcore: objeto %d: %s (code %d)", e.ObjectID, e.Message, e.Code)
}
 
func (c *Conn) fatal(err error) {
    if err == nil {
        return
    }
    c.errOnce.Do(func() {
        c.err = err
        close(c.done)
        c.sock.Close() // desbloquea al que esté parado en ReadMsgUnix
    })
}
 
func (c *Conn) Done() <-chan struct{} { return c.done }
func (c *Conn) Err() error            { return c.err } // válido tras Done()
```
 
Cierre ordenado por la misma puerta, con un error centinela en vez de un
camino aparte:
 
 
Un cierre limpio se distingue de una caída con
`errors.Is(c.Err(), ErrClosed)`. El `sock.Close()` de `fatal` desbloquea el
`ReadMsgUnix`, ese `Dispatch()` devuelve `ErrClosed` y `Run()` sale por ahí,
cerrando en su `defer` los fds que quedaran pendientes.
 
Con esto `Close()` se puede llamar desde dentro de un listener — el caso
`xdg_toplevel.close` — sin ningún cuidado especial: no espera a nadie, solo
fija el error y cierra el socket, y el `Run()` que está más abajo en la pila
se entera al volver del listener.
 
Y `Roundtrip` no necesita vigilar nada aparte: si la conexión se cae mientras
espera el `done`, el `sock.Close()` de `fatal` desbloquea el `ReadMsgUnix`,
el `Dispatch()` de su bucle devuelve el error y sale por ahí. No hay
`select`, ni forma de quedarse esperando un evento que ya no va a llegar.
 
Arranque completo, de principio a superficie:
 
```go
c, err := wlcore.Connect()
reg, err := c.Display().GetRegistry()
 
var comp *wlcore.Compositor
reg.SetListener(wlcore.RegistryListener{
    Global: func(name uint32, iface string, version uint32) {
        if iface == wlcore.CompositorInterface.Name {
            comp, _ = reg.Bind(name, version, wlcore.CompositorInterface)
        }
    },
})
 
err = c.Roundtrip() // bombea hasta que han llegado todos los globals
```
 
El `SetListener` va **antes** del `Roundtrip` por lo obvio: es el `Roundtrip`
el que despacha los globals, y sin listener puesto no hay dónde entregarlos.
Ya no hay nada más sutil que eso — al no despacharse nada hasta que la propia
goroutine bombea, no existe la ventana en la que el compositor te adelanta. Y
el `Roundtrip` no es opcional: sin él sigues sin tener nada bindeado cuando
la siguiente línea intente usar `comp`.
 
### Firma de `Bind`
 
El call site no escribe ni el nombre de interfaz ni la versión máxima: los dos
salen del descriptor que emite el generador, del mismo XML del que salió el
tipo.
 
```go
// a mano, en wlcore/registry.go
type Interface[T Proxy] struct {
    Name       string
    MaxVersion uint32
    New        func(ProxyBase) T
}
 
func (r *Registry) Bind[T Proxy](name, version uint32, iface Interface[T]) (T, error)
 
// generado, uno por interfaz del XML
var CompositorInterface = Interface[*Compositor]{
    Name:       "wl_compositor",
    MaxVersion: 7, // <interface version="7">
    New:        newCompositorFromProxyBase,
}
```
 
`Bind` es un método genérico de `*Registry` (Go 1.27), no una función libre
con `r *Registry` como primer parámetro — ese era el diseño de antes de que
Go admitiera parámetros de tipo en métodos; ya no aplica esa restricción, y
`reg.Bind(name, version, iface)` en el call site lee mejor que
`Bind(reg, name, version, iface)`. `New` es la factory
`func(ProxyBase) T` que trae cada `Interface[T]` generado: siempre el
`new<Tipo>FromProxyBase` de esa interfaz (ver el constructor de dos pasos
más arriba), no una closure escrita a mano por el generador.
 
`Bind` negocia `min(version, iface.MaxVersion)` y le pasa esa al
`NewProxyBase` del hijo. La versión anterior recibía `iface string`,
`version` y `max` sueltos: tres cosas que escribir bien en cada call site, y
dos de ellas (`version`/`max`) `uint32` adyacentes, así que intercambiarlas
compilaba y el síntoma era el compositor cerrando la conexión al primer
request demasiado nuevo. Con el descriptor ese par desaparece del call site.
 
Queda un riesgo residual: `name` y `version` siguen siendo dos `uint32`
seguidos. El orden es el mismo que el del callback (`name`, luego `version`),
que es la única mnemotecnia barata. Si llega a morder, el arreglo es un
`type GlobalName uint32` con un caso especial en el generador para ese arg
concreto de `wl_registry.global`; no vale la pena antes.

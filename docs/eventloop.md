# Bucle de eventos: UI independiente del socket

> **Documento vivo.** Describe `eventloop` tal como está hoy. El diseño y el
> plan con los que se construyó están congelados en
> `docs/archive/specs/2026-09-19-eventloop-design.md` y
> `docs/archive/plans/2026-09-19-eventloop-implementation.md`.

`eventloop` separa la goroutine que posee la conexión Wayland de la que posee
la UI, y las hace hablar solo por mensajes. Existe para tres cosas: que un
pintado lento no retrase el teclado, el puntero ni el `pong` a
`xdg_wm_base.ping`; que un socket atascado no congele la UI; y que la UI
tenga dónde ejecutar animaciones y tareas asíncronas sin esperar a que el
compositor diga algo.

## Modelo

```
goroutine Wayland (Loop.Run)                 goroutine UI (UI.Run)
  poll(socket, eventfd, próximo repeat)        select { inbox | tareas }
  Dispatch → keyboard / pointer / xdg   ──►    Inbox: Key, Pointer, Focus, Configure,
  Tick del repeat                                      BufferRelease, FrameDone, Closed
  ejecuta los Post(...)                 ◄──    Loop.Post(func()): attach, damage, frame,
  contesta ping/pong                                   commit, create_buffer, ...
```

Reglas:

1. **Toda petición al compositor sale de la goroutine Wayland.** La UI no
   llama a `Conn` ni a métodos generados. Encola una closure con `Loop.Post`.
2. **La goroutine Wayland nunca espera a la UI.** `Inbox.Push` no bloquea.
3. **La UI no espera a la goroutine Wayland.** Lo que necesita de una petición
   (un `wl_buffer` creado) le llega como evento o por `UI.Do`, no como retorno.
4. **`keyboard` y `pointer` no cambian.** Siguen con callbacks dentro del
   dispatch; esos callbacks pasan a ser `ui.Push(evento)`.

## Las piezas

### `Loop` — la goroutine Wayland

`Loop.Run` bloquea hasta que la conexión termina, y la goroutine que lo llama
pasa a ser la dueña de `Conn`. Espera con `poll(2)` sobre el socket y un
`eventfd`, así que despierta por un mensaje del compositor, por un `Post` de
otra goroutine o por su propio temporizador (`Deadline`/`OnTick`, pensados para
`Keyboard.NextRepeat` y `Keyboard.Tick`). Cada vuelta hace, en este orden:
ejecutar los `Post` pendientes, despachar el socket si es legible, y llamar a
`OnTick` si `Deadline` ha pasado. Despachar antes del tick permite que una
tecla soltada en la misma vuelta cancele su repetición.

`Post` y `Close` son lo único seguro desde otras goroutines. Un `Post` tras
terminar `Run` se descarta. Cerrar con `Loop.Close` y no con `Conn.Close`
desde fuera: cerrar el socket no despierta un `poll` que espera en él, el
`eventfd` sí.

El protocolo de despertar tiene un orden que importa: **vaciar el `eventfd`,
después bajar el flag `pending`, después tomar la cola.** Al revés, un `Post`
que entra entre los dos pasos sube el flag y escribe el `eventfd`, el vaciado
se traga esa escritura y el flag se queda subido sin nada legible: todos los
`Post` siguientes se saltan su escritura y el bucle duerme con trabajo en
cola. Lo fija un test con una costura entre los dos pasos: un test de estrés
solo lo encontraba de vez en cuando (entre el 8 % y el 20 % de las
ejecuciones, según se midió).

No se usa `DispatchUntil`. Con una fecha ya vencida, Go no lee del socket
aunque haya datos (devuelve `i/o timeout` antes de intentar el syscall), así
que un temporizador atrasado saltaría la lectura de esa vuelta. Con `poll`,
todo lo legible se lee en la misma pasada que sirve al temporizador.

### `Inbox` — de la goroutine Wayland a la UI

Cola con lock cuyo `Push` nunca bloquea. Decide qué merece la pena conservar
según lo que significa para el usuario:

- **Se fusionan** los `Position` y `DragMove` consecutivos: solo importa el
  último.
- **Se acotan** los `Repeated` pendientes (64): una repetición es sintética,
  no algo que el usuario tecleó.
- **Nunca se descarta, fusiona ni reordena** el resto: teclas, botones, clics,
  foco, `Configure`, `BufferRelease`, `FrameDone`.

La fusión solo mira el último evento pendiente; fusionar a través de otro
movería el movimiento por delante de lo que hubo en medio. Los `*wlcore.Surface`
y `*wlcore.Buffer` que llevan los eventos son **solo identidad**: la UI los
compara, no llama a sus métodos.

### `UI` — la goroutine de UI

`UI.Run` entrega los eventos a `Handler.OnEvent`, ejecuta las closures de
`UI.Do` y pinta cuando el `FrameClock` lo permite. Todo lo que la aplicación
hace con widgets y canvas ocurre ahí, sin locks.

- `Push` (desde los callbacks de `keyboard`/`pointer`) y `Do` (desde cualquier
  goroutine) son las entradas. Ambas descartan lo que llega después de que
  `Run` termine.
- `Do` es el punto de entrada de las **tareas asíncronas**: la tarea trabaja
  en su goroutine y publica el resultado con una closure. `UI.Context` se
  cancela al terminar, para que las tareas paren solas.
- `Invalidate` y `SetBufferFree` son solo para la goroutine de UI.
- Los eventos llegan antes que las closures de `Do` dentro de una misma
  vuelta, y se pinta al final.

### `FrameClock` — cuándo pintar

Máquina de estados pura, sin tiempo ni Wayland:

```
idle ──(invalidado o animando)──► pinta y pide callback ──► esperando
esperando ──FrameDone──► (invalidado o animando) ? pinta de nuevo : idle
```

Solo hay **un fotograma en vuelo**: la UI nunca se adelanta a un compositor
lento. Una UI estática no gasta CPU tras su último pintado, una animación va al
ritmo del compositor y una ventana oculta (a la que el compositor deja de mandar
callbacks) deja de pintar sola. El reloj de animación es el timestamp del
callback, no el reloj de pared.

`Handler.Paint` devuelve `(presented, animating)`: si de verdad envió un
fotograma (sin buffer libre devuelve `false` y llama a `SetBufferFree(false)`) y
si quiere un fotograma en cada callback.

### Fases y loader — la ventana se abre primero

Una `UI` empieza en `PhaseLoading` y sale de ahí una sola vez, con
`SetReady()` o `Fail(err)`, llamados desde una closure de `Do`.

- **`PhaseLoading`:** `Paint` debe dibujar `PaintLoader`. La UI sigue pidiendo
  fotogramas aunque `Paint` diga que no anima (un loader quieto parece
  colgado), y **descarta `EvKey` y `EvPointer`**: no tienen widget al que
  llegar y no se guardan para después. Configure, foco, buffers y cierre sí se
  entregan.
- **`PhaseReady`:** la UI real. `SetReady` invalida, así que el siguiente
  fotograma ya es la UI real y no otro del loader.
- **`PhaseFailed`:** `Paint` debe dibujar `PaintFailed`; se muestra una vez y la
  ventana se queda quieta y abierta. Cerrar funciona en cualquier fase.

`loader.go` importa solo `canvas` y `math` (un test lo verifica): el loader no
puede depender de nada que la aplicación cargue, porque las fuentes son
justamente lo lento. No dibuja texto y no asigna. `DrawSpinner` es el mismo
anillo para usarlo como indicador de ocupado dentro de una UI real.

## Uso

```go
w.loop, _ = eventloop.New(conn)
w.loop.Deadline = kbd.NextRepeat
w.loop.OnTick = func(now time.Time) { kbd.Tick(now) }

kbd.OnKey = func(ev keyboard.Event) { ui.Push(eventloop.Event{Kind: eventloop.EvKey, Key: ev}) }
// ... lo mismo para pointer, foco, configure, release y frame callback

go ui.Run(eventloop.Handler{OnEvent: onEvent, Paint: paint})
go func() { // init lento: nada de esto retrasa el primer fotograma
    built := loadSlowStuff()
    ui.Do(func() { app = built; ui.SetReady() })
}()

err := w.loop.Run() // esta goroutine es la de Wayland
ui.Push(eventloop.Event{Kind: eventloop.EvClosed})
```

`example/widgets` es el ejemplo completo: pool de buffers repartida entre las
dos goroutines, loader mientras carga la fuente del sistema
(`WIDGETS_SLOW_INIT=3s` para verlo), cursor que parpadea con un timer, spinner
con el reloj de fotogramas y una tarea lenta lanzada con Enter.

## Cambios en `wlcore`

`Conn` sigue siendo de una goroutine, pero ahora hay una excepción documentada:
**`Close`, `Done` y `Err` son seguros desde cualquiera.** Antes, `fatal`
escribía el error terminal dentro de un `sync.Once` mientras `Dispatch` lo
leía en la goroutine que bombeaba, y cerrar desde fuera era una carrera de
datos. Ahora el error vive detrás de un puntero atómico que se guarda antes de
cerrar `done`. `Err` también es fiable antes de `Done` (`nil` mientras la
conexión sigue viva). Y `Conn.SyscallConn` expone el fd para esperar en él
junto a otra cosa; el I/O sigue siendo de `Dispatch` y de las peticiones.

## Lo que se paga

- **Un salto más de latencia** entre la entrada y la UI, y las respuestas que
  dependen de un serial (cursor, foco) pasan por `Post`.
- **Un compositor atascado bloquea a la goroutine Wayland en un `write`,** sin
  poder leer. No hay deadline de escritura. La UI sigue viva y lo que encoló
  sale cuando el socket se libera; revisar si aparece en uso real.
- **Dos goroutines que depurar.** Los tests del paquete corren con `-race`.

## Cómo se comprueba

Tres pruebas de aceptación sobre un `socketpair` en `eventloop`:

1. Con la UI parada 500 ms, 200 `xdg_wm_base.ping` se contestan en menos de
   50 ms y no se pierde ningún evento hacia la UI.
2. Con el compositor sin leer y la goroutine Wayland bloqueada en un `write`,
   10 000 `Post` retornan al instante y se ejecutan en orden después. La UI
   real sigue pintando y aplicando resultados.
3. Con un init de un segundo, el primer fotograma llega al compositor en menos
   de 100 ms desde el `Configure`; la UI real solo se pinta tras `SetReady`, y
   después no se pinta más.

Además, `example/widgets` se ha probado contra niri: la ventana aparece unos
170 ms después de lanzarla con el init retenido 2-3 s, sobrevive a
redimensionados rápidos y a pantalla completa, y cerrarla desde el compositor
sale con código 0.

## Pendiente

- **Umbral del loader.** Se pinta siempre; si el init tarda menos de unos
  100 ms puede ser peor verlo un instante. Decidir con mediciones.
- **Pool de buffers reutilizable.** Hoy vive en `example/widgets`.
- **Deadline de escritura** si un compositor atascado se vuelve un problema.
- **`Install` → `SetReady`.** La spec congelada habla de `UI.Install(root)`; lo
  construido es `SetReady()`, porque la aplicación posee su UI y el paquete no
  tiene nada que instalar.

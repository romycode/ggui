# Capa de ventana: una ventana Wayland lista para usar

> **Documento vivo.** Describe `window` tal como está hoy. El diseño y el plan
> con los que se construyó están congelados en
> `docs/archive/specs/2026-09-19-window-design.md` y
> `docs/archive/plans/2026-09-19-window-implementation.md`.

`window` abre una ventana Wayland sobre `eventloop` y la mantiene pintada, de
modo que una aplicación escriba solo lo suyo: cómo se pinta y cómo reacciona a
la entrada. Se ocupa de los globals, la superficie y su rol xdg, el
`configure`/`ack`, la pool de buffers shm, el reloj de fotogramas, el foco, el
loader y el apagado. La aplicación pinta en **unidades lógicas** sobre el
`canvas` que se le entrega y no ve nada de eso. `example/widgets` es el ejemplo
completo: su `window.go` pasó de 824 a 396 líneas al migrarlo.

## API

```go
func Run(cfg Config, init func(w *Window) (Content, error)) error
```

- **`Config`:** `Title`, `AppID` (nombre DNS inverso, el que enlaza con el
  `.desktop`) y `Width`/`Height` iniciales en unidades lógicas. Una dimensión a cero o negativa
  toma **su propio** valor por defecto, 640 el ancho y 480 el alto, con
  independencia de la otra (`Config{Width: 300}` es 300x480); título y app id
  vacíos se dejan sin fijar.
- **`Content`:** lo que `init` devuelve, los callbacks de la aplicación. Solo
  `Paint` es obligatorio; los nulos se ignoran. Todos corren en la goroutine de
  UI, de uno en uno, sin locks, y **no deben bloquear**: mientras uno corre, la UI
  ni pinta ni entrega entrada, pero la goroutine Wayland sigue leyendo el socket
  y contestando al compositor (los `ping`, por ejemplo).
  - `Paint(cv, now) (animating bool)`: un fotograma entero en unidades lógicas;
    `now` es el reloj en ms del último frame callback; devuelve si quiere otro
    fotograma ya.
  - `OnKey(keyboard.Event)` y `OnPointer(pointer.Event)`: teclas con keysym y
    texto resueltos; movimiento, botones, clics y arrastres en unidades lógicas.
  - `OnKeyboardFocus(bool)` y `OnPointerFocus(bool)`: foco del teclado, y puntero
    encima de la ventana.
  - `OnResize(w, h int)`: el tamaño lógico, antes del primer `Paint` a cada tamaño.

- **`Window`:** la ventana abierta; la construye `Run` y se la pasa a `init`.
  `Do(fn)`, `Context()`, `SetTitle(s)` y `Close()` son seguros desde **cualquier
  goroutine**; `Invalidate()` y `Size()` son **solo de la de UI**. `Do` encola
  `fn` en la goroutine de UI sin bloquear: es como una tarea de fondo publica su
  resultado, y lo que se encola tras cerrarse la ventana se descarta. `Context`
  se cancela al cerrarse y existe antes de que la ventana esté en pantalla, así
  que `init` puede dárselo a lo que arranque.
- **`Run`** bloquea hasta que la ventana se cierra. Devuelve `nil` si se cerró
  bien (el compositor o `Window.Close`) y el error en otro caso.

### Una aplicación mínima

```go
type app struct {
	win *window.Window
	on  bool
}

func (a *app) paint(cv *canvas.Canvas, now uint32) (animating bool) {
	bg := canvas.Color{R: 0x20, G: 0x20, B: 0x28, A: 0xff}
	if a.on {
		bg = canvas.Color{R: 0x20, G: 0x50, B: 0xc0, A: 0xff}
	}
	cv.Clear(bg)
	return false // nada se mueve: tras este fotograma la ventana se queda quieta
}

func (a *app) onPointer(ev pointer.Event) {
	if ev.Kind == pointer.ButtonDown && ev.Button == 0x110 { // BTN_LEFT
		a.on = !a.on
		a.win.Invalidate()
	}
}

func main() {
	err := window.Run(window.Config{Title: "hola", AppID: "example.hola", Width: 640, Height: 360},
		func(w *window.Window) (window.Content, error) {
			a := &app{win: w} // lo lento va aquí: el loader se ve mientras tanto

			ctx := w.Context()
			go func() { // el trabajo de fondo publica con Do y para con el contexto
				select {
				case <-time.After(2 * time.Second):
					w.Do(func() { a.on = true; w.Invalidate() })
				case <-ctx.Done():
				}
			}()

			return window.Content{Paint: a.paint, OnPointer: a.onPointer}, nil
		})
	if err != nil {
		log.Fatal(err)
	}
}
```

`example/widgets` añade `OnKey` y `OnResize`, una tarea lenta con Enter, un cursor
que parpadea con un timer y `tasks`, con el que `main` espera a sus goroutines
porque `Run` no lo hace.

## Arranque

`Run` conecta y, en la goroutine que lo llamó, que pasa a ser la goroutine
Wayland:

1. Hace bind de `wl_compositor`, `wl_shm` y `xdg_wm_base`, obligatorios: sin uno
   `Run` devuelve un error antes de crear ninguna superficie. `wl_seat` es
   **opcional**: sin él no hay teclado ni puntero, y sigue siendo una ventana.
2. Crea la superficie, el `xdg_surface` y el `xdg_toplevel`, fija título y app id
   y hace un `commit` sin buffer, que es lo que provoca el primer `configure`.
3. Crea el `eventloop.Loop` (con `Keyboard.NextRepeat`/`Tick` de temporizador si
   hay seat), arranca la goroutine de UI, lanza `init` en **una goroutine
   propia** y entra en `Loop.Run`.

Con el primer `configure` (que la goroutine Wayland contesta con
`ack_configure` **antes** de avisar a la UI: no se adjunta nada sin ack) la UI
construye la pool al tamaño de la ventana y, en cuanto el primer buffer existe,
pinta el loader. `init` corre a la vez y nada de eso lo espera. Cuando devuelve,
el `Content` se instala en la goroutine de UI y el siguiente fotograma ya es el
suyo. Un `Content` sin `Paint` no se instala: es un fallo de arranque, no una
ventana colgada de un spinner. La aplicación no ve ni el loader, ni las fases
(`Loading`, `Ready`, `Failed`, las de `eventloop`), ni la superficie, ni la
escala.

## Foco, entrada y tamaño

**Durante la carga.** Las teclas y los eventos de puntero se **descartan**, no
se guardan (lo hace `eventloop`). El foco no: la ventana recuerda el último
estado que dijo el compositor, y al instalar el `Content` le llama, por este
orden, `OnResize` con el tamaño de ahora, `OnKeyboardFocus(true)` y
`OnPointerFocus(true)` para los dispositivos que ya tenían foco. Si el foco se
ganó y se perdió durante la carga, no llama a nada. Después, los callbacks de
foco se llaman a cada cambio; un estado repetido no se vuelve a notificar. **La
posición del puntero no se recupera:** `OnPointerFocus` no la lleva y llega con
el primer movimiento.

**Tamaño.** `OnResize` se llama antes del primer `Paint` a cada tamaño lógico
(la UI atiende los eventos antes de pintar en cada vuelta). Un `configure` que
no cambia el tamaño repinta pero no llama a `OnResize`.

- Una dimensión a cero significa «decide tú» y se conserva **por dimensión**:
  `configure(0, 300)` conserva el ancho y toma el alto.
- Si `init` termina antes del primer `configure`, `OnResize` en la instalación
  usa el tamaño de `Config`; con el primer `configure` se vuelve a llamar con el
  tamaño real, si es distinto.
- Cada cambio sustituye la pool entera, porque un `wl_buffer` no cambia de
  tamaño. Si no se puede construir, ver «Fallos de la pool».

**Aviso: `OnPointer` puede saltarse posiciones.** El `Inbox` de `eventloop`
fusiona los `Position` y `DragMove` consecutivos y acota a 64 las repeticiones de
tecla pendientes. Si un callback tarda, verá el último movimiento y no todos los
intermedios. Aparte de eso, y de que las teclas y los eventos de puntero se
descartan mientras la aplicación carga (ver arriba), con la aplicación ya lista
no se descarta, funde ni reordena nada más: ni los botones, ni los clics, ni el
foco, ni el `configure`, ni las pulsaciones y liberaciones de tecla (de las teclas
solo están acotadas sus repeticiones). Quien dibuje un trazo con los `Position`
tiene que saberlo.

## Pintado

Cada `Paint` pinta **un fotograma entero** en un buffer libre y lo presenta:
`attach`, `damage_buffer` de todo el buffer, `frame` y `commit`, en ese orden y
en la goroutine Wayland. No se conserva nada de lo que un buffer tenía antes.
Si el canvas acaba con un error (sticky), el fotograma no se presenta y se
registra, y **el canvas de ese buffer se reconstruye** sobre el mismo mapeo: los
errores del canvas no se pueden limpiar, y un buffer que no se presentó nunca
queda ocupado, así que la pool lo volvería a dar una y otra vez y la ventana se
congelaría. El siguiente `Paint` empieza con un canvas limpio; no hay repintado
automático, la aplicación pide otro con `Invalidate` como tras cualquier `Paint`
que no tuvo nada que enseñar. Si ni así se puede reconstruir, es un fallo de la
pool (ver «Fallos de la pool»).

El reloj es el de `eventloop`: un solo fotograma en vuelo, y el siguiente solo
tras el frame callback. Una UI estática (`Paint` devuelve `false`) **deja de
hacer commits** tras su último fotograma y no gasta CPU; se repinta con
`Invalidate`. Una animación devuelve `true` mientras se mueve. Que llegue un
buffer solo libera el reloj: no fuerza un repintado que nadie pidió.

## La pool de buffers

Dos buffers ARGB8888, escala fija a 1 (hoy las unidades lógicas son píxeles).
Cada uno es un `memfd` sellado contra encogerse, mapeado por el cliente, con un
`canvas.Canvas` prestado sobre el mapeo. Se reparte entre las dos goroutines: la
de **UI** reserva la memoria, posee el mapeo y el canvas y lleva la contabilidad
(libre, ocupado, muerto); la **Wayland** crea el `wl_shm_pool` (que destruye en
cuanto el buffer existe) y el `wl_buffer`, y los destruye, todo por `Loop.Post`.
El resultado vuelve por `UI.Do` y el `release` como evento.

Un fotograma no se entrega hasta que su `wl_buffer` existe y el compositor no lo
lee. Al cambiar de tamaño los fotogramas viejos mueren: se desmapean, los
buffers ya creados se destruyen, y **un buffer que llega para un fotograma
muerto se destruye a la llegada y no se adopta.** Al cerrar solo se desmapea:
no queda conexión con la que destruir.

### Fallos de la pool

| Situación | Resultado |
| --- | --- |
| No se puede construir la pool al **primer** tamaño | No hay dónde mostrar nada: la ventana se cierra y `Run` devuelve el error. |
| Un cambio de tamaño falla **cargando** | Arranque fallido: pantalla de fallo al tamaño de antes, ventana abierta, `Run` devuelve el error al cerrarla. |
| Un cambio de tamaño falla con la aplicación **lista** | Se registra y se rechaza; la ventana sigue al tamaño anterior y la aplicación no recibe un tamaño que nunca tuvo efecto. `Run` no devuelve error. |
| Un cambio de tamaño falla con el fallo **ya en pantalla** (fase `Failed`) | Igual que con la aplicación lista: solo se registra y se rechaza, la pantalla de fallo sigue al tamaño anterior y `Run` devuelve el error que ya tenía. |
| Un `wl_buffer` no se crea, cargando o con el fallo ya en pantalla, y queda otro fotograma que no ha fallado | Pantalla de fallo, ventana abierta. |
| Un `wl_buffer` no se crea con la aplicación **lista**, o no queda otro fotograma | La ventana se cierra y `Run` devuelve el error: una ventana abierta que deja de actualizarse es el único resultado inaceptable. |
| El canvas de un fotograma no se puede reconstruir tras un error de `Paint` | Es un `wl_buffer` que falla: el fotograma no se vuelve a dar y se aplican las dos filas de arriba. |

Un `wl_buffer` cuya creación falla porque la conexión ya se cerró no es un fallo
de la pool: `Run` cuenta cómo terminó la conexión, y un `Window.Close` durante la
creación de los buffers sigue siendo un cierre ordenado.

## Cierre y errores

La ventana se cierra cuando el compositor lo pide (`xdg_toplevel.close`) o la
aplicación llama a `Window.Close`; `Run` devuelve `nil` y se cancela `Context`.

- **`Window.Close`** va por `Loop.Close`, que cierra el socket **desde la
  goroutine que lo llama** y despierta al bucle. No encola una petición: un
  compositor que dejó de leer bloquea a la goroutine Wayland en un `write`, y una
  closure tras ese `write` no correría nunca. `Conn.Close` es seguro desde
  cualquier goroutine, por eso también lo es desde la de UI. Sirve en una ventana
  ya cerrada.
- **Un error de `init` gana** al modo en que terminó la conexión. Una ventana
  cuyo `init` falló se queda **abierta mostrando el fallo** (`PaintFailed`) hasta
  que se cierra, y entonces `Run` devuelve ese error. Un `Content` sin `Paint` es
  el mismo caso.
- **Un pánico en `init`** se recupera en su goroutine, se registra con la pila y
  es un fallo como cualquier otro (`window: init panicked: ...`); si `init`
  termina su goroutine sin volver (`runtime.Goexit`) también. El proceso no muere
  bajo una ventana abierta.
- **Los errores de conexión** salen como `window: ...` envolviendo el original;
  `wlcore.ErrClosed` es un cierre ordenado y no se devuelve.
- **`Run` espera a la goroutine de UI, nunca a `init`.** `init` es código de la
  aplicación y puede estar parado en lo que sea: se le avisa por `Context` y lo
  que devuelva tarde se descarta. Al retornar `Run`, los callbacks de `Content`
  han terminado; las goroutines que lanzó la aplicación, `init` incluida si
  ignora el contexto, siguen siendo suyas. `example/widgets` las espera con
  `tasks` después de `Run`.

**Qué hace un `init` al que el cierre se le adelanta:** puede devolver
`ctx.Err()` (o cualquier error, o un `Content` inválido, o entrar en pánico) sin
que eso convierta el cierre en un error de `Run`. La regla exacta: **lo que `init`
comunica una vez cancelado el `Context` de la ventana se descarta** (un pánico se
registra igualmente con su pila). La UI cancela el contexto *antes* de que `init`
pueda verlo cerrado, así que esa comprobación es exacta para el patrón habitual,
esperar a `ctx.Done()` y devolver `ctx.Err()`, y no depende de quién llegue antes.
Lo que `init` comunica **mientras la ventana sigue abierta** sí cuenta: se muestra
y gana al cierre ordenado que venga después. `initialize` de `example/widgets`
devuelve un `Content` válido en ese caso, que también es correcto.

## Propiedad de goroutines

La **goroutine Wayland** (la que corre `Loop.Run`) posee `Conn` y todo lo que
sale de él, y es la única que hace peticiones. La **goroutine de UI** posee los
widgets, los canvas y la contabilidad de la pool, y **no llama a `wlcore`**:
encola closures con `Loop.Post`, y lo que dice el compositor vuelve con
`UI.Push` o `UI.Do`, nunca como retorno. La única excepción es `Window.Close` →
`Loop.Close` → `Conn.Close`, seguro desde cualquier goroutine (como `Done` y
`Err`, que la UI de `window` no usa). El foco se guarda como booleanos, nunca
como el `*wlcore.Surface` del evento, que en la UI es solo identidad.

## Cómo se prueba

**`internal/wltest`** es un compositor falso, el otro extremo de un `socketpair`,
solo para tests (importa `testing`). `wltest.NewServer(t, Options)` devuelve un
`Server` con la `*wlcore.Conn` para entregar a `run`; tiene su propia goroutine
de lectura, y quien use la `Conn` la bombea (`window`, con su `Loop`). Habla lo
justo de `wl_compositor`, `wl_shm`, `wl_seat` (teclado y puntero) y xdg-shell
para abrir una ventana, y **lee los píxeles de verdad**: recibe el fd de cada
pool por `SCM_RIGHTS` y lo mapea, así que `Commits()` y `Buffers()` entregan lo
que el cliente pintó y no lo que dice haber pintado.

- `Options`: `Seat`, `ReleaseMode`, `Vsync`, `Omit` (interfaces que el registry no
  anuncia, para probar un global obligatorio ausente), `Width`/`Height` del primer
  `configure` y `ManualConfigure` para dirigirlo desde el test.
- `ReleaseMode`, porque los compositores reales difieren y un cliente que solo
  funciona con uno está roto: `ReleaseImmediately` libera el buffer al hacer
  commit; `ReleaseOnNextCommit` lo retiene hasta el siguiente, y una pool de dos
  se atasca con él si el cliente no espera al `release`.
- Se inyectan `configure`, `ping`, cierre, foco, teclas, modificadores, movimiento
  y botones del puntero (`Configure`, `Ping`, `CloseToplevel`, `Key`,
  `PointerMotion`, ...) y se observan `Commits`, `Requests`, `Buffers`,
  `LiveBuffers`, `Title` y `AppID`.
- **`Errors()`** son las violaciones de protocolo que el falso vio: un `attach`
  antes del primer `ack_configure`, un `ack` de un serial que no se envió, una
  petición a un objeto inexistente, un buffer que no cabe en su pool. Vacío afirma
  que el cliente se portó bien; conviene mirarlo con la ventana ya cerrada, cuando
  no puede llegar nada más.

**Límites del falso.** Una sola ventana (la primera superficie y su toplevel). No
anuncia `wl_output`, `viewporter` ni `fractional-scale`, no modela popups y no
inyecta touch, scroll ni ejes. Contesta a los frame callbacks a un `Vsync` fijo,
sin modelar ventanas ocultas. No implementa la máquina de estados del protocolo
entera, solo lo que lista `Errors()`: que no dé error no prueba que un compositor
real lo acepte. Un `commit` sin `attach` nuevo vuelve a presentar y liberar el
buffer actual. `Server.Commits()` guarda solo los **últimos 64** commits y descarta el más
antiguo si el test no los lee, para no frenar nunca al falso: un test que quiera
ver todos los fotogramas tiene que leerlos a medida que llegan. Si el cliente
cierra primero, `Errors()` puede incluir entradas `writing event ...` del socket
cerrado, que los tests de `window` filtran.

**Contra un compositor real.** `window/real_test.go` abre una ventana en la sesión
viva, espera el primer fotograma, la cierra con `Window.Close` y exige que `Run`
devuelva `nil`. Un error de protocolo del compositor (un `wl_display.error`)
termina la conexión y `Run` lo devuelve envuelto, así que un `nil` lo descarta; el
test lo distingue en el mensaje con `errors.As`. No engancha `Conn.OnError`,
porque `setup` instala el suyo y `OnError` sustituye al anterior. Lo que no puede
ver es un error que el compositor mande después de que el propio cierre haya
terminado la conexión: lo primero que la termina es lo que ella guarda. **No corre
por defecto**, porque abre una ventana en el escritorio de
quien lo lanza (sin la variable, o sin un `WAYLAND_DISPLAY` que apunte a un socket
alcanzable, se salta y dice cómo activarlo):

```
GGUI_REAL_WAYLAND=1 go test ./window -run Real -race -v
```

Que el compositor conteste al frame callback del primer fotograma se registra
pero no se exige: uno con la ventana fuera de vista puede retenerlo. Los tests de
`window` esperan con canales, barreras o sondeos acotados del estado del falso,
no con `time.Sleep` como sincronización.

## Límites y pendiente

- **Sin HiDPI ni escala fraccionaria todavía.** La API ya es en unidades lógicas
  y la capa poseerá la escala (hoy fija a 1, en un único punto de la pool); no
  usa `viewporter` ni `fractional-scale`.
- **Sin forma de cursor:** no usa `cursor-shape`.
- **Una sola ventana,** sin popups y sin decoraciones del lado del cliente.
- **Los pánicos solo se recuperan en `init`.** Uno en un callback de `Content`
  mata el proceso, como cualquier pánico en una goroutine.

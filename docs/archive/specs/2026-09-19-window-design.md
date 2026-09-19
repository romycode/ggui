# Diseño de la capa de ventana

**Fecha:** 2026-09-19

## Objetivo

Un paquete `window` que abra una ventana Wayland lista para usar sobre
`eventloop`, de modo que una aplicación escriba solo lo suyo: cómo se pinta y
cómo reacciona a la entrada. Hoy eso exige montarlo a mano cada vez.

`example/widgets/window.go` tiene 806 líneas, y la mayor parte no es de la
aplicación: registry y binds, `xdg_surface` y `xdg_toplevel`, el ciclo
`configure`/`ack`, la pool de buffers repartida entre las dos goroutines, los
`release`, los frame callbacks, el arranque del bucle y el apagado. Los otros
cinco ejemplos (`keylog`, `wayland`, `hidpi`, `scaling`, `cursorshape`) repiten
el registry, el toplevel y el buffer con memfd. Cada aplicación nueva copiaría
todo eso, y cada corrección, como la del orden del despertar en `Loop`, habría
que aplicarla en cada copia.

Sale también de aquí un pendiente de `eventloop.md`: la pool de buffers
reutilizable.

## Qué no cambia

- **`eventloop` y su contrato.** `window` es un cliente de `eventloop`, no lo
  reabre. La goroutine Wayland posee `Conn`; la UI habla con ella por `Post` y
  por eventos.
- **`keyboard`, `pointer`, `canvas`, `widget` y `text`.**
- **Los ejemplos de bajo nivel** (`wayland`, `keylog`, `hidpi`, `scaling`,
  `cursorshape`) se quedan como están. Enseñan el protocolo sin capas por
  encima, y ese es su valor.

## API

```go
package window

type Config struct {
    Title, AppID  string
    Width, Height int // tamaño lógico inicial; cero es 640x480
}

// Content es lo que la aplicación entrega cuando su UI está lista.
// Todos los callbacks corren en la goroutine de UI.
type Content struct {
    // Paint dibuja un fotograma completo en cv, en unidades lógicas.
    // Devuelve si quiere un fotograma en cada callback del compositor.
    Paint func(cv *canvas.Canvas, now uint32) (animating bool)

    OnKey           func(keyboard.Event)
    OnPointer       func(pointer.Event)
    OnKeyboardFocus func(focused bool)
    OnPointerFocus  func(focused bool)
    // OnResize se llama antes del primer Paint a cada tamaño lógico.
    OnResize func(width, height int)
}

type Window struct{ /* opaco */ }

func (w *Window) Invalidate()                    // solo goroutine de UI
func (w *Window) Size() (width, height int)      // solo goroutine de UI
func (w *Window) Do(fn func())                   // cualquier goroutine
func (w *Window) Context() context.Context       // cancelado al cerrar
func (w *Window) SetTitle(title string)          // cualquier goroutine
func (w *Window) Close()                         // cualquier goroutine

// Run abre la ventana y bloquea hasta que se cierra. init corre en su propia
// goroutine, mientras la ventana muestra el loader.
func Run(cfg Config, init func(w *Window) (Content, error)) error
```

Una aplicación entera:

```go
err := window.Run(window.Config{Title: "widgets", AppID: "ggui.example.widgets"},
    func(w *window.Window) (window.Content, error) {
        app := newApp(loadFont()) // lento: la ventana ya está abierta
        return window.Content{
            Paint:     app.paint,
            OnKey:     app.key,
            OnPointer: app.pointer,
        }, nil
    })
```

Decisiones de forma:

- **`init` devuelve el `Content`** en vez de instalarlo con una llamada. Así
  no hay un estado a medias en el que la ventana esté lista sin contenido.
- **La aplicación no ve fases, ni loader, ni `*wlcore.Surface`, ni escala.** El
  loader y la pantalla de fallo son de la capa. Los eventos de foco llegan como
  `bool`, sin la superficie.
- **Las unidades son siempre lógicas.** `Paint` recibe un canvas cuyo
  `Width()` y `Height()` son el tamaño lógico. La escala vive dentro de la
  capa: hoy es 1 y el día que entre `wp_fractional_scale` solo cambiará la
  creación de canvases y el `wp_viewport`, no esta API.
- **Los datos de `Content` son funciones,** como los listeners del resto del
  repo, y las nulas se ignoran. Solo `Paint` es obligatorio.

## Arranque

Es la secuencia de `eventloop.md`, ahora dentro de `Run`:

```
Connect → registry → binds → superficie xdg → commit inicial (sin buffer)
   → arrancan: bucle Wayland (esta goroutine), UI, init (otra goroutine)
   → Configure → primer fotograma con el LOADER
   → init termina → Do(instalar Content; SetReady) → siguiente fotograma: la app
```

Requeridos: `wl_compositor`, `wl_shm`, `xdg_wm_base`. **`wl_seat` es opcional**:
sin él la ventana no tiene teclado ni puntero, y sigue siendo una ventana
válida. Si falta algo requerido, `Run` devuelve el error **antes** de abrir
nada. El camino hasta el primer fotograma no importa nada de la aplicación.

Si `init` devuelve error, la UI pasa a `PhaseFailed`: se muestra
`PaintFailed`, la ventana sigue abierta y `Run` devuelve ese error al
cerrarse. Cerrar la ventana funciona en cualquier fase.

## Foco y entrada durante la carga

`eventloop` descarta teclas y clics hasta que la UI está lista, pero entrega el
foco. La capa recuerda el último estado de foco de teclado y de puntero y, al
instalar el `Content`, llama a `OnResize` con el tamaño actual y a
`OnKeyboardFocus(true)` / `OnPointerFocus(true)` si estaban enfocados. Así la
aplicación nunca se entera tarde de un foco ganado mientras cargaba. La
posición del puntero no se recupera: llega con el primer movimiento.

## Pool de buffers

Pasa de `example/widgets` a `window/pool.go`, con el mismo reparto entre goroutines:

- La goroutine de UI reserva la memoria (`memfd`, `mmap`, sellado) y es dueña de
  cada `Canvas`.
- La goroutine Wayland crea el `wl_buffer` (`CreatePool`, `CreateBuffer`),
  instala el listener de `release` y lo comunica a la UI con `Do`.
- `release` viaja como evento. Un fotograma solo se entrega si tiene buffer y
  no está ocupado ni muerto.
- Un cambio de tamaño reemplaza la pool entera. Un buffer que aún estaba
  creándose cuando lo reemplazaron se destruye al llegar.
- Dos buffers (`ARGB8888`). Cada fotograma se pinta entero: el contenido previo
  del buffer tiene dos fotogramas de antigüedad y no se conserva nada.
- Al cerrar solo se desmapea: no hay conexión con la que destruir.

## Cierre y errores

Un `xdg_toplevel.close` cierra la conexión desde la goroutine Wayland.
`Window.Close` desde cualquier goroutine usa `Loop.Close`. En ambos casos
`Loop.Run` devuelve `ErrClosed`, la capa empuja `EvClosed`, espera a que la UI
termine y libera la pool. `Run` devuelve `nil` en un cierre ordenado, o el
error que terminó la conexión. Si `init` falló, ese error tiene prioridad.

## Cómo se prueba

La capa se prueba contra un **compositor falso** en `internal/wltest`,
compartido con los tests de `eventloop`, que hoy tienen su propia copia. Habla
lo mínimo: registry con globales, `wl_compositor`, `xdg_wm_base`, `wl_shm` (recibe
el fd de la pool y lo mapea, así el test puede **leer los píxeles** que la
aplicación presentó), `wl_seat` con teclado y puntero, y responde a cada
`commit` con un frame callback tras un vsync simulado. Se le puede pedir un
`configure`, inyectar teclas y movimiento, y cerrar el toplevel.

Con eso se comprueba, con `-race`:

1. La ventana abre y el primer `commit` llega en menos de 100 ms desde el
   `configure`, con el loader en los píxeles, aunque `init` tarde un segundo.
2. Tras `init`, los píxeles pasan a ser los de `Paint`, y una UI quieta deja de
   generar `commit`.
3. Teclado y puntero llegan a los callbacks, en orden, tras la carga; antes no.
4. Un redimensionado rápido no pierde ni duplica buffers y el fotograma
   siguiente tiene el tamaño nuevo.
5. Cerrar desde el compositor y desde `Window.Close` termina `Run` con éxito.
6. Un `init` que falla muestra `PaintFailed` y `Run` devuelve el error.
7. Un compositor que libera el buffer al instante y otro que lo libera tras el
   siguiente `commit` dan el mismo resultado.

Lo que el falso no cubre —diferencias reales entre compositores— se sigue
comprobando a mano en niri, como se hizo con `widgets`.

## Riesgos

- **Fidelidad del compositor falso.** Un test verde contra un falso demuestra
  que la capa cumple lo que el falso entiende del protocolo, no lo que hace
  niri. Por eso queda la verificación manual y por eso el falso implementa el
  ciclo de `release` de dos maneras.
- **`configure` real.** Un compositor puede mandar varios `xdg_toplevel.configure`
  antes de un `xdg_surface.configure`, o tamaño cero. La capa aplica el último
  tamaño pendiente y trata cero como "el que ya tenías".
- **Migrar `widgets` toca sus tests.** Sus tests de `window` dependen de la
  estructura que desaparece; se reescriben sobre `Content`.

## Decisiones abiertas

- **Nombre** (`window` provisional; la alternativa era `app`).
- **Un loader personalizable.** Fuera de la primera versión: `Config` no lleva
  un hook de loader. Si una aplicación lo quiere, se añade sin romper nada.
- **Tres buffers.** Con dos, un compositor lento puede dejar a la UI sin buffer.
  Se mantiene dos y se mide; `Content` no cambiaría.
- **`example/keylog` se queda en bajo nivel.** Necesita ganchos del teclado que
  `window` no debe exponer (`OnKeymap`, `RepeatInfo`).

## Fuera de alcance

Varias ventanas, popups, decoraciones del lado del cliente, cursor y forma del
cursor, HiDPI y escala fraccionaria, portapapeles, y cualquier widget nuevo.

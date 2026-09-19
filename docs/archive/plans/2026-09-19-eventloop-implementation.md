# Event Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Separar la goroutine dueña de `wlcore.Conn` (goroutine Wayland) de la goroutine de UI, con un reloj de fotogramas basado en `wl_surface.frame`, un ejecutor para tareas asíncronas y una ventana que se abre de inmediato con un loader.

**Architecture:** La goroutine Wayland espera en `unix.Poll([sock, eventfd], próximo repeat)`, despacha, ejecuta las closures que la UI le encola con `Post` y empuja eventos a un `Inbox` que nunca bloquea. La UI corre en otra goroutine con su propia cola (`UI.Do`) y una máquina de estados `FrameClock` que pinta a lo sumo un fotograma en vuelo. `keyboard` y `pointer` no cambian.

**Tech Stack:** Go 1.27, `wayland/wlcore`, `golang.org/x/sys/unix` (`Poll`, `Eventfd`), `canvas`, pruebas estándar con `-race`. Sin dependencias nuevas.

**Spec:** `docs/archive/specs/2026-09-19-eventloop-design.md`

## Global Constraints

- Solo Linux y Wayland; sin cgo en el código publicado.
- Dependencias limitadas a la biblioteca estándar y `golang.org/x/...`.
- `wlcore.Conn` y los controladores de entrada pertenecen a **la goroutine Wayland**. La UI no llama a `Conn`, a métodos generados ni a `Conn.Err()` antes de `Done()`.
- Los ficheros `*.gen.go` nunca se editan.
- Los caminos de dibujo de `canvas` siguen sin asignar memoria.
- Todo test del paquete nuevo se ejecuta con `-race`.
- Documentación en español; código y comentarios en inglés.
- El camino hasta el primer frame no importa nada de la aplicación (solo `wlcore` y `canvas`).

## Review Focus

- Un `Post` justo antes de entrar en `Poll` no se pierde (flag atómico y `eventfd` en orden correcto).
- `Inbox.Push` no bloquea nunca, ni con la UI parada.
- Teclas, botones, clics, foco y `Configure` conservan orden y no se descartan; solo se fusionan `Position` y `DragMove` consecutivos y se acotan los `Repeated`.
- Un buffer encolado en un `Present` siempre acaba liberado o marcado libre en el cierre; no hay fugas por `Present` descartados.
- El loader no depende de fuentes, del árbol de widgets ni de nada que cargue la aplicación.
- La UI nunca usa `*wlcore.Surface` salvo como identidad.

---

### Task 0: Corregir la promesa falsa de `DispatchUntil`

Un deadline ya vencido **no** lee del socket aunque haya datos (verificado en Go 1.27.1: devuelve `i/o timeout` con 0 bytes). El comentario de `wire.go` dice lo contrario y ningún test lo cubre.

**Files:**
- Modify: `wayland/wlcore/wire.go` (comentario de `DispatchUntil`)
- Modify: `wayland/wlcore/dispatchuntil_test.go`

- [ ] **Step 1: Write a test that pins the real behavior**

```go
// An expired deadline does not read, but it must not lose anything either:
// the bytes stay buffered in the kernel and the next dispatch delivers them.
func TestDispatchUntilExpiredDeadlineLeavesDataForTheNextDispatch(t *testing.T) {
    c, server := newDispatchTestConn(t)
    body := NewEncoder().Uint32(1).Uint32(1).Bytes()
    if _, err := server.Write(rawMessage(displayID, opEvtDisplayDeleteID, body)); err != nil {
        t.Fatal(err)
    }
    time.Sleep(20 * time.Millisecond)

    if err := c.DispatchUntil(time.Now().Add(-time.Second)); err != nil {
        t.Fatalf("expired deadline: %v", err)
    }
    if err := c.Dispatch(); err != nil {
        t.Fatalf("Dispatch after expired deadline: %v", err)
    }
}
```

- [ ] **Step 2: Run it.** Debe pasar ya (fija el comportamiento, no lo cambia).
- [ ] **Step 3: Fix the comment.** Sustituir el párrafo "A deadline that has already passed does not skip the read..." por uno que diga lo contrario y remita a `eventloop`, que no usa `DispatchUntil`.
- [ ] **Step 4: `go test ./wayland/wlcore -race`.** Commit.

---

### Task 1: Cola de closures (`mailbox`)

**Files:**
- Create: `eventloop/doc.go`, `eventloop/mailbox.go`, `eventloop/mailbox_test.go`

**Interfaces:**
- Produces: `type mailbox struct`, `(*mailbox).put(fn func())` (nunca bloquea), `(*mailbox).take(dst []func()) []func()`, `(*mailbox).signal() <-chan struct{}` (capacidad 1) y un hook `onPut func()` para que `Loop` enganche el `eventfd`.

- [ ] **Step 1: Failing tests:** `put` desde 64 goroutines conserva el orden por productor y no pierde ninguna; `put` con nadie consumiendo no bloquea; la señal no se pierde si `put` ocurre entre `take` y la espera.
- [ ] **Step 2: RED. Step 3: implement** con `sync.Mutex`, slice y `select { case ch <- struct{}{}: default: }`. **Step 4: GREEN con `-race`.** Commit.

---

### Task 2: Inbox de eventos con fusión

**Files:**
- Create: `eventloop/event.go`, `eventloop/inbox.go`, `eventloop/inbox_test.go`

**Interfaces:**
- Produces: `type EventKind uint8` (`EvKey`, `EvPointer`, `EvKeyboardFocus`, `EvPointerFocus`, `EvConfigure`, `EvBufferRelease`, `EvFrameDone`, `EvClosed`), `type Event struct` (unión por campos: `Key keyboard.Event`, `Pointer pointer.Event`, `Surface *wlcore.Surface`, `Width, Height int32`, `Time uint32`, `Buffer *wlcore.Buffer`), `(*Inbox).Push(Event)`, `(*Inbox).Drain(dst []Event) []Event`, `(*Inbox).Signal() <-chan struct{}`.

- [ ] **Step 1: Failing tests** (tabla):
  - 1000 `Position` seguidos sin drenar → 1 elemento, el último.
  - `Position, ButtonDown, Position` → 3 elementos en ese orden (no se fusiona a través de otro evento).
  - `DragMove` consecutivos se fusionan; `DragMove, Position` no.
  - 100 `Key` con `Repeated` sin drenar → se quedan 64; pulsaciones y sueltas (`Pressed`, `Released`) nunca se descartan aunque haya 64 repeats pendientes.
  - `Push` con nadie drenando no bloquea (con timeout de 1 s).
- [ ] **Step 2: RED. Step 3: implement. Step 4: GREEN con `-race`.** Commit.

---

### Task 3: `FrameClock` (máquina de estados pura)

**Files:**
- Create: `eventloop/frameclock.go`, `eventloop/frameclock_test.go`

**Interfaces:**
- Produces: `type FrameClock struct`, `(*FrameClock).Invalidate()`, `(*FrameClock).SetAnimating(bool)`, `(*FrameClock).BufferFree(bool)`, `(*FrameClock).OnFrameDone(t uint32)`, `(*FrameClock).ShouldPaint() bool`, `(*FrameClock).Painted()` (pasa a esperando). Sin dependencias de Wayland ni de tiempo: se prueba con tablas.

- [ ] **Step 1: Failing tests** con la tabla de la spec:
  - idle + `Invalidate` + buffer libre → `ShouldPaint` verdadero; tras `Painted` → falso hasta `OnFrameDone`.
  - esperando + `Invalidate` → falso; tras `OnFrameDone` → verdadero.
  - esperando + `SetAnimating(true)` + `OnFrameDone` → verdadero; con `SetAnimating(false)` y sin `Invalidate` → falso (vuelve a idle).
  - `Invalidate` sin buffer libre → falso; `BufferFree(true)` → verdadero.
  - Nunca dos `ShouldPaint` verdaderos sin un `OnFrameDone` entre medias.
- [ ] **Step 2: RED. Step 3: implement. Step 4: GREEN.** Commit.

---

### Task 4: `Loop`, la goroutine Wayland con `Poll` y `eventfd`

**Files:**
- Create: `eventloop/loop.go`, `eventloop/loop_test.go`, `eventloop/wake_linux.go`

**Interfaces:**
- Produces: `func New(conn *wlcore.Conn) (*Loop, error)`, `(*Loop).Post(fn func())` (seguro desde cualquier goroutine, no bloquea), `(*Loop).Run() error` (bloquea; la goroutine que la llama pasa a ser la goroutine Wayland), campos `Deadline func() time.Time` y `OnTick func(now time.Time)` (para `kbd.NextRepeat` y `kbd.Tick`), `(*Loop).Close()`.

- [ ] **Step 1: Failing tests** sobre un `socketpair` (reutilizar `newDispatchTestConn`):
  - `Post` desde otra goroutine despierta un `Run` que esperaba sin datos, en menos de 50 ms.
  - 10 000 `Post` desde varias goroutines → 10 000 ejecuciones, en orden por productor.
  - Un `Post` justo antes de que `Run` entre en `Poll` no se pierde (bucle de 1000 repeticiones).
  - Con `Deadline` a +30 ms y sin datos, `OnTick` se llama a esa hora.
  - Datos en el socket → `Dispatch` los entrega sin que haga falta ningún `Post`.
  - Cierre de `conn` desde fuera → `Run` devuelve `ErrClosed`.
- [ ] **Step 2: RED. Step 3: implement.** `Poll` sobre el fd obtenido una vez con `SyscallConn().Control`; el `eventfd` con `unix.Eventfd(0, EFD_CLOEXEC|EFD_NONBLOCK)`; flag atómico `pending` para no escribir en el `eventfd` una vez por closure; vaciar el `eventfd` **antes** de tomar la cola. Sin `DispatchUntil`.
- [ ] **Step 4: GREEN con `-race`.** Commit.

---

### Task 5: Aceptación 1 y 2 — ninguna goroutine bloquea a la otra

**Files:**
- Create: `eventloop/acceptance_test.go`

- [ ] **Step 1: Test "UI bloqueada no bloquea el socket".** Se crean `Loop` y `UI` sobre un `socketpair`. El handler de un evento de la UI duerme 500 ms. Mientras tanto el servidor de prueba envía `xdg_wm_base.ping`, y el test espera el `pong` en menos de 50 ms.
- [ ] **Step 2: Test "socket bloqueado no bloquea la UI".** El servidor de prueba no lee; se reduce `SO_SNDBUF` y se encolan `Post` de escritura hasta llenar el buffer (la goroutine Wayland queda bloqueada en `Write`). La UI sigue procesando `Do` y avanzando su `FrameClock` durante 500 ms sin retraso apreciable.
- [ ] **Step 3: Run con `-race -count=20`.** Commit.

---

### Task 6: `UI`, ejecutor y bucle de la goroutine de UI

**Files:**
- Create: `eventloop/ui.go`, `eventloop/ui_test.go`

**Interfaces (provisional):**
- Produces: `func NewUI() *UI`, `(*UI).Do(fn func())` (seguro desde cualquier goroutine), `(*UI).Push(Event)` (para los callbacks de la goroutine Wayland), `(*UI).Context() context.Context` (se cancela en `EvClosed`), `(*UI).Run(h Handler) error` con `Handler{ OnEvent func(Event); Paint func(now uint32) (animating bool) }`.

- [ ] **Step 1: Failing tests:**
  - `Do` desde 32 goroutines: todas las closures se ejecutan en **la misma** goroutine (comprobado con un id de goroutine capturado en `Run`).
  - `EvClosed` cancela `Context()` y hace que `Run` devuelva.
  - Con `Paint` devolviendo `true`, `Run` no vuelve a llamar a `Paint` hasta recibir `EvFrameDone`.
  - Con `Paint` devolviendo `false` y sin `Invalidate`, `Run` no vuelve a pintar (UI estática = sin CPU).
- [ ] **Step 2: RED. Step 3: implement** (un `select` sobre la señal del `Inbox`, la de `mailbox` y `Done`; el `FrameClock` decide cuándo llamar a `Paint`). **Step 4: GREEN con `-race`.** Commit.

---

### Task 7: Ventana inmediata con loader

Requisito firme de la spec: el primer `commit` con contenido no espera a nada de la aplicación.

**Files:**
- Create: `eventloop/loader.go`, `eventloop/loader_test.go`
- Create: `eventloop/startup_test.go`

**Interfaces:**
- Produces: `func PaintLoader(cv *canvas.Canvas, width, height float32, t uint32)` (solo formas, sin texto), `(*UI).Install(root Painter)` (se llama desde `Do`; a partir de ahí `Paint` deja de mostrar el loader), `(*UI).Fail(err error)` (pinta el estado de error).

- [ ] **Step 1: Failing tests:**
  - `PaintLoader` no asigna memoria (`testing.AllocsPerRun`, como el resto de dibujo) y deja el daño dentro del área visible.
  - `PaintLoader` en `t` distinto produce píxeles distintos (está animado) y en el mismo `t` produce los mismos (determinista).
  - **Aceptación 3** (`startup_test.go`): con un init que tarda 2 s, el primer `Present` llega al servidor de prueba en menos de 100 ms desde el `Configure`, y el `Present` de la UI real llega después de que el init acabe y no antes.
  - Teclas y clics antes de `Install` se descartan; `EvClosed` siempre se atiende.
  - `Fail(err)` pinta el estado de error y no cierra la ventana.
- [ ] **Step 2: RED. Step 3: implement.** `PaintLoader` con `canvas` (arco o círculos rotando según `t`); ninguna importación de `text` ni de `widget`.
- [ ] **Step 4: GREEN con `-race`.** Commit.

---

### Task 8: Migrar `example/widgets`

**Files:**
- Modify: `example/widgets/window.go`, `example/widgets/ui.go`

- [ ] **Step 1: Reordenar el arranque.** `run()` conecta, hace registry y roundtrip, crea la superficie xdg y hace el commit inicial **antes** de tocar fuentes. `loadFont()` y `newUI(font)` pasan a una goroutine de trabajo que termina con `ui.Do(func(){ ui.Install(root) })`.
- [ ] **Step 2: Callbacks.** `kbd.OnKey`, `ptr.OnEvent`, `OnFocus` de ambos y el listener de `configure` pasan a `ui.Push(...)`. `typeKey` y `pointerEvent` se ejecutan en la goroutine de UI.
- [ ] **Step 3: Presentación.** `redraw()` deja de tocar `surface`: pinta en un buffer libre y encola un `Post` con `Attach`, `DamageBuffer`, `Frame` y `Commit`. `wl_buffer.release` pasa a ser `EvBufferRelease`.
- [ ] **Step 4: Animaciones y tarea asíncrona de demostración.** Cursor parpadeando y un indicador animado con `SetAnimating(true)`. Un botón lanza una tarea lenta simulada (`time.Sleep`) cuyo resultado vuelve por `Do`. Un init lento simulado (variable de entorno) para ver el loader.
- [ ] **Step 5: `go build ./... && go vet ./... && go test -race ./...`.** Los tests de `example/widgets` (`draw_test.go`, `ui_test.go`) se adaptan al nuevo reparto de goroutines.
- [ ] **Step 6: Verificación manual en sesión Wayland real** (`go run ./example/widgets`): la ventana aparece al instante con el loader; con un pintado lento forzado siguen respondiendo el repeat de teclas y el hover; el compositor no marca la app como bloqueada. Commit.

---

### Task 9: Documentación viva

**Files:**
- Create: `docs/eventloop.md`
- Modify: `docs/estado.md`, `docs/widget.md`, `CLAUDE.md`

- [ ] **Step 1:** `docs/eventloop.md` (ES): modelo de dos goroutines, reglas, Inbox, `FrameClock`, arranque con loader, cómo lanzar tareas asíncronas con `Do`.
- [ ] **Step 2:** `docs/estado.md`: fila nueva de `eventloop`; `docs/widget.md`: "se manejan desde la goroutine de UI".
- [ ] **Step 3:** `CLAUDE.md`: sustituir "Single goroutine" por "`Conn` es de la goroutine Wayland; la UI habla con ella por mensajes", y añadir `eventloop` a la arquitectura.
- [ ] **Step 4:** `go run ./cmd/docaudit -v` sin regresiones. Commit.

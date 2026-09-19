# Window Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Añadir el paquete `window`, que abre una ventana Wayland sobre `eventloop` con loader, pool de buffers y entrega de entrada, de modo que una aplicación solo escriba `Paint` y sus callbacks; y migrar `example/widgets` a él.

**Architecture:** `window.Run` hace el montaje Wayland en la goroutine que lo llama (que pasa a ser la goroutine Wayland), arranca la UI y el `init` de la aplicación en goroutines propias, y conecta todo con `eventloop`. La pool de buffers sale de `example/widgets` a `window/pool.go` con el mismo reparto entre goroutines. Se prueba contra un compositor falso, `internal/wltest`, que mapea el fd de la pool para leer los píxeles presentados.

**Tech Stack:** Go 1.27, `eventloop`, `canvas`, `keyboard`, `pointer`, `wayland/wlcore`, `wayland/xdgshell`, `golang.org/x/sys/unix`. Pruebas estándar con `-race`. Sin dependencias nuevas.

**Spec:** `docs/archive/specs/2026-09-19-window-design.md`

## Global Constraints

- Solo Linux y Wayland; sin cgo.
- Dependencias limitadas a la biblioteca estándar y `golang.org/x/...`.
- `wlcore.Conn` y todo objeto creado de ella pertenecen a la goroutine Wayland. **`window` no llama a `wlcore` desde la goroutine de UI**, salvo `Done` y `Err`.
- Los ficheros `*.gen.go` nunca se editan. Para añadir con `git add` cualquier fichero bajo `wayland/` hace falta `-f` (el `.gitignore` tiene `/wayland`).
- Los caminos de dibujo de `canvas` siguen sin asignar memoria.
- La aplicación no ve fases, loader, `*wlcore.Surface` ni escala; las unidades son lógicas.
- Documentación en español; código y comentarios en inglés.
- Todo test corre con `-race`.

## Review Focus

- Ningún método de `Window` llamable desde la goroutine de UI toca `Conn` (solo `Post`).
- Un buffer que se estaba creando cuando se reemplazó la pool se destruye al llegar; ninguno se filtra ni se adopta dos veces.
- `OnResize` se llama antes del primer `Paint` a cada tamaño, y el foco ganado durante la carga se entrega al instalar el `Content`.
- Un `init` que falla o entra en pánico no deja la ventana sin cerrar ni a `Run` colgado.
- El apagado no deja goroutines: UI, `init` y temporizadores paran con `Window.Context`.
- El compositor falso no acepta lo que uno real rechazaría: `attach` antes del primer `ack_configure` es un error del falso, no un éxito.

---

### Task 0: `internal/wltest`, el compositor falso mínimo compartido

Los tests de `eventloop` ya tienen un `compositor` y un `newTestConn` propios. Se mueven a un paquete interno para no duplicarlos.

**Files:**
- Create: `internal/wltest/wltest.go`
- Modify: `eventloop/loop_test.go`, `eventloop/acceptance_test.go`, `eventloop/startup_test.go`

**Interfaces:**
- Produces: `wltest.NewConn(t) (*wlcore.Conn, *Compositor)`, y en `Compositor`: `ReadRequest()`, `TryReadRequest()`, `Send(objectID, opcode, args...)`, `AnswerSync()`, `Conn() *net.UnixConn`.

- [ ] **Step 1:** Mover los helpers tal cual, exportados; los tests de `eventloop` pasan a usarlos.
- [ ] **Step 2:** `go test ./eventloop -race -count=3` debe pasar **sin cambiar ningún test**, salvo los nombres del helper. Si un test necesita cambios de fondo, algo no se ha movido bien.
- [ ] **Step 3:** Commit.

---

### Task 1: el compositor falso con guion (`wltest.Server`)

**Files:**
- Create: `internal/wltest/server.go`, `internal/wltest/server_test.go`

**Interfaces:**
- Produces: `wltest.NewServer(t, Options) *Server` con `Options{ Seat bool, ReleaseMode ReleaseMode, Vsync time.Duration }`; `(*Server).Conn()` (el `*wlcore.Conn` del cliente); `Commits() <-chan Commit` donde `Commit{ Width, Height int32; Pixels []uint32; At time.Time }` es una copia de la memoria mapeada al momento del commit; `Configure(w, h int32)`; `Key(evdev uint32, pressed bool)`, `PointerMotion(x, y float64)`, `PointerButton(button uint32, pressed bool)`; `CloseToplevel()`; y `Requests() []string` para inspección.
- `ReleaseMode`: `ReleaseImmediately` o `ReleaseOnNextCommit`.

Rastrea id→interfaz para descodificar peticiones: `wl_registry.bind`, `wl_compositor.create_surface`, `xdg_wm_base.get_xdg_surface`, `xdg_surface.get_toplevel` y `ack_configure`, `wl_shm.create_pool` (con fd por `SCM_RIGHTS`), `wl_shm_pool.create_buffer`, `wl_surface.attach/damage_buffer/frame/commit`, `wl_seat.get_keyboard/get_pointer`.

- [ ] **Step 1: Tests del propio falso**, escritos con un cliente mínimo hecho con los bindings de `wlcore` y `xdgshell`, antes de que `window` exista:
  - el registry anuncia los globales (y `wl_seat` solo si `Options.Seat`);
  - tras `commit` sin buffer, el falso manda `toplevel.configure` y `xdg_surface.configure`;
  - un `attach` **antes** del primer `ack_configure` es un error registrado por el falso (`Errors()`), no un éxito;
  - un buffer creado desde un fd se lee bien: lo que el cliente escribe en la memoria aparece en `Commit.Pixels`;
  - cada `commit` con frame pedido recibe su callback tras `Vsync`;
  - los dos `ReleaseMode` liberan cuando dicen;
  - teclas y movimiento llegan al cliente (el teclado con un keymap real de `keyboard/testdata`, enviado por memfd);
  - `CloseToplevel` llega como `xdg_toplevel.close`.
- [ ] **Step 2: RED. Step 3: implementar. Step 4: GREEN con `-race`.** Commit.

---

### Task 2: la pool de buffers (`window/pool.go`)

**Files:**
- Create: `window/doc.go`, `window/pool.go`, `window/pool_test.go`

**Interfaces:**
- Produces: `type pool struct` con `ensure(w, h int32) error`, `free() *frame`, `released(*wlcore.Buffer)`, `close(destroy bool)`, y `frame{ buf, data, cv, busy, dead }`. Recibe `post func(func())`, `do func(func())`, `push func(eventloop.Event)` y una función `createBuffer` que, en producción, crea el `wl_buffer` en la goroutine Wayland; en test se sustituye.

- [ ] **Step 1: Failing tests**, trasladando y ampliando los de `example/widgets/window_test.go`:
  - un frame sin `wl_buffer` o ocupado no se entrega;
  - un tamaño igual no reconstruye; uno distinto reemplaza la pool, marca los viejos como muertos y encola la destrucción de los que ya tenían buffer;
  - un buffer que llega para un frame muerto se destruye y no se adopta;
  - `released` libera el frame que nombra y solo ese;
  - con el falso: los píxeles escritos en `frame.cv` aparecen en el servidor al presentar, y tras un redimensionado el servidor recibe buffers del tamaño nuevo.
- [ ] **Step 2: RED. Step 3: implementar. Step 4: GREEN con `-race`.** Commit.

---

### Task 3: `Run`, el arranque y el loader

**Files:**
- Create: `window/window.go`, `window/setup.go`, `window/window_test.go`

**Interfaces:**
- Produces: `Config`, `Content`, `Window`, `Run` según la spec; `setup` hace registry, binds (`seat` opcional), superficie, toplevel, listeners y commit inicial, y devuelve error antes de abrir nada si falta un global requerido.

- [ ] **Step 1: Failing tests** contra `wltest.Server`:
  - **Aceptación 1:** con un `init` de un segundo, el primer `commit` llega en menos de 100 ms desde el `configure`, y sus píxeles son los del loader (`PaintLoader` con el mismo `now`, o al menos: no son los de `Paint` y hay animación entre dos commits).
  - **Aceptación 2:** tras `init`, los píxeles son los de `Paint`; después el servidor no recibe más `commit` de una UI que no anima.
  - Falta `wl_shm` (o `xdg_wm_base`, o `wl_compositor`): `Run` devuelve un error que nombra el global, y el servidor no vio ninguna superficie.
  - Sin `wl_seat`: la ventana abre y funciona.
  - `Config.Width/Height` a cero dan 640x480.
- [ ] **Step 2: RED. Step 3: implementar.** El arranque replica el orden probado en `example/widgets`: `Roundtrip`, superficie, toplevel, commit inicial, `eventloop.New`, UI y `init` en goroutines, `Loop.Run` en esta.
- [ ] **Step 4: GREEN con `-race -count=5`.** Commit.

---

### Task 4: entrada, foco y redimensionado

**Files:**
- Modify: `window/window.go`, `window/window_test.go`

- [ ] **Step 1: Failing tests:**
  - **Aceptación 3:** `Key` y `PointerMotion` del falso llegan a `OnKey` y `OnPointer` en orden **tras** el `init`; los enviados durante la carga no llegan.
  - Un foco de teclado ganado durante la carga se entrega como `OnKeyboardFocus(true)` al instalar el `Content`, y `OnResize` con el tamaño actual va **antes** del primer `Paint`.
  - Perder el foco llama a `OnKeyboardFocus(false)`.
  - **Aceptación 4:** 30 `Configure` seguidos con tamaños distintos: el último fotograma tiene el tamaño final, `OnResize` vio el último, y el servidor no acumula buffers vivos por encima de la pool (los viejos se destruyen).
  - Un `Configure` con tamaño cero mantiene el actual.
- [ ] **Step 2: RED. Step 3: implementar. Step 4: GREEN con `-race -count=5`.** Commit.

---

### Task 5: cierre, fallos y `Window`

**Files:**
- Modify: `window/window.go`, `window/window_test.go`

- [ ] **Step 1: Failing tests:**
  - **Aceptación 5:** `CloseToplevel` del falso y `Window.Close()` desde otra goroutine terminan `Run` con `nil`; en ambos casos `Window.Context` se cancela y no quedan goroutines (comprobado con un contador de las que `window` arranca, no con `runtime.NumGoroutine`).
  - **Aceptación 6:** un `init` que devuelve error deja la ventana abierta mostrando `PaintFailed` (píxeles distintos a los del loader), y `Run` devuelve ese error al cerrarse.
  - Un `init` que entra en pánico no deja `Run` colgado: el pánico se propaga como pánico de `Run`, tras cerrar la conexión. *(Decisión a confirmar al implementar: propagar o convertir en error.)*
  - **Aceptación 7:** los dos `ReleaseMode` dan el mismo resultado observable en un escenario de 20 fotogramas animados.
  - `SetTitle` llega al servidor como `xdg_toplevel.set_title`; `Do` desde 16 goroutines ejecuta todas las closures en la de UI.
- [ ] **Step 2: RED. Step 3: implementar. Step 4: GREEN con `-race -count=5`.** Commit.

---

### Task 6: migrar `example/widgets`

**Files:**
- Modify: `example/widgets/window.go`, `example/widgets/ui.go`, `example/widgets/window_test.go`, `example/widgets/animation_test.go`

- [ ] **Step 1:** `window.go` pasa a `main` + `window.Run`. La aplicación conserva: el estado de widgets (`ui`), `typeKey`, `pointerEvent`, `submit`, el parpadeo y `initialize` (carga de la fuente), ahora expresados como `Content` y usando `Window.Do`, `Invalidate` y `Context`. Desaparece todo el montaje Wayland y la pool.
- [ ] **Step 2:** Reescribir los tests que dependían de `window` (`freeFrame`, `ensureFrames`, `paint`, `applyConfigure`, `onEvent`) sobre `Content`; los de `ui`, `draw` y `animation` no cambian. Los de la pool ya viven en `window`.
- [ ] **Step 3:** `go build ./... && go vet ./... && go test ./... -race`.
- [ ] **Step 4: Verificación manual en niri**, con los mismos guiones que se usaron para `eventloop` (`WIDGETS_SLOW_INIT=3s`, redimensionados, pantalla completa, cierre): la ventana aparece en unos 170 ms, sobrevive a los redimensionados, sale con código 0 y no hay carreras ni errores de protocolo. Medir además cuántas líneas quedan en `example/widgets/window.go`.
- [ ] **Step 5:** Commit.

---

### Task 7: documentación viva

**Files:**
- Create: `docs/window.md`
- Modify: `docs/estado.md`, `docs/eventloop.md`, `CLAUDE.md`, `README.md`

- [ ] **Step 1:** `docs/window.md` (ES): API, arranque, foco durante la carga, pool, cierre, cómo escribir una aplicación, y cómo se prueba con `wltest`.
- [ ] **Step 2:** `docs/eventloop.md`: quitar de "Pendiente" la pool reutilizable. `docs/estado.md`: fila de `window`. `CLAUDE.md`: sección de `window` y de `internal/wltest`. `README.md`: fila del ejemplo `widgets` y del doc.
- [ ] **Step 3:** `go run ./cmd/docaudit` sin regresiones. Commit.

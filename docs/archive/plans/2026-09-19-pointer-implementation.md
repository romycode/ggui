# Pointer Input Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Añadir `pointer.Pointer`, una capa sobre `wl_pointer` que entregue posición, cambios de botón, clic, doble clic y arrastre mediante eventos semánticos.

**Architecture:** `pointer.Pointer` sigue el ciclo de vida de `keyboard.Keyboard`: el llamador reenvía las capacidades del seat y los callbacks se ejecutan en la goroutine del dispatch. Un estado interno recibe valores Go simples y reconoce gestos; el adaptador Wayland se limita a convertir `wlcore.Fixed` y enums del protocolo, lo que deja la lógica preparada para desacoplarla en otra iteración.

**Tech Stack:** Go 1.27, `wayland/wlcore`, pruebas estándar con `testing`, sin dependencias nuevas.

**Spec:** `docs/archive/specs/2026-09-19-pointer-design.md`

## Global Constraints

- Solo Linux y Wayland; sin cgo en el código publicado.
- Dependencias publicadas limitadas a la biblioteca estándar y `golang.org/x/...`; `pointer` solo importa `wayland/wlcore`.
- `wlcore.Conn` y los controladores de entrada pertenecen a una sola goroutine.
- Los ficheros `*.gen.go` nunca se editan.
- Documentación en español; código y comentarios en inglés.
- Umbral de arrastre: distancia euclídea mayor de 4 unidades lógicas.
- Doble clic: mismo botón, hasta 500 ms y hasta 4 unidades lógicas.

## Review Focus

- Una liberación sin pulsación previa entrega `ButtonUp`, pero nunca inventa clic ni arrastre.
- Una segunda pulsación del mismo botón mientras sigue pulsado no crea dos estados ni dos arrastres.
- `leave`, pérdida de capacidad y `Close` cancelan estados y memoria de doble clic sin emitir finales sintéticos.
- El wraparound de `uint32` conserva una pareja de doble clic corta y rechaza intervalos realmente largos.
- Varios botones cruzan el umbral y terminan en el mismo orden en el que fueron pulsados.

---

### Task 1: Vocabulario público de eventos

**Files:**
- Create: `pointer/doc.go`
- Create: `pointer/event.go`
- Create: `pointer/event_test.go`

**Interfaces:**
- Produces: `type EventKind uint8`, constantes `Position`, `ButtonDown`, `ButtonUp`, `Click`, `DoubleClick`, `DragStart`, `DragMove`, `DragEnd`, método `EventKind.String() string`, y `type Event struct` con `Kind`, `X`, `Y`, `StartX`, `StartY`, `Button`, `Serial`, `Time`.

- [ ] **Step 1: Write the failing event vocabulary tests**

```go
func TestEventKindsHaveStableNames(t *testing.T) {
    tests := []struct {
        kind EventKind
        want string
    }{
        {Position, "position"},
        {ButtonDown, "button-down"},
        {ButtonUp, "button-up"},
        {Click, "click"},
        {DoubleClick, "double-click"},
        {DragStart, "drag-start"},
        {DragMove, "drag-move"},
        {DragEnd, "drag-end"},
        {EventKind(255), "unknown"},
    }
    for _, tt := range tests {
        if got := tt.kind.String(); got != tt.want {
            t.Errorf("EventKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
        }
    }
}
```

- [ ] **Step 2: Run the test and verify RED**

Run: `go test ./pointer -run TestEventKindsHaveStableNames`

Expected: compilation fails because package `pointer` and `EventKind` do not exist.

- [ ] **Step 3: Implement the event vocabulary and package documentation**

```go
type EventKind uint8

const (
    Position EventKind = iota
    ButtonDown
    ButtonUp
    Click
    DoubleClick
    DragStart
    DragMove
    DragEnd
)

type Event struct {
    Kind           EventKind
    X, Y           float32
    StartX, StartY float32
    Button         uint32
    Serial         uint32
    Time           uint32
}
```

Implement `String` with a switch and document every exported declaration. In `doc.go`, explain that events are semantic, callbacks remain synchronous, and scrolling is outside this version.

- [ ] **Step 4: Run tests and documentation audit**

Run: `gofmt -w pointer && go test ./pointer && go run ./cmd/docaudit -v`

Expected: pointer tests pass and package `pointer` reports 100% exported documentation.

- [ ] **Step 5: Commit**

```bash
git add pointer/doc.go pointer/event.go pointer/event_test.go
git commit -m "pointer: define semantic input events"
```

### Task 2: Reconocimiento de posición, clic y arrastre

**Files:**
- Create: `pointer/gesture.go`
- Create: `pointer/gesture_test.go`

**Interfaces:**
- Consumes: `Event` and `EventKind` from Task 1.
- Produces: un `gestureState` privado con `enter`, `leave`, `motion` y `button`; cada método recibe tipos semánticos y una función `emit func(Event)`.

- [ ] **Step 1: Write failing tests for position and button edges**

Create a helper that records emitted values:

```go
func newGestureRecorder() (*gestureState, *[]Event) {
    var events []Event
    g := &gestureState{emit: func(ev Event) { events = append(events, ev) }}
    return g, &events
}

func TestPositionAndButtonEdgesUseTheLatestCoordinates(t *testing.T) {
    g, got := newGestureRecorder()
    g.enter(10, 20)
    g.motion(100, 12, 24)
    g.button(7, 110, 0x110, true)
    g.button(8, 120, 0x110, false)

    wantKinds := []EventKind{Position, Position, ButtonDown, ButtonUp, Click}
    assertKinds(t, *got, wantKinds)
    if ev := (*got)[2]; ev.X != 12 || ev.Y != 24 || ev.Button != 0x110 || ev.Serial != 7 {
        t.Fatalf("button down = %+v", ev)
    }
}

func TestReleaseWithoutPressOnlyEmitsButtonUp(t *testing.T) {
    g, got := newGestureRecorder()
    g.enter(1, 2)
    *got = nil
    g.button(9, 30, 0x111, false)
    assertKinds(t, *got, []EventKind{ButtonUp})
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./pointer -run 'Test(PositionAndButtonEdges|ReleaseWithoutPress)'`

Expected: compilation fails because `gestureState` does not exist.

- [ ] **Step 3: Implement position, presses and ordinary clicks**

Use constants `dragThresholdSquared float32 = 16` and `doubleClickInterval uint32 = 500`. Store active presses in a slice to preserve insertion order:

```go
type press struct {
    button, serial, time uint32
    startX, startY       float32
    dragging             bool
}

type gestureState struct {
    emit func(Event)
    x, y float32
    presses []press
    lastClick click
}
```

`button(..., true)` emits `ButtonDown` and adds a press only when that button is not active. `button(..., false)` always emits `ButtonUp`, removes a matching press, then emits the derived event.

- [ ] **Step 4: Run the focused tests and verify GREEN**

Run: `go test ./pointer -run 'Test(PositionAndButtonEdges|ReleaseWithoutPress)'`

Expected: PASS.

- [ ] **Step 5: Write failing threshold and drag-cycle tests**

Cover an exact `(3, 4)` movement as a click, a movement just over four as `DragStart`, later motion as `DragMove`, release ordering as `ButtonUp` then `DragEnd`, and two simultaneously pressed buttons. Assert complete `Event` values, including origins and press/release serials.

```go
func TestCrossingThresholdProducesACompleteDragCycle(t *testing.T) {
    g, got := newGestureRecorder()
    g.enter(10, 10)
    g.button(7, 100, 0x110, true)
    *got = nil
    g.motion(110, 15, 10)
    g.motion(120, 16, 12)
    g.button(8, 130, 0x110, false)

    assertKinds(t, *got, []EventKind{
        Position, DragStart, Position, DragMove, ButtonUp, DragEnd,
    })
    if ev := (*got)[1]; ev.StartX != 10 || ev.StartY != 10 || ev.Serial != 7 {
        t.Fatalf("drag start = %+v", ev)
    }
    if ev := (*got)[5]; ev.Serial != 8 || ev.Time != 130 {
        t.Fatalf("drag end = %+v", ev)
    }
}
```

- [ ] **Step 6: Run drag tests and verify RED**

Run: `go test ./pointer -run 'Test(ExactThreshold|CrossingThreshold|SimultaneousButtons)'`

Expected: failures because motion does not recognize drags.

- [ ] **Step 7: Implement drag recognition**

After emitting `Position`, walk `presses` in slice order. Compare `dx*dx+dy*dy` with `dragThresholdSquared`. Emit `DragStart` on the first crossing and `DragMove` thereafter. On release, emit `DragEnd` after `ButtonUp` when `dragging` is true.

- [ ] **Step 8: Write failing double-click tests**

Cover the compatible pair, interval exactly 500 ms, distance exactly four, different button, excessive time, excessive distance, reset after leave, and timestamp wraparound:

```go
func TestTimestampWraparoundCanStillDoubleClick(t *testing.T) {
    g, got := newGestureRecorder()
    g.enter(2, 3)
    clickButton(g, 0x110, math.MaxUint32-100)
    clickButton(g, 0x110, 100)
    assertKinds(t, *got, []EventKind{
        Position, ButtonDown, ButtonUp, Click,
        ButtonDown, ButtonUp, DoubleClick,
    })
}
```

- [ ] **Step 9: Implement double-click recognition and cancellation**

Store only the last completed click because changing button begins a new pair. Compare elapsed time with unsigned subtraction and positions with squared distance. `leave` clears presses and last-click state without emitting gesture endings.

- [ ] **Step 10: Add defensive edge-case tests and make the package green**

Add tests that duplicate presses do not duplicate active state, release without press cannot click, and leave cancels multiple active drags. Run:

`gofmt -w pointer && go test ./pointer`

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add pointer/gesture.go pointer/gesture_test.go
git commit -m "pointer: recognize clicks and drags"
```

### Task 3: Adaptador Wayland y ciclo de capacidades

**Files:**
- Create: `pointer/pointer.go`
- Create: `pointer/pointer_test.go`

**Interfaces:**
- Consumes: `gestureState`; `wlcore.Conn`, `wlcore.Seat`, `wlcore.PointerListener`.
- Produces: `New(*wlcore.Conn, *wlcore.Seat) (*Pointer, error)`, `SetCapabilities(wlcore.SeatCapability)`, `Focus() *wlcore.Surface`, `Close() error`, callbacks `OnEvent`, `OnFocus`, `OnError`.

- [ ] **Step 1: Write failing constructor and focus tests**

```go
func TestNewRejectsANilConnOrSeat(t *testing.T) {
    if _, err := New(nil, nil); err == nil {
        t.Error("a nil conn and seat were accepted")
    }
}

func TestEnterMotionAndLeaveTranslateWaylandValues(t *testing.T) {
    p, got := newTestPointer()
    surface := &wlcore.Surface{}
    var focuses []*wlcore.Surface
    p.OnFocus = func(s *wlcore.Surface) { focuses = append(focuses, s) }

    p.enter(surface, wlcore.FixedFromFloat64(1.25), wlcore.FixedFromFloat64(2.5))
    p.motion(10, wlcore.FixedFromFloat64(3.5), wlcore.FixedFromFloat64(4.75))
    p.leave()

    if len(*got) != 2 || (*got)[0].Kind != Position || (*got)[1].Kind != Position {
        t.Fatalf("events = %+v", *got)
    }
    if len(focuses) != 2 || focuses[0] != surface || focuses[1] != nil {
        t.Fatalf("focuses = %+v", focuses)
    }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./pointer -run 'Test(NewRejects|EnterMotionAndLeave)'`

Expected: compilation fails because `Pointer` and `New` do not exist.

- [ ] **Step 3: Implement the controller and listener translation**

`Pointer` stores `conn`, `seat`, an internal `pointerDevice` interface, focus, and `gestureState`. The interface has only `SetListener(wlcore.PointerListener)` and `Release() error`. `New` installs an acquisition closure that calls `seat.GetPointer`; tests replace it with a fake device.

The raw listener routes `Enter`, `Leave`, `Motion`, and `Button` to private methods. It deliberately leaves axis and frame callbacks nil. Convert coordinates with `float32(value.Float64())` and pressed state by equality with `wlcore.PointerButtonStatePressed`.

- [ ] **Step 4: Write failing capability lifecycle tests with a fake device**

Tests must prove acquire-once, release-on-loss, reacquire, release error through `OnError`, `Close` idempotency, and state cancellation. The fake records its listener and release count:

```go
type fakeDevice struct {
    listener wlcore.PointerListener
    releases int
    releaseErr error
}

func (d *fakeDevice) SetListener(l wlcore.PointerListener) { d.listener = l }
func (d *fakeDevice) Release() error {
    d.releases++
    return d.releaseErr
}
```

- [ ] **Step 5: Implement `SetCapabilities`, detach and `Close`**

Mirror `keyboard.Keyboard`: acquire only on a missing-to-present transition; detach on present-to-missing; report asynchronous acquire/release errors through `OnError`; return `Close` release errors directly. Every detach resets focus, presses, and click history. Invoke `OnFocus(nil)` only when a focus actually existed, avoiding a synthetic focus transition from a never-attached device.

- [ ] **Step 6: Run package tests and race tests**

Run: `gofmt -w pointer && go test ./pointer && go test -race ./pointer`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pointer/pointer.go pointer/pointer_test.go
git commit -m "pointer: adapt Wayland pointer input"
```

### Task 4: Migrar el ejemplo de widgets

**Files:**
- Modify: `example/widgets/window.go`
- Modify: `example/widgets/ui_test.go`

**Interfaces:**
- Consumes: `pointer.New`, `pointer.Pointer.SetCapabilities`, `pointer.Event`.
- Produces: integración de ejemplo sin listener crudo de `wl_pointer`.

- [ ] **Step 1: Write or adjust pure UI tests for semantic edge ordering**

Keep widget policy in `ui`: position updates hover, primary `ButtonDown` arms the target, and primary `ButtonUp` releases it. Add a test that down-inside, move-outside, and up-outside does not activate the button; this is the behavior the migration must preserve.

- [ ] **Step 2: Run the example tests before migration**

Run: `go test ./example/widgets`

Expected: PASS, establishing the behavior baseline.

- [ ] **Step 3: Replace the raw pointer fields and listener**

Import `github.com/romycode/ggui/pointer`. Replace `pointer *wlcore.Pointer` and `ptrX/ptrY` with `ptr *pointer.Pointer`. Construct it beside `keyboard.New`, assign `OnEvent`, `OnFocus`, and `OnError`, and forward the same seat capabilities to both controllers.

Map events as follows:

```go
func (w *window) pointerEvent(ev pointer.Event) {
    l := w.layout()
    switch ev.Kind {
    case pointer.Position:
        if w.ui.pointerMoved(l, ev.X, ev.Y) {
            w.redraw()
        }
    case pointer.ButtonDown:
        if ev.Button == btnLeft {
            w.ui.pointerPressed(l, ev.X, ev.Y)
            w.redraw()
        }
    case pointer.ButtonUp:
        if ev.Button == btnLeft {
            if w.ui.pointerReleased(l, ev.X, ev.Y) {
                log.Printf("cleared")
            }
            w.redraw()
        }
    }
}
```

Use `OnFocus(nil)` to call `Button.PointerLeave`. Remove `syncPointer`, `armPointer`, `pointerAt`, and the raw pointer fields. Close both input controllers during teardown.

- [ ] **Step 4: Run example and full compile tests**

Run: `gofmt -w example/widgets/window.go example/widgets/ui_test.go && go test ./example/widgets && go build ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add example/widgets/window.go example/widgets/ui_test.go
git commit -m "example: use pointer input layer"
```

### Task 5: Documentar el paquete y actualizar el estado

**Files:**
- Create: `docs/pointer.md`
- Modify: `README.md`
- Modify: `docs/estado.md`

**Interfaces:**
- Consumes: comportamiento final de Tasks 1–4.
- Produces: documentación pública en español y estado coherente del proyecto.

- [ ] **Step 1: Write the living pointer documentation**

Document construction, shared seat listener, synchronous callbacks, the event table, fixed thresholds, event ordering, focus cancellation, multi-button behavior, current omissions, and the planned future separation from the Wayland adapter. Include a complete compilable usage fragment.

- [ ] **Step 2: Update README and project state**

Add `pointer` to the package and feature summaries. Replace the known “ratón” gap with the remaining omissions: scroll, cursor theme/hotspot integration, and touchpad gestures. Update the widget example description to say it consumes `pointer.Pointer`.

- [ ] **Step 3: Verify documentation consistency**

Run: `go run ./cmd/docaudit -v`

Expected: `pointer` is 100% documented. Search for stale claims:

`rg -n "no hay nada por encima|bindings crudos de.*pointer|syncPointer|armPointer" README.md docs example`

Expected: no stale statement claiming there is no pointer layer.

- [ ] **Step 4: Commit**

```bash
git add docs/pointer.md README.md docs/estado.md
git commit -m "docs: document pointer input layer"
```

### Task 6: Verificación final y revisión

**Files:**
- Modify only files implicated by failures attributable to this change.

- [ ] **Step 1: Run mechanical checks**

Run: `gofmt -l .`

Expected: no output.

Run: `go vet ./...`

Expected: PASS.

- [ ] **Step 2: Run all tests**

Run: `go test ./...`

Expected: PASS.

Run: `go test -race -short ./...`

Expected: PASS. If the managed sandbox rejects Unix socket `setsockopt`, rerun outside that sandbox and report the exact environmental limitation if it remains unavailable.

- [ ] **Step 3: Check generated and dependency boundaries**

Run: `git diff --check && git diff --name-only main...HEAD | rg '\.gen\.go$'`

Expected: `git diff --check` passes and this pointer work adds no generated-file changes.

Run: `go list -deps ./pointer | rg -v '^(github.com/romycode/ggui/(pointer|wayland/wlcore)|internal/|runtime|sync|errors|fmt|math|unsafe|syscall|golang.org/x/sys)'`

Expected: no new third-party dependency.

- [ ] **Step 4: Review the complete diff against the spec**

Read every pointer and example diff. Confirm public comments, event order, cancellation, error ownership, no goroutines, no raw protocol event types in `pointer.Event`, and no behavior regression in `widget.Button`.

- [ ] **Step 5: Commit any verification fixes and record final evidence**

If verification required changes, commit only those fixes with a scoped message. Record the successful commands and any compositor-only manual check that remains unavailable.

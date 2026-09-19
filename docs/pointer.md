# `pointer` — entrada de puntero

> **Documento vivo.** Refleja el estado actual del código. El diseño aprobado
> y congelado está en `docs/archive/specs/2026-09-19-pointer-design.md`.

`pointer.Pointer` convierte los callbacks de `wl_pointer` en eventos de
aplicación: posición, cambios de botón, clic, doble clic y arrastre. También
posee el ciclo de vida del dispositivo, de modo que cada ventana no tenga que
repetir `get_pointer`, instalar listeners y reaccionar a cambios del seat.

## Integración

El seat es compartido por teclado, puntero y tacto. Por eso `Pointer` no toma
su listener: el llamador conserva uno solo y reenvía las capacidades a cada
controlador.

```go
ptr, err := pointer.New(conn, seat)
if err != nil {
    return err
}
defer ptr.Close()

ptr.OnEvent = func(ev pointer.Event) {
    switch ev.Kind {
    case pointer.Position:
        moveHover(ev.X, ev.Y)
    case pointer.ButtonDown:
        press(ev.Button, ev.X, ev.Y)
    case pointer.ButtonUp:
        release(ev.Button, ev.X, ev.Y)
    case pointer.Click:
        click(ev.Button, ev.X, ev.Y)
    case pointer.DoubleClick:
        doubleClick(ev.Button, ev.X, ev.Y)
    case pointer.DragStart, pointer.DragMove, pointer.DragEnd:
        drag(ev)
    }
}
ptr.OnFocus = func(surface *wlcore.Surface) {
    if surface == nil {
        cancelHoverAndPresses()
    }
}
ptr.OnError = func(err error) {
    log.Printf("pointer: %v", err)
}

seat.SetListener(wlcore.SeatListener{
    Capabilities: func(caps wlcore.SeatCapability) {
        ptr.SetCapabilities(caps)
        kbd.SetCapabilities(caps)
    },
})
```

Los callbacks ocurren dentro del dispatch que recibió el mensaje. No hay una
goroutine ni un canal intermedios: el llamador puede responder desde la misma
goroutine que posee `wlcore.Conn`. Por la misma razón, `Pointer` no es seguro
para uso concurrente.

`SetCapabilities` se llama cada vez que llega `wl_seat.capabilities`. Adquiere
el dispositivo al aparecer la capacidad, lo libera al desaparecer y puede
adquirirlo otra vez. `Close` es idempotente y libera el dispositivo actual.
El seat debe ser versión 3 o posterior, que es cuando el protocolo añadió la
petición `wl_pointer.release`; `New` rechaza versiones anteriores.

## Eventos

| `Kind` | Cuándo aparece | Campos específicos |
| --- | --- | --- |
| `Position` | `enter` y cada `motion` | `X`, `Y`, `Time` cuando existe |
| `ButtonDown` | pulsación física | `Button`, `Serial`, `Time`, `X`, `Y` |
| `ButtonUp` | liberación física | `Button`, `Serial`, `Time`, `X`, `Y` |
| `Click` | pulsación y liberación sin arrastre | los del `ButtonUp` |
| `DoubleClick` | segundo clic compatible | los del segundo `ButtonUp` |
| `DragStart` | primer movimiento que supera el umbral | botón, origen, posición y serial de la pulsación |
| `DragMove` | movimiento posterior del arrastre | botón, origen, posición y serial de la pulsación |
| `DragEnd` | liberación que termina el arrastre | botón, origen, posición y serial de la liberación |

Las coordenadas son `float32` en unidades lógicas locales de la superficie,
las mismas que consumen `canvas` y `widget`. `Button` es el código Linux que
envía el compositor; por ejemplo, el botón principal es `0x110` (`BTN_LEFT`).

En una pulsación ordinaria el orden es `ButtonDown`, `ButtonUp`, `Click`. El
segundo clic compatible termina con `DoubleClick` en vez de otro `Click`. Un
arrastre produce `ButtonDown`, `Position` + `DragStart`, cero o más parejas
`Position` + `DragMove`, y finalmente `ButtonUp` + `DragEnd`.

Cada botón mantiene su propia pulsación y origen. Si varios están activos, los
eventos de arrastre de un movimiento salen en su orden de pulsación. Las
liberaciones conservan el orden que entrega el compositor.

## Umbrales

La primera versión usa valores fijos:

- un arrastre empieza al superar cuatro unidades lógicas desde la pulsación;
- una distancia exactamente igual a cuatro todavía puede ser clic;
- un doble clic exige el mismo botón, como máximo 500 ms y como máximo cuatro
  unidades entre liberaciones.

El primer clic se entrega de inmediato. La aplicación no espera 500 ms para
saber si puede actuar; si llega otro compatible recibe después
`DoubleClick`.

## Foco y cancelación

`Focus` devuelve la superficie actual. `OnFocus` recibe esa superficie al
entrar y `nil` al salir. Una salida, pérdida de capacidad o `Close` cancela
todas las pulsaciones, arrastres y memoria de doble clic. No se inventan
`ButtonUp`, `Click` ni `DragEnd`: el dispositivo ya no produjo esos eventos.

## Límites actuales

Esta capa todavía ignora rueda y desplazamiento continuo, fuente y dirección
de los ejes, gestos de touchpad y la elección o dibujo del cursor. Tampoco
decide qué widget recibe un evento: esa política pertenece a la ventana o a
una futura capa de composición.

`Pointer` traduce hoy directamente desde Wayland, pero `Event` no expone
`wlcore.Fixed` ni enums crudos del protocolo. Ese límite permite separar en
otra iteración los eventos de entrada y el adaptador Wayland sin reescribir la
lógica de clic y arrastre de las aplicaciones.

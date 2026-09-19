# Diseño de la capa de puntero

**Fecha:** 2026-09-19

## Objetivo

Añadir un paquete público `pointer` que convierta los eventos de
`wl_pointer` en posición, clic, doble clic y arrastre. La aplicación no
trabajará directamente con el listener del protocolo. La integración seguirá
el patrón de `keyboard.Keyboard` y conservará el modelo de una sola goroutine
de `wlcore`.

La primera versión no incluye rueda, desplazamiento continuo, gestos de
touchpad, cursores ni políticas propias de widgets.

## Forma de la API

El controlador concreto `pointer.Pointer` se construye con `New(conn, seat)`.
El llamador conserva el listener de `wl_seat` y reenvía sus capacidades a
`Pointer.SetCapabilities`, igual que hace con `Keyboard.SetCapabilities`:

```go
ptr, err := pointer.New(conn, seat)
if err != nil {
    return err
}

ptr.OnEvent = func(ev pointer.Event) {
    switch ev.Kind {
    case pointer.Position:
        // La posición cambió.
    case pointer.Click, pointer.DoubleClick:
        // Un botón produjo un clic.
    case pointer.DragStart, pointer.DragMove, pointer.DragEnd:
        // Ciclo de un arrastre.
    }
}

seat.SetListener(wlcore.SeatListener{
    Capabilities: func(caps wlcore.SeatCapability) {
        ptr.SetCapabilities(caps)
    },
})
```

`New`, `SetCapabilities`, `Focus`, `Close` y `OnError` siguen el contrato de
`keyboard.Keyboard`. `SetCapabilities` adquiere `wl_pointer` cuando aparece la
capacidad, lo libera cuando desaparece y permite adquirirlo otra vez. `Close`
es idempotente.

`OnEvent` y `OnFocus` se ejecutan dentro del dispatch que produjo el evento.
No hay canales ni goroutines internas. El tipo no es seguro para uso
concurrente.

## Eventos semánticos

`pointer.Event` no expone `wlcore.Fixed`, `wlcore.PointerButtonState` ni otros
tipos de eventos crudos. Contiene:

- `Kind`, de tipo `EventKind`;
- `X` e `Y`, posición actual en unidades lógicas de la superficie;
- `StartX` y `StartY`, origen de un arrastre;
- `Button`, código Linux del botón;
- `Serial`, serial asociado a la interacción;
- `Time`, timestamp del compositor en milisegundos.

`EventKind` tiene los valores `Position`, `Click`, `DoubleClick`, `DragStart`,
`DragMove` y `DragEnd`. Los campos que no corresponden al tipo de evento valen
cero. Los eventos son valores y no contienen referencias a estado mutable del
controlador.

Como en `Keyboard`, el foco se informa por separado con
`OnFocus func(*wlcore.Surface)`. Así, `Event` representa entrada de aplicación
y no un evento del socket. En una iteración futura se podrá separar también el
foco y la conexión Wayland sin reescribir el reconocimiento de gestos.

## Posición y foco

`wl_pointer.enter` establece `Focus()`, actualiza la posición, llama a
`OnFocus(surface)` y emite `Position`. `wl_pointer.motion` actualiza la
posición y emite `Position`. Las coordenadas `wl_fixed` se convierten a
`float32`, la unidad lógica que usan `canvas` y `widget`.

`wl_pointer.leave` limpia el foco, llama a `OnFocus(nil)` y cancela las
pulsaciones y arrastres pendientes sin inventar eventos. La pérdida de la
capacidad del seat y `Close` hacen el mismo reset. Los tres casos también
olvidan el último clic, de modo que una pareja nunca cruza superficies ni
sesiones del dispositivo.

Los eventos de eje y `wl_pointer.frame` se ignoran en esta versión. Los eventos
semánticos se entregan en el orden del protocolo, por lo que un botón utiliza
la última posición recibida por `enter` o `motion`.

## Clic y doble clic

Todos los botones se siguen de forma independiente. Una pulsación guarda la
posición, el serial y el tiempo. Su liberación produce un clic si el movimiento
desde el origen no superó cuatro unidades lógicas, medidas como distancia
euclídea. Una distancia exactamente igual a cuatro todavía cuenta como clic.

El primer clic se entrega inmediatamente. Un segundo clic del mismo botón
produce `DoubleClick`, en lugar de otro `Click`, si su liberación ocurre como
máximo 500 ms después y como máximo a cuatro unidades del primero. Cambiar de
superficie, botón, exceder la distancia o exceder el tiempo inicia una nueva
pareja y produce un `Click` normal.

Los umbrales son valores internos fijos y documentados. La primera versión no
añade configuración. La diferencia temporal usa aritmética modular de
`uint32`, de modo que el wraparound del timestamp no rompe intervalos cortos.

## Arrastre

Cuando un botón pulsado se mueve a más de cuatro unidades de su origen se emite
`DragStart` una sola vez. Cada movimiento posterior emite `DragMove` y la
liberación emite `DragEnd`. Los tres llevan el botón, el origen y la posición
actual. `DragStart` y `DragMove` conservan el serial de la pulsación porque un
evento de movimiento no trae serial; `DragEnd` lleva el serial de la
liberación. `Click` y `DoubleClick` también llevan el serial de su liberación.

Cada movimiento emite primero `Position`, incluso durante un arrastre. Varios
botones pueden estar pulsados a la vez; cada uno conserva su propio origen y
ciclo. Los gestos se procesan en el orden de pulsación, sin depender del orden
aleatorio de un mapa.

Una salida de foco, pérdida de capacidad o cierre cancela los arrastres sin
emitir `DragEnd`, ya que no hubo una liberación y la superficie dejó de ser un
destino válido.

## Ciclo de vida y errores

`New` rechaza una conexión o seat nulos. `Pointer` posee el `wl_pointer` que
adquiere, pero no posee `Conn`, `Seat` ni las superficies recibidas.

Un fallo de `seat.GetPointer` o de la liberación causada por un cambio de
capacidades se envía a `OnError`, envuelto con contexto. `Close` devuelve el
error de liberación directamente, como `keyboard.Keyboard.Close`.

El paquete no añade dependencias: usa la biblioteca estándar y `wlcore`. No se
modifica ningún fichero generado.

## Ficheros

- `pointer/doc.go`: documentación y alcance del paquete.
- `pointer/event.go`: `EventKind` y `Event`.
- `pointer/pointer.go`: ciclo de vida, foco, posición y gestos.
- `pointer/pointer_test.go`: pruebas del estado y contrato público.
- `example/widgets/window.go`: migración desde el listener crudo.
- `docs/pointer.md`: documentación viva.
- `README.md` y `docs/estado.md`: estado y uso del nuevo paquete.

## Verificación

Las pruebas alimentan directamente la lógica del controlador, como las de
`keyboard.Keyboard`, sin necesitar un compositor. Cubren argumentos nulos,
cambios de capacidades, entrada, movimiento, salida, conversión de coordenadas,
todos los botones, límites exactos, ciclo completo de arrastre, botones
simultáneos, doble clic, wraparound temporal y todas las cancelaciones.

La verificación final ejecuta `gofmt`, `go vet ./...`, `go test ./...` y
`go test -race -short ./...`. El ejemplo se compila automáticamente; la sesión
real se comprueba manualmente con un compositor Wayland.

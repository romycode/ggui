# ggui

Cliente Wayland y canvas 2D en Go puro. Sin cgo, sin enlazar contra
`libwayland-client`: el wire protocol, el despacho de eventos y el
rasterizado están escritos en Go.

## Estado

**Experimental.** La API no es estable y cambia sin aviso. Funciona lo
suficiente para abrir una ventana, negociar el `configure` de xdg-shell,
pintar en un buffer compartido y reaccionar a HiDPI y escala fraccionaria.

Del teclado hay media capa: `keyboard/` compila el keymap XKB que envía el
compositor y traduce keycode + modificadores a keysym y a texto, con dead
keys. Lo que falta es la capa de alto nivel —foco, repetición, eventos—, hoy
solo prototipada en `example/keylog`. Del ratón no hay nada por encima de los
bindings crudos de `wl_pointer`. No hay texto ni widgets: `example/widgets`
prototipa ambos —un campo de texto y un botón— dentro del propio ejemplo,
con una fuente de mapa de bits ASCII, no en una capa reutilizable.

## Por qué sin cgo

Enlazar contra `libwayland-client` obliga a cgo, y cgo cuesta compilación
cruzada, tiempo de build y un salto de stack por llamada. El protocolo
Wayland es un formato binario sencillo sobre un socket Unix: implementarlo
en Go es viable y deja el binario estático.

## Requisitos

- Linux con un compositor Wayland en marcha (`$WAYLAND_DISPLAY` definido).
- Go 1.27 o superior.

## Instalación

```
go get github.com/romycode/ggui
```

## Ejemplo mínimo

Conectar, hacer bind de los globals imprescindibles y forzar un roundtrip:

```go
conn, err := wlcore.Connect()
if err != nil {
    return err
}
defer conn.Close()

reg, err := conn.Display().GetRegistry()
if err != nil {
    return err
}

var (
    compositor *wlcore.Compositor
    shm        *wlcore.Shm
    wmBase     *xdgshell.WmBase
)
reg.SetListener(wlcore.RegistryListener{
    Global: func(name uint32, iface string, version uint32) {
        switch iface {
        case wlcore.CompositorInterface.Name:
            compositor, _ = reg.Bind(name, version, wlcore.CompositorInterface)
        case wlcore.ShmInterface.Name:
            shm, _ = reg.Bind(name, version, wlcore.ShmInterface)
        case xdgshell.WmBaseInterface.Name:
            wmBase, _ = reg.Bind(name, version, xdgshell.WmBaseInterface)
        }
    },
})
if err := conn.Roundtrip(); err != nil {
    return err
}
```

A partir de ahí: `create_surface` → `get_xdg_surface` → `get_toplevel`,
esperar el `configure`, hacer `ack_configure` y **solo entonces** adjuntar
el buffer y hacer `commit`. El flujo completo, con manejo de errores real,
está en los ejemplos.

## Ejemplos

Cada uno se ejecuta con `go run ./example/<nombre>`.

| Ejemplo | Qué demuestra |
| --- | --- |
| `wayland` | Handshake completo de xdg-shell y ventana de color sólido. |
| `hidpi` | Escala entera vía `wl_surface.set_buffer_scale`. |
| `scaling` | Escala fraccionaria con `fractional-scale-v1` y `viewporter`. |
| `cursorshape` | Cambio de cursor por zonas con `cursor-shape-v1`, sin tema ni hotspot. |
| `keylog` | Teclado: keymap XKB, keysym, texto compuesto y modificadores efectivos/consumidos. |
| `widgets` | Campo de texto y botón: `canvas`, ratón y teclado a la vez, y doble buffer con `wl_buffer.release`. |

## Paquetes

| Paquete | Contenido |
| --- | --- |
| `wayland/wlcore` | Runtime escrito a mano: conexión, wire format, despacho, ciclo de vida de objetos. Incluye los bindings del protocolo core. |
| `wayland/xdgshell` | Bindings de xdg-shell (ventanas, popups, positioners). |
| `wayland/viewporter` | Bindings de viewporter (recorte y escalado de superficie). |
| `wayland/fractionalscale` | Bindings de fractional-scale-v1. |
| `wayland/cursorshape` | Bindings de cursor-shape-v1. |
| `wayland/tablet` | Bindings de tablet-v2. |
| `canvas` | Rasterizador 2D por CPU, modo inmediato, cero asignaciones por operación. |
| `keyboard` | Subconjunto de XKB: compilación del keymap, estado de modificadores y dead keys por NFC canónico. |
| `cmd/waygenerator` | Generador de los bindings a partir de los XML de protocolo. |
| `cmd/keysymgen` | Generador de las tablas de keysyms de `keyboard` desde las cabeceras de X11. |
| `cmd/docaudit` | Informe de cobertura de comentarios sobre la superficie exportada. |

Todos los ficheros `*.gen.go` son generados: no editarlos a mano.

## Cobertura de protocolos

Versión más alta declarada en el XML descargado. Que el binding exista no
garantiza que el compositor lo anuncie.

| Protocolo | Interfaces | Versión |
| --- | --- | --- |
| wayland (core) | `wl_display`, `wl_registry`, `wl_compositor`, `wl_shm`, `wl_surface`, `wl_seat`, `wl_pointer`, `wl_keyboard`, `wl_touch`, `wl_output`, data device, subsurfaces… | hasta 6 |
| xdg-shell | `xdg_wm_base`, `xdg_surface`, `xdg_toplevel`, `xdg_popup`, `xdg_positioner` | 7 |
| viewporter | `wp_viewporter`, `wp_viewport` | 1 |
| fractional-scale-v1 | `wp_fractional_scale_manager_v1`, `wp_fractional_scale_v1` | 1 |
| cursor-shape-v1 | `wp_cursor_shape_manager_v1`, `wp_cursor_shape_device_v1` | 2 |
| tablet-v2 | `zwp_tablet_manager_v2`, `zwp_tablet_seat_v2`, `zwp_tablet_tool_v2`, pad y sus controles | 2 |

## Canvas

Rasterizador de modo inmediato sobre memoria prestada por quien llama.
ARGB8888 con alfa premultiplicado, composición `source-over`, coordenadas
lógicas con escala HiDPI inmutable, y damage acumulado en píxeles físicos
listo para `wl_surface.damage_buffer`.

Operaciones: `Clear`, `ClearRect`, `FillRect`, `StrokeRect`,
`FillRoundedRect`, `StrokeRoundedRect`, `FillCircle`, `StrokeCircle` y
`Line` con tres terminaciones.

Los métodos de dibujo no devuelven error: el primero inválido se queda
pegado, las operaciones siguientes son no-ops y se consulta una vez con
`Canvas.Err()` al cerrar el frame.

Pendiente: texto (`DrawMask`), lista de rectángulos dañados y clipping
rectangular.

## Regenerar los bindings

```
make generate-protocols
```

Descarga los XML actuales desde los mirrors de freedesktop a `protocols/`
y ejecuta `waygenerator` sobre ellos. Los XML están versionados en el
repo, así que el diff de una regeneración muestra tanto los cambios de
protocolo como los de código generado.

## Documentación

- Referencia de la API: la documentación de cada paquete (`go doc`).
- `docs/estado.md` — qué hay construido, qué falta y qué restricciones no se
  pueden romper.
- `docs/wlcore.md` — runtime, wire protocol y ciclo de vida de objetos.
- `docs/waygenerator.md` — contrato entre generador y runtime, naming y
  mapeo de tipos XML → Go.
- `docs/canvas.md` — diseño del canvas 2D.
- `docs/keyboard.md` — subconjunto de XKB, composición y huecos medidos
  contra libxkbcommon.
- `docs/archive/` — specs y planes de implementación congelados, con
  fecha. Material histórico, no se mantiene al día.

## Convenciones

- Documentación (`docs/`, README, markdown): español.
- Código y comentarios de código: inglés.

## Licencia

Sin licencia declarada todavía.

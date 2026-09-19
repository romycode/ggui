# Estado del proyecto

> **Documento vivo.** Refleja el estado actual del código y se actualiza con
> él. Los documentos congelados, con fecha, están en `docs/archive/`.

Este documento cubre el **eje de estado**: qué hay construido hoy, qué falta,
y qué restricciones no se pueden romper. Los otros documentos de `docs/`
cubren el eje de diseño —cómo funciona cada pieza y por qué— y el README es
el escaparate. Cuando algo se termine o se rompa, el sitio a actualizar es
este fichero.

## Resumen por paquete

| Paquete | Estado | Qué hay | Qué falta |
| --- | --- | --- | --- |
| `wayland/wlcore` | **Completo** | Runtime a mano (`conn.go`, `proxy.go`, `wire.go`, `fixed.go`, `registry.go`) más el core generado desde `wayland.xml`, y `DispatchUntil` para que el bucle se despierte por un timer propio sin romper el invariante de una sola goroutine. | Nada pendiente conocido. |
| `canvas` | **Completo** | Rasterizador de modo inmediato escrito a mano. Cubre todo el alcance de `canvas.md`, más `DrawMask` (máscaras de cobertura, lo que dibuja el texto). | Lista de rectángulos dañados, clipping rectangular. |
| `keyboard` | **Completo** | `Compile` / `Keymap` / `State` (`xkbmini.go`), `Composer` (`compose.go`) y la capa de integración: `Keyboard`, `Event`, `Mods`, `KeyState` — foco, modificadores, texto compuesto y repetición sin goroutine de timer. Los keysyms son generados. | Solo el split en `input/keyboard` + `input/xkbmini` que describe `keyboard.md`. |
| `cmd/waygenerator` | **Completo** | Cuatro pasadas (`xmlmodel` → `symbols` → `resolve` → `codegen`), con golden files. | Nada pendiente conocido. |
| `wayland/xdgshell` | **Bindings** | `xdg_wm_base`, `xdg_surface`, `xdg_toplevel`, `xdg_popup`, `xdg_positioner`. | Capa propia por encima: decoraciones, popups usables, gestión de estado del toplevel. |
| `wayland/viewporter` | **Bindings** | Generado, sin capa por encima. | — |
| `wayland/fractionalscale` | **Bindings** | Generado, sin capa por encima. | — |
| `wayland/cursorshape` | **Bindings** | Generado, sin capa por encima. | Tema de cursor y hotspot. |
| `wayland/tablet` | **Bindings** | Generado, sin capa por encima. Nunca ejercitado por un ejemplo. | Todo lo que vaya por encima. |
| `widget` | **Empezado** | `Button` (hover, pulsado, deshabilitado, foco, clic al soltar dentro y activación con Espacio/Intro), las interfaces `Font` y `Focusable`, el tipo `Key` y `Chain` (orden de tabulación y un solo enfocado). Solo depende de `canvas`; `Draw` no asigna. | Un segundo widget enfocable (campo de texto), layout, el resto de controles. Ver `widget.md`. |
| `text` | **Empezado** | Descubrimiento de fuentes instaladas (`Find`, sin fontconfig) y `Face`: mide y dibuja una línea con contornos vectoriales, a la escala del canvas, entregando cada glifo a `canvas.DrawMask`. Caché de glifos por escala y posición subpíxel: un `Draw` en caliente no asigna. Implementa `widget.Font`. Ver `text.md`. | Fuentes de respaldo, varias líneas y *shaping*. |
| `cmd/docaudit` | **Completo** | Mide la cobertura de comentarios de la superficie exportada. | — |
| `cmd/keysymgen` | **Completo** | Genera `keyboard/keysyms.gen.go` desde las cabeceras de libxkbcommon. | — |

Del ratón no hay nada por encima de los bindings crudos de `wl_pointer`, en
ningún paquete: es el único de los dos dispositivos de entrada que cada
ventana sigue traduciendo a mano. De texto hay una línea, con fuentes del
sistema (`text`). De widgets hay el primero, `widget.Button`; el campo de
texto sigue siendo un prototipo dentro de `example/widgets`, que usa
`text.Face` y recurre a una fuente de mapa de bits ASCII solo si el sistema
no tiene ninguna legible.

Los dos ejemplos que usan teclado —`keylog` y `widgets`— están migrados a
`keyboard.Keyboard` y a `Conn.DispatchUntil`, así que ninguno compila ya su
propio keymap ni se inventa la repetición. Es también lo único que ejercita
esas dos piezas contra un compositor de verdad.

## Cobertura de protocolos

La tabla de versiones está en el README y no se duplica aquí. Lo que el
README no dice es si existe algo por encima del binding generado:

| Protocolo | Binding | Capa propia |
| --- | --- | --- |
| wayland (core) | sí | sí — `wlcore`, escrita a mano |
| xdg-shell | sí | no |
| viewporter | sí | no |
| fractional-scale-v1 | sí | no |
| cursor-shape-v1 | sí | no |
| tablet-v2 | sí | no |

Que el binding exista no garantiza que el compositor anuncie el protocolo, ni
que ningún ejemplo lo ejercite. `tablet-v2` no lo ejercita ninguno.

## Restricciones

Invariantes del proyecto. Romper una de estas no es un cambio, es una
decisión de diseño nueva, y toca discutirla antes.

- **Sin cgo en el código publicado.** El único cgo del repo es
  `keyboard/oracle_cgo.go`, detrás del tag de build `oracle`, y existe solo
  para contrastar contra la `libxkbcommon` real en los tests.
- **Dependencias del código publicado: solo `golang.org/x/...`,** y hoy tres:
  `x/sys/unix`, `x/text` (`transform` + `unicode/norm`) y `x/image`
  (`font`, `font/opentype`, `math/fixed`). Esta última es de `text/` y **solo
  de `text/`**: ningún paquete de `canvas/`, `keyboard/`, `widget/` o
  `wayland/` la importa, y conviene que siga así. `widget` recibe el texto
  por la interfaz `Font` justo para no tener que hacerlo. `x/image/font/basicfont`
  sigue siendo solo de `example/widgets`.
- **`Conn` es de un solo goroutine.** `objects`, `nextID`, `freeIDs`, `in`,
  `fds` y `oob` no llevan candado. `Roundtrip()` no se puede llamar de forma
  reentrante desde dentro de un listener.
- **Un mensaje malformado es fatal, no recuperable.** El flujo queda
  desalineado; lo que corresponde es cerrar la conexión.
- **Los ficheros `.gen.go` no se editan nunca.** Se sobrescriben en cada
  ejecución del generador. Lo que se cambia son las plantillas de
  `cmd/waygenerator/internal/codegen`.
- **El contrato generador↔runtime es una lista cerrada** (tabla al principio
  de `waygenerator.md`). Si una plantilla necesita de `wlcore` algo que no
  está en esa lista, o el contrato está mal o la plantilla se está pasando.
  Ampliar la API sin más no es la salida.
- **El canvas no asigna.** Cero asignaciones por operación de dibujo, y está
  asertado en `go test` con `testing.AllocsPerRun`, no solo medido en los
  benchmarks. Cualquier cambio en una ruta de dibujo tiene que mantenerlo.
- **El generador se ejecuta desde la raíz del repo.** `main.go` lleva
  `run("protocols", "wayland/wlcore")` escrito a mano.
- **Solo Linux,** con un compositor Wayland en marcha. No hay plan de
  portar a otro sitio.
- **Documentación en español, código y comentarios de código en inglés.**
- **Añadir un protocolo son tres sitios:** el `manifest` de `xmlmodel.go`,
  los mapas `packageOf`/`prefixOf`/`suffixOf` de `symbols.go`, y el target
  `download-protocols` del `makefile`.

## Huecos conocidos

Por orden de lo que más bloquea a lo que menos:

1. **El ratón.** No hay nada por encima de `wl_pointer`: ni entrada/salida
   por zonas, ni arrastre, ni doble clic. Es el hueco que queda del lado de
   la entrada, ahora que el teclado tiene capa; `Keyboard.SetCapabilities`
   está pensado justo para poder convivir con él en el mismo seat.
2. **Texto.** Hay una línea con fuentes del sistema (`text`), integrada en el
   seguimiento de daño vía `canvas.DrawMask` y con caché de glifos. Faltan
   fuentes de respaldo, varias líneas y *shaping*.
3. **Widgets reutilizables.** `widget.Button` existe, con foco y teclado, y
   `widget.Chain` recorre el orden de tabulación. Falta el campo de texto
   —hoy el único enfocable es el botón— y el resto de controles. Ver
   `widget.md`.
4. **CI.** No hay `.github/`. Nada ejecuta los tests salvo a mano.
5. **Licencia.** Sin declarar.

## Cobertura de documentación

`go run ./cmd/docaudit -v` la mide sobre la superficie exportada. Hoy: **84 %
global**, 1 065 símbolos documentados y 185 sin documentar.

Los paquetes públicos están bien. Lo que hunde la media son los internos del
generador, que no se documentaron nunca:

| Paquete | Cobertura |
| --- | --- |
| `cmd/waygenerator/internal/symbols` | 0 % |
| `cmd/waygenerator/internal/xmlmodel` | 4 % |
| `cmd/waygenerator/internal/resolve` | 10 % |
| `cmd/keysymgen/internal/keysymdata` | 62 % |
| `canvas` | 72 % |
| `wayland/xdgshell` | 71 % |
| `wayland/wlcore` | 97 % |
| `keyboard`, `widget`, `text`, `cursorshape`, `fractionalscale`, `tablet`, `viewporter` | 100 % |

## Pruebas

46 ficheros de test, 16 paquetes con tests. Lo que cubren, por si hace falta
saber dónde se está pisando terreno probado:

- `canvas` — tests de asignaciones, fuzzing sobre `New` y sobre las nueve
  operaciones de dibujo, benchmarks.
- `keyboard` — tests con keymaps mínimos a mano, más el oráculo diferencial
  contra `libxkbcommon` 1.13.2 (`go test -tags oracle ./keyboard/...`), hoy
  con cero discrepancias en los cinco keymaps. `Composer` **no tiene
  tests**: compararlo con libxkbcommon no significa nada, porque implementa
  NFC canónica y no el fichero Compose de X11.
- `cmd/waygenerator` — golden files en `internal/codegen/testdata/*.golden`.
  No hay flag `-update`: una expectativa se regenera a mano y a propósito.
- `wayland/wlcore` — tests del wire protocol y del ciclo de vida de objetos, y
  de `DispatchUntil`: que una fecha que vence no es error ni mata la conexión,
  que no se queda puesta en el socket, que no pierde mensajes ni desalinea el
  flujo, que el cero bloquea, y que un fallo de verdad sigue siendo terminal.
- `keyboard` — además del oráculo: los keycodes (`+8`), el texto compuesto y
  que los caracteres de control no salen, los modificadores consumidos, que un
  modificador no llega al composer, y la repetición entera (arranque tras el
  delay, cadencia, que un `Tick` tardío no dispara una ráfaga, y que soltar,
  perder el foco o un `repeat_info` nuevo la cancelan). Usa un keymap sintético
  mínimo; lo que necesita un compositor vivo es el cableado, no el
  comportamiento.
- `widget` — la máquina de estados del botón (clic al soltar dentro, arrastrar
  fuera y volver, cancelar con `PointerLeave` o al deshabilitar), el foco y el
  teclado (Espacio arma y activa al soltar, Intro activa en el acto, Escape y
  perder el foco cancelan, un botón deshabilitado rechaza el foco), qué estados
  se ven distintos, que el anillo de foco no se sale de `Bounds` ni pide
  repintado cuando no se vería, y que `Draw` no asigna ni escribe en el
  padding de fila. De `Chain`: el recorrido en ambos sentidos con vuelta, que
  se salten los que rechazan, que una cadena donde rechazan todos termine,
  que como mucho haya uno enfocado, y el reparto de teclas (el tabulador se
  consume, el resto llega al enfocado). Usa una `Font` falsa y un `Focusable`
  falso —para los casos a los que un `Button` no llega, como aceptar el foco
  sin cambiar de aspecto—: no rasteriza texto.
- `text` — el descubrimiento con las fuentes Go de `x/image` en un directorio
  temporal, el lector de nombres contrastado contra `sfnt` y contra **todas
  las fuentes instaladas** (se salta si no hay), y `Face`: recorte, escala,
  centrado, daño, y que la caché de glifos no se nota —una cara caliente
  dibuja lo mismo que una recién creada—. Un `Draw` en caliente sí está
  asertado sin asignaciones; en frío no. Ver `text.md`.
- Los ejemplos no tienen tests salvo `keylog` y `widgets`, y los suyos son de
  la lógica pura, no de la sesión Wayland. Lo que `keyboard.Keyboard` y
  `DispatchUntil` hacen contra un compositor real solo se comprueba
  ejecutándolos.

## Cómo se mantiene este documento

Hay que tocarlo cuando:

- un paquete cambie de estado en la tabla de arriba;
- se cierre uno de los huecos conocidos, o aparezca uno nuevo;
- cambie una restricción —sobre todo la de dependencias, que es la más fácil
  de romper sin darse cuenta al añadir un ejemplo;
- se añada un protocolo, o alguno estrene capa propia.

Lo que **no** va aquí: diseño (va en el documento del paquete), historia (va
en `docs/archive/`, con fecha) ni instrucciones de uso (van en el README o
en `go doc`).

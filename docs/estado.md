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
| `wayland/wlcore` | **Completo** | Runtime a mano (`conn.go`, `proxy.go`, `wire.go`, `fixed.go`, `registry.go`) más el core generado desde `wayland.xml`. 5 499 de 6 400 líneas son generadas. | Nada pendiente conocido. |
| `canvas` | **Completo** | Rasterizador de modo inmediato, 1 399 líneas escritas a mano. Cubre todo el alcance de `canvas.md`. | `DrawMask` (texto), lista de rectángulos dañados, clipping rectangular. |
| `keyboard` | **Media capa** | `Compile` / `Keymap` / `State` (`xkbmini.go`) y `Composer` (`compose.go`). Los keysyms son generados: 5 957 de 7 018 líneas. | El tipo `Keyboard`, `Event` / `Mods`, ciclo de vida sobre `wlcore.Seat`, timer de repetición y foco. Hoy solo prototipado en `example/keylog`. |
| `cmd/waygenerator` | **Completo** | Cuatro pasadas (`xmlmodel` → `symbols` → `resolve` → `codegen`), con golden files. | Nada pendiente conocido. |
| `wayland/xdgshell` | **Bindings** | `xdg_wm_base`, `xdg_surface`, `xdg_toplevel`, `xdg_popup`, `xdg_positioner`. | Capa propia por encima: decoraciones, popups usables, gestión de estado del toplevel. |
| `wayland/viewporter` | **Bindings** | Generado, sin capa por encima. | — |
| `wayland/fractionalscale` | **Bindings** | Generado, sin capa por encima. | — |
| `wayland/cursorshape` | **Bindings** | Generado, sin capa por encima. | Tema de cursor y hotspot. |
| `wayland/tablet` | **Bindings** | Generado, sin capa por encima. Nunca ejercitado por un ejemplo. | Todo lo que vaya por encima. |
| `cmd/docaudit` | **Completo** | Mide la cobertura de comentarios de la superficie exportada. | — |
| `cmd/keysymgen` | **Completo** | Genera `keyboard/keysyms.gen.go` desde las cabeceras de libxkbcommon. | — |

Del ratón no hay nada por encima de los bindings crudos de `wl_pointer`, en
ningún paquete. No hay capa de texto ni de widgets: `example/widgets`
prototipa un campo de texto y un botón dentro del propio ejemplo, con una
fuente de mapa de bits ASCII.

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
- **Dependencias del código publicado: solo `golang.org/x/...`,** y hoy solo
  dos: `x/sys/unix` y `x/text` (`transform` + `unicode/norm`). Cuidado al
  leer `go.mod`: `golang.org/x/image` está ahí para `example/widgets`, no
  para la librería. Ningún paquete de `canvas/`, `keyboard/` o `wayland/`
  lo importa, y conviene que siga así.
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

1. **La capa `Keyboard`.** Foco, repetición y ensamblado de `Event` sobre
   `wlcore.Seat`. Es lo que separa a `keyboard/` de ser usable sin copiar
   el prototipo de `example/keylog`.
2. **El ratón.** No hay nada por encima de `wl_pointer`: ni entrada/salida
   por zonas, ni arrastre, ni doble clic.
3. **Texto.** Necesita `canvas.DrawMask` y una fuente de verdad. Hoy
   `example/widgets` blitea `basicfont.Face7x13` a mano, y al ser ASCII los
   acentos compuestos con dead keys salen como el glifo de reemplazo.
4. **Widgets reutilizables.** Existe el prototipo dentro de
   `example/widgets`; no existe la capa.
5. **CI.** No hay `.github/`. Nada ejecuta los tests salvo a mano.
6. **Licencia.** Sin declarar.

## Cobertura de documentación

`go run ./cmd/docaudit -v` la mide sobre la superficie exportada. Hoy: **83 %
global**, 965 símbolos documentados y 185 sin documentar.

Los paquetes públicos están bien. Lo que hunde la media son los internos del
generador, que no se documentaron nunca:

| Paquete | Cobertura |
| --- | --- |
| `cmd/waygenerator/internal/symbols` | 0 % |
| `cmd/waygenerator/internal/xmlmodel` | 4 % |
| `cmd/waygenerator/internal/resolve` | 10 % |
| `cmd/keysymgen/internal/keysymdata` | 62 % |
| `canvas` | 71 % |
| `wayland/xdgshell` | 71 % |
| `wayland/wlcore` | 97 % |
| `keyboard`, `cursorshape`, `fractionalscale`, `tablet`, `viewporter` | 100 % |

## Pruebas

41 ficheros de test, 14 paquetes con tests. Lo que cubren, por si hace falta
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
- `wayland/wlcore` — tests del wire protocol y del ciclo de vida de objetos.
- Los ejemplos no tienen tests salvo `keylog` y `widgets`, y los suyos son de
  la lógica pura, no de la sesión Wayland.

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

# waygenerator — diseño de implementación (fase 1: wayland.xml)

Este documento especifica **cómo se construye** `cmd/waygenerator`, la CLI
que lee `protocols/*.xml` y emite los `*.gen.go` de `wayland/`. El contrato
detallado (qué genera exactamente cada construcción XML, convenciones de
nombres, mapeo de tipos, plantillas de request/evento, casos especiales) ya
vive en `docs/waygenerator.md` y es la autoridad para esas decisiones — este
documento no las repite, las referencia y decide la estructura interna, el
orden de trabajo y los criterios de aceptación.

**Spec de referencia:** `docs/wlcore.md` (runtime), `docs/waygenerator.md`
(contrato generador↔runtime, convenciones, casos especiales).

## Alcance de esta fase

- Objetivo: pipeline completo (las 4 pasadas descritas en `waygenerator.md`)
  apuntando **solo a `wayland.xml`** — las 23 interfaces del protocolo core,
  sin excluir ninguna (incluye `wl_shell`/`wl_shell_surface`, obsoletas en la
  práctica pero presentes en el XML sin marcarlo formalmente; el generador no
  decide qué es "obsoleto" fuera del propio XML).
- `xdg-shell.xml` y `wlr-layer-shell.xml` (paquetes `xdgshell`,
  `wlrlayershell`) quedan fuera — el manifiesto fichero→paquete de `symbols`
  los contempla como constantes ya definidas, pero esta fase no genera código
  para ellos ni los incluye en la pasada 0.
- **Criterio de aceptación**: el generador produce `wl_display`, `wl_callback`
  y `wl_registry` como `*.gen.go`, sustituyendo a `display_bootstrap.go`,
  `callback_bootstrap.go` y `registry_bootstrap.go` (que se borran), más el
  resto de las 20 interfaces core que hoy no existen en `wlcore`
  (`wl_compositor`, `wl_shm_pool`, `wl_shm`, `wl_buffer`, `wl_data_offer`,
  `wl_data_source`, `wl_data_device`, `wl_data_device_manager`, `wl_shell`,
  `wl_shell_surface`, `wl_surface`, `wl_seat`, `wl_pointer`, `wl_keyboard`,
  `wl_touch`, `wl_output`, `wl_region`, `wl_subcompositor`, `wl_subsurface`,
  `wl_fixes`). `go build ./... && go test ./...` en todo el repo, en verde,
  es la definición de "terminado" — no una fase aparte.

## Prerrequisito: Go 1.27

`go.mod` sube de 1.26.6 a 1.27 antes de empezar el generador. Go 1.27 añade
**métodos genéricos** (un método puede declarar sus propios parámetros de
tipo, algo que no existía hasta ahora). Esto cambia una decisión ya tomada en
`docs/waygenerator.md` (sección "casos especiales", `wl_registry` y `bind`):
`Bind[T]` estaba documentado como función libre *porque* Go no admitía
métodos genéricos. ya no aplica.

Como trabajo previo al generador (independiente, no bloquea las pasadas):

- `wayland/wlcore/registry.go`: `Bind[T Proxy](r *Registry, name, version
  uint32, iface Interface[T]) (T, error)` → método
  `(*Registry) Bind[T Proxy](name, version uint32, iface Interface[T]) (T,
  error)`. Actualizar `registry_test.go` a la nueva forma de llamada
  (`reg.Bind(...)` en vez de `Bind(reg, ...)`).
- `docs/waygenerator.md`: la sección "casos especiales" sobre `bind` pasa a
  documentar el método genérico como la forma elegida (ya no "función libre
  porque Go no admite métodos genéricos"); `bindRaw` generado pasa a método
  no exportado `(*Registry) bindRaw(...)` en vez de función libre.

## Arquitectura

```
cmd/waygenerator/
├── main.go                 # encadena las 4 pasadas; sin flags, rutas fijas
│                            # (./protocols → ./wayland/<paquete>)
└── internal/
    ├── xmlmodel/            # pasada 0: encoding/xml → AST fiel al XML
    ├── symbols/             # pasada 1: AST → tabla global de símbolos
    ├── resolve/             # pasada 2: referencias → símbolos reales + invariantes
    └── codegen/             # pasada 3: modelo resuelto → *.gen.go
```

`main.go` es solo pegamento, sin lógica propia más allá de encadenar y
reportar el error de la primera pasada que falle:

```go
protos, err := xmlmodel.ParseAll(protocolsDir)   // pasada 0
table       := symbols.Build(protos)              // pasada 1
model, err  := resolve.Resolve(protos, table)      // pasada 2, aborta con file:line
err          = codegen.Emit(model, outDir)          // pasada 3
```

## Flujo de datos entre pasadas

**`xmlmodel`** — tipos que reflejan 1:1 el XML: `Protocol{Interfaces
[]Interface}`, `Interface{Name, Version, Requests, Events, Enums}`,
`Request/Event{Name, Since, Args}`, `Arg{Name, Type, Interface, Enum,
AllowNull, Summary}`, `Enum{Name, Bitfield, Entries}`. Sin interpretar nada;
los únicos errores posibles aquí son "el XML no es XML". Cada nodo guarda su
línea de origen (vía `xml.Decoder.InputOffset`) para que los errores de
pasadas posteriores puedan citarla. `ParseAll(dir string) ([]Protocol,
error)` lee los ficheros del manifiesto interno (`wayland.xml` en esta fase)
en el orden del manifiesto.

**`symbols`** — recorre todos los `Protocol` y construye `Table
map[string]Entry`, `Entry{XMLName, GoPackage, GoType, MaxVersion, ReqOpcodes,
EvtOpcodes map[string]int, Enums map[string]EnumInfo}`. El manifiesto
fichero→paquete (`wayland.xml → wlcore`, y las entradas ya reservadas para
`xdg-shell.xml`/`wlr-layer-shell*.xml` aunque no se procesen esta fase) es una
constante de este paquete. Opcodes de request y de evento se numeran por
separado desde 0, por índice en la lista del XML — el prefijo `opReq`/`opEvt`
lo añade `codegen`, aquí solo se guarda el número.

**`resolve`** — por cada `Interface` produce un `resolve.Interface` con cada
`Arg` resuelto a un `resolve.GoType` (sum type: primitivo / `Fixed` / objeto
tipado con paquete+nombre / objeto dinámico sin `interface=` / enum tipado /
`new_id` con su factory, estático o dinámico). Marca cada evento como
"fd-owning" si algún `Arg` es `type="fd"`. Aquí corren las 3 invariantes del
spec, en este orden, abortando en la primera que falle:

1. DAG de paquetes con `wlcore` en la raíz (irrelevante en esta fase de un
   solo paquete, pero la comprobación vive aquí desde ya para no tener que
   añadirla cuando llegue `xdgshell`).
2. Sin colisiones de nombre Go dentro de un paquete — tipos, y opcodes
   `opReq`/`opEvt` (que homónimos request/evento no colisionen).
3. Ninguna interfaz alcanzable como `new_id` en evento lleva `arg type="fd"`
   en sus propios eventos.

`Resolve(protos []xmlmodel.Protocol, table symbols.Table) (Model, error)` —
el error, si lo hay, ya trae fichero y línea.

**`codegen`** — un `text/template` por "forma": struct del tipo (embebe
`ProxyBase`), constructor `newX` (engancha `OnClear` solo si la interfaz
tiene eventos), `SetListener`/`XListener`, `Dispatch` (variante normal y
variante fd-owning), un método por request (variante normal / con `fd` /
`new_id` estático / `new_id` dinámico vía `bindRaw`), descriptor `var
XInterface = Interface[*X]{...}`, bloque `const` de opcodes. Un fichero
`.gen.go` por interfaz, nombrado por el tipo Go en minúsculas
(`wl_shm_pool` → `shm_pool.gen.go`), cabecera `// Code generated by
waygenerator. DO NOT EDIT.`, interfaces ordenadas por nombre antes de emitir
(orden determinista). Cada fichero pasa por `format.Source` antes de
escribirse a disco; si no parsea, es un bug de plantilla y aborta ahí, no
llega a disco medio escrito.

Caso especial de esta fase: `wl_display`, reconocido por nombre XML
(`"wl_display"`, único caso hardcodeado así en `codegen`), genera el struct,
`Dispatch` y el descriptor normalmente, pero **omite `SetListener`
público** — el listener real lo engancha `conn.go` (a mano) en `Connect()`
sobre el campo interno, igual que hoy con el bootstrap provisional.

## Manejo de errores

Cualquier error de las pasadas 0-2 aborta la generación entera — no hay
generación parcial ni fallback. `codegen` no puede fallar por datos
malformados (si algo llega roto hasta ahí es un bug de `resolve`); solo
puede fallar por una plantilla que emite Go inválido (atrapado por
`format.Source`) o un error de escritura a disco.

## Testing

- **`xmlmodel`**: fixtures XML mínimos (1-2 `<interface>` sintéticas)
  verificando el mapeo AST, incluyendo `allow-null`, `enum="iface.name"` con
  punto, `bitfield="true"`.
- **`symbols`**: fixture que fuerza un request y un evento homónimos en la
  misma interfaz → verifica que se numeran por separado sin colisionar en la
  tabla.
- **`resolve`**: casos que deben resolver (normal, `new_id` sin interfaz →
  dinámico) y un test por invariante que prueba que la dispara (grafo
  cíclico sintético entre dos paquetes ficticios, colisión de nombre,
  fd-en-new_id-evento).
- **`codegen`**: golden files — modelo resuelto sintético pequeño → Go
  esperado, committeado como fixture, comparación byte a byte. Cubre cada
  forma de plantilla al menos una vez: request normal, con `fd`, con `new_id`
  estático, `bind` dinámico, evento normal, evento fd-owning, caso
  `wl_display` sin `SetListener` público.
- **Aceptación (nivel plan)**: generador completo sobre `protocols/wayland.xml`
  real → `wayland/wlcore/`, borrar los 3 `*_bootstrap.go` provisionales,
  `go build ./... && go test ./...` sobre todo el repo en verde. Es el
  criterio de "terminado", no una fase más de testing.

## Fuera de alcance (fases futuras)

- `xdg-shell.xml` → paquete `xdgshell`.
- `wlr-layer-shell.xml` → paquete `wlrlayershell`.
- Cualquier flag de CLI (rutas configurables, dry-run, etc.) — si hace falta
  más adelante para las fases de extensión, se añade entonces.
- Arrays tipados por interfaz concreta (keycodes de `wl_keyboard.enter`,
  estados de `xdg_toplevel.configure`) — el generador emite `[]byte` para
  `array` siempre, sin excepción, tal como documenta `waygenerator.md`.

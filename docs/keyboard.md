# `keyboard` — entrada de teclado

Documento de referencia. Acompaña a `wlcore.md` (runtime a mano) y
`waygenerator.md` (generador de bindings).

## Estado

**Diseño de referencia**, en el mismo sentido que `canvas.md` antes de que
`canvas/` estuviera completo: documenta la pieza entera, compilador XKB,
composición de dead keys y la capa de integración con `wlcore` (foco,
repeat, ensamblado de `Event`), aunque hoy solo está construida una parte.

Implementado, en `keyboard/` (paquete plano; todavía sin el split en
`input/keyboard` + `input/xkbmini` que describe el resto de este documento):

- `Compile`, `Keymap`, `State` — compilador del keymap XKB, en `xkbmini.go`.
- `Composer` — dead keys sobre el flujo de keysyms, en `compose.go`.

Pendiente: el tipo `Keyboard`, `Event`/`Mods`, el ciclo de vida sobre
`wlcore.Seat`, el timer de repeat y la separación en los sub-paquetes
descritos más abajo. Hasta que exista esa capa, quien consuma el paquete
llama directamente a `Compile` / `NewState` / `Composer`:

```go
km, err := keyboard.Compile(keymapString) // al recibir wl_keyboard.keymap
st := km.NewState()
var comp keyboard.Composer

st.UpdateMask(depressed, latched, locked, group) // en wl_keyboard.modifiers
sym := st.Sym(evdevKeycode + 8)                  // en wl_keyboard.key
text := comp.Feed(sym)
```

Tests: `xkbmini_test.go` cubre con keymaps mínimos a mano los bugs que fue
encontrando el oráculo (ver abajo). Además, `oracle_test.go` /
`oracle_cgo.go` (tag de build `oracle`, requiere cgo y las cabeceras de
desarrollo de `libxkbcommon`) comparan `Compile`/`State` contra la
`libxkbcommon` real: compilan el mismo RMLVO con ambas (mismo texto de
keymap para las dos, vía `xkb_keymap_get_as_string`) y barren cada
keycode × grupo × las 256 combinaciones de modificadores reales, más
`Keysym.Rune` contra `xkb_keysym_to_utf32`. Se ejecuta con:

```sh
go test -tags oracle ./keyboard/...
```

Cobertura, contra `libxkbcommon` 1.13.2 (la versión importa: la semántica de
`xkb_state_key_get_one_sym` cambió en 1.9.0), por **dos vías**:

1. **RMLVO sintético** — `us`, `es`, `es(cat)`, `us(intl)`, construidos con
   `xkb_keymap_new_from_names`.
2. **Keymaps reales capturados** — ficheros bajo `keyboard/testdata/`,
   cargados con `xkb_keymap_new_from_string`.

La segunda vía no es un extra. `xkb_keymap_new_from_names` **solo produce
keymaps de un grupo**, y un compositor real manda keymaps multigrupo en
cuanto el usuario tiene dos distribuciones configuradas. Esa diferencia
estructural escondió un fallo que dejaba AltGr completamente inservible
mientras el barrido sintético daba `Consumed` perfecto: sobre el keymap real
había 4672 diferencias de `Sym` y 13 312 de `Consumed`. Ver `resolveVirtualMods`
más abajo.

Las dos vías comparten el mismo cuerpo de comparación (`compareOracle`), a
propósito: si se duplicara, dejarían de comprobar lo mismo y el hueco se
reabriría. Para capturar un keymap nuevo:

```sh
KEYLOG_DUMP_KEYMAP=keyboard/testdata/algo.xkb go run ./example/keylog
```

El barrido lo dirige la lista de keycodes que devuelve el *keymap de
`libxkbcommon`*, no `km.keys`: si `Compile` se deja una tecla, eso tiene que
salir como fallo, no desaparecer de la comparación.

Estado medido hoy, en número de comparaciones que difieren:

| keymap | Sym | Consumed | Rune |
| --- | --- | --- | --- |
| `us` | 0 | 0 | 0 |
| `es` | 128 | 0 | 0 |
| `es(cat)` | 128 | 0 | 0 |
| `us(intl)` | 96 | 0 | 0 |
| `testdata/live-multigroup.xkb` (real, 3 grupos) | 128 | 0 | 0 |

Las causas están medidas, no supuestas:

- **`preserve[]`, resuelto**: ahora se conserva el lado derecho y ambos lados
  se resuelven después de los modificadores virtuales. Eliminó 14 912 de las
  15 552 discrepancias de `Consumed`: `us` llegó a cero y los otros layouts
  quedaron en 320/256/64. La atribución anterior del 100 % a `preserve[]` era
  incorrecta: la misma firma también aparece cuando se elige un tipo de tecla
  equivocado.
- **Tablas parciales de `keysymNames` y `legacyRunes`, resuelto**: la tabla
  generada contiene todos los nombres y las conversiones Unicode de la
  cabecera vendorizada. Además de llevar `Rune` a cero, los pares legacy
  completos como `tslash`/`Tslash` permiten que `guessType` elija el tipo
  automático correcto y eliminan diferencias que afectaban tanto a
  `Consumed` como a `Sym`. Las anotaciones Unicode precedidas por metadatos
  de versión también se procesan; así entran `XF86Numeric0`–`9`,
  `XF86NumericStar` y `XF86NumericPound`.
- **Tipos automáticos, resuelto**: el par `ssharp`/`SSHARP` requiere comprobar
  también la conversión Unicode de mayúscula a minúscula, porque la conversión
  simple inversa no transforma `ß` en `ẞ`. Con ese caso cubierto, `Consumed`
  queda en cero en los cuatro layouts.
- **`Sym`, pendiente**: las 128/128/96 diferencias que quedan en
  `es`/`es(cat)`/`us(intl)` son únicamente la transformación de
  capitalización que `xkb_state_key_get_one_sym` aplica cuando Caps es
  efectivo y no está consumido; por ejemplo, la biblioteca transforma
  `mu`→`Greek_MU`, `ccedilla`→`Ccedilla` y `ssharp`→`SSHARP`. Esta
  transformación de `State.Sym` sigue siendo una tarea separada.

Un aviso que cuesta caro olvidar: `xkb_keymap_get_as_string` serializa los
keysyms **en hexadecimal** (`key <AC09> { [ 0x6c, 0x4c, 0x1b3, 0x1a3 ] };`),
no por nombre como hace `xkbcli compile-keymap`. Como `ParseKeysym`
cortocircuita en `0x`, la tabla `keysymNames` **no se consulta nunca** durante
el barrido. Por tanto, el oráculo hexadecimal no puede validar la ruta por
nombres aunque todas sus columnas lleguen a cero. Antes ocultaba un fallo real:
`ParseKeysym("ISO_Level3_Latch")` devolvía 0 y `resolveVirtualMods` confundía el
resultado con `NoSymbol`, lo que contaminaba AltGr y NumLock. La regresión
dedicada `TestNamedVirtualModifierInterpretsIgnoreUnresolvedKeysyms` cierra ese
hueco: compila un keymap serializado por nombres, comprueba exactamente
`LevelThree=Mod5` y `NumLock=Mod2`, y conserva la protección para nombres
realmente desconocidos.

El segundo hueco del oráculo fue de **alcance**, no de serialización, y salió
igual de caro. `resolveVirtualMods` recorría *todos* los grupos y símbolos de
cada tecla al emparejar un `interpret`. En un keymap de dos distribuciones,
`<RALT>` es `Alt_R` en el grupo 1 y `ISO_Level3_Shift` en el grupo 2, y está en
`modifier_map Mod1`: al mirar todos los grupos, Mod1 se colaba en `LevelThree`
(`0x88` en vez de `0x80`), `masked` ya no casaba con `map[LevelThree]` y **el
nivel 3 quedaba inalcanzable** — AltGr dejaba de funcionar en toda la
distribución. `libxkbcommon` enlaza el `virtualModifier` de un `interpret`
únicamente desde el grupo 1, nivel 1, que es donde `<RALT>` es `Alt_R`.
Comprobado contra la biblioteca con cuatro keymaps mínimos: un símbolo en el
grupo 1 *nivel 2* no enlaza, un grupo 1 ausente no enlaza, y un `NoSymbol`
explícito en grupo 1 nivel 1 **no** cae al grupo 2.

Ese fallo es la razón de la vía de keymaps capturados: el barrido sintético no
podía verlo, porque `xkb_keymap_new_from_names` nunca genera un keymap
multigrupo.

`preserve[]`, los datos completos de keysyms, la ruta por nombres y los keymaps
multigrupo ya están cubiertos. En el oráculo solo queda pendiente la
transformación de capitalización de `State.Sym`. `Composer` no tiene tests
todavía (no tiene sentido compararlo contra `libxkbcommon`: implementa NFC
canónico a propósito, no el fichero Compose de X11 — necesita su propia suite
con secuencias conocidas de ca/es/en).

### Teclas modificadoras y el composer

`Keymap.IsModifierKey(keycode)` responde desde el `modifier_map` del keymap, no
desde un rango de keysyms. Quien alimente el `Composer` **tiene** que filtrar
las modificadoras: si le llega una, cancela el dead key pendiente y el acento se
pierde. Y un rango no vale: Shift/Control/Alt/Super viven en `0xffe1`–`0xffee`,
pero AltGr llega como `ISO_Level3_Shift` (`0xfe03`), muy por debajo de ese
bloque. Con el filtro por rango, pulsar AltGr para escribir un carácter acentuado
descartaba el acento — comprobado en `example/keylog` contra un compositor real.

## Responsabilidad

`keyboard` convierte los eventos crudos de `wl_keyboard` en eventos de
aplicación con texto ya compuesto. Es la única pieza del stack que conoce
XKB.

```
wl_keyboard (wlcore, generado)      keycode + máscaras de modificadores
        │  listener: structs de funcs
        ▼
keyboard.Keyboard                   dueño del estado: keymap, mods, foco, repeat
        │  xkbmini.State            keycode + mods -> keysym
        │  xkbmini.Composer         keysym -> texto (dead keys)
        ▼
chan keyboard.Event                 la app consume aquí
```

El corte listener/channel es deliberado y sigue la convención del proyecto:
los eventos de protocolo son callbacks síncronos porque el orden importa y no
puedes bloquear el bucle de lectura del socket; la aplicación consume por
channel porque ahí sí quieres desacoplar.

Sub-paquetes:

| Paquete | Qué hace |
| --- | --- |
| `input/keyboard` | ciclo de vida, foco, repeat, ensamblado del evento |
| `input/xkbmini` | compilador de keymap XKB (subconjunto cliente) |

## Ciclo de vida

1. `wl_registry` anuncia `wl_seat`; se hace bind.
2. `wl_seat.capabilities` → si el bit `keyboard` está puesto, `get_keyboard()`.
   La capacidad puede **desaparecer** en caliente (desconectas el teclado USB):
   hay que enviar `release` y tirar el estado.
3. Llegan `keymap` y `repeat_info` antes que cualquier `key`. Eso lo garantiza
   el protocolo.
4. `enter` da el foco; `leave` lo quita.
5. Al cerrar, `wl_keyboard.release` (destructor, since v3), no `destroy`.

Puede haber **varios seats**. Un launcher normalmente solo quiere el primero,
pero hay que decidirlo explícitamente y no asumir que hay uno.

## Eventos y tratamiento

| Evento | Qué hace `keyboard` |
| --- | --- |
| `keymap(format, fd, size)` | `mmap` + compila con `xkbmini`; `munmap` y cierra el fd **siempre**, incluso si `format == 0` (`no_keymap`) |
| `enter(serial, surface, keys)` | fija el foco; `keys` es un array de keycodes ya pulsados que hay que sembrar en el estado |
| `leave(serial, surface)` | limpia foco, teclas pulsadas, mods y dead key pendiente; corta el timer de repeat |
| `key(serial, time, key, state)` | resuelve keysym + texto y emite `Event` |
| `modifiers(...)` | `State.UpdateMask`; **no** emite evento |
| `repeat_info(rate, delay)` | reconfigura el timer; puede llegar en cualquier momento, no solo al inicio |

### El fd del keymap

Se mapea en solo lectura y **con `MAP_PRIVATE`**: desde la versión 7 del
protocolo `MAP_SHARED` puede fallar. `size` incluye el NUL final, así que el
string útil es `data[:size-1]`.

Los fds llegan por la cola de `Conn`, no en el cuerpo del mensaje: el
`Dispatch` generado los saca con `Decoder.FD()`. Si no consumes el fd, lo
filtras — y un compositor que reenvía el keymap en cada cambio de layout te
agota los descriptores en una sesión larga.

### Keycodes

`wl_keyboard.key` manda keycodes **evdev**. El keycode XKB es `evdev + 8`.
Todo lo que cruza hacia `xkbmini` va ya en XKB; el `Event` expone ambos para
que quien quiera atajos por posición física no tenga que sumar a mano.

## Compilación del keymap

`xkbmini` implementa el subconjunto del formato XKB v1 que necesita un
cliente: `xkb_keycodes`, `xkb_types`, `xkb_symbols` y lo justo de
`xkb_compatibility` para resolver modificadores virtuales. Quedan fuera a
propósito las acciones (`SetMods`, `LockGroup`…), que son cosa del compositor,
y `xkb_geometry`.

No hay reimplementación de xkbcommon en Go puro; esto es lo mínimo viable, no
un sustituto.

### Selección de nivel

No es "si Shift, nivel 2". Cada tecla declara un tipo, el tipo declara qué
modificadores le importan y una tabla de máscara a nivel:

```
type "ALPHABETIC" {
    modifiers = Shift+Lock;
    map[Shift] = level2;
    map[Lock]  = level2;
};
```

El algoritmo entero es `nivel = tipo.map[efectivos & tipo.modifiers]`, con
nivel 1 si no hay entrada. Por eso Caps sobre `q` da `Q` pero sobre `1` no
hace nada: son tipos distintos.

Modificadores efectivos = `depressed | latched | locked`.

### Modificadores virtuales

`LevelThree` (AltGr) no es un bit. Es un nombre que se codifica sobre un
modificador real — casi siempre Mod5 — a través de los `interpret` de
`xkb_compatibility` cruzados con el `modifier_map` de `xkb_symbols`. Los
reales sí tienen índice fijo y son los que viajan por el cable:

| Bit | Nombre | Uso habitual |
| --- | --- | --- |
| 0 | Shift | |
| 1 | Lock | Caps Lock |
| 2 | Control | |
| 3 | Mod1 | Alt |
| 4 | Mod2 | NumLock |
| 5 | Mod3 | |
| 6 | Mod4 | Super |
| 7 | Mod5 | AltGr / LevelThree |

La columna "uso habitual" es una convención de xkeyboard-config, no una
garantía. Para atajos con Super hay que resolver el virtual, no dar Mod4 por
supuesto.

### Modificadores consumidos

El detalle que rompe los atajos en todo el mundo. Si el tipo de la tecla usó
Shift para elegir el nivel, ese Shift ya está "gastado" y no debe contar en el
match del atajo. `Event.Mods.Consumed` lleva `tipo.modifiers &^ preserve`, y
el match correcto es:

```go
if ev.Sym == symX && ev.Mods.Effective&^ev.Mods.Consumed == ModCtrl {
```

Sin eso, `Ctrl+Shift+X` se comporta distinto según el layout y `Shift+2` nunca
casa con `at` en un teclado US.

## Composición (dead keys)

`Composer` va **encima** de `State`, no dentro: `State` resuelve keysyms,
`Composer` resuelve secuencias de keysyms a texto.

En castellano y catalán no es opcional. `´ \` ¨ ^` no producen carácter:
producen `dead_acute`, `dead_grave`, `dead_diaeresis`, `dead_circumflex`. Sin
composición no se puede escribir `què`, `això` ni `también`.

La implementación mapea cada `dead_*` a su combinante Unicode y deja que
`x/text/unicode/norm` haga NFC. Eso cubre catalán, castellano e inglés
enteros, porque todos sus acentos son composiciones canónicas.

Lo que **no** cubre: las entradas no canónicas del fichero Compose de X11
(`dead_stroke` + `o` = `ø`) y la tecla Compose (`Multi_key`) con sus
secuencias arbitrarias. Si eso hace falta algún día, toca parsear
`/usr/share/X11/locale/*/Compose`.

Reglas de la máquina de estado:

- dead + base que compone → precompuesto
- dead + espacio → carácter espaciador (`´`, `¨`…)
- dead + misma dead → carácter espaciador, secuencia cancelada
- dead + base que no compone → espaciador seguido del base (comportamiento GTK)
- dead + tecla no imprimible → espaciador, secuencia cancelada

Los keysyms de modificador (0xFFE1–0xFFEE) se filtran **antes** de entrar al
composer. Si no, pulsar Shift para escribir `Á` cancela el acento pendiente.

### Keysyms

Tres formas de resolver un keysym a code point:

1. Flag `0x01000000` → los 24 bits bajos son el code point.
2. Rango Latin-1 (0x20–0x7E, 0xA0–0xFF) → el keysym *es* el code point.
3. Legacy → tabla. Aquí caen `EuroSign` (0x20AC, el AltGr+E), las comillas
   tipográficas y todo latin2/3/4, griego y cirílico.

La fuente única es la cabecera de libxkbcommon 1.13.2 vendorizada en
`third_party/libxkbcommon/xkbcommon-keysyms.h`. El comando independiente:

```sh
go run ./cmd/keysymgen
```

la parsea sin red y genera `keyboard/keysyms.gen.go`, que contiene las tablas
de nombre a valor, nombre canónico y conversión Unicode legacy. Las teclas de
función también están identificadas: `F1`–`F12` tienen nombre y valor de
keysym, pero no runa ni texto porque no son imprimibles.

## Repetición

La hace el cliente. `repeat_info` da `rate` en teclas/segundo y `delay` en
milisegundos; `rate == 0` desactiva la repetición.

El timer vive en `keyboard`, no en la app: arranca en el `key` de pulsación,
se cancela al soltar, al perder el foco y al recibir un `repeat_info` nuevo.
Solo repite la última tecla pulsada, y solo si el keymap dice que esa tecla
repite (`repeat= no` existe en `xkb_symbols`, típicamente para modificadores).

Desde la versión 10 de `wl_keyboard` el compositor puede encargarse él y
mandar `key` con estado `repeated` — pero solo si le has anunciado `rate` 0, y
solo si has hecho bind del seat a v10. Con v9 o menos ni se plantea. El
paquete acepta ambos caminos y normaliza a `Event.State == Repeat`.

## Foco

Con `wlr-layer-shell` **no llega ningún `enter`** si no llamas antes a
`zwlr_layer_surface_v1.set_keyboard_interactivity`:

| Valor | Cuándo |
| --- | --- |
| `none` (0) | OSD, barra: no quieres robar el foco |
| `exclusive` (1) | launcher: capturas todo mientras estás abierto |
| `on_demand` (2) | requiere v4 del protocolo; foco al clicar |

Es el fallo número uno al escribir un launcher: todo el código está bien y no
llega una sola tecla.

`enter` trae las teclas ya pulsadas. Si abres el launcher con un atajo, la
tecla del atajo puede seguir físicamente abajo — hay que sembrarlas en el
estado y no emitir eventos de pulsación por ellas.

## Varios layouts

Si el usuario tiene ca+es+us configurados, llega **un solo keymap** con tres
grupos por tecla. El campo `group` de `modifiers` selecciona cuál. No hay
recompilación al cambiar de layout, solo un `modifiers` nuevo — salvo que el
usuario reconfigure de verdad, en cuyo caso llega un `keymap` nuevo y hay que
recompilar y volver a cerrar el fd viejo.

XKB envuelve el grupo por módulo cuando el índice se sale del rango de esa
tecla concreta, que puede tener menos grupos que el keymap.

## API

```go
type Keyboard struct{ /* ... */ }

func New(conn *wlcore.Conn, seat *wlcore.Seat) (*Keyboard, error)
func (k *Keyboard) Events() <-chan Event
func (k *Keyboard) Focus() *wlcore.Surface
func (k *Keyboard) Close() error

type Event struct {
    State   KeyState // Pressed, Released, Repeat
    Keycode uint32   // XKB (evdev + 8)
    Evdev   uint32   // crudo, para atajos por posición física
    Sym     xkbmini.Keysym
    Text    string   // "" si la tecla no produce texto o se la tragó un dead key
    Mods    Mods
    Serial  uint32   // necesario para requests posteriores
    Time    uint32
}

type Mods struct {
    Depressed, Latched, Locked uint32
    Effective                  uint32 // los tres OR-eados
    Consumed                   uint32 // gastados en elegir el nivel
}
```

`Serial` no es decorativo: cualquier request que exija una interacción del
usuario (un `set_selection`, un popup grab) necesita el serial del evento que
la originó.

## Trampas conocidas

- No cerrar el fd del keymap.
- Mapear con `MAP_SHARED`.
- Olvidar el `+8` del keycode.
- Comparar atajos contra los modificadores crudos en vez de restar los
  consumidos.
- No llamar a `set_keyboard_interactivity` en layer shell.
- No resetear el dead key pendiente en `leave`.
- Asumir un solo seat, o que la capacidad `keyboard` no cambia en caliente.
- Dar por hecho que Super es Mod4 sin resolver el virtual.

# Informe de corrección final — generador de keysyms

## Resultado

Resueltos los tres bloqueos importantes de la revisión completa de la rama:

1. La tabla generada coincide con `xkb_keysym_to_utf32` para los dos símbolos
   heredados cuyos comentarios públicos conservan puntos de código obsoletos.
2. `VoidSymbol` conserva su valor XKB explícito `0xffffff` y ya no contamina
   las interpretaciones deliberadas de `NoSymbol`/cero.
3. Las anotaciones Unicode solo aceptan las formas completas `U+...`,
   `<U+...>` y `(U+...)`, incluidos los casos válidos situados después de
   metadatos.

No se modificó `docs/keyboard.md`: las mediciones de los cuatro layouts y sus
afirmaciones continúan siendo exactamente las mismas. Tampoco se modificó
`cmd/waygenerator`.

## Diagnóstico y procedencia

### Ángulos heredados

La cabecera vendorizada de libxkbcommon 1.13.2 contiene:

- `0x0abc` (`leftanglebracket`) anotado como `U+2329`;
- `0x0abe` (`rightanglebracket`) anotado como `U+232A`.

El comportamiento ejecutable local y autoritativo de
`xkb_keysym_to_utf32`, en cambio, devuelve respectivamente `U+27E8` y
`U+27E9`. La referencia medida fue:

- `pkg-config xkbcommon`: `1.13.2`;
- paquete: `libxkbcommon 1.13.2-1.1`;
- biblioteca: `/usr/lib/libxkbcommon.so.0.13.2`;
- SHA-256:
  `0a756040ef5eb110e03234da28ea078979906024a730021c0ca20e19d096eda7`;
- Build ID: `028984f3981fcb975e07f1c9aa9cd062c285cbb1`.

El parser aplica dos excepciones explícitas, documentadas junto a su
procedencia, antes de construir las tablas. El oráculo nuevo recorre los
2.505 valores únicos explícitos de `keysymCanonicalNames`, en orden estable,
y los compara con la biblioteca mediante `xkb_keysym_to_utf32`. La salida
generada solo cambia:

- `legacyRunes[0x0abc]`: `U+2329` → `U+27E8`;
- `legacyRunes[0x0abe]`: `U+232A` → `U+27E9`.

### `VoidSymbol`

`keysyms.gen.go` ya contenía `keysymNames["VoidSymbol"] = 0xffffff`, pero
`ParseKeysym` devolvía antes de consultar la tabla porque agrupaba el nombre
con `NoSymbol`. Además, `isResolvedZeroKeysym` volvía a clasificarlo como cero
deliberado. Se retiró `VoidSymbol` de ambos atajos, conservando sin cambios
`NoSymbol`, la cadena vacía y el cero hexadecimal válido.

### Delimitadores Unicode

La expresión anterior permitía cualquier abridor opcional y daba por válida
la anotación al encontrar espacio después del punto de código; no comprobaba
el cierre ni que este correspondiese al abridor. La extracción separa ahora
las tres formas admitidas, exige `>` o `)` terminal cuando procede y rechaza
un cierre espurio en la forma plana.

## Evidencia TDD RED/GREEN

### Correspondencia de runas

- RED del parser: `Runes[0xabc] = U+2329, want U+27E8` y
  `Runes[0xabe] = U+232A, want U+27E9`.
- RED del oráculo exhaustivo: exactamente los mismos dos fallos:
  `0xabc` produjo `〈` en vez de `⟨` y `0xabe` produjo `〉` en vez de `⟩`.
- GREEN: `TestParseUsesLibxkbcommonLegacyAngleBracketRunes` pasa y
  `TestGeneratedRunesAgainstLibxkbcommon` pasa para los 2.505 valores únicos
  explícitos.

### Identidad de `VoidSymbol`

- RED de nombre: `ParseKeysym(VoidSymbol) = 0x0, want 0xffffff`.
- RED de keymap: las interpretaciones de `VoidSymbol` y `NoSymbol` recibieron
  ambas `0x50`; se esperaban por separado `Mod4=0x40` y `Mod2=0x10`.
- GREEN: el nombre hace ida y vuelta como `0xffffff`/`VoidSymbol`; el keymap
  obtiene las máscaras separadas. También pasan las regresiones previas del
  nombre desconocido, `NoSymbol` y el cero numérico.

### Anotaciones estrictas

- RED: seis casos de tabla —cierres ausentes, cierres cruzados y cierres
  espurios de la forma plana— devolvieron error nulo.
- GREEN: los seis devuelven `invalid Unicode annotation`; siguen pasando las
  formas planas, angulares y parentizadas válidas, incluidas las precedidas
  por metadatos, además de toda la suite de `keysymdata`.

## Verificación

Todas las órdenes Go se ejecutaron con
`GOCACHE=/tmp/ggui-keysym-go-cache`:

- `go test ./cmd/keysymgen ./cmd/keysymgen/internal/keysymdata -count=1` —
  pasa, incluida la prueba de frescura comprometida.
- Dos ejecuciones de `go run ./cmd/keysymgen` conservaron idéntico el SHA-256
  de `keyboard/keysyms.gen.go`:
  `c39c47e0db5a9f483be3ed47f23e638a4a68063c36de2c4c922339e3210d1df4`.
- `go test -tags oracle ./keyboard -run
  TestGeneratedRunesAgainstLibxkbcommon -count=1 -v` — pasa y registra 2.505
  valores explícitos comparados.
- `go build ./...`, `go vet ./...` y
  `go vet -tags oracle ./keyboard/...` — pasan.
- `go test -count=1 ./...` — pasa con permiso para los sockets Unix de
  `wayland/wlcore`; la primera ejecución confinada confirmó únicamente la
  restricción `operation not permitted` del entorno.
- `go test -race -short -count=1 ./...` — pasa.
- `gofmt` y `git diff --check` — sin hallazgos.
- El diff desde `699452cf80ffbf9be933060a21cf0593119987b7` y el diff del árbol
  de trabajo sobre `cmd/waygenerator` están vacíos.

El oráculo de layouts conserva sus resúmenes medidos:

| layout | Sym | Consumed | Rune |
| --- | ---: | ---: | ---: |
| `us` | 0 | 0 | 0 |
| `es` | 128 | 0 | 0 |
| `es(cat)` | 128 | 0 | 0 |
| `us(intl)` | 96 | 0 | 0 |

Su estado 1 sigue siendo el resultado esperado de las diferencias `Sym` de
capitalización ya documentadas y fuera de alcance; no quedan diferencias de
`Consumed` ni `Rune`. `golangci-lint` no está instalado en el entorno y no
forma parte de las puertas aprobadas. No quedan preocupaciones nuevas de esta
ronda.

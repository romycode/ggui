# Informe de implementación — Tarea 4

## Resultado

Implementada y confirmada en el commit
`27470e4cd9617430bae1595505b0417324438d7e`
(`docs: record generated keysym oracle results`).

El oráculo publica ahora un resumen estable por layout. Las dos capturas
posteriores a las correcciones fueron idénticas:

| layout | Sym | Consumed | Rune |
| --- | ---: | ---: | ---: |
| `us` | 0 | 0 | 0 |
| `es` | 128 | 0 | 0 |
| `es(cat)` | 128 | 0 | 0 |
| `us(intl)` | 96 | 0 | 0 |

Las diferencias `Sym` restantes son exclusivamente la transformación de
capitalización de `State.Sym`, que el diseño deja fuera de alcance. Por ese
motivo la ejecución con tag `oracle` termina con estado 1 de forma esperada;
no quedan diferencias `Consumed` ni `Rune`, teclas omitidas ni otro mecanismo
de fallo.

## Cambios

- Añadido a `runOracle` el mensaje exacto
  `mismatches: Sym=%d Consumed=%d Rune=%d`, sin relajar sus errores.
- Actualizada en español la medición y las secciones de oráculo/keysyms de
  `docs/keyboard.md`.
- Conservados los hallazgos previos de `preserve[]` y de la ruta por nombres,
  ahora documentados como resueltos. El fichero inicial coincidía con el hash
  de preservación indicado:
  `99342f8a6b9dc80e70b3b4815d9e278d68d6ea23d804f94ba5685c5cedfdd98e`.
- Documentados la cabecera vendorizada de libxkbcommon 1.13.2,
  `keyboard/keysyms.gen.go`, `go run ./cmd/keysymgen`, la regresión específica
  de keymaps por nombre y que `F1`–`F12` tienen nombre pero no runa/texto.

## Ampliación de alcance dictaminada

La primera medición posterior a la instrumentación no cumplió la aceptación:

| layout | Sym | Consumed | Rune |
| --- | ---: | ---: | ---: |
| `us` | 0 | 0 | 12 |
| `es` | 192 | 64 | 12 |
| `es(cat)` | 192 | 64 | 12 |
| `us(intl)` | 96 | 0 | 12 |

El controlador autorizó corregir ambas causas dentro de la aceptación del
generador/oráculo, después de probarlas mediante depuración sistemática y TDD:

1. Las únicas doce anotaciones Unicode no situadas al principio del comentario
   son `XF86Numeric0`–`9`, `XF86NumericStar` y `XF86NumericPound`, en las líneas
   3074–3085 de la cabecera vendorizada. El parser solo reconocía prefijos y no
   generaba esas doce entradas de `legacyRunes`.
2. El keymap `es` contiene el par automático `ssharp`/`SSHARP`. En Go,
   `unicode.ToUpper('ß')` conserva `ß`, mientras que
   `unicode.ToLower('ẞ') == 'ß'`. La comprobación unidireccional seleccionaba
   `FOUR_LEVEL_SEMIALPHABETIC` y preservaba Lock, explicando exactamente las
   64 diferencias `Consumed`.

La corrección del parser localiza una anotación `U+` después de metadatos y
mantiene el rechazo estricto de anotaciones mal formadas. La corrección del
tipo automático acepta la relación de caja válida en cualquiera de las dos
direcciones Unicode. Tras regenerar, el diff semántico de
`keyboard/keysyms.gen.go` fue exactamente de 12 altas y ninguna baja; el resto
del diff generado es alineación determinista de `gofmt` por las claves más
anchas.

## Evidencia TDD

- RED parser válido:
  `TestParseFindsUnicodeAnnotationAfterMetadata` obtuvo `U+0000`, esperaba
  `U+0030`.
- RED parser inválido:
  `bad unicode after metadata` obtuvo error nulo, esperaba
  `invalid Unicode annotation`.
- GREEN parser: ambos casos y toda la suite `keysymdata` pasan.
- RED tipo automático:
  `TestGuessTypeUsesAsymmetricUnicodeCasePair` obtuvo
  `FOUR_LEVEL_SEMIALPHABETIC`, esperaba `FOUR_LEVEL_ALPHABETIC`.
- GREEN tipo automático: la nueva regresión, la regresión `tslash`/`Tslash` y
  toda la matriz existente de `guessType` pasan.

## Verificación

Todas las órdenes Go se ejecutaron con
`GOCACHE=/tmp/ggui-keysym-go-cache`:

- `gofmt -l cmd/keysymgen keyboard example/keylog` — sin salida.
- `go build ./...` — pasa.
- `go vet ./...` — pasa.
- `go vet -tags oracle ./keyboard/...` — pasa.
- `go test -count=1 ./...` — pasa; se ejecutó con permiso para los tests de
  sockets Unix de `wayland/wlcore`.
- `go test -race -short -count=1 ./...` — pasa.
- `go test ./cmd/keysymgen/... -run TestCommittedOutputIsCurrent -count=1 -v`
  — pasa.
- Una segunda ejecución de `go run ./cmd/keysymgen` mantuvo idéntico el SHA-256
  de la salida:
  `19a7313efc23d8b43b46950392f54de55ac58bfe61e31ed7444e7048e839763c`.
- Dos ejecuciones del oráculo produjeron exactamente los cuatro resúmenes de
  la tabla inicial, siempre con `Consumed=0` y `Rune=0`.
- El barrido de términos obsoletos solicitado no encontró coincidencias.
- `git diff --exit-code bb42bfa..HEAD -- cmd/waygenerator` y el equivalente
  sobre el árbol de trabajo terminaron con estado 0 y sin salida.
- `git diff --cached --check` pasó antes del commit; se inspeccionó completo el
  diff documentado y solo contiene los hallazgos preservados y las secciones
  de oráculo/keysyms pertinentes.

`golangci-lint` no está instalado en el entorno; no forma parte de la puerta de
calidad aprobada. No hay otras preocupaciones abiertas para esta tarea.

## Fix Round 1

La primera revisión detectó dos defectos relacionados en
`findUnicodeAnnotation`, corregidos en
`385f9aed58cf6cd4a3ea4537508e9e8affcd6539`
(`keysymgen: tighten Unicode annotation parsing`):

1. La búsqueda libre de `strings.Index(comment, "U+")` confundía texto
   embebido con una anotación. `GNU+0041 metadata` generaba una correspondencia
   espuria a `U+0041` y `GNU+Linux` devolvía un error de anotación inválida en
   vez de ser un comentario ordinario.
2. La marca de obsolescencia se calculaba antes de localizar la anotación. Una
   forma parentizada después de metadatos, como
   `metadata (U+250C ...)`, podía convertirse incorrectamente en nombre
   canónico.

La localización exige ahora una de las formas exactas `U+`, `<U+` o `(U+` al
principio del comentario o tras un límite de espacio. La marca `deprecated` de
una anotación parentizada se deriva de la forma localizada, no del comienzo
del comentario completo.

Evidencia TDD de la ronda:

- RED: `GNU+0041 metadata` produjo `Runes[0x1234] = U+0041`, cuando no debía
  producir correspondencia.
- RED: `GNU+Linux` devolvió `invalid Unicode annotation`, cuando debía
  ignorarse como texto embebido.
- RED: la anotación `metadata (U+250C ...)` dejó `old_name` como nombre
  canónico, cuando debía elegirse `new_name`.
- GREEN: `TestParseIgnoresEmbeddedUnicodeText` y
  `TestParseTreatsParenthesizedAnnotationAfterMetadataAsDeprecated` pasan;
  también pasa completa la suite de `keysymdata`.

Verificación posterior:

- `gofmt -l cmd/keysymgen keyboard example/keylog` — sin salida.
- `go build ./...`, `go vet ./...` y
  `go vet -tags oracle ./keyboard/...` — pasan.
- `go test -count=1 ./...` — pasa.
- `go test -race -count=1 ./cmd/keysymgen/...` — pasa.
- La prueba de salida comprometida pasa y una nueva ejecución de
  `go run ./cmd/keysymgen` conserva exactamente el SHA-256
  `19a7313efc23d8b43b46950392f54de55ac58bfe61e31ed7444e7048e839763c`.
- El oráculo mantiene los resúmenes `0/0/0`, `128/0/0`, `128/0/0` y
  `96/0/0` para `us`, `es`, `es(cat)` y `us(intl)` respectivamente. Su estado
  1 sigue correspondiendo solo a la capitalización pendiente de `Sym`.
- `cmd/waygenerator` continúa sin diferencias desde `bb42bfa` ni en el árbol
  de trabajo.

No quedan preocupaciones abiertas de la ronda 1.

# `keysymgen` — diseño del generador de tablas XKB

**Fecha:** 2026-08-22

## Objetivo

Sustituir las tablas parciales escritas a mano de `keyboard` por datos
completos y reproducibles de libxkbcommon 1.13.2. El resultado debe cubrir
tres necesidades relacionadas:

1. Parsear los nombres que aparecen en keymaps XKB serializados por nombre,
   incluidos `F1`–`F12`, `ISO_Level3_Latch`, aliases y keysyms de extensiones.
2. Convertir todos los keysyms legacy con correspondencia Unicode mediante
   `Keysym.Rune`.
3. Obtener un nombre legible para un `Keysym`, de modo que herramientas como
   `example/keylog` muestren `F1` en vez de tratar la tecla como desconocida.

`F1` sigue siendo una tecla no imprimible: `Keysym.Rune()` devolverá `-1` y
no generará texto. Reconocerla significa que `State.Sym` devuelve `0xffbe`,
`ParseKeysym("F1")` devuelve el mismo valor y `Keysym(0xffbe).Name()` devuelve
`"F1"`.

## Límites

- Se crea un generador independiente llamado `cmd/keysymgen`.
- `cmd/waygenerator` está fuera de alcance: no se modifica, no se importa y
  no se reutiliza ninguno de sus paquetes internos.
- El código de producción continúa siendo Go puro. El generador tampoco usa
  cgo ni necesita libxkbcommon instalado.
- La transformación de capitalización de `State.Sym` sigue siendo una tarea
  distinta.
- No se generan miles de constantes Go exportadas. El API para identificar
  un símbolo por nombre es `ParseKeysym`; el API inverso es `Keysym.Name`.
- No se amplía `Composer` ni se implementa el fichero Compose de X11.

## Fuente vendorizada

El repositorio contendrá una instantánea de
`include/xkbcommon/xkbcommon-keysyms.h` de libxkbcommon 1.13.2 bajo
`third_party/libxkbcommon/`. Esa cabecera es la fuente autoritativa única para
la generación: contiene los nombres, valores, aliases y anotaciones Unicode.

Junto a la cabecera se guardará un `README.md` que indique:

- proyecto y versión de origen;
- URL del fichero original;
- checksum SHA-256 de la instantánea;
- procedimiento explícito para actualizarla;
- licencia y ubicación del aviso de copyright incluido en la propia
  cabecera.

La generación nunca descarga datos. Tanto desarrollo como CI funcionan sin
red y producen la misma salida a partir de la instantánea versionada.

## Alternativas consideradas

### Una cabecera actual y un generador independiente — elegida

`keysymgen` lee la instantánea de `xkbcommon-keysyms.h` y emite las tres tablas
necesarias. Mantiene un solo origen versionado y coincide con la versión
contra la que se ejecuta el oráculo del repositorio.

### `keysymdef.h` y `keysym-utf.h` por separado

Refleja la separación histórica entre nombres y conversión Unicode, pero
obliga a mantener dos procedencias y dos versiones coordinadas.
`keysym-utf.h` tampoco forma parte de la instalación actual de libxkbcommon;
la cabecera pública ya lleva las anotaciones Unicode que necesita el
generador.

### Tablas estáticas sin generador

Reduciría el código inicial, pero eliminaría la reproducibilidad y volvería a
dejar las actualizaciones como ediciones manuales. No satisface el objetivo.

## Arquitectura

La implementación queda aislada en estos componentes:

```text
third_party/libxkbcommon/xkbcommon-keysyms.h
                │
                ▼
cmd/keysymgen/internal/keysymdata
        parse.go ──► modelo ──► render.go
                │
                ▼
keyboard/keysyms.gen.go
        ├── keysymNames
        ├── keysymCanonicalNames
        └── legacyRunes
```

`cmd/keysymgen/main.go` solo conecta rutas fijas desde la raíz del repositorio
y comunica los errores. El paquete interno contiene funciones puras y
testables con entradas pequeñas, por ejemplo un `io.Reader` para parsear y
una función que devuelve el Go generado como bytes.

El comando se ejecuta explícitamente:

```sh
go run ./cmd/keysymgen
```

No se añade una descarga implícita ni se conecta el generador al build normal.

## Parseo y modelo

El parser solo acepta definiciones `#define XKB_KEY_<nombre> 0x<valor>` y las
formas de anotación Unicode documentadas por la cabecera:

- `/* U+XXXX ... */`: correspondencia directa;
- `/*<U+XXXX ...>*/`: correspondencia de una variante más específica, como
  una tecla del teclado numérico;
- `/*(U+XXXX ...)*/`: correspondencia legacy o semánticamente ambigua.

Las definiciones sin anotación Unicode siguen entrando en las tablas de
nombres, pero no en `legacyRunes`. Las entradas cuyo valor ya se convierte por
el algoritmo Unicode directo o por Latin-1 no se duplican en `legacyRunes`.
Los controles y variantes como `KP_Enter` sí se generan cuando la cabecera los
anota.

Todos los nombres, incluidos aliases obsoletos, se aceptan al parsear un
keymap. Para la dirección inversa se elige el nombre canónico según el orden y
las marcas de deprecación de la cabecera: el primer nombre no obsoleto; si
todos son obsoletos, el primero. El parser falla ante:

- valores hexadecimales inválidos o fuera de `uint32`;
- una anotación Unicode reconocida pero mal formada;
- dos correspondencias Unicode distintas para el mismo keysym;
- nombres duplicados con valores distintos;
- una entrada vacía o una fuente sin definiciones.

## Salida generada y API

`keyboard/keysyms.gen.go` empieza por:

```go
// Code generated by keysymgen. DO NOT EDIT.
```

El fichero contiene literales ordenados determinísticamente y pasa por
`go/format` antes de escribirse. Solo se reemplaza el destino después de que
parseo, validación y formato terminen correctamente.

Las semillas manuales `keysymNames` de `xkbmini.go` y `legacyRunes` de
`compose.go` desaparecen. `ParseKeysym` conserva sus caminos algorítmicos para
hexadecimal, ASCII y `Uxxxx`, y consulta la tabla generada para nombres
explícitos. `Keysym.Rune` conserva las conversiones algorítmicas de Unicode y
Latin-1 y consulta la tabla completa para el resto.

Se añade:

```go
func (k Keysym) Name() string
```

Para un keysym explícito devuelve el nombre canónico generado. Para un keysym
Unicode no nombrado devuelve `U` seguido de un mínimo de cuatro dígitos
hexadecimales mayúsculos; para cualquier otro valor desconocido devuelve
`0x` seguido de ocho dígitos hexadecimales minúsculos. `NoSymbol` devuelve
`NoSymbol`.

`example/keylog` mostrará nombre y valor, por ejemplo:

```text
keysym=F1(0xffbe) text=-
```

El guion de `text` es correcto: indica «no imprimible», no «desconocido».

## Pruebas

El trabajo se desarrolla con TDD y cubre cuatro niveles:

1. **Parser:** fixtures mínimos para nombres, aliases, las tres anotaciones
   Unicode, deprecación y todos los errores estructurales.
2. **Render:** salida dorada pequeña, orden determinista, cabecera generada y
   rechazo de una salida que `go/format` no acepte.
3. **Integración:** regenerar desde la instantánea real y comprobar que la
   salida coincide byte a byte con `keyboard/keysyms.gen.go`.
4. **Comportamiento:**
   - `ParseKeysym("F1")` hasta `ParseKeysym("F12")`;
   - `ParseKeysym("ISO_Level3_Latch") == 0xfe04`;
   - `Keysym.Name` para F-keys, Unicode y valores desconocidos;
   - runas legacy representativas como `tslash`, griego y cirílico;
   - un keymap serializado por nombre que resuelva `LevelThree` y `NumLock`
     sin contaminar sus máscaras con `NoSymbol`;
   - `guessType` sobre pares legacy de minúscula/mayúscula.

La aceptación ejecuta:

```sh
go build ./...
go vet ./...
go test -count=1 ./...
go test -tags oracle -count=1 ./keyboard/...
```

Tras generar la tabla, las diferencias `Rune` del oráculo deben ser cero. Las
diferencias `Consumed` causadas por clasificar mal pares legacy también deben
desaparecer. Las diferencias `Sym` restantes se miden y documentan; no se
oculta ningún cambio detrás de la tarea de capitalización pendiente.

## Documentación y trazabilidad

`docs/keyboard.md` se corrige para hablar de `keysymgen`, de la cabecera
vendorizada y de los nuevos resultados medidos. Se elimina cualquier
sugerencia de reutilizar `waygenerator` para las tablas XKB.

La ejecución mediante desarrollo dirigido por subagentes conserva bajo
`.superpowers/sdd/2026-08-22-keysym-generator/` el progreso, briefs, informes y
paquetes de revisión de cada tarea.

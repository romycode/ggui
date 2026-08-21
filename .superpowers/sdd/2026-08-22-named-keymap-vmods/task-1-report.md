# Informe — Task 1: cerrar el fallo ante keysyms de `interpret` sin resolver

## Diagnóstico

El recorrido confirmado es `Compile` → `parseSymbols`/`parseModifierMap` →
`resolveVirtualMods` → `resolveTypeMasks` → `State.Sym`. Un nombre simbólico
de un `interpret` que no está en `keysymNames` hace que `ParseKeysym` devuelva
`0`. `NoSymbol` explícito también vale `0`. Antes del cambio,
`resolveVirtualMods` comparaba ambos valores como si fueran el mismo keysym y
añadía la máscara real de las teclas `NoSymbol` no relacionadas al modificador
virtual. Como los mapas de tipo usan la máscara completa, `ModMod5` y
`ModMod2` aislados dejaban de seleccionar sus niveles esperados.

La regresión usa un `interpret ISO_Level3_Shift` resoluble, los nombres no
resolubles `ISO_Level3_Latch`, `ISO_Level3_Lock` y `Num_Lock`, y dos teclas
con `NoSymbol` mapeadas a modificadores reales. Sólo observa el contrato
público `Compile` → `NewState` → `UpdateMask` → `Sym`.

Hipótesis confirmada: la igualdad espuria de los dos ceros infla las máscaras
de `LevelThree` y `NumLock`; no es un error de análisis ni de compilación.

## RED

Comando:

```text
go test -count=1 ./keyboard -run '^TestNamedVirtualModifierInterpretsIgnoreUnresolvedKeysyms$'
```

Salida antes del cambio de producción:

```text
--- FAIL: TestNamedVirtualModifierInterpretsIgnoreUnresolvedKeysyms (0.00s)
    xkbmini_test.go:197: Sym(LevelThree with ModMod5) = 0x61, want 0x62
    xkbmini_test.go:208: Sym(NumLock with ModMod2) = 0x63, want 0x64
FAIL
FAIL    github.com/romycode/ggui/keyboard    0.002s
FAIL
```

Falló correctamente: ambas teclas devolvieron el nivel base porque las máscaras
virtuales infladas exigían `ModMod5|ModMod2`, en lugar de aceptar sus máscaras
reales individuales.

## GREEN

Cambio mínimo en `resolveVirtualMods`: después de llamar a `ParseKeysym`, se
omite un `interpret` cuyo resultado cero no sea el `NoSymbol` o `VoidSymbol`
deliberado. Así se conserva el tratamiento intencional de los keysyms cero y
los fallbacks de modificadores virtuales existentes.

Comando:

```text
go test -count=1 ./keyboard -run '^TestNamedVirtualModifierInterpretsIgnoreUnresolvedKeysyms$'
```

Salida:

```text
ok      github.com/romycode/ggui/keyboard    0.002s
```

## Verificación completa

| Comando | Resultado |
| --- | --- |
| `go test -count=1 ./keyboard/...` | exit 0; `keyboard` OK. |
| `gofmt -l keyboard/` | sin salida. |
| `go build ./...` | exit 0. |
| `go vet ./...` | exit 0. |
| `go test -count=1 ./...` | exit 0; todos los paquetes con pruebas OK. La repetición final necesitó permiso para operaciones de socket Unix locales de `wayland/wlcore`; el sandbox sin ese permiso devolvía `operation not permitted`. |
| `go vet -tags oracle ./keyboard/...` | exit 0. |

El barrido sin caché `go test -count=1 -tags oracle ./keyboard/...` terminó
con exit 1, como se espera por sus discrepancias preexistentes. Las cuentas
exactas de Sym/Consumed/Rune permanecen sin cambios:

| Layout | Base | Después |
| --- | --- | --- |
| `us` | 0/0/29 | 0/0/29 |
| `es` | 448/320/52 | 448/320/52 |
| `es(cat)` | 384/256/50 | 384/256/50 |
| `us(intl)` | 160/64/32 | 160/64/32 |

## Archivos cambiados

- `keyboard/xkbmini.go`: descarta los `interpret` simbólicos no resueltos en
  la resolución de modificadores virtuales.
- `keyboard/xkbmini_test.go`: añade la regresión de keymap con nombres.
- `.superpowers/sdd/2026-08-22-named-keymap-vmods/task-1-report.md`: este
  informe requerido.

## Auto-revisión

- El guardia es local a la frontera donde aparece el valor ambiguo; no cambia
  el análisis general de keysyms, `Consumed`, la selección de tipos ni las
  tablas generadas.
- `NoSymbol` y `VoidSymbol` siguen pudiendo representar cero de manera
  deliberada; los fallbacks continúan ejecutándose exactamente como antes.
- La prueba reproduce el defecto por comportamiento público y cubre tanto el
  nivel esperado como el modificador cruzado que no debe seleccionar un nivel.
- `git diff --check` no informa errores de espacio; `gofmt` está limpio.

## Preocupaciones

No hay preocupaciones funcionales nuevas. El oráculo conserva sus fallos
conocidos y medidos; esta ruta con nombres no se puede ejercitar mediante la
serialización hexadecimal del oráculo, por lo que la nueva regresión dedicada
es necesaria.

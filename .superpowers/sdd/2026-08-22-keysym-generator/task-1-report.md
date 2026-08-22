# Informe de la tarea 1: datos de keysyms de libxkbcommon

## Resumen de implementación

- Se vendoriza `xkbcommon-keysyms.h` de libxkbcommon 1.13.2 y se verifica su
  SHA-256: `13023369f65a17411606084e3e09557b4886aeb15f89affba4aaa86490a463f3`.
- Se añade la documentación de procedencia y actualización en español.
- Se implementa `keysymdata.Parse`, que escanea con límite de 1 MiB, valida
  estrictamente definiciones `XKB_KEY_`, conserva todos los aliases, elige
  nombres canónicos deterministas, detecta conflictos y deja en `Runes` sólo
  los mapeos Unicode no algorítmicos.
- Se cubren anotaciones `U+`, `<U+` y `(U+`, aliases explícitos e implícitos,
  definiciones malformadas, conflictos y el snapshot fijado de 2.638 nombres.

## Evidencia RED

Antes de crear `parse.go` se ejecutó:

```sh
GOCACHE=/tmp/ggui-keysym-go-cache go test ./cmd/keysymgen/internal/keysymdata -run TestParse -count=1 -v
```

Resultado: fallo esperado por símbolos no definidos: `undefined: Parse` en
`parse_test.go`. El fallo confirma que las pruebas se escribieron antes de la
implementación y que no fue un fallo de ruta ni de checksum.

## Evidencia GREEN y comandos

```sh
pkg-config --modversion xkbcommon
# 1.13.2
sha256sum /usr/include/xkbcommon/xkbcommon-keysyms.h
# 13023369f65a17411606084e3e09557b4886aeb15f89affba4aaa86490a463f3

gofmt -w cmd/keysymgen/internal/keysymdata
GOCACHE=/tmp/ggui-keysym-go-cache go test ./cmd/keysymgen/internal/keysymdata -count=1 -v
# PASS: los tres tests, incluido el snapshot de 2638 nombres

GOCACHE=/tmp/ggui-keysym-go-cache go test ./...
# El sandbox bloqueó los sockets Unix de wayland/wlcore (operation not permitted).

GOCACHE=/tmp/ggui-keysym-go-cache go test ./...
# Repetido con el permiso de sockets necesario: PASS para todos los paquetes.

git show --check --stat --oneline HEAD
# Sin errores de espacio en blanco; commit ff66dcc.
```

## Archivos modificados

- `third_party/libxkbcommon/xkbcommon-keysyms.h`
- `third_party/libxkbcommon/README.md`
- `cmd/keysymgen/internal/keysymdata/parse.go`
- `cmd/keysymgen/internal/keysymdata/parse_test.go`

## Revisión propia

- Se revisó el commit con `git show --check` y su diff completo.
- Los errores de escáner y validación llevan el prefijo `keysymgen:`.
- Los mapas se inicializan antes de escribir, las ramas de error retornan antes
  y `algorithmicRune` implementa exactamente las reglas pedidas.
- No se modificó `cmd/waygenerator` ni código de teclado de producción.

## Preocupaciones

Ninguna funcional. La suite completa requiere permisos para sockets Unix; con
ese permiso, la ejecución completa pasó.

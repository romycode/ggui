# Datos de keysyms de libxkbcommon

`xkbcommon-keysyms.h` procede de libxkbcommon 1.13.2:

`include/xkbcommon/xkbcommon-keysyms.h`

Origen: <https://github.com/xkbcommon/libxkbcommon/tree/xkbcommon-1.13.2>

SHA-256:
`13023369f65a17411606084e3e09557b4886aeb15f89affba4aaa86490a463f3`

La cabecera conserva íntegros sus avisos de copyright y licencia. Se
vendoriza para que `go run ./cmd/keysymgen` sea reproducible y no dependa de
las cabeceras instaladas ni de la red.

Para actualizarla, se elige primero una versión concreta de libxkbcommon, se
sustituye la cabecera por la de esa etiqueta, se actualizan versión y checksum
en este fichero, y se ejecutan el generador y el oráculo XKB. La actualización
de datos y cualquier cambio de comportamiento se revisan en el mismo commit.

# Diseño del campo de texto

**Fecha:** 2026-09-21

## Objetivo

Un widget `widget.TextField` reutilizable: un campo de texto de **una línea**
con cursor que se mueve libremente, clic para colocarlo y desplazamiento
horizontal cuando el texto no cabe. Sustituye al prototipo que vive dentro de
`example/widgets/ui.go`, que solo sabe añadir al final y borrar el último
carácter, con el cursor siempre al final y una conversión `string(u.text)` en
cada fotograma.

El criterio de éxito que ha fijado el proyecto es que sea **performante**, y la
spec lo trata como un requisito medible y no como un deseo: ver "Criterios de
éxito".

## Alcance

**Dentro:**
- Editar en cualquier posición: insertar, Retroceso, Supr, ←, →, Inicio, Fin.
- Clic para colocar el cursor.
- Desplazamiento horizontal que mantiene el cursor visible.
- *Placeholder* con el campo vacío y sin foco.
- Parpadeo del cursor, llevado por la aplicación (el widget no tiene reloj).
- Integración con `Focusable` y `Chain`.
- `example/widgets` migra al widget, como primer consumidor.

**Fuera, y queda para la siguiente iteración** (ver el último apartado):
selección (Mayús+flechas, arrastre, doble clic), portapapeles, forma del cursor
del ratón sobre el campo, edición multilínea, deshacer, saltos por palabra,
límite de longitud y enmascarado de contraseña.

## Decisiones

| Tema | Decisión | Alternativas descartadas |
| --- | --- | --- |
| Alcance | Editor de una línea con cursor libre | Solo el prototipo empaquetado; editor con selección |
| Entrada | `widget.Key` ampliado con las teclas de edición, más `Insert(texto)` para el texto compuesto | Aceptar `keyboard.Event` (rompería que `widget` solo depende de `canvas`); un tipo de evento propio |
| Parpadeo | Lo lleva la aplicación: `SetCaretVisible(bool)`; el widget no tiene reloj | Que el widget reciba `now`; cursor fijo |
| Estructura | Modelo `editor` no exportado, más la vista `TextField` | Un solo tipo; el modelo en el paquete `text` |
| Rendimiento | Requisito medible | — |

## Superficie pública

Sigue las convenciones de `Button`: campos públicos para configurar, `Bounds`
que pone quien lo usa, y cada método devuelve `bool` con "cambió algo visible".

```go
// key.go: seis teclas nuevas, que traduce el llamador desde sus keysyms.
KeyLeft, KeyRight, KeyHome, KeyEnd, KeyBackspace, KeyDelete

// textfield.go
type TextField struct {
    Bounds      canvas.Rect
    Font        Font
    Style       TextFieldStyle // fondo, borde, borde con foco, texto, placeholder, cursor, radio, relleno
    Placeholder string
    OnChange    func(text string) // tras cada edición del usuario
    OnSubmit    func(text string) // al pulsar Intro
    // estado no exportado: el editor, el desplazamiento, el foco, la visibilidad del cursor
}

func NewTextField(font Font) *TextField
func (t *TextField) Text() string
func (t *TextField) SetText(s string) bool        // cursor al final; no dispara OnChange
func (t *TextField) Insert(text string) bool       // texto ya compuesto, en el cursor
func (t *TextField) KeyDown(k Key) bool
func (t *TextField) KeyUp(k Key) bool              // cumple Focusable; sin efecto
func (t *TextField) PointerDown(x, y float32) bool // coloca el cursor donde se hizo clic
func (t *TextField) SetCaretVisible(v bool) bool
func (t *TextField) Focused() bool
func (t *TextField) SetFocused(focused bool) bool  // cumple Focusable
func (t *TextField) Draw(cv *canvas.Canvas)
func DefaultTextFieldStyle() TextFieldStyle
```

- **El campo no se da el foco a sí mismo.** `PointerDown` solo coloca el
  cursor; quién tiene el foco lo decide el llamador con `Chain.Focus`, como manda
  el contrato de `Focusable` (un widget que se enfocara solo podría dejar dos
  enfocados a la vez).
- **`Insert` descarta los caracteres de control** (`\r`, `\t`…), porque
  `keyboard.Event.Text` los devuelve para Intro y Tab.
- **Los callbacks se llaman de forma síncrona**, en la goroutine de UI, con el
  estado del campo ya actualizado, de modo que pueden llamar de vuelta al campo.
- **No es seguro para uso concurrente**, como el resto de los widgets.
- `Button` ignora las teclas que no conoce, así que ampliar `widget.Key` no
  cambia su comportamiento. `Chain` solo intercepta Tab y Mayús+Tab y reenvía el
  resto al widget enfocado, así que las teclas nuevas llegan al campo sin tocar
  `Chain`.

## El modelo `editor`

No exportado, en `widget/editor.go`, sin `canvas` ni `Font`: puro.

**Estado.** `buf []byte` con el texto en UTF-8; `caret int`, el cursor como
desplazamiento en bytes; y una `string` cacheada del texto, **reconstruida al
final de cada edición** y no en el momento de leerla o dibujarla, de modo que ni
`Text()` ni `Draw` asignan nunca.

**Invariantes**, que las pruebas verifican tras cada operación:
1. `buf` es siempre UTF-8 válido.
2. `0 ≤ caret ≤ len(buf)`.
3. `caret` cae siempre en el límite de un carácter, nunca en mitad de uno.

**Operaciones.** `insert(s)`, `backspace()`, `delete()`, `left()`, `right()`,
`home()`, `end()`, `setText(s)` y `setCaret(offset)` (ajusta al límite de carácter
más cercano). Todas devuelven si algo cambió y ninguna deja el modelo en un
estado inválido. Mover y borrar van de carácter Unicode en carácter, con
`utf8.DecodeRune` y `DecodeLastRune`.

**Decisiones.**
- **Se guarda la `string` cacheada** porque hace gratis el dibujo: pintar solo el
  trozo visible es un subslice `texto[a:b]`, sin asignar. Una edición es O(n) en
  el largo del texto (un `memmove` y una asignación de la `string`); un fotograma
  es O(lo visible). Reconstruirla en la propia edición, y no de forma perezosa,
  es lo que garantiza que el dibujo no asigna: una reconstrucción diferida caería
  en el primer `Draw` tras editar.
- **Sin *gap buffer***: obligaría a reunir los dos tramos en una `string` en cada
  fotograma y se perdería el subslice gratuito. Si un benchmark con 100.000
  caracteres lo contradijera, se revisa con datos.
- **`insert` y `setText` sanean la entrada**: los bytes inválidos pasan a U+FFFD
  y los caracteres de control se descartan. El camino habitual, una entrada ya
  válida y sin controles, no asigna: solo se construye una cadena nueva cuando hay
  algo que quitar o sustituir.
- **Límite documentado:** el cursor se mueve por caracteres Unicode, no por
  grupos de grafemas. Un carácter con marca combinante suelta o un emoji compuesto
  se recorre en varios pasos. `keyboard.Composer` produce texto normalizado (NFC),
  así que los acentos escritos por el usuario llegan ya compuestos.

## Geometría, desplazamiento y dibujo

**Geometría.** `Bounds` es el rectángulo exterior. El área de texto es `Bounds`
menos borde y relleno, y es también el recorte que se pasa a `Font.Draw`. El
texto se centra en vertical, como exige el contrato de `Font`.

**Estado de la vista**, recalculado solo cuando cambia el texto, el cursor o el
ancho, nunca por fotograma: `caretX` (posición horizontal del cursor respecto al
origen del texto) y el desplazamiento, guardado como ancla (el primer carácter
visible y su posición).

**Regla de desplazamiento.** Tras cualquier cambio el cursor debe quedar visible:
si cae a la izquierda del área visible, el desplazamiento lo lleva hasta él; si cae
a la derecha, se ajusta para que quede en el borde derecho, dejando sitio para el
ancho del cursor. Con el cursor al final del texto, el desplazamiento se
recalcula del todo (`max(0, caretX − ancho visible)`), de modo que al borrar se
rellena el hueco de la derecha sin medir el texto entero.

**Dibujo por fotograma**, en O(lo visible) y sin asignar:
1. Fondo redondeado y borde (con el color de foco si lo tiene).
2. Con el campo vacío y sin foco, el *placeholder*.
3. En otro caso, solo el trozo visible como subslice de la `string` cacheada, en
   su posición exacta. Se localiza recorriendo hacia fuera desde el cursor, con
   dos caracteres de margen por lado, y el recorte se encarga del resto.
4. El cursor, si el campo tiene el foco y `SetCaretVisible(true)`: un rectángulo
   del ancho del estilo, dentro del área.

**Clic para colocar el cursor.** Se convierte `x` a coordenadas del texto y se
recorren los caracteres visibles acumulando anchos hasta el límite más cercano:
O(lo visible). Un clic a la izquierda o a la derecha del texto lleva al inicio o
al final.

**Coste por evento.** Una edición o un movimiento hace una o dos llamadas a
`Font.Measure` sobre el prefijo hasta el cursor: O(n), y solo cuando algo cambia.
No hay optimización incremental de entrada, porque dependería de que los anchos
se sumen carácter a carácter y la interfaz `Font` no lo promete (con *kerning* no
se cumpliría). Si el benchmark con 100.000 caracteres mostrara que importa, se
añade entonces un punto de control.

## Eventos y contrato de repintado

Cuándo devuelve `true` cada método (lo que decide si la aplicación llama a
`Window.Invalidate`):

| Llamada | Devuelve `true` cuando… |
| --- | --- |
| `Insert(text)` | cambió el texto (tras sanear); dispara `OnChange` |
| `KeyDown` ←, →, Inicio, Fin | el cursor se movió o el área visible se desplazó; en el borde, `false` |
| `KeyDown` Retroceso, Supr | se borró algo (dispara `OnChange`); en el borde, `false` |
| `KeyDown` Intro | siempre `false` (no cambia nada visible); dispara `OnSubmit` |
| `PointerDown` | el cursor se movió |
| `SetFocused` | cambió el foco; al ganarlo, el cursor empieza visible |
| `SetCaretVisible` | cambió **y** el campo está enfocado; si no, el cursor no se dibuja y no hay que repintar |

Reglas comunes:
- Un campo **sin foco ignora** `Insert` y las teclas, según el contrato de
  `Focusable`; así la aplicación puede llamar a `Insert` sin comprobar quién tiene
  el foco.
- **Ignora Espacio, Escape y Tab**: los espacios llegan como texto por `Insert`,
  y Tab lo gestiona `Chain`.
- **`SetText` no dispara `OnChange`**: es un cambio hecho por el programa, y evita
  bucles con quien lo escucha. Las ediciones del usuario sí lo disparan.
- **Editar, mover el cursor o hacer clic deja el cursor visible**, que es el
  reinicio del parpadeo. La aplicación puede reiniciar además su propio
  temporizador, para que el cursor no se apague justo después de la pulsación.

## Integración

- **`Chain`:** sin cambios; `NewChain(field, button)` funciona tal cual.
- **`window`:** sin cambios. La aplicación traduce los keysyms a las seis teclas
  nuevas más Intro, pasa `ev.Text` a `Insert`, y llama a `Window.Invalidate` solo
  cuando un método devuelve `true`. El parpadeo sigue en un temporizador de la
  aplicación con `Window.Do`, que llama a `SetCaretVisible`.
- **Un campo quieto no repinta.** Escribir repinta el fotograma entero por
  pulsación, porque hoy la capa de ventana presenta el buffer completo; llevar el
  daño exacto es una mejora posterior, ajena a esta feature.
- **`example/widgets`** sustituye el prototipo (`ui.text`, `insert`, `backspace`,
  `drawInput` y el cursor a mano) por un `TextField`. Es la prueba real de que la
  API se usa bien y hace más pequeño el ejemplo. Sus tests de comportamiento se
  reescriben sobre el widget.

## Casos límite

Con la misma política que `Button`:
- **Tamaños raros:** si `Bounds` es cero, negativo o no finito, o si el ancho no
  llega ni al relleno, se dibuja lo que se puede (fondo y borde) o nada, y
  `PointerDown` devuelve `false`. Nunca se emite un rectángulo inválido que
  envenene el canvas (los errores del canvas son pegajosos).
- **Sin `Font`:** el campo se dibuja y se edita, pero no mide ni pinta texto; sin
  anchos, el cursor queda en el origen.
- **Anchos absurdos:** si `Font.Measure` devuelve un valor negativo o no finito, se
  toma como 0.
- **Texto no válido:** `SetText` e `Insert` sanean la entrada; el modelo nunca
  contiene UTF-8 inválido.
- **Cambio de ancho entre fotogramas** (redimensionar la ventana): el
  desplazamiento se recalcula al detectar que el ancho cacheado ya no coincide.

## Pruebas y benchmarks

1. **`editor` puro:** tablas con caracteres de 1 a 4 bytes, entrada inválida,
   caracteres de control, ajuste de `setCaret` y bordes; un test de propiedades
   (fuzz nativo de Go) que aplica secuencias aleatorias de operaciones y comprueba
   los tres invariantes; `AllocsPerRun`: leer el texto cacheado no asigna, una
   edición asigna como mucho una vez.
2. **`TextField` con una fuente falsa de ancho fijo**, para que sea determinista y
   no dependa del paquete `text` (que `widget` no importa): reglas de
   desplazamiento, clic, *placeholder*, foco, la tabla de valores devueltos,
   teclas ignoradas sin foco o para Espacio, Escape y Tab, semántica de
   `OnChange`, `OnSubmit` y `SetText`, y reentrada.
3. **Dibujo**, con el patrón de `Button`: con y sin foco, cursor visible y oculto,
   *placeholder*; que nunca pinte fuera de la región visible ni en el relleno de
   fila; tamaños raros; y `Draw` sin asignaciones.
4. **Integración:** los tests de `example/widgets` reescritos sobre el widget, y el
   de fuente TrueType real que ya existe allí, porque es el ejemplo y no `widget`
   quien puede importar `text`.
5. **Benchmarks** (con `-benchmem`; sus resultados van a `docs/widget.md`): dibujar
   el mismo campo con 1.000 y con 100.000 caracteres; insertar al principio, en
   medio y al final de 100.000 caracteres; mover el cursor; y `PointerDown`.

## Criterios de éxito (rendimiento)

- `Draw`, `Text`, `PointerDown` y todos los movimientos del cursor: **0
  asignaciones**, comprobado en `go test` con `testing.AllocsPerRun`.
- Una edición: **como mucho 1 asignación** (la `string` cacheada). No cuenta el
  crecimiento amortizado de `buf`, que reserva capacidad por duplicación; los
  tests miden con la capacidad ya reservada.
- El coste por fotograma **no crece con el largo del texto**, demostrado por el
  benchmark de 1.000 frente a 100.000 caracteres.
- Un campo **quieto no repinta**.

## Riesgos

- **`Font.Measure` sobre un prefijo largo.** Una edición mide el texto hasta el
  cursor, O(n). Con `text.Face` y 100.000 caracteres puede notarse; el benchmark
  lo decide, y la salida prevista es un punto de control, no una estructura nueva.
- **Grafemas.** El cursor se mueve por caracteres Unicode; con marcas combinantes
  sueltas o emojis compuestos hay que dar varios pasos. Documentado como límite.
- **Repintado del fotograma completo por pulsación**, mientras la capa de ventana
  no lleve daño exacto. Con una interfaz pequeña es despreciable, y no es de este
  widget.

## Siguiente iteración

Ya acordada: **selección** y **forma del cursor del ratón**. El diseño no cierra
ninguna de las dos puertas:

- **Selección.** El editor solo guarda hoy el cursor; añadir un segundo extremo
  (el ancla de la selección) es aditivo. Las teclas con Mayús se resolverían como
  ya se resuelve Mayús+Tab: el llamador traduce Mayús+← a una tecla propia de
  `widget.Key`, porque `widget` no tiene estado de modificadores. El arrastre y el
  doble clic ya los entrega `pointer` (`DragStart`, `DragMove`, `DoubleClick`).
  El portapapeles queda fuera hasta que el proyecto integre `wl_data_device`.
- **Forma del cursor del ratón.** Necesita integrar `cursor-shape` en `pointer`
  (hoy solo hay el binding) y que el campo informe de si el puntero está encima
  (`PointerMove`, que ahora no se necesita). Ambas cosas son aditivas.

## Documentación

`docs/widget.md` gana la sección de `TextField`, con sus límites, lo que queda
para la siguiente iteración y las cifras medidas; `docs/estado.md` actualiza la
fila de `widget`.

# Diseño del campo de texto

**Fecha:** 2026-09-21

## Objetivo

Un widget `widget.TextField` reutilizable: un campo de texto de **una línea**
con cursor que se mueve libremente, clic para colocarlo y desplazamiento
horizontal cuando el texto no cabe. Sustituye al prototipo que vive dentro de
`example/widgets/ui.go`, que solo sabe añadir al final y borrar el último
carácter, con el cursor siempre al final, y que en cada fotograma convierte el
texto con `string(u.text)` **y lo mide entero** para colocar el cursor.

El criterio de éxito que ha fijado el proyecto es que sea **performante**, y la
spec lo trata como un requisito medible y comprobable en `go test`, no como un
deseo: ver "Criterios de éxito".

## Alcance

**Dentro:**
- Editar en cualquier posición: insertar, Retroceso, Supr, ←, →, Inicio, Fin.
- Clic para colocar el cursor.
- Desplazamiento horizontal que mantiene el cursor visible.
- *Placeholder* con el campo vacío y sin foco.
- Parpadeo del cursor, llevado por la aplicación (el widget no tiene reloj).
- Estado deshabilitado, como `Button`.
- Integración con `Focusable` y `Chain`.
- `example/widgets` migra al widget, como primer consumidor.

**Fuera, y queda para la siguiente iteración** (ver el último apartado):
selección (Mayús+flechas, arrastre, doble clic), portapapeles, forma del cursor
del ratón sobre el campo, edición multilínea, deshacer, saltos por palabra,
límite de longitud, solo lectura y enmascarado de contraseña.

## Decisiones

| Tema | Decisión | Alternativas descartadas |
| --- | --- | --- |
| Alcance | Editor de una línea con cursor libre | Solo el prototipo empaquetado; editor con selección |
| Entrada | `widget.Key` ampliado con las teclas de edición, más `Insert(texto)` para el texto compuesto | Aceptar `keyboard.Event` (rompería que `widget` solo depende de `canvas`); un tipo de evento propio |
| Parpadeo | Lo lleva la aplicación: `SetCaretVisible(bool)`; el widget no tiene reloj | Que el widget reciba `now`; cursor fijo |
| Estructura | Modelo `editor` no exportado, más la vista `TextField` | Un solo tipo; el modelo en el paquete `text` |
| Medición | **Siempre relativa al ancla** (el primer carácter visible), con `Measure` sobre subcadenas y búsqueda de límites | Medir el prefijo desde el byte 0; sumar anchos carácter a carácter |
| `Bounds` inválido | Como `Button`: error pegajoso del canvas, sin taparlo | Que el campo lo corrija en silencio |
| Clic más allá del borde | Lleva al final del **tramo visible**, no del buffer | Al final del buffer |
| Rendimiento | Requisito medible | — |

## Por qué la medición es relativa al ancla

Se midió contra el `text.Face` real (fuente sans del sistema, tamaño 16), y esos
datos gobiernan el diseño:

- `Font.Measure` cuesta unos **218 ns por carácter** (232 µs con 1.000; 22,7 ms con
  100.000), sin caché de avances. Medir el prefijo hasta el cursor en cada edición,
  como propuso el primer borrador, serían 45 ms por pulsación con 100.000
  caracteres. Por eso ninguna operación mide nunca desde el byte 0.
- Los avances **no se suman**: `Measure("r.")` difiere de `Measure("r") +
  Measure(".")` hasta 2,5 unidades, y sobre 320 caracteres de "AVAWATAY" la suma
  carácter a carácter se pasa un 8,5 %. Por eso ninguna operación acumula anchos:
  siempre se mide la subcadena entera.
- Dibujar `texto[a:]` con un recorte estrecho cuesta 25 µs planos entre 50 y
  100.000 caracteres, con 0 asignaciones, mientras que dibujar la cadena entera
  desplazada cuesta 22,5 ms. Por eso se dibuja siempre un subslice que empieza en
  el ancla.

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
    Style       TextFieldStyle // fondo, borde, borde con foco, texto, placeholder, cursor, deshabilitado, radio, relleno
    Placeholder string
    OnChange    func(text string) // tras cada edición del usuario
    OnSubmit    func(text string) // al pulsar Intro
    Disabled    bool              // como Button: rechaza el foco e ignora la entrada
    // estado no exportado: el editor, la vista (ancla y medidas cacheadas), el foco, la visibilidad del cursor
}

func NewTextField(placeholder string, font Font) *TextField
func (t *TextField) Text() string
func (t *TextField) SetText(s string) bool         // cursor al final; no dispara OnChange
func (t *TextField) Caret() int                     // desplazamiento en bytes, en un límite de carácter
func (t *TextField) SetCaret(offset int) bool       // ajusta al límite de carácter más cercano
func (t *TextField) Insert(text string) bool        // texto ya compuesto, en el cursor
func (t *TextField) KeyDown(k Key) bool
func (t *TextField) KeyUp(k Key) bool               // cumple Focusable; sin efecto
func (t *TextField) PointerDown(x, y float32) bool  // coloca el cursor donde se hizo clic
func (t *TextField) SetCaretVisible(v bool) bool
func (t *TextField) Focused() bool
func (t *TextField) SetFocused(focused bool) bool   // cumple Focusable
func (t *TextField) Draw(cv *canvas.Canvas)

type TextFieldStyle struct { /* colores, radio, borde, relleno, ancho del cursor */ }
func DefaultTextFieldStyle() TextFieldStyle
```

- **El campo no se da el foco a sí mismo.** `PointerDown` solo coloca el cursor;
  quién tiene el foco lo decide el llamador con `Chain.Focus`, como manda el
  contrato de `Focusable` (un widget que se enfocara solo podría dejar dos
  enfocados a la vez).
- **`Insert` sanea la entrada:** descarta los caracteres de control y sustituye
  los bytes UTF-8 inválidos por U+FFFD. `keyboard.Event.Text` ya no trae
  caracteres de control (`Keyboard` descarta la cadena entera si los tiene), pero
  `Insert` es API pública, quien la llame puede alimentarla desde
  `keyboard.Composer` (que sí devuelve `\r` para Intro) o con datos del programa, y
  `SetText` recibe datos arbitrarios. El modelo no puede depender de que quien
  llama sea cuidadoso.
- **Los callbacks se llaman de forma síncrona**, en la goroutine de UI, con el
  estado del campo ya actualizado, de modo que pueden llamar de vuelta al campo.
- **No es seguro para uso concurrente**, como el resto de los widgets.
- **`Disabled` copia la semántica de `Button`**: rechaza el foco (así `Chain` lo
  salta), ignora teclas, texto y clics, y se dibuja con los colores de
  deshabilitado.
- `Button` ignora las teclas que no conoce, así que ampliar `widget.Key` no
  cambia su comportamiento. `Chain` solo intercepta Tab y Mayús+Tab y reenvía el
  resto al widget enfocado, así que las teclas nuevas llegan al campo sin tocar
  `Chain`.

## El modelo `editor`

No exportado, en `widget/editor.go`, sin `canvas` ni `Font`: puro.

**Estado.** `buf []byte` con el texto en UTF-8; `caret int`, el cursor como
desplazamiento en bytes; una `string` cacheada del texto, **reconstruida al final
de cada edición** y no en el momento de leerla o dibujarla, de modo que ni
`Text()` ni `Draw` asignan nunca; y un contador `version` que sube con cada
edición.

**Invariantes**, que las pruebas verifican tras cada operación:
1. `buf` es siempre UTF-8 válido.
2. `0 ≤ caret ≤ len(buf)`.
3. `caret` cae siempre en el límite de un carácter, nunca en mitad de uno.

**Operaciones.** `insert(s)`, `backspace()`, `delete()`, `left()`, `right()`,
`home()`, `end()`, `setText(s)` y `setCaret(offset)`. Todas devuelven si algo
cambió y ninguna deja el modelo en un estado inválido. Mover y borrar van de
carácter Unicode en carácter, con `utf8.DecodeRune` y `DecodeLastRune`.

**Decisiones.**
- **Se guarda la `string` cacheada** porque hace gratis el dibujo: pintar solo el
  trozo visible es un subslice `texto[a:b]`, sin asignar. Una edición es O(n) en
  el largo del texto (un `memmove` y una asignación de la `string`); un fotograma
  es O(lo visible). Reconstruirla en la propia edición, y no de forma perezosa,
  es lo que garantiza que el dibujo no asigna: una reconstrucción diferida caería
  en el primer `Draw` tras editar.
- **Sin *gap buffer***: obligaría a reunir los dos tramos en una `string` en cada
  fotograma y se perdería el subslice gratuito.
- **El saneado no asigna en el camino habitual**: una entrada ya válida y sin
  controles no construye ninguna cadena nueva.
- **Límite documentado:** el cursor se mueve por caracteres Unicode, no por
  grupos de grafemas. Un carácter con marca combinante suelta o un emoji compuesto
  se recorre en varios pasos. `keyboard.Composer` produce texto normalizado (NFC),
  así que los acentos escritos por el usuario llegan ya compuestos.

## Geometría, desplazamiento y dibujo

**Geometría.** `Bounds` es el rectángulo exterior. El área de texto (`innerW` de
ancho) es `Bounds` menos borde y relleno, y es también el recorte que se pasa a
`Font.Draw`; el texto se centra en vertical, como exige el contrato de `Font`. El
ancho útil para el texto es `viewW = innerW − ancho del cursor`, para que el
cursor quepa siempre dentro del área. Un `Bounds` válido nunca produce un
rectángulo interior inválido: `innerW` se acota a 0 y, si no queda sitio, solo se
dibujan fondo y borde.

**Estado de la vista: una sola representación, todo relativo al ancla.**
- `anchor`: desplazamiento en bytes del primer carácter visible, siempre en un
  límite de carácter. El texto visible se dibuja **desde el ancla, en el origen
  fijo del área de texto**: no hay desplazamiento fraccionario, se desplaza de
  carácter en carácter.
- `caretOff = Measure(texto[anchor:caret])`, la posición del cursor respecto al
  ancla.
- `visEnd`: el primer límite de carácter que queda fuera del área de texto, de
  modo que se dibuja `texto[anchor:visEnd]`, incluido el carácter que asoma por el
  borde derecho.
- No existe ninguna posición absoluta: nada se mide nunca desde el byte 0.

**Dos primitivas**, ambas por duplicación del número de caracteres y búsqueda
binaria, con `Measure` siempre sobre subcadenas y coste O(lo visible):
- `fitBack(i, presupuesto)`: el límite más a la izquierda `b ≤ i` con
  `Measure(texto[b:i]) ≤ presupuesto`.
- `fitFwd(i, presupuesto)`: el límite más a la derecha `e ≥ i` con
  `Measure(texto[i:e]) ≤ presupuesto`.

**Reglas de la vista**, que se aplican tras cualquier cambio de texto, de cursor o
de ancho, y en este orden:
1. Si `caret < anchor`: `anchor = caret`.
2. Si el cursor queda a la derecha: `anchor = max(anchor, fitBack(caret, viewW))`.
3. Sin hueco a la derecha: `anchor = min(anchor, fitBack(len(texto), viewW))`. Si
   el texto desde el ancla cabe entero, esto tira del ancla hacia la izquierda
   hasta llenar el área; si no cabe, no cambia nada. Cubre borrar en medio de un
   campo desplazado, que sin esta regla dejaría texto fuera por la izquierda y
   espacio en blanco a la derecha, y también un texto más corto que el área
   (`anchor = 0`).
4. Se recalculan `caretOff` y `visEnd`.

La regla 3 no rompe la 2: el nuevo ancla cumple `Measure(texto[ancla:len]) ≤
viewW`, y como `caret ≤ len`, el cursor sigue dentro del área.

**Coste.** Cada regla acota su trabajo a unas pocas veces el número de caracteres
visibles, así que **una edición, un movimiento, un clic o un cambio de ancho
cuestan O(lo visible)** (más el `memmove` y la asignación de la `string` en las
ediciones). No hay ningún camino que mida el texto entero. Inicio, Fin, un clic
lejos o `SetText` de un texto largo también caen dentro de esto, porque la
búsqueda parte siempre del cursor o del final, no del ancla antigua.

**Dibujo por fotograma**, sin medir nada y sin asignar:
1. Fondo redondeado y borde (con el color de foco si lo tiene, y el de
   deshabilitado si lo está).
2. Con el campo vacío y sin foco, el *placeholder*.
3. En otro caso, `texto[anchor:visEnd]` desde el origen del área, con el recorte
   del área. El fin se calcula explícitamente y no depende de que `Font.Draw`
   corte al salirse del recorte, cosa que `text.Face` hace pero el contrato de
   `Font` no promete. Que el tramo empiece en el ancla hace que el primer glifo
   pierda el par de *kerning* con el carácter anterior (que ya no se ve); es
   uniforme, inocuo y coherente con `caretOff`, que se mide desde el mismo origen.
4. El cursor, si el campo tiene el foco, no está deshabilitado y
   `SetCaretVisible(true)`: un rectángulo del ancho del estilo en `caretOff`,
   siempre dentro del área.

**Clic para colocar el cursor.** `PointerDown` primero comprueba que el punto está
dentro de `Bounds`, como `Button` (si no, `false`: pulsar el botón vecino no mueve
este cursor). Funciona con o sin foco, porque la aplicación enfoca y hace clic en
la misma pulsación. Después convierte `x` a coordenadas del texto desde el ancla
(`rel = x − origen`) y busca por bisección el límite de carácter `j` entre `anchor`
y `fitFwd(anchor, viewW)` que minimiza `|Measure(texto[anchor:j]) − rel|`,
siempre con `Measure` sobre la subcadena, nunca acumulando. Un clic a la izquierda
del área lleva al ancla y uno a la derecha del último carácter completamente
visible lleva a ese límite, es decir, al **final del tramo visible**, no del
buffer: así el clic no teletransporta el cursor lejos de donde el usuario pulsó ni
provoca un desplazamiento.

**Cambio de `Bounds` entre fotogramas.** `Bounds` es un campo público que el
llamador reescribe en cada fotograma (la convención de `Button`), así que el campo
compara el ancho útil contra el cacheado al entrar en `Draw`, `PointerDown`,
`KeyDown` e `Insert`, y si cambió aplica las reglas de la vista. Como todas son
O(lo visible), el fotograma en que cambia el ancho no es más caro por ello.

## Eventos y contrato de repintado

**Un solo predicado.** Igual que `Button` compara `visual()` antes y después, el
campo captura una instantánea de todo lo que se ve, `visual = {version, caret,
anchor, ancho útil, foco, cursor visible, deshabilitado}`, y **cada método devuelve
`antes != después`**. Así el `bool` no puede desviarse de lo que se dibuja, aunque
el estado visual tenga más piezas que el de `Button` (anillo de foco,
*placeholder*, cursor, ancla), y la iteración siguiente añade estado (hover,
selección) sin reescribir ninguna regla por método.

Ejemplos que salen de ahí, no reglas aparte:

| Llamada | Devuelve `true` cuando… |
| --- | --- |
| `Insert(text)` | cambió el texto (tras sanear); dispara `OnChange` |
| `KeyDown` ←, →, Inicio, Fin | se movió el cursor o el ancla; en el borde, `false` |
| `KeyDown` Retroceso, Supr | se borró algo (dispara `OnChange`); en el borde, `false` |
| `KeyDown` Intro | siempre `false`; dispara `OnSubmit` |
| `PointerDown` | se movió el cursor **o** el ancla |
| `SetFocused` | cambió el foco; al ganarlo, el cursor empieza visible |
| `SetCaretVisible` | cambió **y** el campo está enfocado |
| `SetText`, `SetCaret` | cambió algo visible |

Reglas comunes:
- Un campo **sin foco, o deshabilitado, ignora** `Insert` y las teclas, según el
  contrato de `Focusable`; así la aplicación puede llamar a `Insert` sin comprobar
  quién tiene el foco.
- **Ignora Espacio, Escape y Tab**: los espacios llegan como texto por `Insert`, y
  Tab lo gestiona `Chain`. Como nada se queda "armado", la repetición de teclas es
  inocua.
- **`SetText` no dispara `OnChange`**: es un cambio hecho por el programa, y evita
  bucles con quien lo escucha. Las ediciones del usuario sí lo disparan. `SetText`
  con el mismo texto y el cursor ya al final devuelve `false`.
- **Editar, mover el cursor o hacer clic deja el cursor visible**, que es el
  reinicio del parpadeo. La aplicación puede reiniciar además su propio
  temporizador, para que el cursor no se apague justo después de la pulsación.
- **`SetCaretVisible` en un campo sin foco** guarda el valor y devuelve `false`;
  `SetFocused(true)` siempre lo deja visible, así que el valor guardado no
  importa. Un segundo `SetFocused(true)` sobre un campo ya enfocado no cambia nada.
- **Intro devuelve `false`** aunque `OnSubmit` pueda cambiar la pantalla: quien lo
  escucha repinta por su cuenta, como con `Button.OnClick`.

## Integración

- **`Chain`:** sin cambios. Pero el cableado de un clic no es obvio y es lo que un
  llamador real suele equivocar, porque `Focusable` no expone `Bounds`. Son tres
  llamadas, en este orden, en el código de la aplicación:

  ```go
  // al pulsar el botón izquierdo en (x, y)
  if contains(field.Bounds, x, y) {
      changed := chain.Focus(field)      // primero el foco: solo la aplicación lo decide
      changed = field.PointerDown(x, y) || changed
      if changed { win.Invalidate() }
  }
  ```

  y, para el teclado, `chain.KeyDown(k)` para las teclas de edición (que `Chain`
  reenvía al campo enfocado) y `field.Insert(ev.Text)` para el texto. La sección
  correspondiente de `docs/widget.md` lo documenta, como ya hace con `Button`.
- **`window`:** sin cambios. La aplicación traduce los keysyms a las seis teclas
  nuevas más Intro y llama a `Window.Invalidate` solo cuando un método devuelve
  `true`. El parpadeo sigue en un temporizador de la aplicación con `Window.Do`,
  que llama a `SetCaretVisible`.
- **Un campo quieto y sin foco no pide repintados.** Con el foco, el parpadeo
  repinta unas dos veces por segundo, que es cosa del temporizador de la
  aplicación. Escribir repinta el fotograma entero por pulsación, porque hoy la
  capa de ventana presenta el buffer completo; llevar el daño exacto es una mejora
  posterior, ajena a esta feature.
- **`example/widgets`** sustituye el prototipo (`ui.text`, `insert`, `backspace`,
  `drawInput` y el cursor a mano) por un `TextField`. Es la prueba real de que la
  API se usa bien y hace más pequeño el ejemplo. Sus tests de comportamiento se
  reescriben sobre el widget.

## Casos límite

- **`Bounds` inválido** (negativo o no finito): se entrega al canvas tal cual, que
  lo registra como error pegajoso, exactamente como hace `Button` y como fija
  `docs/widget.md` ("no algo que el widget intente arreglar"). Lo que el campo sí
  garantiza es lo contrario: un `Bounds` **válido** no produce nunca un rectángulo
  interior inválido, por pequeño que sea; si no queda sitio tras borde y relleno,
  se dibujan fondo y borde y nada más. La aritmética de la vista no puede
  colgarse ni entrar en pánico con ningún valor (`innerW` no positivo, `NaN`): se
  acota a 0 y `fitBack` y `fitFwd` devuelven su punto de partida.
- **Sin `Font`:** el campo se dibuja y se edita, pero no mide ni pinta texto; sin
  anchos, el ancla se queda en 0 y el cursor en el origen.
- **Anchos absurdos:** si `Font.Measure` devuelve un valor negativo o no finito, se
  toma como 0.
- **Texto no válido:** `SetText` e `Insert` sanean la entrada; el modelo nunca
  contiene UTF-8 inválido.
- **Texto vacío, cursor al principio y al final, Inicio y Fin con desplazamiento,
  insertar con el campo desplazado, `SetText` con el cursor y el ancla:** cubiertos
  por las reglas de la vista, que son las mismas para todos; los tests los
  recorren explícitamente.

## Pruebas y benchmarks

1. **`editor` puro:** tablas con caracteres de 1 a 4 bytes, entrada inválida,
   caracteres de control, ajuste de `setCaret` y bordes; un test de propiedades
   (fuzz nativo de Go) que aplica secuencias aleatorias de operaciones y comprueba
   los tres invariantes; `AllocsPerRun`: leer el texto cacheado no asigna, una
   edición asigna como mucho una vez.
2. **`TextField` con dos fuentes falsas**, deterministas y sin depender del
   paquete `text` (que `widget` no importa):
   - una de ancho fijo que mide **por caracteres y no por bytes** (la de
     `button_test.go` mide por bytes, con lo que "á" mediría el doble y esa
     geometría quedaría congelada en las expectativas);
   - una con *kerning* que **no suma** (por ejemplo −1 unidad en el par "AV"), que
     es lo que destapa cualquier acumulación de anchos. Con ella se comprueba la
     propiedad central: para cada límite de carácter `j`, `caretOff` y el clic en
     esa posición coinciden (`clic(posición(j)) == j`).
   Ambas **cuentan los caracteres que ven `Measure` y `Draw`**.
3. **El criterio de rendimiento, como aserción y no como benchmark:** con esos
   contadores se comprueba que, en un texto de 1.000, 10.000 y 100.000
   caracteres, una edición al principio, en medio y al final, un movimiento, un
   clic y un fotograma ven un número de caracteres **acotado por el tramo visible
   e independiente del largo del texto**. Los tiempos no se afirman nunca en un
   test; lo que se afirma es el trabajo, que no depende de la máquina.
4. **Reglas de la vista:** escribir al final de una línea larga, Retroceso al
   final, borrar en medio de un campo desplazado (el caso del hueco), mover el
   cursor más allá del borde izquierdo, un clic cerca del borde derecho, reducir
   el ancho del campo, un texto más corto que el área, Inicio y Fin; y la
   semántica de los valores devueltos con el predicado único.
5. **Dibujo**, con el patrón de `Button`: con y sin foco, deshabilitado, cursor
   visible y oculto, *placeholder*; que nunca pinte fuera de la región visible ni
   en el relleno de fila; tamaños muy pequeños; y `Draw` sin asignaciones.
6. **Integración:** los tests de `example/widgets` reescritos sobre el widget, y el
   de fuente TrueType real que ya existe allí, porque es el ejemplo y no `widget`
   quien puede importar `text`.
7. **Benchmarks** (informativos, con `-benchmem`): dibujar el mismo campo con
   1.000 y con 100.000 caracteres, y editar y mover en cada posición. `docs/widget.md`
   recoge la **forma** de los resultados (plano respecto al largo del texto) y no
   cifras absolutas, que envejecen con la máquina.

## Criterios de éxito (rendimiento)

- `Draw`, `Text`, `PointerDown` y todos los movimientos del cursor: **0
  asignaciones**, comprobado en `go test` con `testing.AllocsPerRun`.
- Una edición: **como mucho 1 asignación** (la `string` cacheada). No cuenta el
  crecimiento amortizado de `buf`, que reserva capacidad por duplicación; los
  tests miden con la capacidad ya reservada.
- **El trabajo por fotograma y por evento no crece con el largo del texto**,
  demostrado por la aserción sobre el número de caracteres medidos y dibujados (el
  punto 3 de las pruebas), y no por un benchmark.
- Un campo **sin foco y quieto no pide repintados**, y `SetCaretVisible` sobre él
  devuelve `false`.

## Riesgos

- **`Font.Measure` sobre subcadenas y el `Font` de otros proyectos.** El diseño
  supone solo que `Measure` de una subcadena es monótona en su longitud y que un
  fotograma no mide nada; no supone aditividad. Una `Font` con `Measure` no
  monótona (algo raro con *kerning* negativo extremo) haría la búsqueda binaria
  menos precisa, pero no la puede colgar: las búsquedas acaban siempre por el
  número finito de caracteres.
- **Grafemas.** El cursor se mueve por caracteres Unicode; con marcas combinantes
  sueltas o emojis compuestos hay que dar varios pasos. Documentado como límite.
- **Repintado del fotograma completo por pulsación**, mientras la capa de ventana
  no lleve daño exacto. Con una interfaz pequeña es despreciable, y no es de este
  widget.
- **Desplazamiento por caracteres, no por píxeles.** En textos muy largos, el
  primer carácter visible puede quedar recortado por un pixel al cambiar de ancla;
  no es un problema con una fuente proporcional de tamaño normal, pero es una
  decisión consciente y no un descuido.

## Siguiente iteración

Ya acordada: **selección** y **forma del cursor del ratón**. El diseño no cierra
ninguna de las dos puertas:

- **Selección.** El editor solo guarda hoy el cursor; añadir un segundo extremo
  (el ancla de la selección) es aditivo. Las teclas con Mayús se resolverían como
  ya se resuelve Mayús+Tab: el llamador traduce Mayús+← a una tecla propia de
  `widget.Key`, porque `widget` no tiene estado de modificadores. Esa codificación
  crece por combinaciones (cuatro constantes para la selección, ocho más para los
  saltos por palabra), y `KeyBacktab` es el precedente; la nomenclatura prevista es
  `KeySelectLeft`, `KeySelectRight`, `KeySelectHome`, `KeySelectEnd` y
  `KeyWordLeft`, `KeyWordRight` con sus variantes `Select`. El arrastre y el doble
  clic ya los entrega `pointer` (`DragStart`, `DragMove`, `DoubleClick`). El
  predicado único de repintado es lo que evita que esto obligue a reescribir las
  reglas por método. El portapapeles queda fuera hasta que el proyecto integre
  `wl_data_device`.
- **Forma del cursor del ratón.** Necesita integrar `cursor-shape` en `pointer`
  (hoy solo hay el binding) y que el campo informe de si el puntero está encima
  (`PointerMove`, que ahora no se necesita). Ambas cosas son aditivas, y "el
  aspecto cambió" ya es el contrato de repintado.

## Documentación

`docs/widget.md` gana la sección de `TextField`, con el cableado de un clic, sus
límites, lo que queda para la siguiente iteración y la forma de los resultados
medidos. Cada símbolo exportado nuevo lleva su comentario, y `go run
./cmd/docaudit` no debe retroceder. `docs/estado.md` actualiza la fila de `widget`.

## Revisión

Esta versión incorpora la revisión de la primera con un modelo independiente, que
midió contra el `text.Face` real y encontró dos defectos críticos (el coste del
prefijo y la acumulación de anchos, ambos descritos arriba) y once importantes,
entre ellos huecos en las reglas de desplazamiento, un predicado de repintado por
método que se habría desviado del dibujo, y la falsedad de una afirmación sobre
qué devuelve `keyboard.Event.Text`. El plan de implementación parte de esta
versión.

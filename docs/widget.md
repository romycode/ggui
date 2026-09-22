# `widget` — controles reutilizables

> **Documento vivo.** Refleja el estado actual del código y se actualiza con
> él. El diseño original, congelado y con fecha, está en `docs/archive/`.

Documento de referencia de `widget/`. Acompaña a `canvas.md`, sobre cuyo
rasterizador dibuja todo, y a `estado.md`, que dice qué falta.

## Estado

Construido: **`Button`** y **`TextField`** —el segundo enfocable, que es lo
que le da algo que recorrer a `Chain`—, más las piezas que comparte
cualquier control enfocable: `Focusable`, `Key` y `Chain`, que recorre el
orden de tabulación. El resto de controles (casilla, lista…) y el layout
siguen pendientes; ver *Qué falta*.

## Modelo

Un widget es una struct pequeña y **retenida**: guarda el estado que
sobrevive a un fotograma (hover, pulsado) y nada más. No es dueño de una
ventana, ni de un bucle de eventos, ni de un motor de layout. Quien lo usa:

1. le pone `Bounds`, en unidades lógicas, normalmente en cada fotograma y a
   partir de su propio layout;
2. le pasa los eventos del puntero, en las mismas unidades, y —si lo ha
   enfocado— los del teclado;
3. le pide que se dibuje en un `*canvas.Canvas` que ya tiene.

Los widgets **nunca se repintan solos**. Cada método de evento devuelve un
`bool`: si algo visible cambió. El movimiento del puntero llega en cada
píxel, y solo la transición (entrar, salir) cuesta un repintado.

Como todo lo demás, no son seguros para uso concurrente: se manejan desde una
sola goroutine, la de UI. Con `eventloop` esa goroutine no es la que bombea la
conexión Wayland; ver `eventloop.md`.

## `Button`

```go
b := widget.NewButton("Clear", font)
b.OnClick = func() { /* … */ }

// cada fotograma, con el layout del llamador:
b.Bounds = canvas.Rect{X: 20, Y: 20, Width: 120, Height: 44}
b.Draw(cv)

// desde pointer.Pointer.OnEvent:
if b.PointerMove(x, y) { redraw() }   // Position
if b.PointerDown(x, y) { redraw() }   // ButtonDown
if b.PointerUp(x, y)   { redraw() }   // ButtonUp → llama a OnClick

// desde pointer.Pointer.OnFocus(nil):
if b.PointerLeave() { redraw() }

// desde los de wl_keyboard, si el llamador le ha dado el foco:
if b.SetFocused(true)      { redraw() }
if b.KeyDown(widget.KeySpace) { redraw() }   // key, pressed
if b.KeyUp(widget.KeySpace)   { redraw() }   // key, released → llama a OnClick
```

`pointer.Pointer` recuerda la última posición que entregó Wayland y la incluye
en cada cambio de botón, así que el widget recibe siempre coordenadas lógicas.

### Cuándo se activa

Un clic es *pulsar y soltar dentro*. La pulsación arma el botón; solo la
liberación **dentro de `Bounds`** llama a `OnClick`. Arrastrar fuera con el
botón pulsado y soltar cancela el clic, que es como un usuario se arrepiente.

Mientras el puntero está fuera de un botón armado, este se dibuja **sin
pulsar**, para que se vea que soltar ahí no hará nada; si vuelve a entrar,
vuelve a verse pulsado. Por eso hay que seguir alimentando `PointerMove`
con el botón apretado: Wayland sigue entregando el movimiento a la superficie
que recibió la pulsación.

`PointerLeave` abandona una pulsación en curso: la liberación irá a otra
superficie y aquí nunca podría completarse.

### Deshabilitado

`Disabled = true` hace que el botón ignore las pulsaciones y las teclas,
rechace el foco, y se dibuje con sus colores deshabilitados. Deshabilitarlo
a mitad de un clic —o con Espacio apretado— también lo cancela. Un botón
deshabilitado nunca pide repintado, porque su aspecto ya no lo mueve nada.

### Estados visibles

Los métodos de evento comparan el aspecto antes y después, no el estado
interno, de modo que el `bool` que devuelven es exactamente «un repintado se
vería distinto». Son cuatro: reposo, hover, pulsado y deshabilitado
(`ButtonStyle` da un color de relleno y de etiqueta por estado).

El foco **no es un quinto estado**, sino algo ortogonal: un botón enfocado
sigue estando en reposo, en hover o pulsado, y dibuja el relleno que le toca
bajo su anillo. Por eso lo que se compara no es el estado de relleno solo,
sino el par (relleno, enfocado).

## `TextField`

Un campo de texto de **una línea**: cursor que se mueve libremente, clic
para colocarlo, desplazamiento horizontal cuando el texto no cabe, y
*placeholder* mientras está vacío y sin foco.

```go
f := widget.NewTextField("escribe algo", font)
f.OnChange = func(s string) { /* cada edición del usuario */ }
f.OnSubmit = func(s string) { /* Intro */ }

// cada fotograma, con el layout del llamador:
f.Bounds = canvas.Rect{X: 20, Y: 20, Width: 300, Height: 44}
f.Draw(cv)

// teclas de edición, si el llamador le ha dado el foco:
if f.KeyDown(widget.KeyLeft) { redraw() }   // ←, →, Inicio, Fin, Retroceso, Supr, Intro
if f.Insert(ev.Text)         { redraw() }   // el texto, ya compuesto

// el parpadeo lo lleva la aplicación: el widget no tiene reloj
if f.SetCaretVisible(on) { redraw() }
```

El texto **nunca llega como `Key`** —no hay forma de nombrar un carácter
con una— sino por `Insert`, que sanea lo que reciba: descarta los
caracteres de control y sustituye el UTF-8 inválido por U+FFFD. Lo mismo
hace `SetText`, que además **no dispara `OnChange`**: es un cambio del
programa, y quien escucha no debe entrar en bucle.

### El cableado de un clic

`Focusable` no expone `Bounds`, así que a quién enfoca una pulsación lo
sigue decidiendo el llamador. Son tres llamadas, en este orden:

```go
// al pulsar el botón izquierdo en (x, y)
if hit(field.Bounds, x, y) {           // el hit test es del llamador
    changed := chain.Focus(field)      // primero el foco: solo la aplicación lo decide
    changed = field.PointerDown(x, y) || changed
    if changed { redraw() }
}
```

`PointerDown` **no enfoca** el campo: solo coloca el cursor. Comprueba
`Bounds` por su cuenta —pulsar el botón vecino no mueve este cursor— y
funciona con o sin foco, porque la aplicación enfoca y hace clic en la
misma pulsación. Un clic a la izquierda del texto lleva al primer carácter
visible, y uno a la derecha, al final del **tramo visible**: el cursor no
se teletransporta al final del buffer ni provoca un desplazamiento.

### Por qué todo se mide relativo al ancla

`ancla` es el primer carácter visible. El tramo que se dibuja es
`texto[ancla:visEnd]` —un subslice, gratis— y la posición del cursor es
`Measure(texto[ancla:cursor])`. **Nada se mide nunca desde el byte 0 y
nada acumula anchos carácter a carácter**, porque con *kerning* los
avances no se suman: sobre 320 caracteres de «AVAWATAY», sumar carácter a
carácter se pasa un 8,5 %.

Las dos primitivas (`fitBack`, `fitFwd`) buscan por duplicación de la
distancia y bisección, siempre midiendo subcadenas. El trabajo de medición
por evento es del orden del tramo visible por un factor logarítmico de la
bisección —unas 10 a 50 veces los caracteres visibles—, y **no depende del
largo del texto**. El desplazamiento va de carácter en carácter, no por
píxeles: con una fuente proporcional de tamaño normal no se nota, y es una
decisión, no un descuido.

### Rendimiento, como aserción

No hay ningún test que afirme tiempos. Lo que se afirma es el **trabajo**,
con dos `Font` falsas que cuentan los caracteres que ven `Measure` y
`Draw`: con textos de 1.000, 10.000 y 100.000 caracteres, una edición al
principio, en medio y al final, un movimiento, un clic y un fotograma ven
el mismo número de caracteres. Además, `Draw`, `Text`, `PointerDown` y los
movimientos del cursor **no asignan**, y una edición asigna **una vez** (la
`string` cacheada).

La forma de los benchmarks (`go test ./widget -bench . -benchmem`), que es
lo único que envejece bien:

| Operación | Forma |
| --- | --- |
| `Draw` | plana respecto al largo del texto; 0 asignaciones |
| `PointerDown` | plana; 0 asignaciones |
| editar | **crece** con el largo del texto, por el `memmove` y la copia de la `string`, no por la fuente: el trabajo de medición es constante. 1 asignación por edición |

### Deshabilitado, foco y parpadeo

`Disabled = true` copia la semántica de `Button`: ignora teclas, texto y
clics, rechaza el foco y se dibuja con sus colores deshabilitados. Un campo
**sin foco o deshabilitado ignora `Insert` y las teclas**, así que la
aplicación puede repartir cada pulsación sin mirar quién tiene el foco.

El parpadeo lo lleva la aplicación con `SetCaretVisible`, desde su propio
temporizador: el widget no tiene reloj. Editar, mover el cursor o hacer
clic **dejan el cursor visible**, que es el reinicio del parpadeo.
`SetCaretVisible` sobre un campo sin foco guarda el valor y devuelve
`false`; `SetFocused(true)` siempre lo deja visible.

`Intro` devuelve `false` aunque dispare `OnSubmit`, igual que
`Button.KeyDown(KeyEnter)`: el `bool` solo responde «un repintado se vería
distinto», y quien escucha repinta por su cuenta.

### Límites conocidos

- **El cursor se mueve por caracteres Unicode, no por grafemas.** Una marca
  combinante suelta o un emoji compuesto se recorren en varios pasos.
- **Sin selección, sin portapapeles, sin saltos por palabra, sin deshacer,
  sin multilínea, sin límite de longitud, sin solo lectura ni enmascarado.**
- **Sin forma de cursor del ratón** sobre el campo: hace falta integrar
  `cursor-shape` en `pointer`, del que hoy solo hay el binding.
- **`Font` se fija antes de que el campo tenga texto:** cambiarla después no
  se detecta, porque la vista cachea medidas.
- Si `Measure` no es monótona con la longitud de la subcadena (algo raro,
  con *kerning* negativo extremo), la bisección pierde precisión; no se
  puede colgar, porque el número de caracteres es finito.

### Siguiente iteración

**Selección** y **forma del cursor del ratón**, y las dos son aditivas: el
editor guarda hoy solo el cursor y añadir el otro extremo no cambia ninguna
regla; las teclas con Mayús se codificarían como ya se codifica Mayús+Tab
(`KeySelectLeft`, `KeySelectRight`, …), que las traduce el llamador porque
`widget` no tiene estado de modificadores; y el arrastre y el doble clic ya
los entrega `pointer`.

## Foco y teclado

El foco que se maneja aquí es **el del cliente**. Wayland enfoca
*superficies*; qué control dentro de la superficie se queda el teclado es
asunto de quien integra, y `widget` no lo decide. Un widget recibe si está
enfocado y nunca lo pregunta: eso es lo que permite escribirlo, y probarlo,
sin que sepa que tiene hermanos.

```go
type Focusable interface {
    Focused() bool
    SetFocused(focused bool) bool
    KeyDown(k Key) bool
    KeyUp(k Key) bool
}
```

Las reglas del foco son, entonces, del llamador. En particular
**`PointerDown` no enfoca el botón sobre el que cae**: un widget que se
enfocara solo dejaría dos enfocados.

Un widget puede **rechazar** el foco —uno deshabilitado lo hace—. Como no
hay forma de *preguntarle* si lo quiere, quien lo reparte se lo da y luego
lee `Focused()`. Deshabilitar uno que ya estaba enfocado no le quita el foco
(eso es del llamador), pero sí le quita el anillo: un control que ignora el
teclado no puede parecer que escucha.

### `Chain`

`Chain` es quien conoce a los hermanos: lleva el orden de tabulación y
garantiza que **como mucho uno** esté enfocado.

```go
ch := widget.NewChain(input, clear, cancel)   // el orden es el del tabulador

ch.Focus(input)                  // al pulsar: el llamador decide sobre quién
if ch.KeyDown(k) { redraw() }    // Tab/Mayús-Tab mueven; el resto va al enfocado
if ch.KeyUp(k)   { redraw() }
```

Es deliberadamente lo más pequeño que funciona: un slice en orden y el
miembro que cree que tiene el foco. Todos sus métodos devuelven el mismo
`bool` de siempre, aquí la *o* lógica de quien pierde el foco y quien lo
toma.

**No hace *hit testing*.** `Focusable` no expone `Bounds` —y no puede: en
`Button` es un campo, así que un método con ese nombre no cabe—, de modo que
a quién enfoca una pulsación lo sigue decidiendo el llamador, que para eso
es el dueño del layout. `Chain` se ocupa del orden, del invariante de uno
solo y del reparto de teclas.

Un miembro que rechaza el foco se salta. Si lo rechazan todos, el foco
acaba en ninguna parte: el recorrido dura **una vuelta**, no da vueltas
indefinidamente. Entrar en una cadena sin nadie enfocado aterriza en el
extremo del que viene la dirección —Tab en el primero, Mayús-Tab en el
último—, que es donde el usuario espera que las dos teclas se diferencien.

Límite conocido y explícito: `KeyDown` **consume** `KeyTab` y `KeyBacktab`,
así que un widget que quiera el tabulador para sí —un campo multilínea que
inserte una tabulación— no puede recibirlo a través de una cadena; su
llamador tiene que mirar la tecla antes de entregarla.

### `Key`

`widget` solo depende de `canvas`, así que no tiene keysyms ni keycodes. Las
teclas se nombran **por lo que hacen**, y el llamador traduce —igual que ya
traduce las coordenadas del puntero a unidades lógicas:

| `Key` | Qué hace |
| --- | --- |
| `KeyNone` | nada; es el cero, para una tecla que el llamador no supo mapear |
| `KeySpace` | arma en la pulsación, activa en la liberación |
| `KeyEnter` | activa en la pulsación (Return y el Intro del teclado numérico) |
| `KeyEscape` | abandona una activación empezada con Espacio |
| `KeyTab` | pasa al siguiente widget de una `Chain` |
| `KeyBacktab` | pasa al anterior |
| `KeyLeft` | mueve el cursor un carácter a la izquierda |
| `KeyRight` | mueve el cursor un carácter a la derecha |
| `KeyHome` | mueve el cursor al principio |
| `KeyEnd` | mueve el cursor al final |
| `KeyBackspace` | borra el carácter anterior |
| `KeyDelete` | borra el carácter siguiente |

El conjunto es deliberadamente corto: una tecla sobre la que ningún widget
actúa no tiene constante. Las dos de tabulación no las atiende ningún
widget —ninguno sabe que tiene hermanos—, sino la `Chain`; las seis de
movimiento y borrado las atiende `TextField`. El texto en sí no llega como
`Key` —no hay forma de nombrar un carácter con una—, sino por
`TextField.Insert`.

Cuál de las dos es una pulsación lo decide el llamador, y no es algo que
este paquete pueda deducir: no tiene estado de modificadores, y un Tab con
Mayús llega como el keysym `ISO_Left_Tab` o como Tab con Shift pulsado según
el keymap.

### Cuándo se activa, con teclado

Espacio sigue la misma forma que el puntero: arma en la pulsación, activa en
la liberación, y Escape cancela —es el equivalente a arrastrar fuera—. Perder
el foco con Espacio apretado también lo abandona: la liberación irá al
siguiente enfocado y aquí nunca podría completarse.

Intro activa en el acto. Como no cambia nada visible, `KeyDown` devuelve
`false` aunque haya activado el botón: el `bool` responde solo a «un
repintado se vería distinto», y un llamador cuyo `OnClick` cambió la pantalla
repinta por su cuenta, igual que ya hace tras un clic.

### El anillo

`ButtonStyle` lo controla con `FocusRing` y `FocusRingWidth`; un ancho de 0,
o un color transparente, no dibuja ninguno —y entonces enfocar **no** pide
repintado, porque no se vería nada distinto. El `bool` no promete otra cosa,
y un mismo predicado decide si el anillo se pinta y si el aspecto cambió,
para que los dos no puedan discrepar. Se dibuja **por dentro de `Bounds`**, no alrededor: aquí
no hay motor de layout, así que un anillo por fuera caería sobre lo que el
llamador haya puesto al lado, y el llamador no tiene forma de saber que
tenía que dejar sitio.

El color por defecto es el casi blanco de la etiqueta, no el azul de acento,
porque el azul es el relleno de pulsado: un anillo de acento desaparecería
justo en el momento en que el usuario está actuando sobre el botón.

## Texto

`canvas` no tiene texto, y `widget` **no elige fuente por el llamador**: un
widget que muestra texto recibe un `widget.Font`.

```go
type Font interface {
    Measure(s string) float32
    Draw(cv *canvas.Canvas, at canvas.Point, s string, col canvas.Color, clip canvas.Rect)
}
```

`Draw` recibe el centro vertical del texto en `at.Y`, no la línea base,
porque todo widget quiere su etiqueta centrada en una caja; y no debe pintar
fuera de `clip`.

Esto mantiene a `widget` sin depender de ningún rasterizador de fuentes y no
ensancha `canvas`. La implementación de la librería es `text.Face`, que usa
las fuentes instaladas y rasteriza a la escala del canvas (ver `text.md`):

```go
face, _ := text.NewSystemFace(16, text.Regular)
b := widget.NewButton("Clear", face)
```

`example/widgets` la usa, y recurre a una `Font` de mapa de bits ASCII
(`bitmapFont`, sobre `basicfont.Face7x13`) solo si la máquina no tiene
ninguna fuente legible.

Una `Font` nula es válida: el botón se dibuja sin etiqueta.

## Restricciones que cumple

- **Sin asignaciones al dibujar.** `Button.Draw` y `TextField.Draw` no
  asignan, y está asertado en `go test` con `testing.AllocsPerRun`, como
  las rutas de `canvas`.
- **Solo depende de `canvas`.** Ni `wlcore` ni `keyboard`: el llamador traduce
  los eventos de Wayland —coordenadas y keysyms—, y así los widgets se prueban
  sin conexión.
- **Un `Bounds` inválido es un error de `canvas`,** pegajoso como cualquier
  otro (`Canvas.Err()`), no algo que el widget intente arreglar.

## Qué falta

- **Layout.** Cada llamador calcula sus `Bounds` a mano.
- **Más widgets.** Casilla, lista.
- **Reparto de eventos.** `pointer.Pointer` entrega coordenadas y gestos, pero
  decidir qué widget recibe cada evento sigue siendo política de la ventana.
- **Selección y forma de cursor del ratón en `TextField`** (ver la sección
  homónima más arriba).

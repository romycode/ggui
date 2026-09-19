# `widget` — controles reutilizables

> **Documento vivo.** Refleja el estado actual del código y se actualiza con
> él. El diseño original, congelado y con fecha, está en `docs/archive/`.

Documento de referencia de `widget/`. Acompaña a `canvas.md`, sobre cuyo
rasterizador dibuja todo, y a `estado.md`, que dice qué falta.

## Estado

Construido: **`Button`** —con puntero, foco y teclado— más las piezas que
comparte cualquier control enfocable: `Focusable`, `Key` y `Chain`, que
recorre el orden de tabulación. El resto de controles (campo de texto,
casilla, lista…) y el layout siguen pendientes; ver *Qué falta*.

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

Como todo lo demás, no son seguros para uso concurrente: se manejan desde el
goroutine que bombea la conexión Wayland.

## `Button`

```go
b := widget.NewButton("Clear", font)
b.OnClick = func() { /* … */ }

// cada fotograma, con el layout del llamador:
b.Bounds = canvas.Rect{X: 20, Y: 20, Width: 120, Height: 44}
b.Draw(cv)

// desde los listeners de wl_pointer:
if b.PointerMove(x, y) { redraw() }   // motion / enter
if b.PointerLeave()    { redraw() }   // leave
if b.PointerDown(x, y) { redraw() }   // button, pressed
if b.PointerUp(x, y)   { redraw() }   // button, released → llama a OnClick

// desde los de wl_keyboard, si el llamador le ha dado el foco:
if b.SetFocused(true)      { redraw() }
if b.KeyDown(widget.KeySpace) { redraw() }   // key, pressed
if b.KeyUp(widget.KeySpace)   { redraw() }   // key, released → llama a OnClick
```

`wl_pointer.button` no lleva coordenadas: se define contra la posición del
último `motion` o `enter`, así que quien integra tiene que recordarla.

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

El conjunto es deliberadamente corto: una tecla sobre la que ningún widget
actúa no tiene constante. Las dos últimas no las atiende ningún widget
—ninguno sabe que tiene hermanos—, sino la `Chain`.

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

- **Sin asignaciones al dibujar.** `Button.Draw` no asigna, y está asertado
  en `go test` con `testing.AllocsPerRun`, como las rutas de `canvas`.
- **Solo depende de `canvas`.** Ni `wlcore` ni `keyboard`: el llamador traduce
  los eventos de Wayland —coordenadas y keysyms—, y así los widgets se prueban
  sin conexión.
- **Un `Bounds` inválido es un error de `canvas`,** pegajoso como cualquier
  otro (`Canvas.Err()`), no algo que el widget intente arreglar.

## Qué falta

- **Un segundo widget enfocable.** `Chain` existe, pero `Button` es el único
  que puede entrar en ella; hasta que haya un campo de texto, el orden de
  tabulación no tiene mucho que recorrer.
- **Layout.** Cada llamador calcula sus `Bounds` a mano.
- **Más widgets.** El campo de texto de `example/widgets` sigue siendo un
  prototipo dentro del ejemplo.
- **Ratón sin capa.** `Button` recibe coordenadas ya traducidas; el traslado
  de `wl_pointer` (coordenadas, escala, botón izquierdo) sigue haciéndolo
  cada ventana.

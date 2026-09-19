# `text` — fuentes del sistema y texto sobre el canvas

> **Documento vivo.** Refleja el estado actual del código y se actualiza con
> él. El diseño original, congelado y con fecha, está en `docs/archive/`.

Documento de referencia de `text/`. Acompaña a `canvas.md`, sobre cuyo
píxel prestado escribe, y a `widget.md`, cuya interfaz `widget.Font`
implementa.

## Estado

Construido: descubrimiento de fuentes instaladas (`Find`) y una `Face` que
mide y dibuja una línea de texto. Es el único paquete de la librería que
importa `golang.org/x/image/font`; ver *Dependencias*.

## Encontrar una fuente

```go
face, err := text.NewSystemFace(16, text.Regular)               // sans-serif habitual
face, err := text.NewSystemFace(16, text.Bold, "Inter", "Noto Sans")
font, err := text.Find(text.Regular, "Inter", "Noto Sans")      // *opentype.Font
```

No hay fontconfig: eso exigiría cgo. `Find` recorre los mismos directorios
que recorrería fontconfig, por orden de prioridad:

1. `$XDG_DATA_HOME/fonts` (por defecto `~/.local/share/fonts`)
2. `~/.fonts`
3. el `fonts` de cada entrada de `$XDG_DATA_DIRS` (por defecto
   `/usr/local/share:/usr/share`)

Sigue enlaces simbólicos a directorios, con un tope de profundidad para
que un ciclo termine, y lee `.ttf`, `.otf`, `.ttc` y `.otc`. Si un fichero
es una colección lo decide su número mágico, no la extensión.

**Se casa por los nombres de dentro del fichero, no por el nombre del
fichero.** Se usa el par tipográfico (IDs 16/17) y el heredado (1/2): una
fuente con más de cuatro estilos por familia lleva `Inter` / `Light` en el
primero e `Inter Light` / `Regular` en el segundo, y cualquiera de los dos
puede ser lo que el llamador escribe. La comparación ignora mayúsculas y
espacios repetidos.

Reglas de desempate:

- Una familia **más arriba en la lista** del llamador gana, esté donde esté
  en el disco: la lista es su orden de preferencia.
- Para una misma familia gana el directorio **más arriba**, de modo que las
  fuentes del usuario tapan las del sistema.
- Si no hay coincidencia, el error envuelve `text.ErrNotFound`.

Sin familias, `NewSystemFace` prueba Inter, Noto Sans, DejaVu Sans,
Liberation Sans, Cantarell, Adwaita Sans, Ubuntu, Roboto y Arimo.

### Coste

Un recorrido completo de unas 800 fuentes (650 MB) tarda **unos 15 ms**; si
gana la primera opción se detiene antes, en unos 6 ms. Hacerlo una vez al
arrancar, no por fotograma.

Es así porque `names.go` lee a mano solo el directorio de tablas y la tabla
`name` de cada fichero. Con `sfnt.ParseReaderAt`, que decodifica tablas que
aquí no hacen falta, el mismo recorrido tardaba unos 225 ms. Una vez elegida
la fuente, se lee entera con `opentype.Parse`: el descubrimiento no la
retiene.

## `Face`

```go
face, _ := text.NewFace(parsed, 16) // 16 unidades lógicas
defer face.Close()

w := face.Measure("Clear")                        // unidades lógicas
face.Draw(cv, canvas.Point{X: x, Y: cy}, "Clear", color, clip)
```

Implementa `widget.Font`, así que se le pasa a `widget.NewButton` tal cual.

### Escala

El tamaño es lógico. `Draw` rasteriza al tamaño **por la escala del
canvas**: una cara de 16 sobre un canvas 2x dibuja glifos de 32 píxeles,
nítidos, no un mapa de bits ampliado. Las caras rasterizadas se guardan por
escala (hasta 8), con sus glifos, así que dibujar a una escala ya vista no
cuesta nada.

`Measure` no recibe escala, y no la necesita: los contornos van **sin
hinting**, con lo que los avances son lineales en el tamaño. Se mide a
escala 1 y se dibuja a la física; la diferencia es la cuantización de 1/64
de unidad. Con hinting completo los avances se redondearían a píxeles
enteros y medir sin conocer la escala sería mentira.

### Posición vertical

`at.Y` es el centro de la **caja de línea** (ascendente más descendente), no
la línea base ni la altura de las mayúsculas. La línea base se redondea a un
píxel entero, para que los trazos horizontales queden nítidos; la X conserva
su fracción, que el rasterizador respeta.

### Caché de glifos

Cada cara escalada guarda los glifos que se dibujan con ella, así que una
etiqueta repintada en cada fotograma rasteriza sus glifos **una vez** y luego
solo los compone. Un `Draw` en caliente no asigna nada, y está asertado en
`go test` con `testing.AllocsPerRun`.

La clave es el rune **y la posición subpíxel**, no el rune solo. El
rasterizador respeta la fracción de la X —es lo que hace que el espaciado
entre letras sea uniforme en vez de saltar de píxel en píxel—, así que la
cobertura de un glifo depende de esa fracción. Cachear por rune a secas
daría letras mal dibujadas; cachear por la fracción exacta fallaría casi
siempre, porque hay 64.

Se cuantiza a **cuartos de píxel**: el error de posición queda acotado en un
octavo de píxel, por debajo de lo que el ojo resuelve a tamaños de interfaz,
y la caché en cuatro máscaras por glifo. En vertical no hay nada que
cuantizar, porque la línea base ya se redondea a un píxel entero.

El avance con el que se mueve la pluma es el **real**, no el de la posición
cuantizada, así que la cuantización no se acumula a lo largo de la línea.

Dos detalles que la hacen correcta:

- **La máscara se copia.** El rasterizador de `x/image` reutiliza un único
  búfer para todos los glifos; una máscara cacheada que apuntara a él la
  machacaría el glifo siguiente de la misma línea.
- **La caché cuelga de la cara escalada,** no de la `Face`. Una máscara no
  significa nada a otro tamaño, así que descartar una escala descarta sus
  glifos con ella.

Está acotada en 512 máscaras por escala. Al llegar al tope se vacía entera,
en vez de elegir una víctima: hacerlo bien pide contadores de uso que esto no
tiene motivo para llevar, y el tope está puesto donde alcanzarlo ya significa
que el repertorio no es el que esta caché espera.

### Composición

Los glifos se componen *source-over* con la convención del canvas: color
recto, destino premultiplicado. El alfa efectivo es cobertura × alfa del
color, y el resultado es correcto también sobre un destino translúcido, no
solo sobre uno opaco; ningún canal supera nunca al alfa.

Nada de eso lo implementa `text`: la fórmula vive una sola vez, en el
compositor de `canvas`, y aquí solo se producen las máscaras. Cuando el texto
llegó, `text` traía su propio `blend` —justo el segundo compositor que
`canvas.md` advertía que aparecería si el suyo no estaba factorizado—; con
`DrawMask` se ha borrado.

### Límites

Solo una línea, de izquierda a derecha. Sin *shaping*, sin reordenación
bidireccional, sin fuentes de respaldo. Se aplica *kerning*; ligaduras y
escrituras complejas no. Los caracteres de control no ocupan espacio ni
dibujan. Un rune que la fuente no tiene dibuja el glifo de reemplazo de la
fuente, normalmente un recuadro.

## Daño

`Face.Draw` no escribe en los píxeles prestados: entrega cada glifo a
`canvas.DrawMask` como una máscara de cobertura y un color. Así el texto lo
compone **el compositor del canvas**, el mismo que cualquier figura, y entra
en `Canvas.Damage` como cualquier otro dibujo. El daño que resulta es
exactamente la región entintada, no la caja del clip ni el buffer entero.

El recorte al `clip` lo sigue haciendo `text`, recortando la máscara antes de
pasarla: `DrawMask` ya garantiza que nada se salga del buffer, pero la caja
dentro de la que una etiqueta tiene que quedarse es la del widget, y el
canvas no sabe nada de ella.

## Errores

`Draw` no puede devolver un error, así que `Face` copia la convención del
canvas: el primero se queda pegado y se consulta con `Face.Err()`. Solo
ocurre si no se puede construir una cara a la escala de un canvas.

## Dependencias

`text` importa `golang.org/x/image` (`font`, `font/opentype`, `math/fixed`).
Es una dependencia nueva de la librería publicada, aceptada a propósito para
tener fuentes vectoriales sin cgo. Se queda **contenida en este paquete**:
`widget` y `canvas` no la importan; `widget` recibe el texto por la interfaz
`Font` justo para no depender de ella.

## Pruebas

- **Descubrimiento**, con las fuentes Go que trae `x/image`, escritas a un
  directorio temporal: no depende de lo que tenga la máquina. Cubre estilo y
  familia por nombres internos, preferencias, directorios, enlaces (ciclo y
  enlace roto), ficheros que no son fuentes y una colección `.ttc` armada a
  mano.
- **Lector de nombres (`names.go`)**, contrastado contra `sfnt.Font.Name`
  con las fuentes Go y, si la máquina las tiene, con **todas las fuentes
  instaladas** (849 en la máquina donde se escribió): coincide con el
  oráculo en todas. La única discrepancia es del oráculo: en una fuente de
  iconos con nombres codificados como Symbol devuelve los bytes UTF-16 sin
  decodificar. También hay entradas mal formadas (truncadas, tablas
  imposibles, offsets fuera de rango) que no pueden hacer *panic* ni dar
  coincidencia.
- **`Face`**: el texto no sale del `clip` ni escribe en el relleno de fila;
  el tamaño de glifo sigue a la escala; el centrado vertical; que el daño
  acumulado es exactamente la región entintada y no se sale del `clip`; y
  que `Face` cumple `widget.Font`.
- **Caché de glifos**, cuya prueba principal es que **no se note**: una cara
  que ya ha dibujado mucho texto, a muchas posiciones, dibuja píxel por
  píxel lo mismo que una recién creada —en posiciones enteras, en cada
  cuarto de píxel y entre dos cuartos—. Es lo que delata una colisión de
  clave, un desplazamiento obsoleto o una máscara del bin equivocado.
  Además: que un `Draw` en caliente no asigna; que hay una máscara por bin
  subpíxel y que las posiciones dentro de un bin la comparten; que las
  escalas no se prestan glifos y que descartar una escala descarta los
  suyos; que la caché está acotada; y que una máscara cacheada no apunta al
  búfer que el rasterizador reutiliza.

La composición premultiplicada y su invariante de canal ≤ alfa se prueban en
`canvas`, con `DrawMask` y `blendPixel`, que es donde vive ahora la fórmula.

Un `Draw` **en caliente** sí está asertado como libre de asignaciones, igual
que las rutas de dibujo de `canvas` y que `Button.Draw`. En frío no: el
rasterizador de `x/image` reutiliza búferes pero no lo garantiza, y encima la
caché tiene que copiar cada máscara nueva.

`BenchmarkDraw` mide las dos mitades por separado, con la misma cara y el
mismo canvas, cambiando solo el estado de la caché. En la máquina donde se
escribió esto, con una etiqueta de 26 caracteres a 16 unidades:

| Escala | En frío | En caliente | Mejora |
| --- | --- | --- | --- |
| 1 | 78,9 µs, 51 asignaciones | 13,4 µs, 0 | 5,9× |
| 1,5 | 114 µs, 51 | 21,8 µs, 0 | 5,3× |
| 2 | 149 µs, 51 | 33,0 µs, 0 | 4,5× |

Construir la cara queda fuera de la medida a propósito: analizar una fuente
no es lo que la caché arregla, y pagarlo ahí adornaría la comparación.

## Qué falta

- **Fuentes de respaldo.** Hoy un rune que la fuente principal no cubre sale
  como recuadro, aunque otra fuente instalada lo tenga (CJK, emoji).
- **Varias líneas, alineación y *shaping*.**
- **Fuentes variables.** Se usa la instancia por defecto; los ejes de
  peso o anchura no se controlan.
- **La fuente por defecto del escritorio.** Se prueba una lista fija en lugar
  de preguntar a la configuración del sistema.

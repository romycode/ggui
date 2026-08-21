# Diseño de un canvas 2D en Go

## Estado

**Implementado en `canvas/`.** La primera versión cubre todo el alcance
descrito aquí: buffer prestado, ARGB8888 premultiplicado, HiDPI con escala
inmutable, rectángulos, rectángulos redondeados, círculos y líneas con sus
tres terminaciones, error pegajoso y damage acumulado.

Cero asignaciones por operación de dibujo, comprobado en `go test` con
`testing.AllocsPerRun`, no solo en los benchmarks. Fuzzing sobre `New` y
sobre las nueve operaciones de dibujo: ~10M de ejecuciones por objetivo sin
fallos, verificando que nada escribe en el padding, que el damage nunca sale
de la región visible y que el slice prestado conserva identidad, longitud y
capacidad.

Tres puntos donde la implementación es más estricta que lo que este
documento describe, todos a mejor:

- `StrokeRect` usa cobertura **exacta**, no aproximada: el borde es la
  diferencia de dos rectángulos alineados a los ejes, y ambos tienen
  cobertura analítica. De paso, las esquinas componen una sola vez.
- `FillRoundedRect` con radio 0 delega en `FillRect`, así que hereda esa
  cobertura exacta en vez de la aproximación por distancia.
- Un grosor de trazo que supera el interior disponible rellena la figura en
  vez de recortar el anillo. Un anillo exactamente así de profundo deja su
  punto más interior sobre su propia frontera, que se renderizaría al 50 %.

Diseño original congelado:
`docs/superpowers/specs/2026-08-21-canvas-design.md`. Plan de
implementación (11 tareas, TDD):
`docs/superpowers/plans/2026-08-21-canvas-implementation.md`.

Sin cerrar todavía, por orden de probabilidad de que haga falta: `DrawMask`
para texto, lista de rectángulos dañados y clipping rectangular propio.

## Objetivo

Construir una herramienta de dibujo 2D portátil en Go. El consumidor entrega
una superficie de memoria, sus dimensiones físicas, su `stride`, unas
dimensiones lógicas y un factor de escala. El canvas valida esa descripción y
dibuja directamente sobre la memoria recibida.

La primera versión tendrá una única abstracción operativa principal: `Canvas`.
El dibujo será inmediato mediante CPU: cada llamada modificará el buffer, sin
árbol de escena, lista persistente de comandos ni bucle de renderizado interno.

## Alcance inicial

Incluye:

- Buffer externo prestado por el consumidor, sin copia.
- `stride` configurable, expresado en píxeles.
- ARGB8888 premultiplicado, colores con alfa y composición `source-over`.
- Coordenadas subpíxel y unidades lógicas independientes de la densidad.
- Escala HiDPI explícita, entera o fraccionaria e inmutable.
- Dimensiones físicas decididas por la integración de plataforma.
- Antialiasing por cobertura aproximada (ver rasterización).
- Rectángulos, rectángulos redondeados, círculos y líneas.
- Rellenos, bordes interiores y tres terminaciones de línea.
- Limpieza total o parcial.
- Recorte contra los límites del canvas.
- **Acumulación del rectángulo dañado**, en coordenadas físicas.
- Errores inspeccionables sin dibujos parciales.

Quedan fuera:

- Un `Renderer` independiente, listas de comandos y árboles de escena.
- Layout, componentes de interfaz, texto e imágenes.
- Elipses, polígonos, curvas Bézier, paths y degradados.
- Transformaciones arbitrarias; solo se incluye la conversión HiDPI uniforme.
- Clipping personalizado. Barato de añadir cuando haga falta —es intersectar
  un rectángulo más en el paso 4 del pipeline—, pero sin caso de uso hoy.
- Lista de rectángulos dañados: se acumula **uno solo**, su unión.
- Reserva, liberación, redimensionado o intercambio de buffers.
- Detección de DPI, monitores o protocolos de ventanas.
- Sincronización entre goroutines.
- Corrección gamma: la composición ocurre en sRGB, sin linealizar.

## Modelo general

El consumidor posee el almacenamiento. `Canvas` recibe un `[]uint32`, lo valida
y conserva una referencia prestada; no lo copia, reserva, amplía, reemplaza ni
libera.

Cada píxel visible es un `uint32` en formato ARGB8888 premultiplicado. Las
filas pueden contener padding: el inicio de la fila física `y` está en
`y * Stride`, no necesariamente en `y * Width`.

El consumidor decide cuándo dibujar. El canvas no observa cambios ni
reconstruye contenido automáticamente. El buffer conserva su estado entre
operaciones.

Esto permite limpiar y reconstruir toda la superficie o solo una región, usar
memoria compartida o un pool de buffers y presentar la misma memoria a otro
sistema sin una copia adicional.

## Descriptor del buffer

```go
type Buffer struct {
    Pixels []uint32
    Width  int
    Height int
    Stride int
}
```

`Buffer` solo describe memoria; no introduce otra capa de renderizado.

- `Pixels`: memoria prestada.
- `Width`: ancho físico visible en píxeles.
- `Height`: alto físico visible en píxeles.
- `Stride`: distancia, en elementos `uint32`, entre el inicio de dos filas.

La dirección de un píxel visible es:

```go
pixel := &buffer.Pixels[y*buffer.Stride+x]
```

El padding entre `Width` y `Stride` no forma parte del canvas. Ninguna
operación, incluido `Clear`, debe modificarlo.

## Coordenadas y HiDPI

El canvas distingue dos espacios:

- `width` y `height`: tamaño lógico usado por la API, **enteros**.
- `buffer.Width` y `buffer.Height`: tamaño físico visible.
- `scale`: píxeles físicos por unidad lógica.
- `buffer.Stride`: disposición física de las filas; no es una escala.

El tamaño lógico es entero a propósito. Con escala fraccionaria acaba
literalmente en `wp_viewport.set_destination`, que toma `int32`: un canvas de
`800.5` unidades lógicas no es expresable al compositor, así que no debe ser
representable en el constructor. Las **figuras** sí son `float32` —posiciones,
dimensiones, radios y grosores— porque ahí el subpíxel es el objetivo.

El consumidor no multiplica manualmente cada figura para HiDPI.

El tamaño físico no lo calcula ni lo reserva el canvas. La integración de
plataforma aplica su política de redondeo, crea el buffer físico y entrega
ambas descripciones al constructor. El canvas comprueba que sean coherentes.

Ejemplo:

```go
canvas, err := New(
    Buffer{
        Pixels: acquirePixelsFromPlatform(),
        Width:  1200,
        Height: 900,
        Stride: 1216,
    },
    800, 600, // tamaño lógico
    1.5,      // escala
)
if err != nil {
    return err
}

canvas.FillRect(
    Rect{X: 10.5, Y: 20, Width: 120, Height: 48},
    Color{R: 30, G: 30, B: 30, A: 255},
)
```

El rectángulo se rasteriza físicamente en `X = 15.75`, `Y = 30`,
`Width = 180` y `Height = 72`. La segunda fila empieza a `1216` píxeles del
inicio de la primera.

### Transformación de la geometría

Cada operación valida sus argumentos y transforma una sola vez la geometría
completa:

```go
physicalX      := logicalX * scale
physicalY      := logicalY * scale
physicalWidth  := logicalWidth * scale
physicalHeight := logicalHeight * scale
```

La geometría física conserva sus fracciones hasta la rasterización. No se
redondean por separado posiciones, tamaños, radios o grosores, ya que eso
podría crear huecos, solapamientos o grosores inconsistentes.

El bucle de píxeles trabaja exclusivamente en espacio físico. No multiplica ni
divide cada píxel por `scale`; la transformación ocurre una vez por figura.

### Coherencia de tamaño y escala

`scale` debe ser finito y mayor que cero. Admite valores como `1`, `1.25`,
`1.5`, `1.75` o `2`.

El constructor comprueba que la dimensión física corresponda al tamaño lógico
escalado, permitiendo una diferencia inferior a un píxel físico por eje:

```go
abs(float32(buffer.Width)-float32(width)*scale) < 1
abs(float32(buffer.Height)-float32(height)*scale) < 1
```

Esta tolerancia acepta las políticas de redondeo de cada plataforma sin hacer
que el canvas elija una. No autoriza un buffer arbitrariamente
sobredimensionado.

La escala, las dimensiones y la referencia al buffer son inmutables para el
`Canvas`. Si cualquiera cambia, el consumidor crea otro `Canvas` y vuelve a
dibujar. El objeto es barato porque no reserva la superficie.

### Entrada

La captura de entrada queda fuera del canvas. Si una plataforma entrega
posiciones físicas, el consumidor puede convertirlas:

```go
logicalX := physicalX / scale
logicalY := physicalY / scale
```

## Formato de píxel y color

El valor lógico de cada píxel es:

```text
0xAARRGGBB
```

En little-endian, sus bytes aparecen como `B, G, R, A`. El contrato público se
define por el valor lógico ARGB8888.

El alfa es premultiplicado. Por ejemplo, un rojo con aproximadamente 50 % de
opacidad se almacena como `0x80800000`, no como `0x80FF0000`.

El consumidor entrega colores sin premultiplicar:

```go
type Color struct {
    R uint8
    G uint8
    B uint8
    A uint8
}
```

El canvas premultiplica el `Color` una sola vez al comenzar cada operación.

**Sin corrección gamma.** La composición ocurre sobre valores sRGB tratados
como si fueran lineales, igual que hacen Cairo y Skia por defecto. Es un
compromiso consciente y no un descuido: linealizar duplicaría el coste por
píxel y obligaría a tablas de conversión. Se nota, y donde más se notará es en
texto fino claro sobre fondo oscuro, que aparecerá algo más delgado de lo
debido. Si llega a molestar cuando entre el texto, se revisa entonces con una
medida delante.

## Composición y opacidad

Las operaciones `Fill*`, `Stroke*` y `Line` componen mediante `source-over`.

**La cobertura escala las cuatro componentes, no solo el alfa.** Es la
consecuencia directa de trabajar premultiplicado y el error clásico del
formato: escalar únicamente `A` deja el RGB demasiado brillante para su nuevo
alfa y produce un halo claro en todos los bordes antialiasados.

Con `src` ya premultiplicado y `cov` la cobertura geométrica en `[0,1]`:

```text
src' = (Sr·cov, Sg·cov, Sb·cov, Sa·cov)
dst  = src' + dst·(1 − Sa')
```

En enteros de 8 bits la multiplicación es la aproximación habitual, exacta para
los extremos `0` y `255` y sin división:

```go
func mul8(a, b uint32) uint32 {
    t := a*b + 0x80
    return (t + (t >> 8)) >> 8
}
```

`Clear` y `ClearRect` **reemplazan** contenido en vez de usar `source-over`.
Limpiar con transparencia total produce `0x00000000`.

`Clear` reemplaza todos los píxeles visibles, sin tocar padding. En
`ClearRect`, los píxeles totalmente cubiertos se reemplazan; en un límite
subpíxel, el valor anterior y el color de limpieza se interpolan según la
cobertura —una interpolación lineal, no un `source-over`:

```text
dst = lerp(dst, clear', cov)
```

Una operación de dibujo con alfa cero es válida y no modifica el buffer.

### Un solo compositor

Todas las figuras terminan en la misma función privada: dado un índice de
píxel, un color premultiplicado y una cobertura, escribe el resultado. Hay dos
variantes —componer y reemplazar— y nada más.

No es una abstracción especulativa: es lo que evita tener dos implementaciones
de la fórmula de arriba cuando entre el texto. `x/image/vector` rasteriza
glifos a una máscara de cobertura (`*image.Alpha`), que es exactamente el mismo
par color+cobertura por píxel que produce un círculo. Con el compositor
factorizado, `DrawMask` es reutilizarlo; sin factorizar, es un segundo
compositor con sus propios bugs de premultiplicado.

`DrawMask` **no** entra en esta versión. Solo se garantiza que el punto de
entrada exista por dentro.

## Tipos públicos

```go
type Buffer struct {
    Pixels []uint32
    Width  int
    Height int
    Stride int
}

type Point struct {
    X float32
    Y float32
}

type Rect struct {
    X      float32
    Y      float32
    Width  float32
    Height float32
}

// PixelRect es geometría física entera. Solo lo devuelve Damage().
type PixelRect struct {
    X      int
    Y      int
    Width  int
    Height int
}

type Color struct {
    R uint8
    G uint8
    B uint8
    A uint8
}

type LineCap uint8

const (
    LineCapButt LineCap = iota
    LineCapSquare
    LineCapRound
)
```

## API pública inicial

```go
func New(buffer Buffer, width, height int, scale float32) (*Canvas, error)

func (c *Canvas) Width() int          // lógico
func (c *Canvas) Height() int         // lógico
func (c *Canvas) Scale() float32
func (c *Canvas) PixelWidth() int     // físico
func (c *Canvas) PixelHeight() int    // físico
func (c *Canvas) Stride() int
func (c *Canvas) Pixels() []uint32

func (c *Canvas) Err() error

func (c *Canvas) Damage() (PixelRect, bool)
func (c *Canvas) ResetDamage()

func (c *Canvas) Clear(color Color)
func (c *Canvas) ClearRect(rect Rect, color Color)
func (c *Canvas) FillRect(rect Rect, color Color)
func (c *Canvas) StrokeRect(rect Rect, width float32, color Color)
func (c *Canvas) FillRoundedRect(rect Rect, radius float32, color Color)
func (c *Canvas) StrokeRoundedRect(rect Rect, radius, width float32, color Color)
func (c *Canvas) FillCircle(center Point, radius float32, color Color)
func (c *Canvas) StrokeCircle(center Point, radius, width float32, color Color)
func (c *Canvas) Line(
    from Point,
    to Point,
    width float32,
    cap LineCap,
    color Color,
)
```

`Pixels()` devuelve la memoria prestada, no una copia.

## Errores

### Error pegajoso, no error por llamada

**Ninguna operación de dibujo devuelve `error`.** El `Canvas` guarda el primero
que ocurra y a partir de ahí toda operación es un no-op. El consumidor lo
comprueba una vez, al final del frame:

```go
c.Clear(bg)
c.FillRoundedRect(box, 6, panelBG)
c.StrokeRect(box, 1, border)
c.Line(a, b, 1, LineCapButt, fg)
if err := c.Err(); err != nil {
    return err
}
```

El motivo: **todos los errores posibles son bugs del programador**, no
condiciones de runtime. `NaN`, radio negativo, `LineCap` desconocido — nada de
eso depende del compositor, del usuario ni del estado del sistema. Una función
de pintado con treinta primitivas no debe tener treinta bloques `if err != nil`
para condiciones que, si aparecen, aparecen siempre y en la primera ejecución.

Es el mismo patrón y el mismo razonamiento que `bufio.Writer`, `sql.Rows` y el
`Decoder` de `wlcore`: el cero de cada tipo, o el no-op, y un `Err()` al final.

`New` **sí** devuelve `error`, porque ahí no hay objeto sobre el que dejar el
error pegado.

### El error es permanente

Una vez fijado no se limpia. Un `Canvas` envenenado se descarta y se crea otro
sobre el mismo buffer; el objeto es barato porque no reserva nada. Es lo que
hace Cairo con `cairo_t` y encaja con la inmutabilidad que ya tiene el canvas.

El coste, dicho sin adornos: **un argumento malo en la primera figura hace que
el resto del frame no se dibuje, en silencio hasta el `Err()` final.** Es
aceptable precisamente porque el error solo aparece con un bug, y un frame en
blanco con un error concreto en la mano es más fácil de depurar que un frame
casi correcto. Pero conviene no llamar a `Err()` solo en la ruta de error:
llamarlo siempre al cerrar el frame.

### Forma de los errores

```go
var ErrInvalidArgument = errors.New("canvas: invalid argument")
```

Los errores concretos envuelven `ErrInvalidArgument` e incluyen operación,
argumento, valor recibido y restricción incumplida.

```text
canvas: New: invalid argument "buffer.Stride": must be at least buffer.Width (got 100)
canvas: New: invalid argument "buffer.Pixels": buffer is too short (need 4096, got 4000)
canvas: New: invalid argument "scale": must be finite and greater than zero (got 0)
canvas: FillCircle: invalid argument "radius": must not be negative (got -3)
canvas: Line: invalid argument "cap": unknown LineCap(4)
```

Son inválidos:

- Tamaño lógico menor o igual que cero.
- Escala menor o igual que cero, `NaN` o infinita.
- Dimensiones físicas no positivas.
- `Stride` menor que el ancho físico.
- Buffer demasiado corto o cálculos que desborden.
- Incoherencia entre tamaño lógico, escala y tamaño físico.
- Dimensiones, radios o grosores **negativos**; cero es el no-op documentado.
- Coordenadas o medidas `NaN` o infinitas.
- Un `LineCap` desconocido.

Cada operación valida todos sus argumentos antes de escribir el primer píxel.
Si registra un error, el buffer queda exactamente como estaba y el damage
acumulado no cambia. Las figuras válidas que no intersectan el canvas no son
errores.

`New` con dimensiones lógicas `0 × 0`, `0 × h` o `w × 0` es inválido. La regla
de dimensiones cero como no-op solo afecta a figuras dibujadas sobre un canvas
existente.

## Regiones dañadas

`Canvas` acumula la **unión** de los bounding boxes físicos ya recortados de
todo lo que ha escrito de verdad:

```go
func (c *Canvas) Damage() (PixelRect, bool)
func (c *Canvas) ResetDamage()
```

`Damage` devuelve `ok == false` si no se ha tocado nada desde el último reset.
El rectángulo está en **píxeles físicos**, que es justo lo que pide
`wl_surface.damage_buffer`.

Esto no es una extensión opcional. El pipeline de rasterización ya calcula ese
rectángulo en el paso 4 y hasta ahora lo tiraba; recuperarlo cuesta dos campos
y una unión por operación. Sin él, la integración Wayland tiene que marcar el
buffer entero en cada commit —recomponer 1920×40 porque ha cambiado un reloj—
y eso convierte el ciclo de frames en el cuello de botella del panel entero.

Reglas:

- Las operaciones que no escriben (alfa cero, dimensión cero, figura
  completamente fuera, canvas en error) **no** amplían el damage.
- `Clear` daña toda la región visible.
- El damage lo resetea el consumidor tras el commit, no el canvas.

Limitación asumida: al ser un solo rectángulo, dos cambios pequeños en esquinas
opuestas dañan toda la superficie entre ellos. Una lista de rectángulos con
heurística de fusión es la evolución natural, pero no antes de tener un caso
que lo pida medido.

## Semántica de las figuras

### Rectángulos

`Rect.X` y `Rect.Y` definen la esquina superior izquierda. `Rect.Width` y
`Rect.Height` no pueden ser negativos.

Si una o ambas dimensiones son cero, la operación es un no-op: no modifica el
buffer ni el damage. Se aplica a `ClearRect`, `FillRect`, `StrokeRect`,
`FillRoundedRect` y `StrokeRoundedRect`.

### Rectángulos redondeados

El mismo radio se aplica a las cuatro esquinas. **Radio cero es válido y
equivale a la figura sin redondear** (`FillRoundedRect` → `FillRect`,
`StrokeRoundedRect` → `StrokeRect`), por coherencia con la regla de "cero es
no-op" que ya rige las dimensiones. Un tema con `radius: 0` no debe obligar a
ramificar en el call site.

Si el radio supera `min(width, height) / 2`, se limita automáticamente a ese
máximo.

### Círculos

El círculo se define por su centro y un radio no negativo. Radio cero es un
no-op.

### Bordes

Los bordes se dibujan completamente hacia el interior. El bounding box exterior
no cambia al aplicar un borde.

El grosor no puede ser negativo; grosor cero es un no-op. Si supera el espacio
interior disponible, desaparece la parte vacía y el resultado equivale
visualmente a una figura rellena.

### Líneas

- `LineCapButt`: termina exactamente en los puntos indicados.
- `LineCapSquare`: se extiende medio grosor más allá de cada extremo.
- `LineCapRound`: termina con un semicírculo de radio igual a medio grosor.

Una línea requiere dos puntos finitos y un grosor no negativo; grosor cero es
un no-op. Si ambos puntos coinciden, `LineCapRound` produce un círculo,
`LineCapSquare` un cuadrado y `LineCapButt` no modifica el buffer.

## Recorte

Todas las operaciones se recortan contra `PixelWidth()` y `PixelHeight()`.

- Una figura parcialmente exterior dibuja solo su parte visible.
- Una figura completamente exterior es válida y no modifica el buffer.
- El padding nunca forma parte de la región visible.
- No existen regiones de clipping personalizadas.

## Validación del constructor

`New` valida todo antes de crear un canvas utilizable:

- `width` y `height` deben ser mayores que cero.
- `scale` debe ser finito y mayor que cero.
- `buffer.Width` y `buffer.Height` deben ser mayores que cero.
- `buffer.Stride` debe ser al menos `buffer.Width`.
- `buffer.Pixels` debe alcanzar hasta el último píxel visible.
- El tamaño físico debe ser coherente con el lógico y `scale`.
- Los cálculos de índices y tamaños deben comprobar desbordamientos.

La longitud mínima no es necesariamente `Stride * Height`, porque no hace falta
padding después de la última fila:

```go
required := (buffer.Height-1)*buffer.Stride + buffer.Width
```

El cálculo se realiza de forma segura y debe cumplirse
`len(buffer.Pixels) >= required`. El canvas puede conservar el slice completo,
pero solo accede a los píxeles visibles.

## Rasterización y antialiasing

La primera implementación calcula la cobertura **por figura**, no mediante
supersampling de toda la superficie. Hay dos técnicas distintas y conviene no
confundirlas bajo un mismo nombre:

- **Rectángulos alineados a los ejes: cobertura exacta.** El área de
  intersección entre el píxel y el rectángulo es analítica y se calcula sin
  aproximar.
- **Círculos, rectángulos redondeados y líneas: cobertura aproximada por
  distancia con signo.** Se evalúa la distancia del centro del píxel a la
  frontera de la figura y se convierte con `clamp(0.5 - d, 0, 1)`. Es una
  estimación del área cubierta, no el área real; el error es visible sobre todo
  en radios muy pequeños, donde la curvatura dentro del píxel deja de ser
  despreciable.

La aproximación es la que usa todo el mundo y se ve bien. Queda escrita para
que nadie mida el error dentro de seis meses y crea que ha encontrado un bug.

Flujo de una operación:

1. Validar los argumentos. Si falla, fijar el error y salir sin escribir.
2. Si el canvas ya está en error, salir.
3. Multiplicar una vez por `scale` posiciones, dimensiones, radios y grosores.
4. Calcular el bounding box físico con margen de antialiasing.
5. Recortarlo contra `buffer.Width` y `buffer.Height`. Si queda vacío, salir.
6. Premultiplicar el color.
7. Recorrer solo los píxeles visibles del bounding box.
8. Calcular la cobertura geométrica.
9. Localizar cada píxel mediante `y*Stride+x`.
10. Componer o reemplazar mediante el compositor único.
11. Unir el bounding box recortado al damage acumulado.

El antialiasing se resuelve en espacio físico y cubre aproximadamente un píxel
físico alrededor de la frontera. Los píxeles completamente interiores evitan
cálculos de cobertura cuando sea posible.

## Rendimiento

El objetivo es redibujar cuando cambia el contenido, no mantener una animación
continua de toda la superficie.

Requisitos:

- Cero asignaciones durante una operación de dibujo.
- Ninguna asignación grande en `New`; el almacenamiento ya existe.
- Conversión de escala una sola vez por figura.
- Trabajo limitado al bounding box visible.
- Escritura directa para cobertura total y color opaco.
- Retorno inmediato para color transparente, dimensión cero, figura exterior o
  canvas en error.
- Conversión del color una vez por operación.
- Acceso por `y*Stride+x` sin recorrer padding.

El coste de `stride` es un cálculo simple por fila o índice y resulta
despreciable frente a cobertura y composición. Puede calcularse una base por
fila y avanzar linealmente.

El buffer externo permite integración sin copias adicionales, memoria
compartida, pools y doble o triple buffering. Puede conservarse un `Canvas` por
buffer o crear uno barato al adquirirlo.

No se fija todavía un presupuesto de milisegundos ni un objetivo de FPS. Los
benchmarks establecerán la línea base.

### Coste de HiDPI

El trabajo y la memoria física crecen aproximadamente con el cuadrado de la
escala:

```text
physicalPixels ≈ logicalWidth × logicalHeight × scale²
```

| Scale | Buffer físico para 1920 × 1080 | Píxeles relativos | Memoria ARGB8888 |
| ---: | ---: | ---: | ---: |
| `1` | `1920 × 1080` | `1×` | ~7.9 MiB |
| `1.5` | `2880 × 1620` | `2.25×` | ~17.8 MiB |
| `2` | `3840 × 2160` | `4×` | ~31.6 MiB |

El coste procede de procesar más píxeles, no de multiplicar la geometría por
`scale`. Cada figura procesa solo su región visible, el consumidor redibuja
cuando cambia algo y el damage acumulado limita lo que el compositor tiene que
recomponer.

## Integración con Wayland

El diseño es independiente de Wayland, pero puede rasterizar directamente los
buffers de una superficie Wayland.

### Memoria compartida y formato

Con `wl_shm`, la capa Wayland:

1. Crea y dimensiona un descriptor de memoria compartida.
2. Lo mapea en el proceso.
3. Crea un `wl_shm_pool` y un `wl_buffer` con `WL_SHM_FORMAT_ARGB8888`.
4. Expone la región mapeada como `[]uint32` en su capa de plataforma.
5. Construye un `Canvas` sobre ese slice y dibuja.

Un `make([]uint32, n)` ordinario del heap de Go no es por sí solo el
almacenamiento que espera `wl_shm`; se necesita memoria compartida respaldada
por un descriptor. Convertir la región mapeada, normalmente `[]byte`, a
`[]uint32` pertenece a la integración de plataforma y puede requerir una
utilidad acotada con `unsafe`.

Wayland expresa el `stride` de `wl_shm_pool.create_buffer` en bytes. El canvas
lo expresa en píxeles `uint32`:

```go
if strideBytes%4 != 0 {
    return errInvalidStride
}
stridePixels := strideBytes / 4
```

ARGB8888 coincide con el contrato lógico `0xAARRGGBB`; en little-endian sus
bytes son `B, G, R, A`. RGB debe estar premultiplicado por alfa. Véase la
[especificación principal de Wayland](https://wayland.freedesktop.org/docs/html/apa.html).

### Escala entera

Para escalado entero, la capa configura
`wl_surface.set_buffer_scale(scaleInt)`. El buffer tiene las dimensiones
físicas correspondientes y el canvas recibe el mismo factor como `scale`. La
geometría pública sigue siendo lógica.

### Escala fraccionaria

Con [`wp_fractional_scale_v1`](https://gitlab.freedesktop.org/wayland/wayland-protocols/-/blob/main/staging/fractional-scale/fractional-scale-v1.xml),
el compositor comunica una escala preferida en unidades de `1/120`:

```go
scale := float32(preferredScale) / 120
```

La integración calcula las dimensiones físicas con la política del protocolo y
las entrega al canvas. Para presentar:

- Mantiene `wl_surface.set_buffer_scale(1)`.
- Usa [`wp_viewport.set_destination`](https://gitlab.freedesktop.org/wayland/wayland-protocols/-/blob/main/stable/viewporter/viewporter.xml)
  con el tamaño lógico. Que el tamaño lógico del canvas sea entero es lo que
  hace que esa llamada sea posible sin redondear aquí.
- Entrega al canvas tamaño lógico, escala fraccionaria y tamaño físico.

El canvas no conoce `wl_surface`, `wl_buffer`, `wp_viewport` ni el origen de la
escala.

### Ciclo de vida de los buffers

Después de `attach`, daño y `commit`, el compositor puede seguir leyendo el
buffer. El consumidor no debe dibujar ni reutilizar esa memoria hasta recibir
`wl_buffer.release` o la liberación equivalente del protocolo usado.

La integración habitual mantiene dos o tres buffers:

- Adquiere uno libre.
- Dibuja mediante su `Canvas`.
- Consulta `Damage()`, lo adjunta, marca daño y hace `commit`.
- Llama a `ResetDamage()`.
- Lo marca ocupado.
- Lo habilita de nuevo al recibir la liberación.

Nunca se dibuja sobre un buffer ocupado por el compositor.

`wl_surface.damage_buffer` usa coordenadas físicas, que es exactamente lo que
devuelve `Damage()`. No hay conversión en medio.

Aviso con el pool: **cada buffer tiene su propio damage**. Si el frame N se
pinta sobre el buffer A y el frame N+1 sobre el B, el contenido de B es el del
frame N−1, no el del N; marcar solo lo que cambió respecto al frame anterior
deja B desactualizado en las zonas que cambiaron en N. La política correcta con
doble buffer es acumular el damage de los últimos frames o repintar la unión.
Esa decisión es de la capa de plataforma; el canvas solo informa de lo que
tocó.

## Propiedad y vida útil

El consumidor conserva la propiedad del almacenamiento y debe mantenerlo válido
durante toda operación que use el canvas.

`Canvas`:

- No copia los píxeles.
- No cambia la longitud o capacidad del slice.
- No libera ni desmapea la memoria.
- No sustituye el buffer.
- No toca el padding.

`Pixels()` devuelve el slice prestado. Es una vía de escape deliberada, y quien
la use asume lo que el canvas garantiza por su cuenta: no escribir en el
padding, no salirse de la región visible y no dejar el buffer con valores no
premultiplicados. El damage tampoco registra esas escrituras.

Es una violación del contrato desmapear, invalidar o reutilizar la memoria para
otro propósito mientras el canvas pueda acceder a ella. Para cambiar de
superficie se crea otro `Canvas`.

## Concurrencia

`Canvas` no es seguro para uso concurrente.

El consumidor debe impedir que una goroutine dibuje mientras otra dibuja, lee,
presenta o modifica el mismo buffer. La sincronización y el estado
libre/ocupado corresponden a la integración externa.

No se incluye un mutex interno porque no podría proteger las escrituras
directas sobre el slice ni el acceso de una plataforma o compositor.

## Pruebas

### Pruebas unitarias

- Validación de dimensiones lógicas, físicas, `scale` y `Stride`.
- Rechazo de un slice demasiado corto, incluido padding en la última fila.
- Cálculos de longitud e índices sin desbordamiento.
- Aceptación de diferencias de redondeo inferiores a un píxel y rechazo de
  tamaños incoherentes.
- Uso del buffer original sin copia.
- Acceso correcto con `Stride > Width`.
- Garantía de que ninguna operación toca padding.
- Conservación de geometría física fraccionaria.
- Valores exactos de `Clear` y `ClearRect`.
- Rellenos opacos y composición `source-over`.
- **Cobertura parcial sobre fondo oscuro: comprobar que el RGB se escala con la
  cobertura.** Es el test que detecta el halo del premultiplicado; sin él, el
  bug pasa desapercibido sobre fondo blanco.
- **`mul8` exacta en `0` y `255`.**
- Recorte parcial y total.
- Bordes hacia dentro y limitación del radio de esquina.
- Radio cero equivalente a la figura sin redondear.
- Los tres `LineCap`.
- Dimensiones, radios y grosores cero como no-op, sin damage.
- Argumentos negativos, no finitos y valores desconocidos.
- **Error pegajoso: el primer error se conserva, las operaciones posteriores no
  escriben, `Err()` devuelve el primero.**
- Garantía de que un error no modifica el buffer ni el damage.
- **Damage**: vacío al inicio, unión correcta de varias figuras, recortado a la
  región visible, sin ampliarse por no-ops, reseteable.
- Identidad e inmutabilidad de la descripción del buffer.

### Pruebas visuales

- Antialiasing de círculos.
- Esquinas redondeadas.
- Bordes de distintos grosores.
- Terminaciones de línea.
- Superposición con opacidad, sobre fondo claro **y oscuro**.
- Resultado idéntico con buffer compacto y con padding.

### Fuzzing

Se combinarán dimensiones lógicas y físicas, escalas, `stride`, longitudes y
coordenadas ordinarias, negativas, enormes, `NaN` e infinitos. Se verificará
que:

- No se accede fuera del slice ni se trata padding como contenido.
- No aparece un `panic` por una entrada externa.
- Los errores no causan escrituras parciales.
- El damage nunca sale de la región visible.
- El slice conserva identidad, longitud y capacidad.
- Ningún cálculo de tamaño o índice desborda.

### Benchmarks

- `New` sobre buffers existentes.
- `Clear` con buffer compacto y con padding.
- Figuras con `scale` `1`, `1.25`, `1.5` y `2`.
- Rectángulos, rectángulos redondeados, círculos y líneas.
- Colores opacos y semitransparentes.
- Figuras parcialmente recortadas.

Los benchmarks comprobarán cero asignaciones por operación y separarán el coste
fijo de transformar la figura del coste de procesar más píxeles.

## Evolución posible

Si aparecen necesidades reales, se podrán estudiar por separado:

- **`DrawMask`** para texto e imágenes: el compositor interno ya está
  factorizado para admitirlo, así que es superficie de API, no arquitectura.
- Lista de rectángulos dañados con heurística de fusión.
- Clipping rectangular propio.
- Extraer un `Renderer`.
- Registrar o reutilizar listas de comandos.
- Añadir transformaciones arbitrarias.
- Composición con corrección gamma.
- Incorporar paths y degradados.
- Añadir otros formatos de píxel.
- Ofrecer utilidades de plataforma para mapear memoria.
- Paralelizar operaciones grandes.

Estas extensiones no forman parte del contrato inicial y deberán justificarse
con casos de uso o mediciones.

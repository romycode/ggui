# Diseño del bucle de eventos: UI independiente del socket

**Fecha:** 2026-09-19

## Objetivo

Que la UI corra en su propia goroutine, con su propio reloj, y no dependa de
que el socket de Wayland tenga algo que leer ni de que una escritura termine.
Motivación concreta: la UI va a tener **animaciones** y **tareas asíncronas**.

Hoy hay una sola goroutine que lee el socket, despacha, ejecuta los handlers
de teclado y puntero, y también pinta (`redraw()` se llama desde los
listeners). Eso tiene tres costes:

- la UI solo se ejecuta cuando llega un mensaje del socket o vence el
  deadline del repeat de teclas: no hay dónde colgar un tick de animación ni
  el resultado de una tarea asíncrona;
- un pintado lento retrasa la lectura de teclado y puntero;
- un pintado lento retrasa el `pong` a `xdg_wm_base.ping`, y el compositor
  puede marcar la aplicación como bloqueada.

## Qué no cambia

- `wlcore.Conn` sigue siendo de una sola goroutine, sin locks. Cambia quién es
  esa goroutine: pasa a ser la **goroutine Wayland**, y la UI queda fuera del
  contrato.
- `keyboard` y `pointer` no se tocan. Siguen con callbacks dentro del dispatch,
  y lo que dicen `keyboard.md` y `pointer.md` ("sin goroutine ni canal
  intermedios") sigue siendo cierto para esos paquetes: el salto de goroutine
  ocurre por encima de ellos.
- `canvas` y `widget` siguen sin ser seguros para uso concurrente; pasan a
  ser propiedad de la goroutine de UI.

## Modelo: dos goroutines que solo hablan por mensajes

```
goroutine Wayland (dueña de Conn)          goroutine UI (dueña de widgets y Canvas)
  poll(sock, eventfd, próximo repeat)        select { inbox | tareas | reloj }
  Dispatch → keyboard / pointer / xdg   ──►  Inbox: Key, Pointer, Focus, Configure,
  Tick del repeat                                    BufferRelease, FrameDone, Closed
  ejecuta los Post(...)                 ◄──  Post(func()): attach/damage/frame/commit,
  responde ping/pong sola                            create_buffer, set_cursor...
```

Reglas:

1. **Toda petición al compositor sale de la goroutine Wayland.** La UI no
   llama a `Conn` ni a ningún método generado. Una petición se encola con
   `Loop.Post(func())`.
2. **La goroutine Wayland nunca espera a la UI.** El Inbox no bloquea al
   insertar. Un socket lleno frena a la goroutine Wayland y a nadie más.
3. **La UI no espera a la goroutine Wayland**, salvo que ella misma lo pida.
   Lo que necesita un resultado de una petición (crear un buffer) lo recibe
   como evento, no como valor de retorno.
4. **El `pong` lo contesta la goroutine Wayland.** No depende de la UI.

## Despertar la goroutine Wayland

Hoy espera en `ReadMsgUnix` y solo despierta por datos o por deadline. Un
`Post` tiene que despertarla, así que el bucle pasa a esperar en
`unix.Poll([sock, eventfd], hasta el próximo repeat)`:

- `eventfd` legible: se vacía y se ejecutan los `Post` pendientes.
- `sock` legible: `Dispatch()`. Con el socket ya legible no bloquea.
- timeout: `Tick` del teclado.

`Post` solo escribe en el `eventfd` si no había ya un aviso pendiente (un
flag atómico), para no hacer un syscall por cada closure.

**No se usa `DispatchUntil` en este bucle.** Al preparar esta spec se
comprobó que Go **no lee** de un socket con un deadline ya vencido aunque haya
datos en el buffer (devuelve `i/o timeout` sin leer). El comentario de
`DispatchUntil` dice lo contrario y ningún test lo cubre. Es un fallo latente
del bucle actual, con impacto pequeño: un repeat atrasado hace que una vuelta
salte la lectura. La tarea 0 del plan corrige el comentario y fija con un
test el comportamiento real.

Descartado: despertar con `SetReadDeadline` desde otra goroutine.
`dispatchUntil` sobrescribe el deadline al entrar y un aviso enviado justo
antes se pierde.

## Inbox: de la goroutine Wayland a la UI

Cola con mutex y una señal `chan struct{}` de capacidad 1. Insertar nunca
bloquea. Contiene un tipo `Event` con variantes: `Key`, `Pointer`,
`KeyboardFocus`, `PointerFocus`, `Configure`, `BufferRelease`, `FrameDone`,
`Closed`.

Política:

- **Se fusionan** `pointer.Position` y `pointer.DragMove` consecutivos: si el
  último elemento pendiente es del mismo tipo, se sustituye. Un ratón a 1 kHz
  no puede acumular una cola mientras la UI pinta.
- **Nunca se descartan ni reordenan** teclas, botones, clics, foco ni
  `Configure`.
- **Se acotan** los `Repeated` pendientes (64): pasado ese punto se descartan
  los nuevos. Un repeat no es una pulsación del usuario.
- Los eventos de foco llevan `*wlcore.Surface`. La UI lo usa **solo como
  identidad**, sin llamar a sus métodos.

## UI: ejecutor propio y reloj de fotogramas

La UI tiene su cola de closures, `UI.Do(func())`, segura desde cualquier
goroutine. Es el punto de entrada de las **tareas asíncronas**: una goroutine
de trabajo hace su tarea, y para publicar el resultado llama a `Do`. La UI
mantiene un `context.Context` que se cancela en `Closed`.

El reloj de animación es `wl_surface.frame`, no un `time.Ticker`: es lo único
que sabe cuándo el compositor va a mostrar algo y se detiene solo si la
ventana está oculta. Máquina de estados (`FrameClock`, lógica pura, sin
Wayland):

```
idle ──(dirty | animating)──► pinta ya, pide frame ──► esperando
esperando ──FrameDone──► (dirty | animating) ? pinta y pide frame : idle
esperando ──BufferRelease──► reintenta si había pintado pendiente sin buffer
```

Consecuencias que se buscan: una UI estática no gasta CPU; una animación va al
ritmo del compositor; como solo hay un fotograma en vuelo, la UI nunca acumula
frames por delante de un compositor lento; y el timestamp del callback es el
reloj de la animación.

Un `Present` es una closure con `Attach`, `DamageBuffer`, `Frame` y `Commit`,
en ese orden (el callback se entrega tras el siguiente commit). La UI marca el
buffer como ocupado al encolar y lo libera al recibir `BufferRelease`.

La creación y el redimensionado de buffers (`CreatePool`, `CreateBuffer`)
tocan la tabla de objetos, así que van por `Post` y devuelven su resultado
con `UI.Do`. La reserva de memoria (`memfd`, `mmap`) no toca `Conn` y puede
hacerse en la UI.

## Arranque: la ventana primero

**Requisito firme:** la ventana se abre de inmediato. Nada de lo que la
aplicación necesite para construir su UI real (descubrir fuentes, cargar
datos, construir el árbol de widgets) puede retrasar el primer `commit`. Si
esa preparación tarda, la ventana muestra un **loader** hasta que esté lista.

Hoy no se cumple: `run()` evalúa `newWindow(conn, loadFont())` antes de crear
la superficie, y `text.Find` recorre directorios de fuentes, así que el
primer `commit` espera a esa E/S.

Secuencia objetivo:

```
conectar → registry → superficie xdg → commit inicial (sin buffer)
    → Configure → primer frame con el LOADER   ◄── aquí ya hay ventana
              ╰─ en paralelo: init de la app (goroutine de trabajo)
    → init termina → UI.Do(instalar UI real) → siguiente frame ya es la UI real
```

Reglas:

1. **El camino hasta el primer frame no importa nada de la aplicación.** Solo
   `wlcore` y `canvas`. El bucle y la ventana se levantan primero; la app se
   inicializa después, en su propia goroutine.
2. **El loader no usa texto.** Es lo que no puede esperar a las fuentes, así
   que se dibuja solo con formas de `canvas` (un arco o círculos girando). Si
   hace falta mostrar un texto, aparece cuando las fuentes estén listas.
3. **El loader se anima con el mismo `FrameClock`** que cualquier otra
   animación: sin `Ticker`, sin caso especial.
4. **La UI real se instala con `UI.Do`.** Al terminar el init, la goroutine de
   trabajo llama a `Do(func(){ ui.Install(root) })`. El siguiente frame ya es
   la UI real; el loader sale sin parpadeo porque solo hay un fotograma en
   vuelo.
5. **Los eventos de entrada durante el loader no se pierden.** La UI los
   recibe igual. Mientras no haya UI real instalada se descartan las teclas
   y los clics, salvo el cierre de la ventana, que siempre funciona.
6. **Un init que falla** se muestra en el mismo sitio en que estaba el loader
   (estado de error), no cierra la ventana en silencio.
7. **Loader con retardo, decisión abierta:** si el init tarda menos que un
   umbral pequeño (p. ej. 100 ms), mostrar el loader un instante es peor que
   no mostrarlo. La primera versión lo pinta siempre; el umbral se decide con
   mediciones reales, no antes.

## Cierre y errores

- `Conn.Done()` es un canal y puede esperarse desde cualquier goroutine. La
  UI **no llama a `Conn.Err()`** hasta que `Done` esté cerrado: `err` se
  escribe sin lock dentro de `fatal`.
- Cuando `Done` se cierra, la goroutine Wayland empuja `Closed` y sale; la UI
  sale del bucle y cancela su contexto.
- Orden de apagado: la UI deja de encolar, la goroutine Wayland termina y
  después se llama a `DrainFDs`.

## Cómo se comprueba que funciona

Dos pruebas de aceptación con `-race` sobre un `socketpair`:

1. **UI bloqueada no bloquea el socket:** la UI duerme 500 ms dentro de un
   handler y mientras tanto el servidor de prueba envía un `ping` y espera el
   `pong` en menos de 50 ms.
2. **Socket bloqueado no bloquea la UI:** el servidor de prueba deja de leer
   y se llena el buffer de envío. La UI sigue recibiendo `Do` y sigue
   avanzando su reloj.

3. **La ventana no espera al init:** con un init de la aplicación que
   tarda 2 s, el primer `Present` (el loader) llega al servidor de prueba en
   menos de 100 ms desde el `Configure`, y el `Present` de la UI real llega
   después de que el init termine y no antes.

Y una comprobación manual en una sesión Wayland real con `example/widgets`
(cursor parpadeando, un indicador animado, y un init lento simulado con
`time.Sleep` para ver el loader).

## Decisiones abiertas

- **Nombre del paquete** (`eventloop` provisional) y si `UI` vive en él o en
  un paquete propio.
- **Compositor atascado en escritura.** La goroutine Wayland se bloquearía
  en `Write` sin poder leer. Se deja sin deadline de escritura en la primera
  versión. Revisar si aparece en uso real.
- **Extraer el pool de buffers** del ejemplo a un paquete reutilizable. Fuera
  de esta iteración.

## Fuera de alcance

Varias ventanas, varias colas por objeto (`wl_event_queue`), `wp_presentation`
para temporización precisa, y touch.

# TextField Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Añadir `widget.TextField`, un campo de texto de una línea con cursor libre, clic para colocarlo y desplazamiento horizontal, y migrar a él el prototipo que hoy vive dentro de `example/widgets`.

**Architecture:** Tres piezas no exportadas y una exportada. `editor` (en `widget/editor.go`) es el modelo puro: bytes, cursor y una `string` cacheada que se reconstruye al final de cada edición. `view` (en `widget/textview.go`) es la ventana desplazada sobre ese texto: un ancla, y dos primitivas de búsqueda (`fitBack`/`fitFwd`) que miden **siempre subcadenas relativas al ancla**, nunca desde el byte 0 ni sumando anchos carácter a carácter. `TextField` (en `widget/textfield.go`) junta las dos, añade estilo, foco, callbacks y dibujo, y devuelve en cada método el mismo `bool` que `Button`: «un repintado se vería distinto», calculado con un único predicado.

**Tech Stack:** Go 1.27, solo `canvas` de este módulo más la biblioteca estándar (`math`, `strings`, `unicode`, `unicode/utf8`). Sin dependencias nuevas. Pruebas estándar con `-race`, `testing.AllocsPerRun` y fuzzing nativo.

**Spec:** `docs/archive/specs/2026-09-21-textfield-design.md`

## Global Constraints

- **`widget` solo depende de `canvas`** de este módulo. Ni `keyboard`, ni `text`, ni `wlcore`: el llamador traduce keysyms y coordenadas. La biblioteca estándar sí.
- **Sin cgo**, solo Linux, dependencias limitadas a la estándar y `golang.org/x/...`.
- **Documentación en español; código y comentarios de código en inglés (EN_US).**
- **`Draw`, `Text`, `PointerDown` y todo movimiento del cursor: 0 asignaciones**, comprobado con `testing.AllocsPerRun`, no solo medido en un benchmark. Una edición: **como mucho 1 asignación** (la `string` cacheada), medida con la capacidad de `buf` ya reservada.
- **Ninguna operación mide desde el byte 0 ni acumula anchos**: `Measure` se llama siempre sobre la subcadena entera que interesa, porque los avances no son aditivos con *kerning*.
- **Un `Bounds` inválido es un error pegajoso de `canvas`**, como en `Button`, no algo que el widget arregle. Lo que sí se garantiza: un `Bounds` **válido** nunca produce un rectángulo interior inválido.
- **Los widgets nunca se repintan solos** y no son seguros para uso concurrente.
- **Todo test corre con `-race`** y ninguno usa `time.Sleep` como sincronización.
- Cada símbolo exportado nuevo lleva comentario: `go run ./cmd/docaudit` no debe retroceder.
- Verificación al final de cada tarea: `go build ./...`, `go vet ./...`, `go test ./widget/... ./example/... -race`.

## Review Focus

- Ninguna ruta mide el texto entero: las aserciones de trabajo (Tarea 4) lo prueban contando caracteres, no cronometrando.
- El `bool` que devuelve cada método sale **siempre** del predicado único `visual()`, nunca de una regla escrita a mano por método.
- `PointerDown` no enfoca el campo (lo haría el llamador) y comprueba `Bounds` por su cuenta.
- `Draw` no mide nada y no asigna: el tramo visible es un subslice de la `string` cacheada.
- El saneado no asigna cuando la entrada ya es válida.

## File Structure

| Fichero | Responsabilidad |
| --- | --- |
| `widget/key.go` *(modificado)* | Seis constantes nuevas: `KeyLeft`, `KeyRight`, `KeyHome`, `KeyEnd`, `KeyBackspace`, `KeyDelete`. |
| `widget/editor.go` *(nuevo)* | `editor`: bytes, cursor, `string` cacheada, `version`, saneado. Sin `canvas` ni `Font`. |
| `widget/editor_test.go` *(nuevo)* | Tablas, invariantes tras cada operación, fuzz nativo, asignaciones. |
| `widget/textview.go` *(nuevo)* | `view` (ancla, `caretX`, `visEnd`, `viewW`, `innerW`), `measure`/`budget`, los tres helpers de límites, `fitBack`/`fitFwd`, `sync`, `hit`. |
| `widget/textview_test.go` *(nuevo)* | Las dos `Font` falsas (contadoras) y las pruebas de las primitivas y de las reglas de la vista. |
| `widget/textfield.go` *(nuevo)* | `TextField`, `TextFieldStyle`, `DefaultTextFieldStyle`, el predicado `visual()`, `ensure`/`sync`, `Draw`, `PointerDown`. |
| `widget/textfield_test.go` *(nuevo)* | Comportamiento, valores devueltos, dibujo, aserciones de trabajo y benchmarks. |
| `widget/doc.go` *(modificado)* | Una frase sobre el campo de texto. |
| `example/widgets/ui.go`, `window.go` *(modificados)* | El prototipo sustituido por `widget.TextField` y una `widget.Chain`. |
| `docs/widget.md`, `docs/estado.md` *(modificados)* | Sección de `TextField` y fila de estado. |

---

### Task 1: las teclas de edición y el modelo `editor`

**Files:**
- Modify: `widget/key.go`
- Create: `widget/editor.go`
- Create: `widget/editor_test.go`

**Interfaces:**
- Consumes: nada del resto del plan.
- Produces:
  - `KeyLeft, KeyRight, KeyHome, KeyEnd, KeyBackspace, KeyDelete Key` (constantes nuevas al final del bloque `const` existente, después de `KeyBacktab`).
  - `type editor struct { buf []byte; caret int; text string; version uint64 }` y sus métodos, todos con receptor `*editor`: `insert(s string) bool`, `backspace() bool`, `delete() bool`, `left() bool`, `right() bool`, `home() bool`, `end() bool`, `setText(s string) bool`, `setCaret(offset int) bool`, `commit()`.
  - `sanitize(s string) string` e `isClean(s string) bool`.

- [ ] **Step 1: Añadir las seis teclas a `widget/key.go`.**

En el comentario del tipo `Key`, tras el párrafo que empieza «The set is deliberately small», añadir:

```go
// The six editing keys, [KeyLeft] to [KeyDelete], are [TextField]'s. The
// text itself never arrives as a Key — there is no way to name a character
// with one — but through [TextField.Insert], already composed.
```

Y al final del bloque `const`, después de `KeyBacktab`:

```go
	// KeyLeft moves the caret one character towards the start.
	KeyLeft

	// KeyRight moves the caret one character towards the end.
	KeyRight

	// KeyHome moves the caret to the start of the text.
	KeyHome

	// KeyEnd moves the caret to the end of the text.
	KeyEnd

	// KeyBackspace deletes the character before the caret.
	KeyBackspace

	// KeyDelete deletes the character after the caret.
	KeyDelete
```

Las constantes van **al final**: son `iota`, y meterlas en medio renumeraría las que ya existen.

- [ ] **Step 2: Escribir el test que falla, `widget/editor_test.go`.**

```go
package widget

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// checkInvariants asserts the three the editor promises after every
// operation: valid UTF-8, a caret inside the text, and a caret on a
// character boundary. The cached string and the buffer must also agree, or
// Text and Draw would show something the editor does not hold.
func checkInvariants(t *testing.T, e *editor, what string) {
	t.Helper()
	if !utf8.Valid(e.buf) {
		t.Fatalf("%s: buffer is not valid UTF-8: %q", what, e.buf)
	}
	if e.caret < 0 || e.caret > len(e.buf) {
		t.Fatalf("%s: caret %d outside 0..%d", what, e.caret, len(e.buf))
	}
	if e.caret < len(e.buf) && !utf8.RuneStart(e.buf[e.caret]) {
		t.Fatalf("%s: caret %d is in the middle of a character", what, e.caret)
	}
	if e.text != string(e.buf) {
		t.Fatalf("%s: cached text %q does not match the buffer %q", what, e.text, e.buf)
	}
}

func TestEditorInsertAndDelete(t *testing.T) {
	cases := []struct {
		name  string
		do    func(e *editor)
		text  string
		caret int
	}{
		{"insert ascii", func(e *editor) { e.insert("abc") }, "abc", 3},
		{"insert two bytes", func(e *editor) { e.insert("ñ") }, "ñ", 2},
		{"insert three bytes", func(e *editor) { e.insert("日") }, "日", 3},
		{"insert four bytes", func(e *editor) { e.insert("𝄞") }, "𝄞", 4},
		{"insert in the middle", func(e *editor) { e.insert("ac"); e.left(); e.insert("b") }, "abc", 2},
		{"backspace one character", func(e *editor) { e.insert("a日"); e.backspace() }, "a", 1},
		{"backspace at the start", func(e *editor) { e.insert("a"); e.home(); e.backspace() }, "a", 0},
		{"delete one character", func(e *editor) { e.insert("日a"); e.home(); e.delete() }, "a", 0},
		{"delete at the end", func(e *editor) { e.insert("a"); e.delete() }, "a", 1},
		{"control characters are dropped", func(e *editor) { e.insert("a\r\n\tb") }, "ab", 2},
		{"invalid utf-8 becomes U+FFFD", func(e *editor) { e.insert("a\xffb") }, "a�b", 5},
		{"home and end", func(e *editor) { e.insert("añ"); e.home(); e.end() }, "añ", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e editor
			c.do(&e)
			checkInvariants(t, &e, c.name)
			if e.text != c.text || e.caret != c.caret {
				t.Fatalf("text %q caret %d, want %q and %d", e.text, e.caret, c.text, c.caret)
			}
		})
	}
}

func TestEditorReportsWhetherAnythingChanged(t *testing.T) {
	var e editor
	if e.backspace() || e.delete() || e.left() || e.right() || e.home() || e.end() {
		t.Fatal("an operation on an empty editor reported a change")
	}
	if e.insert("") || e.insert("\r") {
		t.Fatal("inserting nothing reported a change")
	}
	if e.version != 0 {
		t.Fatalf("version %d after no edit, want 0", e.version)
	}
	if !e.insert("ab") {
		t.Fatal("inserting text reported no change")
	}
	if e.version != 1 {
		t.Fatalf("version %d after one edit, want 1", e.version)
	}
	if e.right() || e.end() {
		t.Fatal("moving right at the end reported a change")
	}
}

func TestEditorSetTextAndSetCaret(t *testing.T) {
	var e editor
	if !e.setText("a\x00ñ") {
		t.Fatal("setText reported no change")
	}
	if e.text != "añ" || e.caret != 3 {
		t.Fatalf("text %q caret %d after setText, want %q and 3", e.text, e.caret, "añ")
	}
	if e.setText("añ") {
		t.Fatal("setText with the same text and the caret at the end reported a change")
	}
	e.home()
	if !e.setText("añ") {
		t.Fatal("setText with the caret elsewhere reported no change")
	}
	if e.caret != 3 {
		t.Fatalf("caret %d after setText, want it at the end", e.caret)
	}

	// setCaret rounds backwards, never into the middle of a character.
	for _, c := range []struct{ in, want int }{{-5, 0}, {0, 0}, {1, 1}, {2, 1}, {3, 3}, {99, 3}} {
		e.setCaret(c.in)
		if e.caret != c.want {
			t.Fatalf("setCaret(%d) left the caret at %d, want %d", c.in, e.caret, c.want)
		}
		checkInvariants(t, &e, "setCaret")
	}
	if e.setCaret(3) {
		t.Fatal("setCaret to where the caret already is reported a change")
	}
}

// Reading the text must be free, and an edit must cost at most the one
// string the cache is rebuilt from. The buffer is grown first, because its
// amortized doubling is not what is being measured.
func TestEditorAllocations(t *testing.T) {
	var e editor
	e.setText(strings.Repeat("a", 4096))

	if n := testing.AllocsPerRun(200, func() { _ = e.text }); n != 0 {
		t.Fatalf("reading the cached text allocates %v times, want 0", n)
	}
	if n := testing.AllocsPerRun(200, func() { _ = sanitize("already clean") }); n != 0 {
		t.Fatalf("sanitizing clean text allocates %v times, want 0", n)
	}
	n := testing.AllocsPerRun(200, func() {
		e.insert("x")
		e.backspace()
	})
	if n > 2 {
		t.Fatalf("an insert and a backspace allocate %v times, want at most one each", n)
	}
}

func FuzzEditor(f *testing.F) {
	f.Add("hello", []byte{0, 1, 2, 3, 4, 5, 6, 7, 8})
	f.Add("añ日𝄞", []byte{3, 3, 1, 2, 7, 7, 4})
	f.Add("\xff\x00ok", []byte{8, 8, 1})

	f.Fuzz(func(t *testing.T, seed string, ops []byte) {
		var e editor
		e.setText(seed)
		checkInvariants(t, &e, "setText")

		for i, op := range ops {
			switch op % 9 {
			case 0:
				e.insert("a")
			case 1:
				e.insert("ñ日𝄞")
			case 2:
				e.insert(seed)
			case 3:
				e.backspace()
			case 4:
				e.delete()
			case 5:
				e.left()
			case 6:
				e.right()
			case 7:
				e.home()
			case 8:
				e.setCaret(i)
			}
			checkInvariants(t, &e, "after op")
		}

		// Nothing the editor holds may be a control character or invalid.
		for _, r := range e.text {
			if unicode.IsControl(r) {
				t.Fatalf("a control character survived: %q", e.text)
			}
		}
	})
}
```

- [ ] **Step 3: Comprobar que falla.**

Run: `go test ./widget/ -run 'TestEditor'`
Expected: FAIL de compilación, `undefined: editor`.

- [ ] **Step 4: Escribir `widget/editor.go`.**

```go
package widget

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// editor is the text model behind [TextField]: the bytes, the caret, and
// nothing about how either of them looks. It knows no font, no canvas and
// no focus, which is what lets the editing rules be tested on their own.
//
// Three invariants hold after every operation: buf is valid UTF-8, the
// caret is within it, and the caret sits on a rune boundary.
//
// The zero value is an empty editor, ready to use.
type editor struct {
	// buf is the text, in UTF-8.
	buf []byte
	// caret is an offset into buf, on a rune boundary.
	caret int
	// text is buf as a string, rebuilt at the end of each edit rather than
	// when it is read. That is what makes reading and drawing free: the
	// visible run is a substring of this one, and a lazy rebuild would put
	// its allocation in the first frame after an edit.
	text string
	// version counts the edits. Two states with the same version hold the
	// same bytes, which is what lets the repaint predicate stand for the
	// text without comparing it.
	version uint64
}

// commit republishes the cached string and counts the edit. Every method
// that changes buf ends here.
func (e *editor) commit() {
	e.text = string(e.buf)
	e.version++
}

// insert puts s at the caret, after sanitizing it, and reports whether the
// text changed. The caret ends after what was inserted.
func (e *editor) insert(s string) bool {
	s = sanitize(s)
	if s == "" {
		return false
	}
	// Grow by len(s) first, then slide the tail right and write s into the
	// hole: one growth of buf, and no temporary of its own.
	e.buf = append(e.buf, s...)
	copy(e.buf[e.caret+len(s):], e.buf[e.caret:])
	copy(e.buf[e.caret:], s)
	e.caret += len(s)
	e.commit()
	return true
}

// backspace deletes the character before the caret and reports whether it
// deleted anything.
func (e *editor) backspace() bool {
	if e.caret == 0 {
		return false
	}
	_, size := utf8.DecodeLastRune(e.buf[:e.caret])
	e.buf = append(e.buf[:e.caret-size], e.buf[e.caret:]...)
	e.caret -= size
	e.commit()
	return true
}

// delete deletes the character after the caret and reports whether it
// deleted anything. The caret does not move.
func (e *editor) delete() bool {
	if e.caret >= len(e.buf) {
		return false
	}
	_, size := utf8.DecodeRune(e.buf[e.caret:])
	e.buf = append(e.buf[:e.caret], e.buf[e.caret+size:]...)
	e.commit()
	return true
}

// left moves the caret one character back and reports whether it moved.
func (e *editor) left() bool {
	if e.caret == 0 {
		return false
	}
	_, size := utf8.DecodeLastRune(e.buf[:e.caret])
	e.caret -= size
	return true
}

// right moves the caret one character forward and reports whether it moved.
func (e *editor) right() bool {
	if e.caret >= len(e.buf) {
		return false
	}
	_, size := utf8.DecodeRune(e.buf[e.caret:])
	e.caret += size
	return true
}

// home moves the caret to the start and reports whether it moved.
func (e *editor) home() bool {
	if e.caret == 0 {
		return false
	}
	e.caret = 0
	return true
}

// end moves the caret to the end and reports whether it moved.
func (e *editor) end() bool {
	if e.caret == len(e.buf) {
		return false
	}
	e.caret = len(e.buf)
	return true
}

// setText replaces the text with a sanitized s and puts the caret at the
// end. It reports whether the text or the caret changed.
func (e *editor) setText(s string) bool {
	s = sanitize(s)
	if s == e.text {
		return e.end()
	}
	e.buf = append(e.buf[:0], s...)
	e.caret = len(e.buf)
	// s is already the string commit would build, so use it.
	e.text, e.version = s, e.version+1
	return true
}

// setCaret moves the caret to offset, clamped into the text and rounded
// backwards to the character boundary at or before it, and reports whether
// it moved.
func (e *editor) setCaret(offset int) bool {
	if offset < 0 {
		offset = 0
	}
	if offset > len(e.buf) {
		offset = len(e.buf)
	}
	for offset > 0 && offset < len(e.buf) && !utf8.RuneStart(e.buf[offset]) {
		offset--
	}
	if offset == e.caret {
		return false
	}
	e.caret = offset
	return true
}

// sanitize drops the control characters of s and replaces every byte that
// is not valid UTF-8 with U+FFFD.
//
// Text that is already clean is returned as it came, without building a
// string, because that is what every keystroke of a real keyboard is:
// keyboard.Event.Text carries no control characters. The public API cannot
// rely on that, though — a caller may feed it keyboard.Composer output,
// which returns "\r" for Return, or bytes of its own.
func sanitize(s string) string {
	if isClean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isClean reports whether s can be stored as it is. A real U+FFFD in the
// input answers false, like the invalid byte it cannot be told apart from
// here; sanitize then writes the same character back, so the only cost is
// one string nobody was going to notice.
func isClean(s string) bool {
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 5: Comprobar que pasa, con carrera y fuzz.**

Run: `go test ./widget/ -race -count=2` → PASS.
Run: `go test ./widget/ -run FuzzEditor -fuzz FuzzEditor -fuzztime 30s` → PASS, sin *crashers*. Si el fuzz deja un caso en `widget/testdata/fuzz/`, **no se borra**: se arregla el código y el caso se queda como semilla.
Run: `go vet ./...` → sin salida.

- [ ] **Step 6: Commit.**

```bash
git add widget/key.go widget/editor.go widget/editor_test.go
git commit -m "$(cat <<'EOF'
widget: add the editing keys and the text editor model

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XEKsqW1CKz9Kav57ei3LuH
EOF
)"
```

---

### Task 2: la vista desplazada y sus primitivas

La pieza donde está el rendimiento: **todo se mide relativo al ancla**, con `Measure` sobre subcadenas y búsquedas por duplicación y bisección. Nada mide desde el byte 0 y nada suma anchos carácter a carácter.

**Files:**
- Create: `widget/textview.go`
- Create: `widget/textview_test.go`

**Interfaces:**
- Consumes: `Font` (de `widget/font.go`: `Measure(string) float32`, `Draw(*canvas.Canvas, canvas.Point, string, canvas.Color, canvas.Rect)`).
- Produces:
  - `type view struct { anchor int; caretX float32; visEnd int; viewW float32; innerW float32 }`.
  - `func (v *view) sync(f Font, text string, caret int, viewW, innerW float32)`.
  - `func (v *view) hit(f Font, text string, rel float32) int`.
  - `func measure(f Font, s string) float32`, `func budget(w float32) float32`.
  - `func floorBoundary(s string, i int) int`, `func ceilBoundary(s string, i int) int`, `func nextBoundary(s string, i int) int`.
  - `func fitBack(f Font, s string, i int, limit float32) int`, `func fitFwd(f Font, s string, i int, limit float32) int`.
  - En el test, las dos `Font` falsas: `type runeFont struct{…}` con `newRuneFont(advance float32) *runeFont`, `newKernFont(advance float32) *runeFont`, los contadores `measured`, `drawn`, `calls`, los campos `at`, `text`, `col`, `clip` y `reset()`.

- [ ] **Step 1: Escribir los tests que fallan, `widget/textview_test.go`.**

Las dos fuentes falsas van aquí y las usan también las tareas 3 y 4 (mismo paquete). La de `button_test.go` mide **por bytes**, con lo que «á» mediría el doble que «a»; esa no sirve para geometría.

```go
package widget

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/romycode/ggui/canvas"
)

// runeFont is the fake Font the text field is tested against: a fixed
// advance per *rune*, not per byte like the one in button_test.go, because
// a per-byte font would make "á" twice as wide as "a" and freeze that in
// the expectations of every geometry test here.
//
// It also counts what it is asked, which is how the work assertions check
// that a field holding 100.000 characters measures and draws no more of
// them than one holding 1.000.
type runeFont struct {
	advance float32
	// kern is added to the advance of two adjacent runes. A font with one
	// of these is not additive — Measure("AV") is not Measure("A") plus
	// Measure("V") — which is what catches any code that adds widths up
	// instead of measuring the substring it means.
	kern map[[2]rune]float32

	measured int // runes Measure has seen
	drawn    int // runes Draw has seen
	calls    int // Measure calls

	at   canvas.Point
	text string
	col  canvas.Color
	clip canvas.Rect
}

func newRuneFont(advance float32) *runeFont { return &runeFont{advance: advance} }

// newKernFont is the same font with one kerning pair: "AV" is three units
// narrower than an A and a V apart.
func newKernFont(advance float32) *runeFont {
	return &runeFont{advance: advance, kern: map[[2]rune]float32{{'A', 'V'}: -3}}
}

func (f *runeFont) Measure(s string) float32 {
	f.calls++
	var w float32
	prev := rune(-1)
	for _, r := range s {
		f.measured++
		if prev >= 0 {
			w += f.kern[[2]rune{prev, r}]
		}
		w += f.advance
		prev = r
	}
	return w
}

func (f *runeFont) Draw(_ *canvas.Canvas, at canvas.Point, s string, col canvas.Color, clip canvas.Rect) {
	f.drawn += utf8.RuneCountInString(s)
	f.at, f.text, f.col, f.clip = at, s, col, clip
}

func (f *runeFont) reset() { f.measured, f.drawn, f.calls = 0, 0, 0 }

// boundaries lists every rune boundary of s, both ends included.
func boundaries(s string) []int {
	b := []int{0}
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		b = append(b, i)
	}
	return b
}

// slowFitBack is fitBack by brute force, for the searches to be compared
// against.
func slowFitBack(f Font, s string, i int, limit float32) int {
	i = floorBoundary(s, i)
	best := i
	for _, b := range boundaries(s) {
		if b <= i && measure(f, s[b:i]) <= budget(limit) && b < best {
			best = b
		}
	}
	return best
}

func slowFitFwd(f Font, s string, i int, limit float32) int {
	i = ceilBoundary(s, i)
	best := i
	for _, e := range boundaries(s) {
		if e >= i && measure(f, s[i:e]) <= budget(limit) && e > best {
			best = e
		}
	}
	return best
}

var sampleTexts = []string{
	"",
	"a",
	"hello world",
	"añañañ",
	"AVAVAVAWAY",
	"日本語のテキスト",
	"aé日𝄞bc𝄞",
	strings.Repeat("AV", 40),
	strings.Repeat("añ日𝄞", 30),
}

func TestFitBackAndFitFwdMatchBruteForce(t *testing.T) {
	for _, f := range []Font{newRuneFont(10), newKernFont(10), nil} {
		for _, s := range sampleTexts {
			for _, limit := range []float32{0, 1, 9.5, 10, 25, 100, 1e9} {
				for _, i := range boundaries(s) {
					if got, want := fitBack(f, s, i, limit), slowFitBack(f, s, i, limit); got != want {
						t.Fatalf("fitBack(%q, %d, %v) = %d, want %d", s, i, limit, got, want)
					}
					if got, want := fitFwd(f, s, i, limit), slowFitFwd(f, s, i, limit); got != want {
						t.Fatalf("fitFwd(%q, %d, %v) = %d, want %d", s, i, limit, got, want)
					}
				}
			}
		}
	}
}

// Offsets that are not boundaries, and offsets outside the text, are
// snapped rather than a panic.
func TestFitPrimitivesSnapAndClamp(t *testing.T) {
	f := newRuneFont(10)
	const s = "añ日"
	for i := -5; i <= len(s)+5; i++ {
		if got, want := fitBack(f, s, i, 15), slowFitBack(f, s, i, 15); got != want {
			t.Fatalf("fitBack(%d) = %d, want %d", i, got, want)
		}
		if got, want := fitFwd(f, s, i, 15), slowFitFwd(f, s, i, 15); got != want {
			t.Fatalf("fitFwd(%d) = %d, want %d", i, got, want)
		}
	}
}

func TestSyncIsIdempotentAndKeepsTheCaretInside(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	for _, f := range []Font{newRuneFont(10), newKernFont(10)} {
		for range 500 {
			s := sampleTexts[rng.Intn(len(sampleTexts))]
			bs := boundaries(s)
			caret := bs[rng.Intn(len(bs))]
			innerW := float32(rng.Intn(120))

			var v view
			v.anchor = bs[rng.Intn(len(bs))]
			v.sync(f, s, caret, max(innerW-2, 0), innerW)
			first := v
			v.sync(f, s, caret, max(innerW-2, 0), innerW)

			if v != first {
				t.Fatalf("sync is not idempotent on %q caret %d innerW %v: %+v then %+v", s, caret, innerW, first, v)
			}
			if v.caretX > v.viewW {
				t.Fatalf("caret at %v is outside the %v wide view (%q, caret %d)", v.caretX, v.viewW, s, caret)
			}
			if v.anchor > caret {
				t.Fatalf("anchor %d is past the caret %d", v.anchor, caret)
			}
		}
	}
}

// Rule 3: whatever the anchor was, a text that fits entirely leaves it at
// the start rather than scrolled with blank space on the right.
func TestSyncPullsTheAnchorBackWhenTheTextFits(t *testing.T) {
	f := newRuneFont(10)
	v := view{anchor: 6}
	v.sync(f, "abcdefgh", 8, 200, 202)
	if v.anchor != 0 {
		t.Fatalf("anchor = %d for a text that fits, want 0", v.anchor)
	}
}

// Deleting in the middle of a scrolled field used to leave text off the
// left edge and blank space on the right; rule 3 is what closes that hole.
func TestSyncClosesTheGapAfterDeletingInAScrolledField(t *testing.T) {
	f := newRuneFont(10)
	s := strings.Repeat("x", 20) // 200 units
	v := view{anchor: 15}
	v.sync(f, s, 20, 100, 102)
	// The last ten characters fill the 100 units: the anchor cannot be
	// further right than that.
	if v.anchor != 10 {
		t.Fatalf("anchor = %d, want 10", v.anchor)
	}
}

// The central property of the hit test: clicking exactly on a boundary
// lands on that boundary, with a font whose advances do not add up.
func TestHitLandsOnEveryVisibleBoundary(t *testing.T) {
	f := newKernFont(10)
	s := strings.Repeat("AVWañ日", 20)

	for _, caret := range []int{0, len(s) / 2, len(s)} {
		var v view
		v.sync(f, s, caret, 98, 100)
		for _, j := range boundaries(s) {
			if j < v.anchor || j > v.visEnd {
				continue
			}
			rel := measure(f, s[v.anchor:j])
			if rel > v.viewW {
				continue
			}
			if got := v.hit(f, s, rel); got != j {
				t.Fatalf("hit at the boundary %d (rel %v) = %d", j, rel, got)
			}
		}
	}
}

func TestHitClampsToTheVisibleRun(t *testing.T) {
	f := newRuneFont(10)
	s := strings.Repeat("x", 100)
	var v view
	v.sync(f, s, 0, 98, 100)

	if got := v.hit(f, s, -50); got != v.anchor {
		t.Fatalf("a click left of the area = %d, want the anchor %d", got, v.anchor)
	}
	far := v.hit(f, s, 1e6)
	if far == len(s) {
		t.Fatal("a click past the right edge jumped to the end of the text")
	}
	if far != 9 {
		t.Fatalf("a click past the right edge = %d, want the last boundary that fits, 9", far)
	}
}

func TestNilFontKeepsTheViewAtTheStart(t *testing.T) {
	var v view
	v.sync(nil, "hello world", 11, 98, 100)
	if v.anchor != 0 || v.caretX != 0 {
		t.Fatalf("view with no font = %+v, want anchor 0 and caret 0", v)
	}
}

// Rule 1: a caret moved left of the anchor drags the anchor with it.
func TestSyncDragsTheAnchorWithACaretMovedLeft(t *testing.T) {
	f := newRuneFont(10)
	s := strings.Repeat("x", 60)
	var v view
	v.sync(f, s, 60, 98, 100)
	if v.anchor == 0 {
		t.Fatal("a text far longer than the area did not scroll")
	}
	v.sync(f, s, 3, 98, 100)
	if v.anchor != 3 {
		t.Fatalf("anchor = %d after the caret moved left of it, want 3", v.anchor)
	}
	if v.caretX != 0 {
		t.Fatalf("caretX = %v with the caret on the anchor, want 0", v.caretX)
	}
}

// hostileFont answers what a Font never should, which the view has to take
// as no width at all rather than propagate into the searches.
type hostileFont struct{ w float32 }

func (f hostileFont) Measure(string) float32                                             { return f.w }
func (hostileFont) Draw(*canvas.Canvas, canvas.Point, string, canvas.Color, canvas.Rect) {}

func TestAbsurdWidthsAreTakenAsZero(t *testing.T) {
	for _, w := range []float32{-10, float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.NaN())} {
		f := hostileFont{w: w}
		if got := measure(f, "abc"); got != 0 {
			t.Errorf("measure with a font returning %v = %v, want 0", w, got)
		}
		var v view
		v.sync(f, "hello world", 11, float32(math.NaN()), float32(math.NaN()))
		if v.anchor != 0 || v.caretX != 0 || v.viewW != 0 || v.innerW != 0 {
			t.Errorf("view with absurd widths = %+v, want everything at zero", v)
		}
		if got := v.hit(f, "hello world", -1); got != 0 {
			t.Errorf("a click left of the area = %d, want the anchor", got)
		}
		// Every boundary measures the same nothing, so there is no
		// position to tell from any other and the click lands at the end
		// of the run — which, with no widths, is the whole text.
		if got := v.hit(f, "hello world", 5); got != len("hello world") {
			t.Errorf("a click with a font that measures nothing = %d, want the end of the run", got)
		}
	}
}
```

El import de este fichero es, por tanto, `math`, `math/rand`, `strings`, `testing`, `unicode/utf8` y `github.com/romycode/ggui/canvas`.

- [ ] **Step 2: Comprobar que falla.**

Run: `go test ./widget/ -run 'TestFit|TestSync|TestHit|TestNilFont'`
Expected: FAIL de compilación, `undefined: view`.

- [ ] **Step 3: Escribir `widget/textview.go`.**

```go
package widget

import (
	"math"
	"unicode/utf8"
)

// view is the scrolled window onto a text: which character is the first
// visible one, and the measurements a frame needs. Everything here is
// relative to that character — the anchor — and nothing is ever measured
// from the start of the text, which is what keeps the work per event
// bounded by what is on screen instead of by how much has been typed.
type view struct {
	// anchor is the byte offset of the first visible character, on a rune
	// boundary. The visible run is drawn from the fixed origin of the text
	// area, so scrolling moves by whole characters and never by a fraction
	// of one.
	anchor int
	// caretX is the caret's offset from that origin, the width of
	// text[anchor:caret].
	caretX float32
	// visEnd is the first boundary past the text area, so text[anchor:visEnd]
	// includes the character that pokes out of the right edge and is clipped.
	visEnd int
	// viewW is the width the caret is kept inside: the text area less the
	// caret's own width, so a caret at the end of the run still fits.
	viewW float32
	// innerW is the text area's width, which is also the clip handed to
	// [Font.Draw]. It is cached to notice a Bounds the caller resized.
	innerW float32
}

// measure is [Font.Measure] with the answers a Font is not trusted to get
// right taken out: no font at all, and a width that is negative or not a
// number, measure zero.
func measure(f Font, s string) float32 {
	if f == nil || s == "" {
		return 0
	}
	w := f.Measure(s)
	if !(w >= 0) || math.IsInf(float64(w), 1) {
		return 0
	}
	return w
}

// budget clamps a width to something the searches can work with: anything
// negative or not a number is no room at all.
func budget(w float32) float32 {
	if !(w >= 0) {
		return 0
	}
	return w
}

// floorBoundary returns the rune boundary at or before i.
func floorBoundary(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// ceilBoundary returns the rune boundary at or after i.
func ceilBoundary(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}

// nextBoundary returns the boundary after i, which must be one itself.
func nextBoundary(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	if i < 0 {
		i = 0
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	return i + size
}

// fitBack returns the leftmost rune boundary b <= i whose text up to i
// fits in limit: measure(s[b:i]) <= limit.
//
// It doubles its way back from i until it overshoots and then bisects, so
// the substrings it measures are a few times what fits in limit and never
// the whole text. Every measurement is of one substring — advances are not
// additive under kerning, so nothing here adds widths up.
func fitBack(f Font, s string, i int, limit float32) int {
	limit = budget(limit)
	i = floorBoundary(s, i)
	if i == 0 {
		return 0
	}

	// lo fits (the empty string always does), hi does not.
	lo, hi := i, -1
	for step := 1; ; step *= 2 {
		cand := floorBoundary(s, i-step)
		if measure(f, s[cand:i]) <= limit {
			lo = cand
			if cand == 0 {
				return 0
			}
			continue
		}
		hi = cand
		break
	}

	for nextBoundary(s, hi) < lo {
		mid := floorBoundary(s, hi+(lo-hi)/2)
		if mid <= hi || mid >= lo {
			mid = nextBoundary(s, hi)
		}
		if measure(f, s[mid:i]) <= limit {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// fitFwd is fitBack the other way round: the rightmost rune boundary
// e >= i whose text from i fits in limit, measure(s[i:e]) <= limit.
func fitFwd(f Font, s string, i int, limit float32) int {
	limit = budget(limit)
	i = ceilBoundary(s, i)
	if i >= len(s) {
		return len(s)
	}

	// fit fits, over does not.
	fit, over := i, -1
	for step := 1; ; step *= 2 {
		cand := ceilBoundary(s, i+step)
		if measure(f, s[i:cand]) <= limit {
			fit = cand
			if cand == len(s) {
				return len(s)
			}
			continue
		}
		over = cand
		break
	}

	for nextBoundary(s, fit) < over {
		mid := ceilBoundary(s, fit+(over-fit)/2)
		if mid <= fit || mid >= over {
			mid = nextBoundary(s, fit)
		}
		if measure(f, s[i:mid]) <= limit {
			fit = mid
		} else {
			over = mid
		}
	}
	return fit
}

// sync applies the view rules after any change to the text, the caret or
// the width, and recomputes what a frame draws. It is idempotent: running
// it twice over the same state gives the same view.
func (v *view) sync(f Font, text string, caret int, viewW, innerW float32) {
	v.viewW, v.innerW = budget(viewW), budget(innerW)
	caret = floorBoundary(text, caret)

	a := floorBoundary(text, min(max(v.anchor, 0), len(text)))
	// fill is the rightmost the anchor may ever be: put it further and the
	// text would not reach the right edge. With the caret at the end — the
	// common case, typing — it is also what rule 2 asks for, so the two
	// rules share the one search.
	fill := fitBack(f, text, len(text), v.viewW)

	// 1. The caret is left of the anchor: it drags the anchor with it.
	if caret < a {
		a = caret
	}
	// 2. The caret is past the right edge: pull the anchor forward just
	//    enough to bring it back in.
	b := fill
	if caret != len(text) {
		b = fitBack(f, text, caret, v.viewW)
	}
	if b > a {
		a = b
	}
	// 3. No gap at the right: if everything from the anchor fits, pull the
	//    anchor back until the area is full. This is what closes the hole
	//    left by deleting in the middle of a scrolled field, and what puts
	//    a text shorter than the area back at anchor 0.
	if fill < a {
		a = fill
	}

	v.anchor = a
	v.caretX = measure(f, text[a:caret])
	v.visEnd = nextBoundary(text, fitFwd(f, text, a, v.innerW))
}

// hit returns the character boundary a click rel units right of the text
// area's origin lands on. It is the nearest boundary of the visible run,
// with ties going left, and it never leaves that run: a click past the
// last visible character lands at the end of what is on screen rather than
// at the end of the text, so clicking does not scroll.
func (v *view) hit(f Font, text string, rel float32) int {
	if !(rel > 0) {
		return v.anchor
	}
	if rel > v.viewW {
		rel = v.viewW
	}

	left := fitFwd(f, text, v.anchor, rel)
	right := nextBoundary(text, left)
	if right <= left || right > len(text) {
		return left
	}
	wr := measure(f, text[v.anchor:right])
	if wr > v.viewW {
		return left
	}
	if wr-rel < rel-measure(f, text[v.anchor:left]) {
		return right
	}
	return left
}
```

- [ ] **Step 4: Comprobar que pasa.**

Run: `go test ./widget/ -race -count=2` → PASS.
Run: `go vet ./...` → sin salida.

- [ ] **Step 5: Commit.**

```bash
git add widget/textview.go widget/textview_test.go
git commit -m "$(cat <<'EOF'
widget: add the anchor-relative scrolled view for text

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XEKsqW1CKz9Kav57ei3LuH
EOF
)"
```

---

### Task 3: `TextField`, el estado y los valores devueltos

Todo menos `Draw` y `PointerDown`, que son la tarea 4.

**Files:**
- Create: `widget/textfield.go`
- Create: `widget/textfield_test.go`

**Interfaces:**
- Consumes: `editor` y sus métodos (Tarea 1); `view`, `measure`, `budget` (Tarea 2); `Font` (`widget/font.go`); `Focusable` (`widget/focus.go`); `contains(r canvas.Rect, x, y float32) bool` (ya existe en `widget/button.go`, **no se redefine**).
- Produces:
  - `type TextFieldStyle struct { Fill, Text, Placeholder, Caret, DisabledFill, DisabledText, BorderColor, FocusBorder canvas.Color; Border, Corner, Padding, CaretWidth float32 }`.
  - `func DefaultTextFieldStyle() TextFieldStyle`.
  - `type TextField struct { Bounds canvas.Rect; Font Font; Style TextFieldStyle; Placeholder string; OnChange, OnSubmit func(text string); Disabled bool; … }`.
  - `func NewTextField(placeholder string, font Font) *TextField`.
  - Métodos: `Text() string`, `Caret() int`, `Focused() bool`, `SetText(string) bool`, `SetCaret(int) bool`, `Insert(string) bool`, `KeyDown(Key) bool`, `KeyUp(Key) bool`, `SetFocused(bool) bool`, `SetCaretVisible(bool) bool`.
  - No exportados que la tarea 4 usa: `type fieldVisual struct`, `visual() fieldVisual`, `caretShown() bool`, `placeholderShown() bool`, `metrics() (border, padding float32)`, `textX() float32`, `innerWidth() float32`, `visibleText() string`, `ensure()`, `sync()`.
  - `var _ Focusable = (*TextField)(nil)`.

- [ ] **Step 1: Escribir los tests que fallan, `widget/textfield_test.go`.**

El helper `newTestCanvas(t, w, h, pad)` ya existe en `button_test.go` y se reutiliza tal cual.

```go
package widget

import (
	"strings"
	"testing"

	"github.com/romycode/ggui/canvas"
)

var fieldBounds = canvas.Rect{X: 100, Y: 50, Width: 200, Height: 44}

// newTestField returns a focused field 200 units wide drawn with a 10-unit
// fixed-pitch font: 200 - 2*(2+12) = 172 units of text area, 170 of which
// the caret is kept inside.
func newTestField(t *testing.T) (*TextField, *runeFont) {
	t.Helper()
	font := newRuneFont(10)
	f := NewTextField("type here", font)
	f.Bounds = fieldBounds
	f.SetFocused(true)
	return f, font
}

func TestFieldGeometry(t *testing.T) {
	f, _ := newTestField(t)
	if got, want := f.innerWidth(), float32(172); got != want {
		t.Fatalf("innerW = %v, want %v", got, want)
	}
	if got, want := f.textX(), float32(114); got != want {
		t.Fatalf("textX = %v, want %v", got, want)
	}
	f.SetText("abc")
	if got, want := f.vw.viewW, float32(170); got != want {
		t.Fatalf("viewW = %v, want %v", got, want)
	}
	if got, want := f.vw.caretX, float32(30); got != want {
		t.Fatalf("caretX = %v, want %v", got, want)
	}
}

func TestTypingScrollsAndKeepsTheCaretVisible(t *testing.T) {
	f, font := newTestField(t)
	for range 40 {
		f.Insert("x")
	}
	if f.vw.anchor == 0 {
		t.Fatal("a text far longer than the field did not scroll")
	}
	if f.vw.caretX > f.vw.viewW {
		t.Fatalf("caret at %v, outside the %v wide view", f.vw.caretX, f.vw.viewW)
	}
	if got := measure(font, f.Text()[f.vw.anchor:f.Caret()]); got != f.vw.caretX {
		t.Fatalf("caretX %v does not match the text from the anchor, %v", f.vw.caretX, got)
	}
}

func TestKeysMoveEditAndReportChanges(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText("abc")

	if f.KeyDown(KeyRight) {
		t.Fatal("moving right at the end reported a change")
	}
	if !f.KeyDown(KeyLeft) {
		t.Fatal("moving left reported no change")
	}
	if f.Caret() != 2 {
		t.Fatalf("caret %d after Left, want 2", f.Caret())
	}
	if !f.KeyDown(KeyHome) || f.Caret() != 0 {
		t.Fatalf("Home left the caret at %d", f.Caret())
	}
	if f.KeyDown(KeyBackspace) {
		t.Fatal("Backspace at the start reported a change")
	}
	if !f.KeyDown(KeyDelete) || f.Text() != "bc" {
		t.Fatalf("text %q after Delete, want %q", f.Text(), "bc")
	}
	if !f.KeyDown(KeyEnd) || f.Caret() != 2 {
		t.Fatalf("End left the caret at %d", f.Caret())
	}
	if !f.KeyDown(KeyBackspace) || f.Text() != "b" {
		t.Fatalf("text %q after Backspace, want %q", f.Text(), "b")
	}
	if f.KeyDown(KeySpace) || f.KeyDown(KeyEscape) || f.KeyDown(KeyTab) || f.KeyDown(KeyNone) {
		t.Fatal("a key the field does not act on reported a change")
	}
	if f.KeyUp(KeyBackspace) {
		t.Fatal("a key release reported a change")
	}
}

func TestCallbacks(t *testing.T) {
	f, _ := newTestField(t)
	var changes, submits []string
	f.OnChange = func(s string) { changes = append(changes, s) }
	f.OnSubmit = func(s string) { submits = append(submits, s) }

	f.SetText("ab") // the program's own change: no OnChange
	f.Insert("c")
	f.KeyDown(KeyBackspace)
	f.KeyDown(KeyLeft)
	if f.KeyDown(KeyEnter) {
		t.Fatal("Enter reported a visible change")
	}
	if len(changes) != 2 || changes[0] != "abc" || changes[1] != "ab" {
		t.Fatalf("OnChange saw %q", changes)
	}
	if len(submits) != 1 || submits[0] != "ab" {
		t.Fatalf("OnSubmit saw %q", submits)
	}
}

func TestUnfocusedAndDisabledFieldsIgnoreInput(t *testing.T) {
	f, _ := newTestField(t)
	f.SetFocused(false)
	if f.Insert("x") || f.KeyDown(KeyBackspace) || f.Text() != "" {
		t.Fatal("an unfocused field took input")
	}
	if !f.SetText("abc") {
		t.Fatal("SetText on an unfocused field reported no change")
	}

	f.SetFocused(true)
	f.Disabled = true
	if f.Insert("x") || f.KeyDown(KeyDelete) {
		t.Fatal("a disabled field took input")
	}
	f.focused = false
	if f.SetFocused(true) || f.Focused() {
		t.Fatal("a disabled field took the focus")
	}
}

func TestFocusAndCaretVisibility(t *testing.T) {
	f, _ := newTestField(t)
	f.SetFocused(false)
	if f.SetCaretVisible(false) {
		t.Fatal("hiding the caret of an unfocused field reported a change")
	}
	if !f.SetFocused(true) {
		t.Fatal("focusing reported no change")
	}
	if !f.caretShown() {
		t.Fatal("gaining the focus left the caret hidden")
	}
	if f.SetFocused(true) {
		t.Fatal("focusing an already focused field reported a change")
	}
	if !f.SetCaretVisible(false) || f.caretShown() {
		t.Fatal("hiding the caret of a focused field did not report a change")
	}
	f.Style.CaretWidth = 0
	f.SetCaretVisible(true)
	if f.caretShown() {
		t.Fatal("a zero-width caret is shown")
	}
}

// Editing and moving the caret restart the blink.
func TestUserActionsShowTheCaretAgain(t *testing.T) {
	cases := []struct {
		name string
		act  func(f *TextField)
	}{
		{"typing", func(f *TextField) { f.Insert("a") }},
		{"backspace", func(f *TextField) { f.SetText("ab"); f.KeyDown(KeyBackspace) }},
		{"moving", func(f *TextField) { f.SetText("ab"); f.KeyDown(KeyLeft) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _ := newTestField(t)
			f.SetCaretVisible(false)
			c.act(f)
			if !f.caretShown() {
				t.Fatal("the caret is still hidden after the user acted")
			}
		})
	}
}

func TestSetTextAndSetCaretSemantics(t *testing.T) {
	f, _ := newTestField(t)
	if f.SetText("") {
		t.Fatal("setting the same empty text reported a change")
	}
	if !f.SetText("hello") {
		t.Fatal("setting new text reported no change")
	}
	if f.SetText("hello") {
		t.Fatal("setting the same text with the caret at the end reported a change")
	}
	if !f.SetCaret(2) || f.Caret() != 2 {
		t.Fatalf("SetCaret(2) left the caret at %d", f.Caret())
	}
	if f.SetCaret(2) {
		t.Fatal("SetCaret to the same place reported a change")
	}
	f.SetText("añ")
	if !f.SetCaret(2) || f.Caret() != 1 {
		t.Fatalf("SetCaret(2) inside a character left the caret at %d, want 1", f.Caret())
	}
}

// A field whose width the caller changed must re-apply the view rules on
// the next call that reads it, without being told.
func TestAWidthChangeReachesTheView(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 30))
	wide := f.vw.anchor

	f.Bounds.Width = 100
	f.ensure()
	if f.vw.anchor == wide {
		t.Fatal("narrowing the field did not move the anchor")
	}
	if f.vw.caretX > f.vw.viewW {
		t.Fatalf("caret at %v is outside the %v wide view after the resize", f.vw.caretX, f.vw.viewW)
	}
}

// A field that is focused and then disabled shows its placeholder again:
// unfocusing it has to report that, or the frame keeps the caret nobody
// can justify.
func TestDisabledFocusedEmptyFieldReportsThePlaceholderComingBack(t *testing.T) {
	f, _ := newTestField(t)
	f.Disabled = true
	if !f.SetFocused(false) {
		t.Fatal("unfocusing a disabled empty field, which brings its placeholder back, reported no change")
	}
}

func TestTextAndCaretMovesDoNotAllocate(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 60))

	if n := testing.AllocsPerRun(100, func() { _ = f.Text() }); n != 0 {
		t.Fatalf("Text allocates %v times per call, want 0", n)
	}
	if n := testing.AllocsPerRun(100, func() { f.KeyDown(KeyLeft); f.KeyDown(KeyRight) }); n != 0 {
		t.Fatalf("moving the caret allocates %v times per call, want 0", n)
	}
}

func TestInsertAllocatesAtMostOnce(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 4096))

	n := testing.AllocsPerRun(200, func() {
		f.Insert("y")
		f.KeyDown(KeyBackspace)
	})
	if n > 2 {
		t.Fatalf("an insert and a backspace allocate %v times, want at most one each", n)
	}
}
```

- [ ] **Step 2: Comprobar que falla.**

Run: `go test ./widget/ -run 'TestField|TestTyping|TestKeys|TestCallbacks|TestUnfocused|TestFocusAnd|TestUserActions|TestSetText|TestAWidth|TestDisabledFocused|TestTextAndCaret|TestInsertAllocates'`
Expected: FAIL de compilación, `undefined: NewTextField`.

- [ ] **Step 3: Escribir `widget/textfield.go`** (sin `Draw` ni `PointerDown`: llegan en la tarea 4).

```go
package widget

import "github.com/romycode/ggui/canvas"

// TextFieldStyle is the look of a [TextField]: the colors of the box and
// of what sits in it, plus the geometry the text is laid out against. The
// zero value draws nothing visible, so start from [DefaultTextFieldStyle].
type TextFieldStyle struct {
	// Fill is the background color.
	Fill canvas.Color
	// Text is the color of the field's contents.
	Text canvas.Color
	// Placeholder is the color of [TextField.Placeholder], shown while the
	// field is empty and unfocused.
	Placeholder canvas.Color
	// Caret is the caret's color. A transparent one hides the caret, and
	// then focusing the field reports no repaint, because none would look
	// different.
	Caret canvas.Color
	// DisabledFill replaces Fill while the field is disabled.
	DisabledFill canvas.Color
	// DisabledText replaces Text while the field is disabled.
	DisabledText canvas.Color

	// BorderColor is the resting outline color.
	BorderColor canvas.Color
	// FocusBorder replaces BorderColor while the field holds the focus and
	// is not disabled. Unlike [Button], a field shows the focus by its own
	// outline rather than by a ring: it has no fill states of its own for
	// a ring to have to read against.
	FocusBorder canvas.Color
	// Border is the outline width in logical units. Zero draws no outline.
	Border float32
	// Corner is the corner radius in logical units.
	Corner float32
	// Padding is the gap between the border and the text, in logical units.
	Padding float32
	// CaretWidth is the caret's width in logical units. Zero draws no
	// caret.
	CaretWidth float32
}

// DefaultTextFieldStyle returns a dark-theme style to go with
// [DefaultButtonStyle]: a sunken box that outlines in accent blue while it
// holds the focus.
func DefaultTextFieldStyle() TextFieldStyle {
	return TextFieldStyle{
		Fill:         canvas.Color{R: 0x2b, G: 0x30, B: 0x38, A: 0xff},
		Text:         canvas.Color{R: 0xe6, G: 0xe9, B: 0xef, A: 0xff},
		Placeholder:  canvas.Color{R: 0x6b, G: 0x72, B: 0x80, A: 0xff},
		Caret:        canvas.Color{R: 0x4c, G: 0x9a, B: 0xff, A: 0xff},
		DisabledFill: canvas.Color{R: 0x24, G: 0x28, B: 0x2f, A: 0xff},
		DisabledText: canvas.Color{R: 0x6b, G: 0x72, B: 0x80, A: 0xff},
		BorderColor:  canvas.Color{R: 0x3a, G: 0x40, B: 0x49, A: 0xff},
		FocusBorder:  canvas.Color{R: 0x4c, G: 0x9a, B: 0xff, A: 0xff},
		Border:       2,
		Corner:       8,
		Padding:      12,
		CaretWidth:   2,
	}
}

// TextField is a single-line text field: it shows one line of text with a
// caret the user moves freely, scrolls horizontally when the text does not
// fit, and shows Placeholder while it is empty and unfocused.
//
// TextField implements [Focusable]. It never takes the focus itself; the
// caller gives it, and a click is three calls in the caller's code:
//
//	if contains(field.Bounds, x, y) {
//	    changed := chain.Focus(field)
//	    changed = field.PointerDown(x, y) || changed
//	    if changed { redraw() }
//	}
//
// The caret does not blink by itself — this package has no clock. The
// application flips it with [TextField.SetCaretVisible] from a timer of
// its own; every edit, caret move and click turns it back on, which is the
// reset a user expects while typing.
//
// The caret moves by Unicode characters and not by grapheme clusters: a
// lone combining mark or a composed emoji takes more than one step.
//
// Font must be set before the field holds any text: the view caches
// measurements, and replacing the font afterwards is not noticed. Build a
// field with [NewTextField].
type TextField struct {
	// Bounds is where the field sits, in logical units. The caller sets it
	// — typically every frame, from its own layout. A width the caller
	// changed is noticed by the next call that reads it.
	Bounds canvas.Rect
	// Font measures and draws the text. A nil Font edits and draws the box
	// as usual but shows no text: with no widths, the anchor stays at the
	// start and the caret at the origin.
	Font Font
	// Style is the field's look. It may be changed at any time.
	Style TextFieldStyle
	// Placeholder is shown, in the placeholder color, while the field is
	// empty and unfocused.
	Placeholder string
	// OnChange is called with the new text after an edit the user made —
	// typing, Backspace, Delete. SetText does not call it: that is the
	// program's own change, and a listener that wrote back would loop. It
	// may be nil.
	OnChange func(text string)
	// OnSubmit is called with the current text when [KeyEnter] reaches the
	// focused field. It may be nil.
	OnSubmit func(text string)
	// Disabled makes the field ignore keys, text and clicks, refuse the
	// focus, and draw in its disabled colors.
	Disabled bool

	ed editor
	vw view
	// focused is whether the caller has given this field the keyboard
	// focus.
	focused bool
	// caretOn is the blink phase the application last set.
	caretOn bool
}

var _ Focusable = (*TextField)(nil)

// NewTextField returns an empty field with the given placeholder, drawn
// with font and [DefaultTextFieldStyle]. Set Bounds before use.
func NewTextField(placeholder string, font Font) *TextField {
	return &TextField{
		Placeholder: placeholder,
		Font:        font,
		Style:       DefaultTextFieldStyle(),
		caretOn:     true,
	}
}

// Text returns the field's contents. It allocates nothing: the string is
// rebuilt when the text changes, not when it is read.
func (t *TextField) Text() string { return t.ed.text }

// Caret returns the caret's position as a byte offset into
// [TextField.Text], always on a character boundary.
func (t *TextField) Caret() int { return t.ed.caret }

// Focused reports whether the field holds the keyboard focus, as the
// caller last set it.
func (t *TextField) Focused() bool { return t.focused }

// SetText replaces the contents with s, dropping its control characters
// and replacing invalid UTF-8 with U+FFFD, and puts the caret at the end.
// It reports whether a repaint would look different. It does not call
// OnChange.
func (t *TextField) SetText(s string) bool {
	t.ensure()
	before := t.visual()
	t.ed.setText(s)
	// The old anchor means nothing against new text; start from the left
	// and let the rules scroll to wherever the caret ended up.
	t.vw.anchor = 0
	t.sync()
	return t.visual() != before
}

// SetCaret moves the caret to offset, clamped into the text and rounded
// backwards to the character boundary at or before it, and reports whether
// a repaint would look different.
func (t *TextField) SetCaret(offset int) bool {
	t.ensure()
	before := t.visual()
	if t.ed.setCaret(offset) {
		t.sync()
	}
	return t.visual() != before
}

// Insert puts already-composed text at the caret and reports whether a
// repaint would look different. Control characters are dropped and invalid
// UTF-8 becomes U+FFFD, so a caller may feed it whatever its composer
// produced. OnChange is called if the text changed.
//
// A field that is not focused, or that is disabled, ignores it, so the
// caller may hand every keystroke to each widget it owns without first
// working out whose it is.
func (t *TextField) Insert(text string) bool {
	if !t.focused || t.Disabled {
		return false
	}
	t.ensure()
	before := t.visual()
	if !t.ed.insert(text) {
		return t.visual() != before
	}
	t.caretOn = true
	t.sync()

	changed := t.visual() != before
	if t.OnChange != nil {
		t.OnChange(t.ed.text)
	}
	return changed
}

// KeyDown handles a key press and reports whether a repaint would look
// different. A field that is not focused, or that is disabled, ignores
// every key.
//
// [KeyLeft], [KeyRight], [KeyHome] and [KeyEnd] move the caret;
// [KeyBackspace] and [KeyDelete] edit and call OnChange; [KeyEnter] calls
// OnSubmit. Text arrives through [TextField.Insert] and not from here, so
// [KeySpace] is ignored like every other key the field does not act on,
// and nothing latches — a key repeat is harmless.
//
// Enter reports false even though it submitted: the bool answers only
// "would a repaint look different", and a caller whose OnSubmit changed
// the screen repaints on that account, exactly as it does after a click.
func (t *TextField) KeyDown(k Key) bool {
	if !t.focused || t.Disabled {
		return false
	}
	if k == KeyEnter {
		if t.OnSubmit != nil {
			t.OnSubmit(t.ed.text)
		}
		return false
	}

	t.ensure()
	before := t.visual()

	acted, edited := false, false
	switch k {
	case KeyLeft:
		acted = t.ed.left()
	case KeyRight:
		acted = t.ed.right()
	case KeyHome:
		acted = t.ed.home()
	case KeyEnd:
		acted = t.ed.end()
	case KeyBackspace:
		acted, edited = t.ed.backspace(), true
	case KeyDelete:
		acted, edited = t.ed.delete(), true
	default:
		return false
	}
	if !acted {
		return t.visual() != before
	}
	t.caretOn = true
	t.sync()

	changed := t.visual() != before
	if edited && t.OnChange != nil {
		t.OnChange(t.ed.text)
	}
	return changed
}

// KeyUp handles a key release and reports whether a repaint would look
// different. Nothing latches in a text field, so it never does anything;
// it is here because [Focusable] asks for it.
func (t *TextField) KeyUp(Key) bool { return false }

// SetFocused gives the field the keyboard focus or takes it away, and
// reports whether its appearance changed. Gaining the focus always leaves
// the caret showing, so a field the application had blinked off does not
// come back invisible.
//
// A disabled field refuses the focus, so a caller walking a focus order
// can offer it to each widget in turn and let them decline.
func (t *TextField) SetFocused(focused bool) bool {
	if focused && t.Disabled {
		return false
	}
	before := t.visual()
	if focused && !t.focused {
		t.caretOn = true
	}
	t.focused = focused
	return t.visual() != before
}

// SetCaretVisible sets the caret's blink phase and reports whether a
// repaint would look different. On a field that is not focused it stores
// the value and reports false — there is no caret to show — and focusing
// the field turns it back on anyway.
func (t *TextField) SetCaretVisible(v bool) bool {
	before := t.visual()
	t.caretOn = v
	return t.visual() != before
}

// fieldVisual is everything about the field a repaint would show. Every
// method compares it before and after, so the "did anything change" they
// report is exactly "would a repaint look different" — and adding state
// later (hover, a selection) does not mean rewriting a rule per method.
type fieldVisual struct {
	// version stands for the text itself: the same version is the same
	// bytes.
	version uint64
	// anchor and caretX are where the visible run starts and where the
	// caret sits within it. caretX is zero while the caret is not shown,
	// so a caret nobody can see cannot report a move.
	anchor int
	caretX float32
	// innerW is the text area's width. ensure normally equalizes it before
	// a comparison; it is here so that a change of width can never be
	// reported as nothing.
	innerW float32
	// caret, placeholder and focus are what is on screen rather than what
	// is set: a caret the style hides, or a placeholder an empty field is
	// too focused to show, must not be claimed as a repaint.
	caret       bool
	placeholder bool
	focus       bool
	disabled    bool
}

func (t *TextField) visual() fieldVisual {
	v := fieldVisual{
		version:     t.ed.version,
		anchor:      t.vw.anchor,
		innerW:      t.vw.innerW,
		caret:       t.caretShown(),
		placeholder: t.placeholderShown(),
		focus:       t.focused && !t.Disabled,
		disabled:    t.Disabled,
	}
	if v.caret {
		v.caretX = t.vw.caretX
	}
	return v
}

// caretShown is the single predicate behind both the drawing and the
// change reporting, so the two cannot drift into disagreeing about whether
// the caret is there.
func (t *TextField) caretShown() bool {
	st := &t.Style
	return t.focused && t.caretOn && !t.Disabled && st.CaretWidth > 0 && st.Caret.A > 0
}

// placeholderShown is the same for the placeholder, which stands in for
// the text while the field is empty and nobody is typing in it.
func (t *TextField) placeholderShown() bool {
	return t.ed.text == "" && !t.focused && t.Placeholder != ""
}

// metrics returns the border and padding the geometry is built from, with
// anything negative or not a number taken as zero.
func (t *TextField) metrics() (border, padding float32) {
	return budget(t.Style.Border), budget(t.Style.Padding)
}

// textX is the left edge of the text area, where the first visible
// character starts and which caretX is measured from.
func (t *TextField) textX() float32 {
	border, padding := t.metrics()
	return t.Bounds.X + border + padding
}

// innerWidth is the width of the text area: Bounds less the border and the
// padding on both sides, and never negative.
func (t *TextField) innerWidth() float32 {
	border, padding := t.metrics()
	return budget(t.Bounds.Width - 2*(border+padding))
}

// visibleText is the run this frame draws: a substring of the cached text,
// which costs nothing to take.
func (t *TextField) visibleText() string {
	s := t.ed.text
	a := min(max(t.vw.anchor, 0), len(s))
	e := min(max(t.vw.visEnd, a), len(s))
	return s[a:e]
}

// ensure re-applies the view rules if the text area's width has changed
// since they last ran. Bounds and Style are public fields the caller
// rewrites whenever it likes, so this is how a resize reaches the view;
// every method that reads the geometry calls it first. The rules cost what
// is on screen, so the frame a field is resized in is no dearer than any
// other.
func (t *TextField) ensure() {
	if t.innerWidth() != t.vw.innerW {
		t.sync()
	}
}

// sync applies the view rules to the current text, caret and width.
func (t *TextField) sync() {
	innerW := t.innerWidth()
	// The caret is kept inside the area less its own width, so that a
	// caret at the end of the visible run is drawn inside the field and
	// not on its border.
	t.vw.sync(t.Font, t.ed.text, t.ed.caret, budget(innerW-budget(t.Style.CaretWidth)), innerW)
}
```

- [ ] **Step 4: Comprobar que pasa.**

Run: `go test ./widget/ -race -count=2` → PASS.
Run: `go vet ./...` → sin salida. `go run ./cmd/docaudit` → `widget` sigue al 100 %.

- [ ] **Step 5: Commit.**

```bash
git add widget/textfield.go widget/textfield_test.go
git commit -m "$(cat <<'EOF'
widget: add TextField, its style and its repaint predicate

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XEKsqW1CKz9Kav57ei3LuH
EOF
)"
```

---

### Task 4: dibujo, clic, aserciones de rendimiento y documentación

**Files:**
- Modify: `widget/textfield.go` (añadir `Draw` y `PointerDown`)
- Modify: `widget/textfield_test.go` (añadir los tests de dibujo, de trabajo y los benchmarks)
- Modify: `widget/doc.go`
- Modify: `docs/widget.md`

**Interfaces:**
- Consumes: todo lo de las tareas 1-3, más `contains` (`widget/button.go`) y `newTestCanvas(t *testing.T, w, h, pad int) (*canvas.Canvas, []uint32)` (`widget/button_test.go`).
- Produces: `func (t *TextField) Draw(cv *canvas.Canvas)` y `func (t *TextField) PointerDown(x, y float32) bool`.

- [ ] **Step 1: Escribir los tests que fallan** (añadir al final de `widget/textfield_test.go`; el import de `fmt` hace falta para los benchmarks).

```go
func TestClickPlacesTheCaret(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText("abcdef")
	y := fieldBounds.Y + fieldBounds.Height/2

	// Boundary 3 sits 30 units right of the text origin.
	if !f.PointerDown(f.textX()+31, y) {
		t.Fatal("a click that moved the caret reported no change")
	}
	if f.Caret() != 3 {
		t.Fatalf("caret %d after a click at 31 units, want 3", f.Caret())
	}
	// 29 units round to the same boundary: nothing moved.
	if f.PointerDown(f.textX()+29, y) {
		t.Fatal("a click that moved nothing reported a change")
	}
	if f.PointerDown(5, 5) {
		t.Fatal("a click outside the field reported a change")
	}
	if f.Caret() != 3 {
		t.Fatalf("caret %d after a click outside, want it untouched", f.Caret())
	}
	f.PointerDown(fieldBounds.X+1, y)
	if f.Caret() != 0 {
		t.Fatalf("caret %d after a click on the left edge, want 0", f.Caret())
	}
	f.PointerDown(fieldBounds.X+fieldBounds.Width-1, y)
	if f.Caret() != 6 {
		t.Fatalf("caret %d after a click past the text, want 6", f.Caret())
	}
}

// A click works on an unfocused field — the application focuses and clicks
// on the same press — but never focuses it: a widget that focused itself
// would leave two of them focused at once.
func TestClickDoesNotFocusTheFieldAndWorksUnfocused(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText("abcdef")
	f.SetFocused(false)

	f.PointerDown(f.textX()+31, fieldBounds.Y+10)
	if f.Focused() {
		t.Fatal("a click focused the field")
	}
	if f.Caret() != 3 {
		t.Fatalf("caret %d after a click on an unfocused field, want 3", f.Caret())
	}
	f.Disabled = true
	if f.PointerDown(f.textX()+1, fieldBounds.Y+10) {
		t.Fatal("a disabled field took a click")
	}
}

// A click past the right edge of a scrolled field lands at the end of what
// is visible and must not scroll.
func TestClickPastTheRightEdgeDoesNotScroll(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 60))
	f.SetCaret(0)
	anchor, y := f.vw.anchor, fieldBounds.Y+fieldBounds.Height/2

	f.PointerDown(fieldBounds.X+fieldBounds.Width-1, y)
	if f.vw.anchor != anchor {
		t.Fatalf("anchor moved from %d to %d on a click", anchor, f.vw.anchor)
	}
	if f.Caret() == len(f.Text()) {
		t.Fatal("a click past the right edge jumped to the end of the text")
	}
}

// Clicking a field restarts the blink, like an edit does.
func TestClickingShowsTheCaretAgain(t *testing.T) {
	f, _ := newTestField(t)
	f.SetText("ab")
	f.SetCaretVisible(false)
	f.PointerDown(fieldBounds.X+fieldBounds.Width/2, fieldBounds.Y+fieldBounds.Height/2)
	if !f.caretShown() {
		t.Fatal("the caret is still hidden after a click")
	}
}

// With a font whose advances do not add up, clicking where the caret is
// drawn must put the caret back exactly there, for every visible boundary.
func TestCaretAndClickAgreeWithAKerningFont(t *testing.T) {
	font := newKernFont(10)
	f := NewTextField("", font)
	f.Bounds = fieldBounds
	f.SetFocused(true)
	f.SetText(strings.Repeat("AVWañ", 12))
	f.SetCaret(0)

	for j := f.vw.anchor; j <= f.vw.visEnd; j++ {
		if f.SetCaret(j); f.Caret() != j {
			continue // not a character boundary
		}
		if f.vw.caretX > f.vw.viewW {
			continue
		}
		f.PointerDown(f.textX()+f.vw.caretX, fieldBounds.Y+10)
		if f.Caret() != j {
			t.Fatalf("clicking where the caret for %d is drawn landed on %d", j, f.Caret())
		}
	}
}

func TestDrawsTheVisibleRunClippedToTheTextArea(t *testing.T) {
	cv, _ := newTestCanvas(t, 640, 200, 0)
	f, font := newTestField(t)
	f.SetText(strings.Repeat("x", 60))
	font.reset()

	f.Draw(cv)

	if font.text != f.Text()[f.vw.anchor:f.vw.visEnd] {
		t.Fatalf("drew %q, want the visible run", font.text)
	}
	if font.calls != 0 {
		t.Fatalf("Draw measured %d times, want none", font.calls)
	}
	want := canvas.Rect{X: f.textX(), Y: fieldBounds.Y + 2, Width: 172, Height: 40}
	if font.clip != want {
		t.Fatalf("clip %+v, want %+v", font.clip, want)
	}
	if font.at != (canvas.Point{X: f.textX(), Y: fieldBounds.Y + fieldBounds.Height/2}) {
		t.Fatalf("text anchored at %+v", font.at)
	}
	if err := cv.Err(); err != nil {
		t.Fatalf("canvas error: %v", err)
	}
}

func TestPlaceholderShowsOnlyWhileEmptyAndUnfocused(t *testing.T) {
	cv, _ := newTestCanvas(t, 640, 200, 0)
	f, font := newTestField(t)
	f.SetFocused(false)

	f.Draw(cv)
	if font.text != "type here" {
		t.Fatalf("drew %q, want the placeholder", font.text)
	}
	f.SetFocused(true)
	font.reset()
	font.text = ""
	f.Draw(cv)
	if font.text != "" {
		t.Fatalf("a focused empty field drew %q", font.text)
	}
}

func TestEachStateDrawsDifferently(t *testing.T) {
	snapshot := func(set func(f *TextField)) []uint32 {
		cv, px := newTestCanvas(t, 640, 200, 0)
		f, _ := newTestField(t)
		f.SetText("abc")
		set(f)
		f.Draw(cv)
		if err := cv.Err(); err != nil {
			t.Fatalf("canvas error: %v", err)
		}
		return px
	}
	base := snapshot(func(f *TextField) {})
	states := map[string][]uint32{
		"unfocused":  snapshot(func(f *TextField) { f.SetFocused(false) }),
		"no caret":   snapshot(func(f *TextField) { f.SetCaretVisible(false) }),
		"disabled":   snapshot(func(f *TextField) { f.Disabled = true }),
		"empty":      snapshot(func(f *TextField) { f.SetText("") }),
		"scrolled":   snapshot(func(f *TextField) { f.SetText(strings.Repeat("x", 60)) }),
		"caret home": snapshot(func(f *TextField) { f.SetCaret(0) }),
	}
	for name, px := range states {
		if equalPixels(base, px) {
			t.Errorf("%s renders the same as the resting field", name)
		}
	}
}

func equalPixels(a, b []uint32) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Nothing the field draws may land in row padding.
func TestFieldDrawStaysInsideTheVisibleRegion(t *testing.T) {
	const w, h, pad = 320, 160, 8
	cv, px := newTestCanvas(t, w, h, pad)
	for i := range px {
		px[i] = 0xdeadbeef
	}
	f, _ := newTestField(t)
	// Straddle the right edge so the field is clipped by the buffer.
	f.Bounds = canvas.Rect{X: w - 40, Y: 20, Width: 120, Height: 44}
	f.SetText("abcdef")

	f.Draw(cv)

	for y := range h {
		for x := w; x < w+pad; x++ {
			if got := px[y*(w+pad)+x]; got != 0xdeadbeef {
				t.Fatalf("padding written at row %d, column %d: %#08x", y, x, got)
			}
		}
	}
}

// A valid Bounds must never produce an invalid inner rectangle, however
// little room is left after the border and the padding.
func TestTinyFieldsStillDraw(t *testing.T) {
	sizes := []canvas.Rect{
		{X: 10, Y: 10, Width: 0, Height: 0},
		{X: 10, Y: 10, Width: 1, Height: 1},
		{X: 10, Y: 10, Width: 4, Height: 4},
		{X: 10, Y: 10, Width: 28, Height: 28},
		{X: 10, Y: 10, Width: 29, Height: 3},
	}
	for _, r := range sizes {
		cv, _ := newTestCanvas(t, 320, 160, 0)
		f, _ := newTestField(t)
		f.Bounds = r
		f.SetText("abc")
		f.Draw(cv)
		if err := cv.Err(); err != nil {
			t.Fatalf("canvas error at %+v: %v", r, err)
		}
	}
}

func TestFieldDrawAndClickDoNotAllocate(t *testing.T) {
	cv, _ := newTestCanvas(t, 640, 200, 0)
	f, _ := newTestField(t)
	f.SetText(strings.Repeat("x", 60))

	if n := testing.AllocsPerRun(100, func() { f.Draw(cv) }); n != 0 {
		t.Fatalf("Draw allocates %v times per call, want 0", n)
	}
	x, y := fieldBounds.X+50, fieldBounds.Y+10
	if n := testing.AllocsPerRun(100, func() { f.PointerDown(x, y) }); n != 0 {
		t.Fatalf("PointerDown allocates %v times per call, want 0", n)
	}
}

// The performance criterion, as an assertion rather than a benchmark: the
// work per event is bounded by what is on screen and does not grow with
// the text. Times are never asserted — the work is, and that does not
// depend on the machine.
func TestWorkDoesNotGrowWithTheText(t *testing.T) {
	// The field is 172 units wide with a 10-unit font: 18 runes at most,
	// counting the one that pokes out of the right edge.
	const visible = 18
	const limit = 100 * visible

	work := func(n int, act func(f *TextField)) int {
		font := newRuneFont(10)
		f := NewTextField("", font)
		f.Bounds = fieldBounds
		f.SetFocused(true)
		f.SetText(strings.Repeat("x", n))
		font.reset()
		act(f)
		return font.measured + font.drawn
	}

	cv, _ := newTestCanvas(t, 640, 200, 0)
	acts := map[string]func(f *TextField){
		"insert at the end":    func(f *TextField) { f.Insert("y") },
		"insert at the start":  func(f *TextField) { f.SetCaret(0); f.Insert("y") },
		"insert in the middle": func(f *TextField) { f.SetCaret(len(f.Text()) / 2); f.Insert("y") },
		"backspace":            func(f *TextField) { f.KeyDown(KeyBackspace) },
		"move left":            func(f *TextField) { f.KeyDown(KeyLeft) },
		"home":                 func(f *TextField) { f.KeyDown(KeyHome) },
		"end":                  func(f *TextField) { f.KeyDown(KeyHome); f.KeyDown(KeyEnd) },
		"click":                func(f *TextField) { f.PointerDown(fieldBounds.X+90, fieldBounds.Y+10) },
		"frame":                func(f *TextField) { f.Draw(cv) },
	}
	for name, act := range acts {
		small, mid, big := work(1_000, act), work(10_000, act), work(100_000, act)
		if mid > small || big > small {
			t.Errorf("%s: %d runes at 1k, %d at 10k, %d at 100k: the work grows with the text", name, small, mid, big)
		}
		if big > limit {
			t.Errorf("%s: %d runes at 100k, more than the %d the visible run allows", name, big, limit)
		}
	}
}

// A frame that changed nothing must not measure anything at all.
func TestAnUnchangedFrameMeasuresNothing(t *testing.T) {
	cv, _ := newTestCanvas(t, 640, 200, 0)
	f, font := newTestField(t)
	f.SetText(strings.Repeat("x", 1000))
	f.Draw(cv)
	font.reset()

	f.Draw(cv)
	f.Draw(cv)
	if font.calls != 0 {
		t.Fatalf("two unchanged frames called Measure %d times, want none", font.calls)
	}
	if font.drawn > 40 {
		t.Fatalf("two unchanged frames drew %d runes, want about twice the visible run", font.drawn)
	}
}

// benchField is a field of n characters, focused, with the caret at the
// end, and a canvas to draw into.
func benchField(n int) (*TextField, *canvas.Canvas) {
	px := make([]uint32, 640*200)
	cv, _ := canvas.New(canvas.Buffer{Pixels: px, Width: 640, Height: 200, Stride: 640}, 640, 200, 1)
	f := NewTextField("", newRuneFont(10))
	f.Bounds = fieldBounds
	f.SetFocused(true)
	f.SetText(strings.Repeat("x", n))
	return f, cv
}

// The benchmarks are informative: what they are read for is the shape —
// flat in the length of the text — and not the absolute numbers.
func BenchmarkTextFieldDraw(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			f, cv := benchField(n)
			b.ReportAllocs()
			for b.Loop() {
				f.Draw(cv)
			}
		})
	}
}

func BenchmarkTextFieldEdit(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		for _, where := range []string{"end", "middle"} {
			b.Run(fmt.Sprintf("%s/n=%d", where, n), func(b *testing.B) {
				f, _ := benchField(n)
				if where == "middle" {
					f.SetCaret(n / 2)
				}
				b.ReportAllocs()
				for b.Loop() {
					f.Insert("y")
					f.KeyDown(KeyBackspace)
				}
			})
		}
	}
}

func BenchmarkTextFieldPointerDown(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			f, _ := benchField(n)
			x, y := fieldBounds.X+90, fieldBounds.Y+10
			b.ReportAllocs()
			for b.Loop() {
				f.PointerDown(x, y)
			}
		})
	}
}
```

- [ ] **Step 2: Comprobar que falla.**

Run: `go test ./widget/ -run 'TestClick|TestDraws|TestPlaceholder|TestEachState|TestFieldDraw|TestTiny|TestWork|TestAnUnchanged|TestCaretAndClick'`
Expected: FAIL de compilación, `f.Draw undefined` y `f.PointerDown undefined`.

- [ ] **Step 3: Añadir `PointerDown` y `Draw` a `widget/textfield.go`**, justo después de `SetCaretVisible`.

```go
// PointerDown handles a primary-button press at (x, y) and reports whether
// a repaint would look different. A press outside Bounds is not this
// field's — pressing the button next to it must not move this caret — and
// a disabled field ignores every press.
//
// It puts the caret where the click landed and nothing else: it does not
// focus the field, because a widget that focused itself would leave two of
// them focused at once. It works whether or not the field is focused,
// since the application focuses and clicks on the same press.
func (t *TextField) PointerDown(x, y float32) bool {
	if t.Disabled || !contains(t.Bounds, x, y) {
		return false
	}
	t.ensure()
	before := t.visual()
	t.caretOn = true
	t.ed.setCaret(t.vw.hit(t.Font, t.ed.text, x-t.textX()))
	t.sync()
	return t.visual() != before
}

// Draw paints the field into cv. It measures nothing and allocates
// nothing: the visible run is a substring of the cached text, and its
// geometry was computed when the text last changed.
//
// Bounds must be a valid canvas rectangle: a negative size is recorded as
// a canvas error like any other bad argument. A valid one never produces
// an invalid inner rectangle — a field too small to hold its border and
// padding draws its box and nothing else.
func (t *TextField) Draw(cv *canvas.Canvas) {
	t.ensure()
	st := &t.Style

	fill, text := st.Fill, st.Text
	outline := st.BorderColor
	if t.Disabled {
		fill, text = st.DisabledFill, st.DisabledText
	} else if t.focused {
		outline = st.FocusBorder
	}

	cv.FillRoundedRect(t.Bounds, st.Corner, fill)
	if st.Border > 0 {
		cv.StrokeRoundedRect(t.Bounds, st.Corner, st.Border, outline)
	}

	innerW := t.vw.innerW
	if !(innerW > 0) {
		return
	}
	border, padding := t.metrics()
	x := t.textX()
	// The clip is the text area, full height between the borders: the
	// visible run is cut here and not by Font.Draw, which the Font
	// contract does not promise will clip at all.
	clip := canvas.Rect{
		X:      x,
		Y:      t.Bounds.Y + border,
		Width:  innerW,
		Height: max(t.Bounds.Height-2*border, 0),
	}

	if t.Font != nil {
		at := canvas.Point{X: x, Y: t.Bounds.Y + t.Bounds.Height/2}
		if t.placeholderShown() {
			t.Font.Draw(cv, at, t.Placeholder, st.Placeholder, clip)
		} else if run := t.visibleText(); run != "" {
			t.Font.Draw(cv, at, run, text, clip)
		}
	}

	if t.caretShown() {
		cv.FillRect(canvas.Rect{
			X:      x + t.vw.caretX,
			Y:      t.Bounds.Y + border + padding/2,
			Width:  st.CaretWidth,
			Height: max(t.Bounds.Height-2*(border+padding/2), 0),
		}, st.Caret)
	}
}
```

- [ ] **Step 4: Comprobar que pasa y medir.**

Run: `go test ./widget/ -race -count=2` → PASS.
Run: `go test ./widget/ -run XXX -bench . -benchmem` → los benchmarks corren; `Draw` y `PointerDown` con 0 asignaciones, y sus tiempos **iguales** con 1.000 y con 100.000 caracteres. `Edit` sí crece con el texto (el `memmove` y la copia de la `string`), con 1 asignación por edición.

- [ ] **Step 5: Mencionar el campo en `widget/doc.go`.**

En la sección `# Text`, tras el párrafo que explica `Font`, añadir:

```go
// [TextField] is the one widget that both measures and scrolls text. It
// asks its Font only for the substring it shows, and only when the text,
// the caret or its width changed, so a frame of a field holding a hundred
// thousand characters costs what a frame of an empty one does.
```

- [ ] **Step 6: Escribir la sección de `docs/widget.md`** (español). Va después de la sección `## `Button`` y antes de `## Foco y teclado`.

````markdown
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
````

Además, en la misma página:

- **Estado:** cambiar «Construido: **`Button`**…» por «Construido: **`Button`** y **`TextField`** —el segundo enfocable, que es lo que le da algo que recorrer a `Chain`—, más las piezas que comparte cualquier control enfocable…».
- **Tabla de `Key`:** añadir las seis filas nuevas (`KeyLeft` «mueve el cursor un carácter a la izquierda», `KeyRight` «…a la derecha», `KeyHome` «al principio», `KeyEnd` «al final», `KeyBackspace` «borra el carácter anterior», `KeyDelete` «borra el siguiente») y una frase: el texto no llega como `Key`, sino por `TextField.Insert`.
- **Restricciones que cumple:** en el punto «Sin asignaciones al dibujar», añadir `TextField.Draw` junto a `Button.Draw`.
- **Qué falta:** quitar «Un segundo widget enfocable» y «El campo de texto de `example/widgets` sigue siendo un prototipo»; dejar layout, reparto de eventos y el resto de controles (casilla, lista), más selección y forma de cursor.

- [ ] **Step 7: Verificar todo y commit.**

Run: `go build ./... && go vet ./... && go test ./widget/ -race -count=2` → PASS.
Run: `go run ./cmd/docaudit` → `widget` al 100 %, cobertura global sin bajar.

```bash
git add widget/textfield.go widget/textfield_test.go widget/doc.go docs/widget.md
git commit -m "$(cat <<'EOF'
widget: draw the text field, place its caret on a click, document it

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XEKsqW1CKz9Kav57ei3LuH
EOF
)"
```

---

### Task 5: migrar `example/widgets` al widget

El ejemplo es la prueba de que la API se usa bien. Deja de tener campo de texto propio: `ui.text`, `ui.focused`, `insert`, `backspace` y `drawInput` desaparecen.

**Files:**
- Modify: `example/widgets/ui.go`, `example/widgets/window.go`
- Modify: `example/widgets/ui_test.go`, `example/widgets/window_test.go`, `example/widgets/draw_test.go`, `example/widgets/animation_test.go`
- Modify: `docs/estado.md`

**Interfaces:**
- Consumes: `widget.TextField` y su API entera (tareas 3 y 4), `widget.Chain`, `widget.Key` con las seis teclas nuevas.
- Produces (dentro de `package main`): `ui` con los campos `field *widget.TextField`, `button *widget.Button`, `focus *widget.Chain`, `font widget.Font`, `clicked`, `caretOn`, `busy`, `status`, `now`; métodos `place(layout)`, `pointerMoved(layout, x, y) bool`, `pointerPressed(layout, x, y) bool`, `pointerReleased(layout, x, y) bool`, `keyDown(widget.Key) bool`, `keyUp(widget.Key) bool`, `insert(string) bool`, `blur() bool`, `setCaretVisible(bool) bool`, `text() string`, `editing() bool`, `animating() bool`; y en `window.go`, `(*app).widgetKey(keyboard.Event) widget.Key` y `(*app).submit(text string)`.

- [ ] **Step 1: Reescribir el estado y el dibujo en `example/widgets/ui.go`.**

Se borran: las constantes `corner`, `border`, `caretW`, `textPad`; las variables `colorInput`, `colorBorder`, `colorText`; la función `drawInput`; los campos `text`, `focused` y los métodos `insert` y `backspace` de `ui`. `placeholderString`, `colorBackground`, `colorAccent` y `colorTextDim` se quedan.

```go
// ui is the whole widget state. There is no retained widget tree: two
// widgets, the chain that keeps at most one of them focused, and a frame
// that is a pure function of this plus the window size.
type ui struct {
	// field is the text input. Its Bounds come from the layout every time
	// it is used, see place.
	field *widget.TextField
	// button clears the field.
	button *widget.Button
	// focus is the tab order over the two. It is what knows they are
	// siblings: a widget is told whether it is focused and never asks.
	focus *widget.Chain

	// font draws and measures every string in the window, the field's and
	// the button's alike.
	font widget.Font

	// clicked is set by the button's OnClick and read back by
	// pointerReleased, so the window can tell that a release fired it.
	clicked bool
	// caretOn is the blink phase this application drives, which it hands
	// to the field: the widget has no clock. It can lag the field's own by
	// one tick after the user types, which resets it, and the next tick
	// puts them back in step.
	caretOn bool

	// busy means a background task is running. The window keeps asking the
	// compositor for frames while it is, so the spinner moves.
	busy bool
	// status is the line under the controls: what the last task did.
	status string
	// now is the compositor's millisecond clock as of the frame being
	// drawn. Animations read it instead of the wall clock, so a frame is a
	// pure function of the ui.
	now uint32
}

// newUI builds the ui drawing with font, with its button wired to clear
// the field.
func newUI(font widget.Font) *ui {
	u := &ui{font: font, caretOn: true}
	u.field = widget.NewTextField(placeholderString, font)
	u.button = widget.NewButton("Clear", font)
	u.button.OnClick = func() {
		u.field.SetText("")
		u.clicked = true
	}
	u.focus = widget.NewChain(u.field, u.button)
	return u
}

// place puts both widgets where the layout says. A widget owns its bounds
// but not the layout, so the caller pushes them in before every use.
func (u *ui) place(l layout) {
	u.field.Bounds = l.input
	u.button.Bounds = l.button
}

// pointerMoved updates the button's hover and reports whether anything
// visible changed. Motion arrives on every pixel the pointer crosses;
// repainting for each one would be pure waste when only a transition is
// visible.
func (u *ui) pointerMoved(l layout, x, y float32) bool {
	u.place(l)
	return u.button.PointerMove(x, y)
}

// pointerPressed moves the focus and lets the widgets take the press. The
// three calls on the field are the wiring docs/widget.md describes: the
// application decides the focus, because Focusable exposes no Bounds, and
// the widget places the caret.
func (u *ui) pointerPressed(l layout, x, y float32) bool {
	u.place(l)

	changed := false
	if hit(l.input, x, y) {
		changed = u.focus.Focus(u.field)
		changed = u.field.PointerDown(x, y) || changed
	} else {
		changed = u.focus.Blur()
	}
	return u.button.PointerDown(x, y) || changed
}

// pointerReleased hands the release to the button and reports whether it
// fired. The click-on-release rule itself lives in widget.Button.
func (u *ui) pointerReleased(l layout, x, y float32) bool {
	u.place(l)
	u.clicked = false
	u.button.PointerUp(x, y)
	return u.clicked
}

// keyDown hands a translated key to the chain, which moves the focus on
// Tab and forwards everything else to whichever widget has it.
func (u *ui) keyDown(k widget.Key) bool { return u.focus.KeyDown(k) }

func (u *ui) keyUp(k widget.Key) bool { return u.focus.KeyUp(k) }

// insert offers composed text to the field, which ignores it unless it has
// the focus.
func (u *ui) insert(s string) bool { return u.field.Insert(s) }

// blur takes the caret away from whatever has it.
func (u *ui) blur() bool { return u.focus.Blur() }

// setCaretVisible drives the blink from the application's timer.
func (u *ui) setCaretVisible(v bool) bool {
	u.caretOn = v
	return u.field.SetCaretVisible(v)
}

// text is what the field holds, which submitting sends.
func (u *ui) text() string { return u.field.Text() }

// editing reports whether the field has the caret.
func (u *ui) editing() bool { return u.field.Focused() }

// animating reports whether the ui wants a frame on every compositor
// callback. Only a running task does: its spinner moves.
func (u *ui) animating() bool { return u.busy }

// draw paints one complete frame. It always repaints everything: each
// frame goes into a buffer the compositor has finished with, whose
// previous contents are two frames old, so there is nothing to preserve.
func draw(cv *canvas.Canvas, l layout, u *ui) {
	cv.Clear(colorBackground)

	u.place(l)
	u.field.Draw(cv)
	u.button.Draw(cv)
	drawStatus(cv, l, u)
}
```

- [ ] **Step 2: Traducir las teclas en `example/widgets/window.go`.**

Ampliar el bloque de keysyms (los valores son los de `keyboard/keysyms.gen.go`):

```go
// The keysyms this window gives a meaning to. The keyboard package carries
// keysym *names* rather than Go constants, so they are spelled out here.
const (
	symBackSpace = keyboard.Keysym(0xff08)
	symDelete    = keyboard.Keysym(0xffff)
	symLeft      = keyboard.Keysym(0xff51)
	symRight     = keyboard.Keysym(0xff53)
	symHome      = keyboard.Keysym(0xff50)
	symEnd       = keyboard.Keysym(0xff57)
	symReturn    = keyboard.Keysym(0xff0d)
	symKPEnter   = keyboard.Keysym(0xff8d)
	symEscape    = keyboard.Keysym(0xff1b)
	symTab       = keyboard.Keysym(0xff09)
	symLeftTab   = keyboard.Keysym(0xfe20) // ISO_Left_Tab, what Shift-Tab is on some keymaps
	symSpace     = keyboard.Keysym(0x0020)
)
```

Y sustituir `typeKey` y `submit`:

```go
// typeKey turns one key event into an edit, a focus move or an activation.
// Everything below the keysym — the keycode arithmetic, the keymap, the
// dead keys, the modifiers that must not reach the composer — is the
// keyboard package's; what is left here is this window's own policy about
// which key does what, and the translation into the keys a widget
// understands. The widget package has no keysyms on purpose.
//
// A repeat arrives as an ordinary event, so holding Backspace erases and
// holding a letter types, with no extra work at this level.
func (a *app) typeKey(ev keyboard.Event) {
	k := a.widgetKey(ev)

	if ev.State == keyboard.Released {
		if a.ui.keyUp(k) {
			a.host.Invalidate()
		}
		return
	}
	if ev.Sym == symEscape {
		// Escape drops the caret in this window, rather than reaching the
		// widgets, where it would only cancel a Space held on the button.
		if a.ui.blur() {
			a.host.Invalidate()
		}
		return
	}
	if k != widget.KeyNone {
		if a.ui.keyDown(k) {
			a.host.Invalidate()
		}
		return
	}
	if a.ui.insert(ev.Text) {
		a.host.Invalidate()
	}
}

// widgetKey translates a keysym into the key the widgets are driven by, or
// KeyNone for one they have no name for — whose text, if it has any, goes
// to the field instead.
//
// Space is the one that depends on what is focused: in the text field it
// is a character like any other, and on the button it is the activation
// that arms on the press and fires on the release.
func (a *app) widgetKey(ev keyboard.Event) widget.Key {
	switch ev.Sym {
	case symLeft:
		return widget.KeyLeft
	case symRight:
		return widget.KeyRight
	case symHome:
		return widget.KeyHome
	case symEnd:
		return widget.KeyEnd
	case symBackSpace:
		return widget.KeyBackspace
	case symDelete:
		return widget.KeyDelete
	case symReturn, symKPEnter:
		return widget.KeyEnter
	case symLeftTab:
		return widget.KeyBacktab
	case symTab:
		// Which of the two a press is cannot be worked out by the widget
		// package: it has no modifier state, and a shifted Tab arrives as
		// either keysym depending on the keymap.
		if ev.Mods.Effective&keyboard.ModShift != 0 {
			return widget.KeyBacktab
		}
		return widget.KeyTab
	case symSpace:
		if a.ui.editing() {
			return widget.KeyNone
		}
		return widget.KeySpace
	}
	return widget.KeyNone
}

// submit starts the slow task Enter stands for: a request to a server,
// say. It is wired to the field's OnSubmit, runs on a goroutine of its
// own, knows nothing of Wayland or the UI, and publishes its outcome
// through Do, which is how any background work reaches the widgets. It
// stops early if the window closes first.
//
// A second Enter while the first is in flight does nothing.
func (a *app) submit(text string) {
	if a.ui.busy {
		return
	}
	delay := submitDelay // read here, on the UI goroutine
	a.ui.busy = true
	a.ui.status = fmt.Sprintf("submitting %q…", text)
	a.host.Invalidate()

	ctx := a.host.Context()
	a.tasks.start(func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		a.host.Do(func() {
			a.ui.busy = false
			a.ui.status = fmt.Sprintf("submitted %q", text)
			a.host.Invalidate()
		})
	})
}
```

`newApp` conecta el callback, que es lo que hace que Intro llegue por el widget y no por un `switch` del ejemplo:

```go
// newApp returns an application drawing with font, in a window of the size
// it asks for until told otherwise.
func newApp(h host, t *tasks, font widget.Font) *app {
	a := &app{host: h, tasks: t, ui: newUI(font), width: defaultWidth, height: defaultHeight}
	a.ui.field.OnSubmit = a.submit
	return a
}
```

`pointerEvent` pasa a repintar solo si algo cambió, y `blinkTick` habla con el widget:

```go
	case pointer.ButtonDown:
		if ev.Button == btnLeft {
			if a.ui.pointerPressed(l, ev.X, ev.Y) {
				a.host.Invalidate()
			}
		}
```

```go
// blinkTick flips the caret, and repaints only if there is a caret to
// flip: an unfocused window has nothing to blink and should stay quiet.
func (a *app) blinkTick() {
	if !a.ui.editing() {
		return
	}
	if a.ui.setCaretVisible(!a.ui.caretOn) {
		a.host.Invalidate()
	}
}
```

```go
// keyboardFocus follows the keyboard focus of the whole window, which is a
// different thing from which control inside it owns the caret. Losing it
// has to drop the caret too: the user is typing somewhere else now.
func (a *app) keyboardFocus(focused bool) {
	if !focused && a.ui.blur() {
		a.host.Invalidate()
	}
}
```

Actualizar el comentario de paquete: el punto 4 menciona «here one bool that a pointer press sets»; pasa a decir que el foco dentro de la superficie lo reparte una `widget.Chain`, que Tab lo mueve y que un clic son las tres llamadas de `docs/widget.md`.

- [ ] **Step 3: Reescribir los tests del ejemplo.**

Imports que hay que añadir: `github.com/romycode/ggui/widget` en `ui_test.go`, y `fmt`, `strings` y `widget` en `draw_test.go` (para el benchmark con fuente real).

`example/widgets/ui_test.go`:
- `TestButtonDoesNotFireWhenTheReleaseLandsOutside`, `TestNewAppButtonClearsTheStoredUI`, `TestButtonClearsTheTextWhenPressedAndReleasedInside`: `u.text = []rune("hello")` → `u.field.SetText("hello")`; `string(u.text)` → `u.text()`.
- `TestNewAppInitializesItsUI`: comprobar también `a.ui.field == nil` y `a.ui.focus == nil`.
- `TestPressingTheInputFocusesItAndPressingElsewhereDoesNot`: `u.focused` → `u.editing()`.
- `TestBackspaceOnEmptyTextIsANoop`, `TestBackspaceDeletesOneRuneNotOneByte`, `TestInsertIsIgnoredWhileTheInputIsNotFocused`, `TestInsertOfEmptyTextReportsNoChange`, `TestInsertDropsControlCharacters`: se sustituyen por un único test de integración, porque el comportamiento ya es del widget y allí está probado:

```go
// The field is widget.TextField now: what this checks is the wiring, not
// the editing rules, which widget tests on its own.
func TestTypingGoesToTheFocusedFieldOnly(t *testing.T) {
	l := computeLayout(600, 300)
	u := newUI(bitmapFont{})

	if u.insert("x") || u.text() != "" {
		t.Fatal("an unfocused field took text")
	}

	x, y := center(l.input)
	u.pointerPressed(l, x, y)
	if !u.editing() {
		t.Fatal("a press inside the field did not focus it")
	}
	if !u.insert("añ") || u.text() != "añ" {
		t.Fatalf("text = %q after typing, want %q", u.text(), "añ")
	}
	if !u.keyDown(widget.KeyBackspace) || u.text() != "a" {
		t.Fatalf("text = %q after Backspace, want %q", u.text(), "a")
	}
	if u.insert("\r") {
		t.Fatal("a control character reached the field")
	}

	u.pointerPressed(l, 5, 5)
	if u.editing() {
		t.Fatal("a press outside the field left it focused")
	}
}

// Tab is what the chain is for, and it now has two widgets to walk.
func TestTabMovesTheFocusBetweenTheFieldAndTheButton(t *testing.T) {
	u := newUI(bitmapFont{})
	u.keyDown(widget.KeyTab)
	if !u.editing() {
		t.Fatal("Tab did not land on the field first")
	}
	u.keyDown(widget.KeyTab)
	if u.editing() || !u.button.Focused() {
		t.Fatal("a second Tab did not move on to the button")
	}
}
```

`example/widgets/animation_test.go`:
- `TestNewUIStartsWithAVisibleCaret` se queda (`u.caretOn`).
- `TestCaretBlinkChangesWhatTheInputRenders`: `shown.focused = true` → `shown.focus.Focus(shown.field)`; `hidden.caretOn = false` → `hidden.setCaretVisible(false)`; el tercer caso (`unfocused`) no cambia.
- `TestUserActionsShowTheCaretAgain` y `TestInputThatChangesNothingLeavesTheCaretAlone` **se borran**: eso ya es comportamiento de `widget.TextField` y está probado allí (`TestUserActionsShowTheCaretAgain`, `TestClickingShowsTheCaretAgain`).
- `TestStatusAndSpinnerNeverBreakTheFrame`: `u.text, u.focused = …` → `u.field.SetText("hello"); u.focus.Focus(u.field)`.

`example/widgets/draw_test.go`: mismas dos sustituciones en `TestDrawAFrameRecordsNoCanvasError`, `TestDrawNeverWritesIntoRowPadding`, `TestLongTextIsClippedToTheInput`, `TestFocusChangesWhatTheInputRenders`, `TestDrawSurvivesAWindowNarrowerThanTheButton` y `TestDrawWithATrueTypeFontStaysInsideItsControls`. Este último **se queda tal cual por lo demás**: es el único test con una fuente TrueType real, y le toca al ejemplo porque `widget` no puede importar `text`. Se le añade al lado un benchmark informativo:

```go
// BenchmarkTextFieldWithASystemFont is the one measurement taken against a
// real outline font: widget cannot import text, so this belongs here. It
// is informative — what it says is that the field's cost does not follow
// the length of the text — and it is skipped on a machine with no font we
// can read.
func BenchmarkTextFieldWithASystemFont(b *testing.B) {
	face, err := text.NewSystemFace(fontSize, text.Regular)
	if err != nil {
		b.Skipf("no system font: %v", err)
	}
	defer face.Close()

	px := make([]uint32, testWidth*testHeight)
	cv, err := canvas.New(canvas.Buffer{
		Pixels: px, Width: testWidth, Height: testHeight, Stride: testWidth,
	}, testWidth, testHeight, 1)
	if err != nil {
		b.Fatalf("canvas.New: %v", err)
	}
	l := computeLayout(testWidth, testHeight)

	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("draw/n=%d", n), func(b *testing.B) {
			u := newUI(face)
			u.place(l)
			u.focus.Focus(u.field)
			u.field.SetText(strings.Repeat("x", n))
			b.ReportAllocs()
			for b.Loop() {
				u.field.Draw(cv)
			}
		})
		b.Run(fmt.Sprintf("type/n=%d", n), func(b *testing.B) {
			u := newUI(face)
			u.place(l)
			u.focus.Focus(u.field)
			u.field.SetText(strings.Repeat("x", n))
			b.ReportAllocs()
			for b.Loop() {
				u.field.Insert("y")
				u.field.KeyDown(widget.KeyBackspace)
			}
		})
	}
}
```

`example/widgets/window_test.go`:
- `returnKey()` se queda igual (sigue siendo `symReturn`).
- `TestSubmitRunsInTheBackgroundAndPublishesThroughDo`, `TestSubmitAsksForARepaint`, `TestSubmitIsIgnoredWhileATaskIsRunning`, `TestSubmitDoesNotOutliveTheWindow`: `a.ui.text, a.ui.focused = []rune("hi"), true` → `a.ui.field.SetText("hi"); a.ui.focus.Focus(a.ui.field)`. El resto igual: Intro sigue llegando por `typeKey`, ahora a través de la cadena y de `OnSubmit`.
- `TestKeysActOnlyOnAFocusedInputAndOnPresses`: `a.ui.focused = true` → `a.ui.focus.Focus(a.ui.field)`; `string(a.ui.text)` → `a.ui.text()`; el caso de Escape comprueba `a.ui.editing()`.
- `TestBlinkTickTogglesTheCaretOnlyWhileFocused`: igual, con `a.ui.focus.Focus(a.ui.field)` en vez de `a.ui.focused = true`.
- `TestFocusingTheInputShowsTheCaret`: `a.ui.caretOn = false` → `a.ui.setCaretVisible(false)`, y tras el clic comprueba `a.ui.caretOn == false` **y** que el clic devolvió `true`; lo que el campo hace con su propio parpadeo ya lo prueba `widget`. Reescrito:

```go
// Clicking into the field is a user action: the widget shows its caret
// again, and the application has to hear that something changed.
func TestClickingTheInputAsksForARepaint(t *testing.T) {
	a, h := newTestApp(t)
	a.ui.setCaretVisible(false)
	l := a.layout()
	x, y := center(l.input)

	a.pointerEvent(pointer.Event{Kind: pointer.ButtonDown, Button: btnLeft, X: x, Y: y})
	if !a.ui.editing() {
		t.Fatal("clicking the field did not focus it")
	}
	if h.invalidations.Load() == 0 {
		t.Error("clicking into the field asked for no repaint")
	}
}
```
- `TestLosingTheKeyboardFocusDropsTheCaret`: `a.ui.focused = true` → `a.ui.focus.Focus(a.ui.field)`, y las comprobaciones sobre `a.ui.focused` → `a.ui.editing()`.
- `TestResizeMovesTheLayoutTheClicksAreTestedAgainst` y `TestOtherPointerButtonsAreIgnored`: `a.ui.focused` → `a.ui.editing()`.
- `TestPaintDrawsTheRealUI`: `a.ui.text, a.ui.focused = …` → `a.ui.field.SetText("hello"); a.ui.focus.Focus(a.ui.field)`.

- [ ] **Step 4: Comprobar que todo pasa.**

Run: `go build ./... && go vet ./...`
Run: `go test ./widget/... ./example/... -race -count=2` → PASS.
Run: `go test ./... -race` → PASS.
Run: `go test ./example/widgets -run XXX -bench BenchmarkTextFieldWithASystemFont -benchmem` → corre, o se salta con el mensaje de «no system font».

- [ ] **Step 5: Comprobación manual contra el compositor real** (solo con una sesión Wayland viva y alguien delante; si no la hay, se salta y se anota. El proceso abre una ventana: hay que cerrarla, no dejarlo corriendo en segundo plano).

Run: `go run ./example/widgets`
Comprobar: escribir; ←/→/Inicio/Fin mueven; Retroceso y Supr borran donde está el cursor; escribir más de lo que cabe desplaza y el cursor sigue visible; clic dentro coloca el cursor donde se pulsó; clic fuera quita el foco; Tab pasa al botón y Mayús+Tab vuelve; Intro lanza la tarea y el spinner gira; Clear vacía el campo; el cursor parpadea y no se apaga mientras se escribe; redimensionar la ventana no rompe el campo. Salir con la X y comprobar código de salida 0.

- [ ] **Step 6: Actualizar `docs/estado.md`.**

- Fila de `widget`: «`Button` … más `TextField` (cursor libre, clic para colocarlo, desplazamiento horizontal, *placeholder*, deshabilitado), las interfaces `Font` y `Focusable`, el tipo `Key` y `Chain`. Solo depende de `canvas`; `Draw` no asigna y el trabajo por evento no crece con el largo del texto.» Pendiente: layout, el resto de controles, selección y forma de cursor.
- Párrafo bajo la tabla: «el campo de texto sigue siendo un prototipo dentro de `example/widgets`» → el ejemplo usa `widget.TextField` y una `widget.Chain`.
- Hueco conocido 2: ya no falta el campo de texto; falta layout, el resto de controles y la selección.
- Sección *Pruebas*, entrada de `widget`: añadir el editor (tablas, invariantes, fuzz), las primitivas de la vista contra fuerza bruta, la propiedad de que clic y cursor coinciden con una fuente con *kerning*, y las aserciones de trabajo con las fuentes contadoras.

- [ ] **Step 7: Commit.**

```bash
git add example/widgets docs/estado.md
git commit -m "$(cat <<'EOF'
example/widgets: replace the prototype input with widget.TextField

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XEKsqW1CKz9Kav57ei3LuH
EOF
)"
```

---

## Notas de la spec que este plan precisa

Cambios de redacción hechos sobre `docs/archive/specs/2026-09-21-textfield-design.md` en el mismo commit que este plan, porque el texto original decía algo que la implementación no puede cumplir tal cual:

1. **`SetCaret`**: decía «ajusta al límite de carácter **más cercano**»; redondea **hacia atrás**, al límite anterior o al propio. Es lo que hace que `SetCaret` sea idempotente y que un desplazamiento en medio de un carácter no salte hacia delante.
2. **Coste de las primitivas**: decía O(lo visible); es O(lo visible · log lo visible) en trabajo de `Measure`, por la bisección. El factor logarítmico se declara, y lo importante —que no depende del largo del texto— se mantiene.
3. **El predicado de repintado** lleva también si el *placeholder* está a la vista: sin él, quitar el foco a un campo vacío y deshabilitado cambiaría el dibujo sin que ningún `bool` lo dijera.

Y una diferencia de nombre, no de fondo: la spec llama `caretOff` a la posición del cursor respecto al ancla; el código la llama `caretX`, porque es una coordenada y no un desplazamiento en bytes como el resto de `off`/`offset` del paquete.

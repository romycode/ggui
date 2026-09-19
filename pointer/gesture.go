package pointer

const (
	dragThresholdSquared float32 = 16
	doubleClickInterval  uint32  = 500
)

type press struct {
	button         uint32
	serial         uint32
	time           uint32
	startX, startY float32
	dragging       bool
}

type click struct {
	valid  bool
	button uint32
	time   uint32
	x, y   float32
}

type gestureState struct {
	emit func(Event)

	x, y       float32
	presses    []press
	lastClick  click
	generation uint64
}

func (g *gestureState) enter(x, y float32) {
	g.x, g.y = x, y
	g.emitEvent(Event{Kind: Position, X: x, Y: y})
}

func (g *gestureState) leave() {
	g.presses = nil
	g.lastClick = click{}
	g.generation++
}

func (g *gestureState) motion(t uint32, x, y float32) {
	g.x, g.y = x, y
	generation := g.generation
	g.emitEvent(Event{Kind: Position, X: x, Y: y, Time: t})
	if g.generation != generation {
		return
	}

	for i := 0; i < len(g.presses); i++ {
		p := &g.presses[i]
		kind := DragMove
		if !p.dragging {
			dx, dy := x-p.startX, y-p.startY
			if dx*dx+dy*dy <= dragThresholdSquared {
				continue
			}
			p.dragging = true
			g.lastClick = click{}
			kind = DragStart
		}
		ev := Event{
			Kind:   kind,
			X:      x,
			Y:      y,
			StartX: p.startX,
			StartY: p.startY,
			Button: p.button,
			Serial: p.serial,
			Time:   t,
		}
		g.emitEvent(ev)
		if g.generation != generation {
			return
		}
	}
}

func (g *gestureState) button(serial, t, button uint32, down bool) {
	if down {
		if g.pressIndex(button) < 0 {
			g.presses = append(g.presses, press{
				button: button,
				serial: serial,
				time:   t,
				startX: g.x,
				startY: g.y,
			})
		}
		g.emitEvent(Event{
			Kind:   ButtonDown,
			X:      g.x,
			Y:      g.y,
			Button: button,
			Serial: serial,
			Time:   t,
		})
		return
	}

	i := g.pressIndex(button)
	var (
		p     press
		found bool
	)
	if i >= 0 {
		p = g.presses[i]
		g.presses = append(g.presses[:i], g.presses[i+1:]...)
		found = true
	}
	generation := g.generation
	g.emitEvent(Event{
		Kind:   ButtonUp,
		X:      g.x,
		Y:      g.y,
		Button: button,
		Serial: serial,
		Time:   t,
	})
	if !found || g.generation != generation {
		return
	}
	if p.dragging {
		g.emitEvent(Event{
			Kind:   DragEnd,
			X:      g.x,
			Y:      g.y,
			StartX: p.startX,
			StartY: p.startY,
			Button: button,
			Serial: serial,
			Time:   t,
		})
		return
	}
	kind := Click
	dx, dy := g.x-g.lastClick.x, g.y-g.lastClick.y
	if g.lastClick.valid &&
		g.lastClick.button == button &&
		t-g.lastClick.time <= doubleClickInterval &&
		dx*dx+dy*dy <= dragThresholdSquared {
		kind = DoubleClick
	}
	ev := Event{
		Kind:   kind,
		X:      g.x,
		Y:      g.y,
		Button: button,
		Serial: serial,
		Time:   t,
	}
	if kind == DoubleClick {
		g.lastClick = click{}
		g.emitEvent(ev)
		return
	}
	g.lastClick = click{valid: true, button: p.button, time: t, x: g.x, y: g.y}
	g.emitEvent(ev)
}

func (g *gestureState) pressIndex(button uint32) int {
	for i := range g.presses {
		if g.presses[i].button == button {
			return i
		}
	}
	return -1
}

func (g *gestureState) emitEvent(ev Event) {
	if g.emit != nil {
		g.emit(ev)
	}
}

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

	x, y      float32
	presses   []press
	lastClick click
}

func (g *gestureState) enter(x, y float32) {
	g.x, g.y = x, y
	g.emitEvent(Event{Kind: Position, X: x, Y: y})
}

func (g *gestureState) leave() {
	g.presses = nil
	g.lastClick = click{}
}

func (g *gestureState) motion(t uint32, x, y float32) {
	g.x, g.y = x, y
	g.emitEvent(Event{Kind: Position, X: x, Y: y, Time: t})

	for i := range g.presses {
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
		g.emitEvent(Event{
			Kind:   kind,
			X:      x,
			Y:      y,
			StartX: p.startX,
			StartY: p.startY,
			Button: p.button,
			Serial: p.serial,
			Time:   t,
		})
	}
}

func (g *gestureState) button(serial, t, button uint32, down bool) {
	kind := ButtonUp
	if down {
		kind = ButtonDown
	}
	g.emitEvent(Event{
		Kind:   kind,
		X:      g.x,
		Y:      g.y,
		Button: button,
		Serial: serial,
		Time:   t,
	})

	if down {
		if g.pressIndex(button) >= 0 {
			return
		}
		g.presses = append(g.presses, press{
			button: button,
			serial: serial,
			time:   t,
			startX: g.x,
			startY: g.y,
		})
		return
	}

	i := g.pressIndex(button)
	if i < 0 {
		return
	}
	p := g.presses[i]
	g.presses = append(g.presses[:i], g.presses[i+1:]...)
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
	kind = Click
	dx, dy := g.x-g.lastClick.x, g.y-g.lastClick.y
	if g.lastClick.valid &&
		g.lastClick.button == button &&
		t-g.lastClick.time <= doubleClickInterval &&
		dx*dx+dy*dy <= dragThresholdSquared {
		kind = DoubleClick
	}
	g.emitEvent(Event{
		Kind:   kind,
		X:      g.x,
		Y:      g.y,
		Button: button,
		Serial: serial,
		Time:   t,
	})
	if kind == DoubleClick {
		g.lastClick = click{}
		return
	}
	g.lastClick = click{valid: true, button: p.button, time: t, x: g.x, y: g.y}
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

package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// This file holds the custom widgets and layouts that reproduce the
// mockup's flat, state-based design: fixed-gap flex-like layouts, the 64px
// selection tiles, the 46x26 toggle switch, the 74px pill CTA and the 36px
// round gear button.

// ---------------------------------------------------------------------------
// Layouts (the mockup uses flexbox with fixed gaps; Fyne has no such layouts)
// ---------------------------------------------------------------------------

// hgapLayout lays children out in a row with a fixed gap, each at its min
// width, vertically centered in the row.
type hgapLayout struct{ gap float32 }

func (h hgapLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, ht float32
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		w += m.Width
		n++
		if m.Height > ht {
			ht = m.Height
		}
	}
	if n > 1 {
		w += h.gap * float32(n-1)
	}
	return fyne.NewSize(w, ht)
}

func (h hgapLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	x := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		o.Resize(m)
		o.Move(fyne.NewPos(x, (size.Height-m.Height)/2))
		x += m.Width + h.gap
	}
}

// tileRowLayout is a row where every visible child gets an equal share of the
// width (mockup .tile { flex: 1 }).
type tileRowLayout struct{ gap float32 }

func (t tileRowLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, ht float32
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		w += m.Width
		n++
		if m.Height > ht {
			ht = m.Height
		}
	}
	if n > 1 {
		w += t.gap * float32(n-1)
	}
	return fyne.NewSize(w, ht)
}

func (t tileRowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	n := 0
	for _, o := range objs {
		if o.Visible() {
			n++
		}
	}
	if n == 0 {
		return
	}
	w := (size.Width - t.gap*float32(n-1)) / float32(n)
	ht := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		if mh := o.MinSize().Height; mh > ht {
			ht = mh
		}
	}
	x := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		o.Resize(fyne.NewSize(w, ht))
		o.Move(fyne.NewPos(x, 0))
		x += w + t.gap
	}
}

// vgapLayout stacks children vertically with a fixed gap and stretches every
// child to the full width (mockup #stControls, top-aligned).
type vgapLayout struct{ gap float32 }

func (v vgapLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		if m.Width > w {
			w = m.Width
		}
		h += m.Height
		n++
	}
	if n > 1 {
		h += v.gap * float32(n-1)
	}
	return fyne.NewSize(w, h)
}

func (v vgapLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		mh := o.MinSize().Height
		o.Resize(fyne.NewSize(size.Width, mh))
		o.Move(fyne.NewPos(0, y))
		y += mh + v.gap
	}
}

// centerVGap stacks children vertically with a fixed gap and centers the
// stack horizontally and vertically (mockup treating / complete states).
type centerVGap struct{ gap float32 }

func (c centerVGap) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		if m.Width > w {
			w = m.Width
		}
		h += m.Height
		n++
	}
	if n > 1 {
		h += c.gap * float32(n-1)
	}
	return fyne.NewSize(w, h)
}

func (c centerVGap) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	total := float32(0)
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		total += o.MinSize().Height
		n++
	}
	if n > 1 {
		total += c.gap * float32(n-1)
	}
	y := (size.Height - total) / 2
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		o.Resize(m)
		o.Move(fyne.NewPos((size.Width-m.Width)/2, y))
		y += m.Height + c.gap
	}
}

// padLayout gives its single child fixed insets (the mockup's padding values).
type padLayout struct{ top, right, bottom, left float32 }

func (p padLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) == 0 {
		return fyne.Size{}
	}
	m := objs[0].MinSize()
	return fyne.NewSize(m.Width+p.left+p.right, m.Height+p.top+p.bottom)
}

func (p padLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) == 0 {
		return
	}
	objs[0].Move(fyne.NewPos(p.left, p.top))
	objs[0].Resize(fyne.NewSize(size.Width-p.left-p.right, size.Height-p.top-p.bottom))
}

// bodyLayout is the mockup .body: one state section is visible at a time.
// The treating / complete section stretches to the remaining height (its
// content centers inside it), the controls section stays top-aligned, and the
// advanced section sits below with the body gap.
type bodyLayout struct {
	gap      float32
	treat    fyne.CanvasObject
	complete fyne.CanvasObject
}

func (b bodyLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		if m.Width > w {
			w = m.Width
		}
		h += m.Height
		n++
	}
	if n > 1 {
		h += b.gap * float32(n-1)
	}
	return fyne.NewSize(w, h)
}

func (b bodyLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	natural := float32(0)
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		natural += o.MinSize().Height
		n++
	}
	gaps := float32(0)
	if n > 1 {
		gaps = b.gap * float32(n-1)
	}
	extra := size.Height - natural - gaps
	if extra < 0 {
		extra = 0
	}
	y := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		h := o.MinSize().Height
		if o == b.treat || o == b.complete {
			h += extra
		}
		o.Resize(fyne.NewSize(size.Width, h))
		o.Move(fyne.NewPos(0, y))
		y += h + b.gap
	}
}

// segPillLayout draws the mode segmented control as the mockup's .seg pill:
// a rounded, tile-colored background behind the button row.
type segPillLayout struct {
	bg  *canvas.Rectangle
	row *fyne.Container
}

func (s segPillLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	m := s.row.MinSize()
	return fyne.NewSize(m.Width+6, m.Height+4)
}

func (s segPillLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	s.bg.Resize(size)
	s.row.Move(fyne.NewPos(3, 2))
	s.row.Resize(fyne.NewSize(size.Width-6, size.Height-4))
}

// ---------------------------------------------------------------------------
// tile — 64px rounded selection tile (mockup .tile)
// ---------------------------------------------------------------------------

type tile struct {
	widget.BaseWidget
	res    fyne.Resource
	text   string
	labelW float32 // cached width of the 15px bold label text
	on     bool
	click  func()
}

func newTile(res fyne.Resource, text string, click func()) *tile {
	t := &tile{res: res, text: text, click: click}
	t.ExtendBaseWidget(t)
	// Measure the label once: MinSize() is called on every layout pass.
	tx := canvas.NewText(text, color.White)
	tx.TextSize = 15
	tx.TextStyle = fyne.TextStyle{Bold: true}
	t.labelW = tx.MinSize().Width
	return t
}

func (t *tile) MinSize() fyne.Size {
	t.ExtendBaseWidget(t)
	// 14px side padding, 30px icon, 12px icon-label gap (mockup .tile).
	return fyne.NewSize(14+30+12+t.labelW+14, 64)
}

func (t *tile) CreateRenderer() fyne.WidgetRenderer {
	t.ExtendBaseWidget(t)
	return newTileRenderer(t)
}

func (t *tile) Tapped(*fyne.PointEvent) {
	if t.click != nil {
		t.click()
	}
}

func (t *tile) Cursor() desktop.Cursor { return desktop.PointerCursor }

// setOn updates the selected state (no-op if unchanged).
func (t *tile) setOn(on bool) {
	if t.on == on {
		return
	}
	t.on = on
	t.Refresh()
}

// tileRow is a group of tiles with a single selection (duration / profile).
type tileRow struct {
	tiles []*tile
}

// obj returns the container to place in the layout (equal-width tile row).
func (r *tileRow) obj(gap float32) fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, len(r.tiles))
	for i, t := range r.tiles {
		objs[i] = t
	}
	return fyne.NewContainerWithLayout(tileRowLayout{gap: gap}, objs...)
}

// set selects index i (no per-tile change notification needed beyond the
// highlight).
func (r *tileRow) set(i int) {
	for j, t := range r.tiles {
		t.setOn(j == i)
	}
}

type tileRenderer struct {
	bg      *canvas.Rectangle
	row     fyne.CanvasObject
	icon    *widget.Icon
	iconOff fyne.Resource
	iconOn  fyne.Resource
	label   *canvas.Text
	t       *tile
}

func newTileRenderer(t *tile) *tileRenderer {
	iconOff := theme.NewThemedResource(t.res)
	iconOff.ColorName = theme.ColorNameForeground
	iconOn := theme.NewThemedResource(t.res)
	iconOn.ColorName = heatColorNavy
	icon := widget.NewIcon(iconOff)
	label := canvas.NewText(t.text, color.White)
	label.TextSize = 15
	label.TextStyle = fyne.TextStyle{Bold: true}
	row := container.NewCenter(fyne.NewContainerWithLayout(hgapLayout{gap: 12},
		container.NewGridWrap(fyne.NewSize(30, 30), icon), label))
	bg := canvas.NewRectangle(color.Black)
	bg.CornerRadius = 16
	bg.StrokeWidth = 1
	r := &tileRenderer{bg: bg, row: row, icon: icon, iconOff: iconOff, iconOn: iconOn, label: label, t: t}
	r.applyTheme()
	return r
}

func (r *tileRenderer) applyTheme() {
	th := r.t.Theme()
	if th == nil {
		return
	}
	v := theme.VariantDark // heatTheme ignores the variant
	white := th.Color(theme.ColorNameForeground, v)
	if r.t.on {
		r.bg.FillColor = th.Color(theme.ColorNamePrimary, v)
		r.bg.StrokeColor = th.Color(theme.ColorNamePrimary, v)
		r.icon.SetResource(r.iconOn)
		r.label.Color = th.Color(heatColorNavy, v)
	} else {
		r.bg.FillColor = th.Color(heatColorTile, v)
		r.bg.StrokeColor = th.Color(heatColorTileEdge, v)
		r.icon.SetResource(r.iconOff)
		r.label.Color = white
	}
}

func (r *tileRenderer) Destroy()                     {}
func (r *tileRenderer) MinSize() fyne.Size           { return r.t.MinSize() }
func (r *tileRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.bg, r.row} }
func (r *tileRenderer) Refresh()                     { r.applyTheme() }
func (r *tileRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.row.Resize(size)
}

// ---------------------------------------------------------------------------
// toggle — 46x26 iOS-style switch (mockup .tg)
// ---------------------------------------------------------------------------

type toggle struct {
	widget.BaseWidget
	on      bool
	changed func(bool)
}

func newToggle(initial bool, changed func(bool)) *toggle {
	t := &toggle{on: initial, changed: changed}
	t.ExtendBaseWidget(t)
	return t
}

func (t *toggle) MinSize() fyne.Size {
	t.ExtendBaseWidget(t)
	return fyne.NewSize(46, 26)
}

func (t *toggle) CreateRenderer() fyne.WidgetRenderer {
	t.ExtendBaseWidget(t)
	return newToggleRenderer(t)
}

func (t *toggle) Tapped(*fyne.PointEvent) {
	t.on = !t.on
	t.Refresh()
	if t.changed != nil {
		t.changed(t.on)
	}
}

func (t *toggle) Cursor() desktop.Cursor { return desktop.PointerCursor }

// setOn syncs the state without firing the callback (controller -> UI).
func (t *toggle) setOn(on bool) {
	if t.on == on {
		return
	}
	t.on = on
	t.Refresh()
}

type toggleRenderer struct {
	track *canvas.Rectangle
	knob  *canvas.Circle
	t     *toggle
}

func newToggleRenderer(t *toggle) *toggleRenderer {
	track := canvas.NewRectangle(color.Black)
	track.CornerRadius = 13
	knob := canvas.NewCircle(color.White)
	r := &toggleRenderer{track: track, knob: knob, t: t}
	r.applyTheme()
	return r
}

func (r *toggleRenderer) applyTheme() {
	th := r.t.Theme()
	if th == nil {
		return
	}
	if r.t.on {
		r.track.FillColor = th.Color(heatColorGreen, theme.VariantDark)
	} else {
		r.track.FillColor = th.Color(heatColorToggleOff, theme.VariantDark)
	}
	r.knob.FillColor = th.Color(theme.ColorNameForeground, theme.VariantDark)
}

func (r *toggleRenderer) Destroy()           {}
func (r *toggleRenderer) MinSize() fyne.Size { return fyne.NewSize(46, 26) }
func (r *toggleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.knob}
}
func (r *toggleRenderer) Refresh() { r.applyTheme() }
func (r *toggleRenderer) Layout(size fyne.Size) {
	r.track.Resize(size)
	k := float32(20)
	x := float32(3)
	if r.t.on {
		x = size.Width - 3 - k
	}
	r.knob.Move(fyne.NewPos(x, 3))
	r.knob.Resize(fyne.NewSize(k, k))
}

// ---------------------------------------------------------------------------
// cta — large pill button (mockup .cta, 74px, radius 999)
// ---------------------------------------------------------------------------

type ctaStyle int

const (
	ctaStyleOff   ctaStyle = iota // gray, "insert device"
	ctaStyleReady                 // white, "start treatment"
	ctaStyleStop                  // red, "stop treatment"
)

type cta struct {
	widget.BaseWidget
	text   string
	style  ctaStyle
	fixedW float32 // > 0: keep this width (stop button), 0: stretch to layout
	click  func()
}

func newCTA(text string, style ctaStyle, fixedW float32, click func()) *cta {
	b := &cta{text: text, style: style, fixedW: fixedW, click: click}
	b.ExtendBaseWidget(b)
	return b
}

func (b *cta) MinSize() fyne.Size {
	b.ExtendBaseWidget(b)
	tx := canvas.NewText(b.text, color.White)
	tx.TextSize = 19
	tx.TextStyle = fyne.TextStyle{Bold: true}
	w := 40 + tx.MinSize().Width
	if b.fixedW > w {
		w = b.fixedW
	}
	return fyne.NewSize(w, 74)
}

func (b *cta) setLabel(s string) {
	b.text = s
	b.Refresh()
}

func (b *cta) setStyle(s ctaStyle) {
	b.style = s
	b.Refresh()
}

func (b *cta) CreateRenderer() fyne.WidgetRenderer {
	b.ExtendBaseWidget(b)
	return newCTARenderer(b)
}

func (b *cta) Tapped(*fyne.PointEvent) {
	if b.click != nil {
		b.click()
	}
}

func (b *cta) Cursor() desktop.Cursor { return desktop.PointerCursor }

type ctaRenderer struct {
	bg    *canvas.Rectangle
	label *canvas.Text
	row   *fyne.Container
	b     *cta
}

func newCTARenderer(b *cta) *ctaRenderer {
	bg := canvas.NewRectangle(color.Black)
	bg.CornerRadius = 37
	label := canvas.NewText(b.text, color.White)
	label.TextSize = 19
	label.TextStyle = fyne.TextStyle{Bold: true}
	r := &ctaRenderer{bg: bg, label: label, row: container.NewCenter(label), b: b}
	r.apply()
	return r
}

func (r *ctaRenderer) apply() {
	th := r.b.Theme()
	if th == nil {
		return
	}
	v := theme.VariantDark
	switch r.b.style {
	case ctaStyleReady:
		r.bg.FillColor = th.Color(theme.ColorNamePrimary, v)
		r.label.Color = th.Color(heatColorNavy, v)
	case ctaStyleStop:
		r.bg.FillColor = th.Color(heatColorCtaStop, v)
		r.label.Color = th.Color(theme.ColorNameForeground, v)
	default:
		r.bg.FillColor = th.Color(heatColorCtaOff, v)
		r.label.Color = th.Color(heatColorCtaOffTxt, v)
	}
}

func (r *ctaRenderer) Destroy()           {}
func (r *ctaRenderer) MinSize() fyne.Size { return r.b.MinSize() }
func (r *ctaRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.row}
}
func (r *ctaRenderer) Refresh() {
	r.label.Text = r.b.text
	r.apply()
}
func (r *ctaRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.row.Resize(size)
}

// ---------------------------------------------------------------------------
// roundBtn — 36px circular gear button (mockup .gear)
// ---------------------------------------------------------------------------

type roundBtn struct {
	widget.BaseWidget
	res   fyne.Resource
	click func()
}

func newRoundBtn(res fyne.Resource, click func()) *roundBtn {
	b := &roundBtn{res: res, click: click}
	b.ExtendBaseWidget(b)
	return b
}

func (b *roundBtn) MinSize() fyne.Size {
	b.ExtendBaseWidget(b)
	return fyne.NewSize(36, 36)
}

func (b *roundBtn) CreateRenderer() fyne.WidgetRenderer {
	b.ExtendBaseWidget(b)
	return newRoundBtnRenderer(b)
}

func (b *roundBtn) Tapped(*fyne.PointEvent) {
	if b.click != nil {
		b.click()
	}
}

func (b *roundBtn) Cursor() desktop.Cursor { return desktop.PointerCursor }

type roundBtnRenderer struct {
	bg     *canvas.Rectangle
	center *fyne.Container
	b      *roundBtn
}

func newRoundBtnRenderer(b *roundBtn) *roundBtnRenderer {
	res := theme.NewThemedResource(b.res)
	res.ColorName = theme.ColorNameForeground
	bg := canvas.NewRectangle(color.Black)
	bg.CornerRadius = 18
	bg.StrokeWidth = 1
	center := container.NewCenter(container.NewGridWrap(fyne.NewSize(19, 19), widget.NewIcon(res)))
	r := &roundBtnRenderer{bg: bg, center: center, b: b}
	r.applyTheme()
	return r
}

func (r *roundBtnRenderer) applyTheme() {
	th := r.b.Theme()
	if th == nil {
		return
	}
	r.bg.FillColor = th.Color(heatColorTile, theme.VariantDark)
	r.bg.StrokeColor = th.Color(heatColorTileEdge, theme.VariantDark)
}

func (r *roundBtnRenderer) Destroy()           {}
func (r *roundBtnRenderer) MinSize() fyne.Size { return fyne.NewSize(36, 36) }
func (r *roundBtnRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.center}
}
func (r *roundBtnRenderer) Refresh() { r.applyTheme() }
func (r *roundBtnRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.center.Resize(size)
}

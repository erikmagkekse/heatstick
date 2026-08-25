// Command heatstick is a desktop app (X11, Wayland, Windows) that replicates
// the "heat it" smartphone app for the Kamedi heat it dongle.
package main

import (
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"image/color"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"heatstick/device"
)

//go:embed assets/icons
var iconFS embed.FS

// iconResource loads a bundled SVG icon by file name.
func iconResource(name string) fyne.Resource {
	data, err := iconFS.ReadFile("assets/icons/" + name)
	if err != nil {
		return nil
	}
	return fyne.NewStaticResource(name, data)
}

const appID = "com.erikmagkekse.heatstick"

// tempBase values (index into device.TemperatureLevels).
const (
	baseChild = 1 // 48.5 degC
	baseAdult = 3 // 51.5 degC
)

// Window sizes: in Normal mode the window follows the visible state's
// natural height (no dead space below the content); Advanced mode is taller
// to fit the statistics + debug cards. Normal-mode heights were measured
// from rendered content (last content pixel + 30px): content must fit inside
// the scroll or Fyne 2.8 paints a bottom overflow shadow.
var (
	idleWinSize     = fyne.NewSize(520, 460) // controls state (content ends at y=428)
	treatingWinSize = fyne.NewSize(520, 560) // 250px treatment disc (content ends at y=530)
	doneWinSize     = fyne.NewSize(520, 440) // 230px completed disc (content ends at y=407)
	advancedWinSize = fyne.NewSize(520, 860)
)

var (
	flagDark     = flag.Bool("dark", false, "start in dark mode (default: follow the system light/dark setting)")
	flagTreat    = flag.Bool("treat", false, "start a treatment automatically once connected")
	flagStayOpen = flag.Bool("stay-open", false, "keep the window open when the dongle is unplugged (default: close)")
	flagAuto     = flag.Bool("auto", false, "internal: this instance was started by the hotplug monitor")
)

// trafficLog is a small thread-safe ring buffer of frame log lines.
type trafficLog struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

func (t *trafficLog) push(dir string, frame []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, fmt.Sprintf("%s  %s  %s", time.Now().Format("15:04:05.000"), dir, device.FrameHex(frame)))
	if len(t.lines) > t.cap {
		t.lines = t.lines[len(t.lines)-t.cap:]
	}
}

func (t *trafficLog) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

func (t *trafficLog) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = nil
}

type ctrl struct {
	mu             sync.Mutex
	dev            *device.Device
	connected      bool
	connectedAt    time.Time
	lastConnectTry time.Time // throttles the auto-reconnect USB probe

	status device.Status // live device status

	// settings
	tempBase  int
	sensitive bool
	durLevel  int
	soundsOn  bool // live mirror of the gear popover's Sounds toggle

	// treatment
	treating    bool
	treatStart  time.Time
	preheatSec  float64
	durationSec float64
	lastError   string

	// debug info
	version    *device.VersionInfo
	versionErr string
	stats      *device.Statistics
	statsRaw   []byte
	statsErr   string

	// disconnectFn is called once when the dongle is unplugged while
	// connected (the GUI closes the window unless -stay-open is set).
	disconnectFn       func()
	disconnectNotified bool

	log *trafficLog
}

func (c *ctrl) tempLevel() int {
	lv := c.tempBase
	if c.sensitive {
		lv--
	}
	if lv < 0 {
		lv = 0
	}
	if lv > 3 {
		lv = 3
	}
	return lv
}

func (c *ctrl) targetTemp() float64 { return device.TemperatureLevels[c.tempLevel()] }

func (c *ctrl) durationSecSetting() float64 { return device.DurationLevels[c.durLevel] }

// The setting setters lock c.mu: the UI thread writes settings while the
// poll goroutine reads them under c.mu (tempLevel/startTreatment).
func (c *ctrl) setDurLevel(lv int) {
	c.mu.Lock()
	c.durLevel = lv
	c.mu.Unlock()
}

func (c *ctrl) setTempBase(b int) {
	c.mu.Lock()
	c.tempBase = b
	c.mu.Unlock()
}

func (c *ctrl) setSensitive(on bool) {
	c.mu.Lock()
	c.sensitive = on
	c.mu.Unlock()
}

// setSoundsOn / soundsEnabled mirror the live "Sounds" toggle so the
// unplug cue (fired from the poll goroutine) honors it like the other cues.
func (c *ctrl) setSoundsOn(on bool) {
	c.mu.Lock()
	c.soundsOn = on
	c.mu.Unlock()
}

func (c *ctrl) soundsEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.soundsOn
}

func (c *ctrl) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}
	dev, err := device.Open()
	if err != nil {
		c.lastError = err.Error()
		return err
	}
	// Arm the firmware's phase LED (standby = green slow-flash, warmup = violet
	// blink, active/ready = blue steady). This matches the real app's on-connect
	// command and makes the dongle's LED follow the treatment phase.
	_ = dev.SetLed(255, 255, 255, 1)
	dev.SetFrameLog(c.log.push)
	c.dev = dev
	c.connected = true
	c.connectedAt = time.Now()
	c.lastError = ""
	c.disconnectNotified = false
	return nil
}

func (c *ctrl) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnectLocked()
}

// disconnectLocked clears the connection state; the caller must hold c.mu.
func (c *ctrl) disconnectLocked() {
	if c.dev != nil {
		_ = c.dev.Close()
	}
	c.dev = nil
	c.connected = false
	c.treating = false
}

func (c *ctrl) startTreatment() error {
	c.mu.Lock()
	dev := c.dev
	if !c.connected {
		c.mu.Unlock()
		return fmt.Errorf("no device connected")
	}
	lv, dur := c.tempLevel(), c.durLevel
	c.mu.Unlock()

	preheat, err := dev.StartHeating(byte(lv), byte(dur))
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.treating = true
	c.treatStart = time.Now()
	c.preheatSec = preheat
	c.durationSec = device.DurationLevels[dur]
	c.lastError = ""
	c.mu.Unlock()
	return nil
}

func (c *ctrl) abort() {
	c.mu.Lock()
	dev := c.dev
	c.treating = false
	c.mu.Unlock()
	if dev != nil {
		_ = dev.Abort()
	}
}

// pollOnce refreshes the live status from the device.
func (c *ctrl) pollOnce() {
	c.mu.Lock()
	dev := c.dev
	if !c.connected {
		// Present() enumerates the whole USB tree, so throttle the
		// auto-(re)connect probe to ~1/s.
		try := time.Since(c.lastConnectTry) >= time.Second
		if try {
			c.lastConnectTry = time.Now()
		}
		c.mu.Unlock()
		if try && device.Present() {
			_ = c.connect()
		}
		return
	}
	treating := c.treating
	c.mu.Unlock()

	st, err := dev.GetStatus()
	if err != nil {
		// Distinguish a real unplug from a transient USB error.
		if device.Present() {
			return
		}
		c.mu.Lock()
		if !c.connected {
			c.mu.Unlock()
			return
		}
		c.disconnectLocked()
		c.lastError = err.Error()
		fn, fire := c.disconnectFn, !c.disconnectNotified
		if fire {
			c.disconnectNotified = true
		}
		c.mu.Unlock()
		if fire && fn != nil {
			fn()
		}
		return
	}
	c.mu.Lock()
	c.status = *st
	// A treatment is over once the device returns to idle.
	if treating && st.ExternalStatus == 0x00 {
		c.treating = false
	}
	c.mu.Unlock()
}

// snapshot returns a consistent read of the controller state for the UI.
func (c *ctrl) snapshot() (connected, treating bool, status device.Status, preheat, duration float64, start time.Time, err string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected, c.treating, c.status, c.preheatSec, c.durationSec, c.treatStart, c.lastError
}

// settingsSnapshot returns a consistent read of the treatment settings plus
// the effective target temperature and duration.
func (c *ctrl) settingsSnapshot() (tempBase int, sensitive bool, durLevel int, target, durSec float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tempBase, c.sensitive, c.durLevel, device.TemperatureLevels[c.tempLevel()], device.DurationLevels[c.durLevel]
}

type debugInfo struct {
	version    *device.VersionInfo
	versionErr string
	stats      *device.Statistics
	statsRaw   []byte
	statsErr   string
}

func (c *ctrl) debugSnapshot() debugInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return debugInfo{c.version, c.versionErr, c.stats, c.statsRaw, c.statsErr}
}

func (c *ctrl) readVersion() {
	c.mu.Lock()
	dev := c.dev
	c.mu.Unlock()
	if dev == nil {
		return
	}
	v, err := dev.GetVersionInfo()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.versionErr = err.Error()
		return
	}
	c.version = v
	c.versionErr = ""
}

func (c *ctrl) readStats() {
	c.mu.Lock()
	dev := c.dev
	var ver *device.VersionInfo
	if c.version != nil {
		ver = c.version
	}
	c.mu.Unlock()
	if dev == nil {
		return
	}
	raw, err := dev.GetStatistics()
	if err == nil {
		legacy := false
		if ver != nil {
			legacy = ver.IsLegacy()
		}
		st, derr := device.DecodeStatistics(raw, legacy)
		if derr != nil {
			err = derr
		} else {
			c.mu.Lock()
			c.stats = st
			c.statsRaw = raw
			c.statsErr = ""
			c.mu.Unlock()
		}
	}
	c.mu.Lock()
	if err != nil {
		c.statsErr = err.Error()
	}
	c.mu.Unlock()
}

func (c *ctrl) setLED(r, g, b, modifier byte) {
	c.mu.Lock()
	dev := c.dev
	c.mu.Unlock()
	if dev == nil {
		return
	}
	_ = dev.SetLed(r, g, b, modifier)
}

func (c *ctrl) sendRaw(frame []byte) ([]byte, error) {
	c.mu.Lock()
	dev := c.dev
	c.mu.Unlock()
	if dev == nil {
		return nil, fmt.Errorf("no device connected")
	}
	return dev.Request(frame)
}

// parseHexFrame parses "ff 0a ff ff ff 03" or "ff0afffff03" into 12 bytes.
func parseHexFrame(s string) ([]byte, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	var out []byte
	for _, tok := range fields {
		if tok == "" {
			continue
		}
		if len(tok)%2 != 0 {
			return nil, fmt.Errorf("odd-length token %q", tok)
		}
		for i := 0; i < len(tok); i += 2 {
			b, err := hex.DecodeString(tok[i : i+2])
			if err != nil {
				return nil, fmt.Errorf("bad byte %q", tok[i:i+2])
			}
			out = append(out, b[0])
		}
	}
	if len(out) != 12 {
		return nil, fmt.Errorf("frame must be 12 bytes, got %d", len(out))
	}
	return out, nil
}

type ui struct {
	win fyne.Window // set after the window is created; used to resize on mode switch

	// top bar
	modeSeg *segmented
	gearBtn *roundBtn
	popover *widget.PopUp

	// controls state (mockup #stControls)
	durRow        *tileRow
	profRow       *tileRow
	sensBg        *canvas.Rectangle
	sensLabel     *canvas.Text
	sensSmall     *canvas.Text
	sensToggle    *toggle
	targetLine    *canvas.Text
	cta           *cta
	stateControls fyne.CanvasObject

	// treating state (mockup #stTreating)
	treatCircle   *canvas.Circle
	treatNum      *canvas.Text
	treatSub      *canvas.Text
	stopCta       *cta
	stateTreating fyne.CanvasObject

	// complete state (mockup #stComplete)
	doneCircle    *canvas.Circle
	doneLabel     *canvas.Text
	doneSub       *canvas.Text
	stateComplete fyne.CanvasObject

	// settings (gear popover)
	soundsCheck    *widget.Check
	autostartCheck *widget.Check
	darkCheck      *widget.Check
	volumeSlider   *widget.Slider
	volLabel       *widget.Label

	// advanced mode (hidden in Normal mode)
	advWrap      fyne.CanvasObject
	statsCard    *widget.Card
	statsLabel   *widget.RichText
	debugCard    *widget.Card
	rawEntry     *widget.Entry
	rawResult    *widget.Label
	versionLabel *widget.Label
	trafficLabel *widget.RichText

	// theme state
	dark bool

	// language: resolved at startup, switched in the settings menu (rebuilds UI)
	lang    string
	langSeg *segmented

	// last state seen by refresh, for edge-triggered sound cues and the
	// transient "Treatment Completed" display
	lastTreating  bool
	lastPhase     string
	lastConnected bool
	showDone      bool
	doneAt        time.Time

	// window sizing: track the visible state (Normal mode resizes the window
	// to fit the state's content) and whether Advanced mode is active
	lastUIState string
	advanced    bool
}

// segmented is a compact pill of buttons where the active one is highlighted
// (mockup .seg, used for the Normal/Advanced switch). It wraps a plain
// *fyne.Container which is what gets placed in the layout tree — Fyne only
// recurses into concrete *fyne.Container when painting, so the wrapper itself
// must not be the canvas object.
type segmented struct {
	bg    *canvas.Rectangle
	box   *fyne.Container
	btns  []*widget.Button
	cur   int
	onChg func(int)
}

// Obj returns the container to place in the layout.
func (s *segmented) Obj() fyne.CanvasObject { return s.box }

func newSegmented(labels []string, initial int, onChg func(int)) *segmented {
	s := &segmented{cur: initial, onChg: onChg}
	row := container.NewHBox()
	for i, l := range labels {
		b := widget.NewButton(l, func() {
			s.cur = i
			s.refresh()
			if s.onChg != nil {
				s.onChg(i)
			}
		})
		s.btns = append(s.btns, b)
		row.Add(b)
	}
	s.bg = canvas.NewRectangle(color.Transparent)
	s.bg.CornerRadius = 18
	s.bg.StrokeWidth = 1
	s.box = fyne.NewContainerWithLayout(segPillLayout{bg: s.bg, row: row}, s.bg, row)
	s.refresh()
	s.refreshTheme()
	return s
}

// set syncs the highlight to index i (no-op if unchanged).
func (s *segmented) set(i int) {
	if i < 0 || i >= len(s.btns) || i == s.cur {
		return
	}
	s.cur = i
	s.refresh()
	if s.onChg != nil {
		s.onChg(i)
	}
}

func (s *segmented) refresh() {
	for i, b := range s.btns {
		if i == s.cur {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.LowImportance
		}
		b.Refresh()
	}
}

// refreshTheme re-resolves the pill background from the active theme (the
// main refresh loop calls this whenever the theme changes).
func (s *segmented) refreshTheme() {
	a := fyne.CurrentApp()
	if a == nil {
		return
	}
	th := a.Settings().Theme()
	s.bg.FillColor = th.Color(heatColorTile, theme.VariantDark)
	s.bg.StrokeColor = th.Color(heatColorTileEdge, theme.VariantDark)
	s.bg.Refresh()
}

// appSettings are the persisted user preferences (Fyne Preferences, stored
// under the app id).
type appSettings struct {
	tempBase  int
	sensitive bool
	durLevel  int
	advanced  bool
	dark      bool
	sounds    bool
	volume    int    // 0-100, applied to all sound cues
	lang      string // resolved language: "de" or "en"
}

func loadSettings(a fyne.App) appSettings {
	p := a.Preferences()
	s := appSettings{sounds: true, tempBase: baseChild}
	if b := p.Int("tempBase"); b == baseChild || b == baseAdult {
		s.tempBase = b
	}
	s.sensitive = p.Bool("sensitive")
	if lv := p.Int("durLevel"); lv >= 0 && lv < len(device.DurationLevels) {
		s.durLevel = lv
	}
	s.advanced = p.Bool("advanced")
	s.dark = p.Bool("dark")
	s.sounds = !p.BoolWithFallback("soundsOff", false) // unset -> sounds on
	s.volume = 80
	if v := p.IntWithFallback("volume", 80); v >= 0 && v <= 100 {
		s.volume = v
	}
	if l := p.String("lang"); l == "de" || l == "en" {
		s.lang = l
	} else {
		s.lang = "" // follow the system locale
	}
	return s
}

func main() {
	// Subcommands run before the GUI: `heatstick monitor` (headless hotplug
	// watcher) and `heatstick hotplug install|uninstall` (autostart setup).
	if sub := os.Args[1:]; len(sub) > 0 {
		switch sub[0] {
		case "monitor":
			runMonitor()
			return
		case "hotplug":
			if len(sub) > 1 && sub[1] == "install" {
				runHotplugInstall()
				return
			}
			if len(sub) > 1 && sub[1] == "uninstall" {
				runHotplugUninstall()
				return
			}
			fmt.Fprintln(os.Stderr, "usage: heatstick hotplug (install|uninstall)")
			os.Exit(2)
		}
	}

	flag.Parse()

	// Single instance: if another instance is active, ask it to come to the
	// front and exit.
	l, own := becomeInstance()
	if !own {
		fmt.Println("heatstick is already running; brought it to the front.")
		return
	}
	if l != nil {
		defer func() {
			_ = l.Close()
			if network, addr := instanceSocket(); network == "unix" {
				_ = os.Remove(addr)
			}
		}()
	}

	a := app.NewWithID(appID)
	s := loadSettings(a)
	if s.lang == "" {
		s.lang = resolveLang("")
	}
	// The app always uses the Kamedi navy theme; the Dark toggle selects
	// the deeper-navy variant.
	a.Settings().SetTheme(heatThemeFor(*flagDark || s.dark))
	// Apply the saved volume to all sound cues before any can play.
	SetSoundVolume(float64(s.volume) / 100)

	c := &ctrl{tempBase: s.tempBase, sensitive: s.sensitive, durLevel: s.durLevel, soundsOn: s.sounds, log: &trafficLog{cap: 120}}

	u := &ui{}
	content := buildUI(a, c, u, s)

	w := a.NewWindow("heatstick")
	u.win = w
	w.Resize(idleWinSize)
	w.SetContent(content)
	w.CenterOnScreen()

	if l != nil {
		serveActivation(l, func() {
			w.Show()
			w.RequestFocus()
		})
	}

	// Close the window when the dongle is unplugged, unless -stay-open.
	// The unplug cue is the plug-in chime's notes in reverse order (natural
	// bell strikes, not a reversed recording); in close mode the window
	// stays up until it has finished (the process exits as soon as the last
	// window closes). The delay runs in its own goroutine so the poll loop
	// (and its auto-reconnect) is not blocked in between.
	c.disconnectFn = func() {
		if c.soundsEnabled() {
			playSound("treat-unplug.wav")
		}
		if *flagStayOpen {
			return
		}
		go func() {
			time.Sleep(2100 * time.Millisecond)
			fyne.Do(func() { w.Close() })
		}()
	}

	// Poll the device for live status.
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		for range t.C {
			c.pollOnce()
		}
	}()

	// Refresh the UI.
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		for range t.C {
			fyne.Do(func() { refresh(a, c, u) })
		}
	}()

	// Refresh the traffic log.
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		for range t.C {
			fyne.Do(func() {
				u.trafficLabel.Segments = monoseg(u.trafficText(c), u.lang)
				u.trafficLabel.Refresh()
			})
		}
	}()

	// Auto-connect on startup.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = c.connect()
	}()

	// Auto-start a treatment (useful for screenshots / demos).
	if *flagTreat {
		go func() {
			deadline := time.Now().Add(180 * time.Second)
			for time.Now().Before(deadline) {
				connected, treating, _, _, _, _, _ := c.snapshot()
				if connected && !treating {
					if err := c.startTreatment(); err == nil {
						return
					}
				}
				time.Sleep(200 * time.Millisecond)
			}
		}()
	}

	// One-time notices: the independent-project disclaimer on the very first
	// launch, and (if that was already seen) an explanation of the automatic
	// start the first time the hotplug monitor launches the app.
	showDisclaimer := !a.Preferences().Bool("seenDisclaimer")
	if showDisclaimer {
		a.Preferences().SetBool("seenDisclaimer", true)
	}
	showAutoStart := *flagAuto && !a.Preferences().Bool("seenAutoStart")
	if showAutoStart {
		a.Preferences().SetBool("seenAutoStart", true)
	}

	w.Show()
	if showDisclaimer {
		dialog.NewInformation(t(s.lang, "disclaimerTitle"), t(s.lang, "disclaimerBody"), w).Show()
	} else if showAutoStart {
		dialog.NewInformation(t(s.lang, "autoStartTitle"), t(s.lang, "autoStartBody"), w).Show()
	}
	a.Run()
}

func monoseg(text, lang string) []widget.RichTextSegment {
	if text == "" {
		text = t(lang, "noTraffic")
	}
	return []widget.RichTextSegment{
		&widget.TextSegment{Text: text, Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Monospace: true}}},
	}
}

func (u *ui) trafficText(c *ctrl) string { return c.log.text() }

func buildUI(a fyne.App, c *ctrl, u *ui, s appSettings) fyne.CanvasObject {
	u.lang = s.lang
	// --- Settings controls (live in the gear popover) ---
	// SetChecked fires OnChanged in Fyne 2.8, so assign each callback only
	// after the initial state to avoid side effects at startup.
	u.darkCheck = widget.NewCheck(t(u.lang, "darkMode"), nil)
	u.darkCheck.SetChecked(*flagDark || s.dark)
	u.darkCheck.OnChanged = func(on bool) {
		u.dark = on
		a.Settings().SetTheme(heatThemeFor(on))
		a.Preferences().SetBool("dark", on)
	}
	u.soundsCheck = widget.NewCheck(t(u.lang, "soundsRow"), nil)
	u.soundsCheck.SetChecked(s.sounds)
	u.soundsCheck.OnChanged = func(on bool) {
		c.setSoundsOn(on)
		a.Preferences().SetBool("soundsOff", !on)
	}
	u.autostartCheck = widget.NewCheck(t(u.lang, "autostartRow"), nil)
	u.autostartCheck.SetChecked(hotplugInstalled())
	u.autostartCheck.OnChanged = func(on bool) {
		go func() {
			err := func() error {
				if on {
					return hotplugInstall()
				}
				return hotplugUninstall()
			}()
			if err == nil {
				if on {
					// One short explanation of what the monitor now does.
					fyne.Do(func() {
						dialog.NewInformation(t(u.lang, "autostartTitle"), t(u.lang, "autostartBody"), u.win).Show()
					})
				}
				return
			}
			// GUI path must never kill the app: report and roll the
			// checkbox back instead of exiting.
			fmt.Fprintf(os.Stderr, "heatstick: autostart: %v\n", err)
			// SetChecked fires OnChanged in Fyne 2.8, so park the
			// callback for the duration of the programmatic reset.
			fyne.Do(func() {
				cb := u.autostartCheck.OnChanged
				u.autostartCheck.OnChanged = nil
				u.autostartCheck.SetChecked(!on)
				u.autostartCheck.OnChanged = cb
			})
		}()
	}
	u.volLabel = widget.NewLabel(fmt.Sprintf("%d%%", s.volume))
	u.volLabel.Alignment = fyne.TextAlignTrailing
	u.volumeSlider = widget.NewSlider(0, 100)
	u.volumeSlider.SetValue(float64(s.volume))
	u.volumeSlider.OnChanged = func(v float64) {
		SetSoundVolume(v / 100)
		a.Preferences().SetInt("volume", int(v))
		u.volLabel.Text = fmt.Sprintf("%d%%", int(v))
		u.volLabel.Refresh()
	}
	volumeRow := container.NewBorder(nil, nil, widget.NewLabel(t(u.lang, "volumeRow")), u.volLabel, u.volumeSlider)
	// Language: System / DE / EN. Switching rebuilds the whole UI, since
	// every label text is resolved at build time.
	langIdx := 0
	switch s.lang {
	case "de":
		langIdx = 1
	case "en":
		langIdx = 2
	}
	u.langSeg = newSegmented([]string{t(u.lang, "langSystem"), "DE", "EN"}, langIdx, func(i int) {
		pref := []string{"", "de", "en"}[i]
		a.Preferences().SetString("lang", pref)
		newLang := resolveLang(pref)
		if newLang == u.lang {
			return
		}
		if u.popover != nil {
			u.popover.Hide()
			u.popover = nil
		}
		// Resync the settings snapshot from live state, then rebuild.
		s.lang = newLang
		s.dark = u.dark
		s.advanced = u.advanced
		s.sounds = c.soundsEnabled()
		s.volume = int(u.volumeSlider.Value)
		u.win.SetContent(buildUI(a, c, u, s))
	})
	languageRow := container.NewBorder(nil, nil, widget.NewLabel(t(u.lang, "languageRow")), u.langSeg.Obj(), layout.NewSpacer())
	settingsContent := container.NewPadded(container.NewVBox(
		u.autostartCheck,
		volumeRow,
		u.soundsCheck,
		u.darkCheck,
		languageRow,
	))
	u.dark = *flagDark || s.dark

	// --- Top bar: logo, mode switch, settings gear (mockup .topbar) ---
	modeIdx := 0
	if s.advanced {
		modeIdx = 1
	}
	u.modeSeg = newSegmented([]string{t(u.lang, "modeNormal"), t(u.lang, "modeAdvanced")}, modeIdx, func(i int) {
		u.setMode(i == 1)
		a.Preferences().SetBool("advanced", i == 1)
	})
	u.gearBtn = newRoundBtn(theme.SettingsIcon(), func() {
		// Build the popup lazily: the window/canvas only exists once main has
		// created it, which is always true by the time a tap arrives.
		if u.popover == nil {
			u.popover = widget.NewPopUp(settingsContent, u.win.Canvas())
		}
		// Anchor the popover's right edge to the gear's right edge. Tapping
		// the gear re-opens/re-anchors it; tapping anywhere else dismisses it.
		pw := settingsContent.MinSize().Width
		gs := u.gearBtn.Size()
		rel := fyne.NewPos(gs.Width-pw, gs.Height)
		u.popover.ShowAtRelativePosition(rel, u.gearBtn)
	})

	// Logo: "heat" + chip "stick" (mockup .logo / .logo-chip)
	th := a.Settings().Theme()
	white := th.Color(theme.ColorNameForeground, theme.VariantDark)
	navy := th.Color(heatColorNavy, theme.VariantDark)
	logoText := canvas.NewText("heat", white)
	logoText.TextSize = 26
	logoText.TextStyle = fyne.TextStyle{Bold: true}
	chipText := canvas.NewText("stick", navy)
	chipText.TextSize = 20
	chipText.TextStyle = fyne.TextStyle{Bold: true}
	ms := chipText.MinSize()
	chipBg := canvas.NewRectangle(th.Color(heatColorChip, theme.VariantDark))
	chipBg.CornerRadius = 9
	chipSize := fyne.NewSize(ms.Width+20, 29)
	chipBg.Resize(chipSize)
	chip := container.NewGridWrap(chipSize,
		container.NewStack(chipBg, container.NewCenter(chipText)))
	logo := fyne.NewContainerWithLayout(hgapLayout{gap: 7}, logoText, chip)

	right := fyne.NewContainerWithLayout(hgapLayout{gap: 10}, u.modeSeg.Obj(), u.gearBtn)
	topBar := container.NewBorder(nil, nil, logo, right, layout.NewSpacer())
	topPad := fyne.NewContainerWithLayout(padLayout{top: 20, right: 22, bottom: 14, left: 22}, topBar)

	// --- Controls state (mockup #stControls): flat tiles, no cards ---
	u.durRow = &tileRow{tiles: []*tile{
		newTile(iconResource("dur-short.svg"), t(u.lang, "durShort"), func() {
			c.setDurLevel(0)
			a.Preferences().SetInt("durLevel", 0)
		}),
		newTile(iconResource("dur-medium.svg"), t(u.lang, "durMedium"), func() {
			c.setDurLevel(1)
			a.Preferences().SetInt("durLevel", 1)
		}),
		newTile(iconResource("dur-long.svg"), t(u.lang, "durLong"), func() {
			c.setDurLevel(2)
			a.Preferences().SetInt("durLevel", 2)
		}),
	}}
	u.profRow = &tileRow{tiles: []*tile{
		newTile(iconResource("person-child.svg"), t(u.lang, "personChild"), func() {
			c.setTempBase(baseChild)
			a.Preferences().SetInt("tempBase", baseChild)
		}),
		newTile(iconResource("person-adult.svg"), t(u.lang, "personAdult"), func() {
			c.setTempBase(baseAdult)
			a.Preferences().SetInt("tempBase", baseAdult)
		}),
	}}
	u.sensToggle = newToggle(s.sensitive, func(on bool) {
		c.setSensitive(on)
		a.Preferences().SetBool("sensitive", on)
	})
	u.sensLabel = canvas.NewText(t(u.lang, "sensitiveRow"), white)
	u.sensLabel.TextSize = 15
	u.sensLabel.TextStyle = fyne.TextStyle{Bold: true}
	u.sensSmall = canvas.NewText("−1.5 °C", th.Color(heatColorMuted, theme.VariantDark))
	u.sensSmall.TextSize = 12
	u.sensSmall.TextStyle = fyne.TextStyle{Bold: true}
	feather := widget.NewIcon(iconResource("feather.svg"))
	sensLeft := fyne.NewContainerWithLayout(hgapLayout{gap: 14},
		container.NewGridWrap(fyne.NewSize(26, 26), feather), u.sensLabel)
	sensRight := fyne.NewContainerWithLayout(hgapLayout{gap: 14}, u.sensSmall, u.sensToggle)
	sensContent := fyne.NewContainerWithLayout(padLayout{right: 18, left: 18},
		container.NewBorder(nil, nil, sensLeft, sensRight, layout.NewSpacer()))
	u.sensBg = canvas.NewRectangle(th.Color(heatColorTile, theme.VariantDark))
	u.sensBg.CornerRadius = 18
	u.sensBg.StrokeColor = th.Color(heatColorTileEdge, theme.VariantDark)
	u.sensBg.StrokeWidth = 1
	sensRow := fyne.NewContainerWithLayout(fixedHeightLayout{62},
		container.NewStack(u.sensBg, sensContent))

	u.targetLine = canvas.NewText("", th.Color(heatColorMuted, theme.VariantDark))
	u.targetLine.TextSize = 13
	u.targetLine.TextStyle = fyne.TextStyle{Italic: true}
	u.targetLine.Alignment = fyne.TextAlignCenter

	// CTA (mockup .cta): gray "insert" while unplugged, white "start" when
	// connected. Tapping it while unplugged retries the connection.
	u.cta = newCTA(t(u.lang, "ctaInsert"), ctaStyleOff, 0, func() {
		connected, treating, _, _, _, _, _ := c.snapshot()
		if !connected {
			go func() { _ = c.connect() }()
			return
		}
		if treating {
			go c.abort()
			return
		}
		go func() {
			if err := c.startTreatment(); err != nil {
				c.mu.Lock()
				c.lastError = err.Error()
				c.mu.Unlock()
			}
		}()
	})

	u.stateControls = fyne.NewContainerWithLayout(vgapLayout{gap: 14},
		u.durRow.obj(12), u.profRow.obj(12), sensRow, u.targetLine, u.cta)

	// --- Treating state (mockup #stTreating): 250px circle + countdown ---
	// The disc follows the stick's phase LED: violet while warming up,
	// blue once the treatment is active (colors set in refresh).
	u.treatCircle = canvas.NewCircle(th.Color(heatColorPhaseWarmup, theme.VariantDark))
	u.treatCircle.StrokeColor = th.Color(heatColorPhaseWarmupRing, theme.VariantDark)
	u.treatCircle.StrokeWidth = 7
	u.treatNum = canvas.NewText("", white)
	u.treatNum.TextSize = 96
	u.treatNum.TextStyle = fyne.TextStyle{Bold: true}
	treatDisc := container.NewGridWrap(fyne.NewSize(250, 250),
		container.NewStack(u.treatCircle, container.NewCenter(u.treatNum)))
	treatLabel := canvas.NewText(t(u.lang, "treatProgress"), white)
	treatLabel.TextSize = 21
	treatLabel.TextStyle = fyne.TextStyle{Bold: true}
	u.treatSub = canvas.NewText(t(u.lang, "warmingUp"), th.Color(heatColorPhaseWarmupRing, theme.VariantDark))
	u.treatSub.TextSize = 13
	u.treatSub.TextStyle = fyne.TextStyle{Bold: true}
	u.stopCta = newCTA(t(u.lang, "ctaStop"), ctaStyleStop, 260, func() { go c.abort() })
	u.stateTreating = fyne.NewContainerWithLayout(centerVGap{gap: 22},
		treatDisc, treatLabel, u.treatSub, u.stopCta)
	u.stateTreating.Hide()

	// --- Complete state (mockup #stComplete): 230px check circle ---
	u.doneCircle = canvas.NewCircle(th.Color(heatColorGreenSoft, theme.VariantDark))
	u.doneCircle.StrokeColor = th.Color(heatColorGreen, theme.VariantDark)
	u.doneCircle.StrokeWidth = 6
	checkRes := theme.NewThemedResource(iconResource("check.svg"))
	checkRes.ColorName = heatColorGreenDeep
	doneCheck := widget.NewIcon(checkRes)
	doneDisc := container.NewGridWrap(fyne.NewSize(230, 230),
		container.NewStack(u.doneCircle,
			container.NewCenter(container.NewGridWrap(fyne.NewSize(110, 110), doneCheck))))
	u.doneLabel = canvas.NewText(t(u.lang, "treatDone"), white)
	u.doneLabel.TextSize = 21
	u.doneLabel.TextStyle = fyne.TextStyle{Bold: true}
	u.doneSub = canvas.NewText("", th.Color(heatColorMuted, theme.VariantDark))
	u.doneSub.TextSize = 13
	u.stateComplete = fyne.NewContainerWithLayout(centerVGap{gap: 22},
		doneDisc, u.doneLabel, u.doneSub)
	u.stateComplete.Hide()

	// --- Statistics (advanced mode) ---
	u.statsLabel = widget.NewRichTextWithText("")
	u.statsLabel.Wrapping = fyne.TextWrapWord
	u.statsCard = widget.NewCard(t(u.lang, "statsCard"), "", container.NewVBox(
		widget.NewButtonWithIcon(t(u.lang, "readStats"), theme.HistoryIcon(), func() {
			go func() {
				c.readStats()
				fyne.Do(func() { refreshStats(u, c.debugSnapshot()) })
			}()
		}),
		u.statsLabel,
	))

	// --- Debug (advanced mode) ---
	u.rawEntry = widget.NewMultiLineEntry()
	u.rawEntry.PlaceHolder = t(u.lang, "rawPlaceholder")
	u.rawResult = widget.NewLabel("")
	u.rawResult.Wrapping = fyne.TextWrapWord
	sendRawBtn := widget.NewButtonWithIcon(t(u.lang, "sendBtn"), theme.MailSendIcon(), func() {
		go func() {
			text := u.rawEntry.Text
			frame, err := parseHexFrame(text)
			var msg string
			if err != nil {
				msg = err.Error()
			} else {
				resp, rerr := c.sendRaw(frame)
				if rerr != nil {
					msg = rerr.Error()
				} else {
					ck := "BAD"
					if device.ChecksumOK(resp) {
						ck = "ok"
					}
					msg = fmt.Sprintf("resp: %s   (checksum %s)", device.FrameHex(resp), ck)
				}
			}
			fyne.Do(func() {
				u.rawResult.Text = msg
				u.rawResult.Refresh()
			})
		}()
	})

	u.versionLabel = widget.NewLabel("")
	u.versionLabel.Wrapping = fyne.TextWrapWord

	ledBtn := func(key string, r, g, b, mod byte) *widget.Button {
		btn := widget.NewButton(t(u.lang, key), func() { c.setLED(r, g, b, mod) })
		btn.Importance = widget.LowImportance
		return btn
	}
	ledRow := container.NewHBox(
		ledBtn("ledStandby", 255, 255, 255, 1),
		ledBtn("ledOff", 0, 0, 0, 0),
		ledBtn("ledGreen", 0, 255, 0, 4),
		ledBtn("ledRed", 255, 0, 0, 4),
		ledBtn("ledBlue", 0, 0, 255, 4),
		ledBtn("ledWhite", 255, 255, 255, 4),
	)

	u.trafficLabel = widget.NewRichTextWithText("")
	u.trafficLabel.Wrapping = fyne.TextWrapWord
	// Cap the log to a bounded, vertically scrollable box. Use an explicit
	// container.NewScroll (the RichText's built-in Scroll property paints empty
	// inside a fixed-height container); fixedHeightLayout forces it to 200px.
	trafficBox := fyne.NewContainerWithLayout(fixedHeightLayout{200},
		container.NewScroll(u.trafficLabel))

	u.debugCard = widget.NewCard(t(u.lang, "debugCard"), "", container.NewVBox(
		widget.NewLabelWithStyle(t(u.lang, "rawSend"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		u.rawEntry,
		container.NewBorder(nil, nil, sendRawBtn, nil, layout.NewSpacer()),
		u.rawResult,
		widget.NewSeparator(),
		container.NewHBox(
			widget.NewButtonWithIcon(t(u.lang, "readVersion"), theme.InfoIcon(), func() {
				go func() {
					c.readVersion()
					fyne.Do(func() { refreshVersion(u, c.debugSnapshot()) })
				}()
			}),
			layout.NewSpacer(),
		),
		u.versionLabel,
		widget.NewSeparator(),
		widget.NewLabelWithStyle(t(u.lang, "ledRow"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		ledRow,
		widget.NewSeparator(),
		container.NewHBox(
			widget.NewLabelWithStyle(t(u.lang, "trafficRow"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			layout.NewSpacer(),
			widget.NewButton(t(u.lang, "clearBtn"), func() { c.log.clear() }),
		),
		trafficBox,
	))

	// --- Body: one state section visible at a time, advanced below ---
	u.advWrap = fyne.NewContainerWithLayout(vgapLayout{gap: 12}, u.statsCard, u.debugCard)
	body := fyne.NewContainerWithLayout(
		bodyLayout{gap: 14, treat: u.stateTreating, complete: u.stateComplete},
		u.stateControls, u.stateTreating, u.stateComplete, u.advWrap)
	bodyPad := fyne.NewContainerWithLayout(padLayout{top: 8, right: 22, bottom: 24, left: 22}, body)
	// Restore the UI mode (hides the advanced section in Normal mode).
	u.setMode(s.advanced)
	return container.NewBorder(topPad, nil, nil, nil, container.NewScroll(bodyPad))
}

// setMode shows the advanced section in Advanced mode and hides it in
// Normal, and resizes the window to fit the active mode's content.
func (u *ui) setMode(advanced bool) {
	u.advanced = advanced
	u.lastUIState = "" // the next refresh resizes for the current state
	if advanced {
		u.advWrap.Show()
	} else {
		u.advWrap.Hide()
	}
	if u.win != nil && advanced {
		u.win.Resize(advancedWinSize)
	}
}

func refreshVersion(u *ui, d debugInfo) {
	if d.versionErr != "" {
		u.versionLabel.Text = d.versionErr
	} else if d.version != nil {
		u.versionLabel.Text = d.version.String()
	} else {
		u.versionLabel.Text = ""
	}
	u.versionLabel.Refresh()
}

func refreshStats(u *ui, d debugInfo) {
	if d.statsErr != "" {
		u.statsLabel.Segments = monoseg(d.statsErr, u.lang)
	} else if d.stats != nil {
		text := d.stats.String() + fmt.Sprintf("raw: %s", device.FrameHex(d.statsRaw))
		u.statsLabel.Segments = monoseg(text, u.lang)
	} else {
		u.statsLabel.Segments = monoseg("", u.lang)
	}
	u.statsLabel.Refresh()
}

// refresh updates the UI from the controller state (main goroutine).
func refresh(a fyne.App, c *ctrl, u *ui) {
	connected, treating, st, preheat, duration, start, err := c.snapshot()

	// Calm sound cues, edge-triggered on state changes.
	phase := ""
	if connected {
		phase = st.Phase()
	}
	// Treatment just finished: show the "Treatment Completed" circle for
	// 2.6s (mockup behavior), then the controls come back.
	if connected && !treating && u.lastTreating && !u.showDone {
		u.showDone = true
		u.doneAt = time.Now()
	}
	if u.showDone && time.Since(u.doneAt) > 2600*time.Millisecond {
		u.showDone = false
	}
	if treating || !connected {
		u.showDone = false
	}
	if c.soundsEnabled() {
		switch {
		case connected && !u.lastConnected:
			// Dongle plugged in (fresh start or re-plug): hello cue.
			playSound("treat-start.wav")
		case connected && treating && u.lastTreating && phase == "active" && u.lastPhase == "warmup":
			playSound("phase-active.wav")
		case connected && !treating && u.lastTreating:
			// Treatment finished: complete cue.
			playSound("treat-complete.wav")
		}
	}
	u.lastTreating, u.lastPhase, u.lastConnected = treating, phase, connected

	th := a.Settings().Theme()
	v := a.Settings().ThemeVariant()
	white := th.Color(theme.ColorNameForeground, v)
	green := th.Color(heatColorGreen, v)
	greenDeep := th.Color(heatColorGreenDeep, v)
	muted := th.Color(heatColorMuted, v)

	// Re-resolve theme-dependent static colors (Dark toggle / treating bg).
	u.sensBg.FillColor = th.Color(heatColorTile, v)
	u.sensBg.StrokeColor = th.Color(heatColorTileEdge, v)
	u.sensBg.Refresh()
	u.sensLabel.Color = white
	u.sensLabel.Refresh()
	u.sensSmall.Color = muted
	u.sensSmall.Refresh()
	u.targetLine.Color = muted
	u.doneSub.Color = muted
	u.treatSub.Color = greenDeep
	u.treatNum.Color = white
	u.treatCircle.FillColor = th.Color(heatColorBg, v)
	u.treatCircle.StrokeColor = green
	u.treatCircle.Refresh()
	u.doneCircle.FillColor = th.Color(heatColorGreenSoft, v)
	u.doneCircle.StrokeColor = green
	u.doneCircle.Refresh()
	u.modeSeg.refreshTheme()

	// Treatment settings (sync controls to controller state).
	tempBase, sensitive, durLevel, target, durSec := c.settingsSnapshot()
	u.durRow.set(durLevel)
	if tempBase == baseAdult {
		u.profRow.set(1)
	} else {
		u.profRow.set(0)
	}
	u.sensToggle.setOn(sensitive)
	targetText := fmt.Sprintf(t(u.lang, "targetFor"), target, durSec)
	u.targetLine.Text = targetText
	u.targetLine.Refresh()
	u.doneSub.Text = targetText
	u.doneSub.Refresh()

	// State sections: exactly one is visible (mockup state machine).
	state := "idle"
	switch {
	case treating:
		state = "treating"
		u.stateControls.Hide()
		u.stateTreating.Show()
		u.stateComplete.Hide()
	case u.showDone:
		state = "done"
		u.stateControls.Hide()
		u.stateTreating.Hide()
		u.stateComplete.Show()
	default:
		u.stateControls.Show()
		u.stateTreating.Hide()
		u.stateComplete.Hide()
	}
	// Normal mode: the window follows the visible state's height, so no dead
	// space is left below the content (Advanced keeps its fixed size).
	if state != u.lastUIState {
		u.lastUIState = state
		if u.win != nil && !u.advanced {
			switch state {
			case "treating":
				u.win.Resize(treatingWinSize)
			case "done":
				u.win.Resize(doneWinSize)
			default:
				u.win.Resize(idleWinSize)
			}
		}
	}

	// CTA: insert / start (mockup texts and colors).
	if !connected {
		if err != "" {
			u.cta.setLabel(t(u.lang, "ctaNoDevice"))
		} else {
			u.cta.setLabel(t(u.lang, "ctaInsert"))
		}
		u.cta.setStyle(ctaStyleOff)
	} else {
		u.cta.setLabel(t(u.lang, "ctaStart"))
		u.cta.setStyle(ctaStyleReady)
	}

	// Treating state: big temperature during warm-up, countdown when active.
	// The disc and sub text take the stick's LED color for the phase.
	if treating {
		var disc, ring fyne.ThemeColorName
		if phase == "active" {
			rem := duration - (time.Since(start).Seconds() - preheat)
			if rem < 0 {
				rem = 0
			}
			u.treatNum.TextSize = 96
			u.treatNum.Text = fmt.Sprintf("%.0f", math.Ceil(rem))
			u.treatSub.Text = fmt.Sprintf("%.1f °C", st.Temperature)
			disc, ring = heatColorPhaseActive, heatColorPhaseActiveRing
		} else {
			u.treatNum.TextSize = 50
			u.treatNum.Text = fmt.Sprintf("%.1f °C", st.Temperature)
			u.treatSub.Text = t(u.lang, "warmingUp")
			disc, ring = heatColorPhaseWarmup, heatColorPhaseWarmupRing
		}
		u.treatCircle.FillColor = th.Color(disc, theme.VariantDark)
		u.treatCircle.StrokeColor = th.Color(ring, theme.VariantDark)
		u.treatSub.Color = th.Color(ring, theme.VariantDark)
		u.treatCircle.Refresh()
		u.treatNum.Refresh()
		u.treatSub.Refresh()
	}
}

// fixedHeightLayout resizes its children to a fixed height (width fills the
// available space). Used to cap the traffic log to a bounded, scrollable box —
// Fyne widgets size to their renderer's MinSize, so a plain layout cannot
// constrain a scrolling RichText's height.
type fixedHeightLayout struct{ h float32 }

func (f fixedHeightLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w float32
	for _, o := range objects {
		if mw := o.MinSize().Width; mw > w {
			w = mw
		}
	}
	return fyne.NewSize(w, f.h)
}

func (f fixedHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(fyne.NewSize(size.Width, f.h))
	}
}

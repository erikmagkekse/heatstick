// Command heatstick is a desktop app (X11, Wayland, Windows) that replicates
// the "heat it" smartphone app for the Kamedi heat it dongle.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"image/color"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"heatstick/device"
)

const appID = "com.erikmagkekse.heatstick"

// tempBase values (index into device.TemperatureLevels).
const (
	baseChild = 1 // 48.5 degC
	baseAdult = 3 // 51.5 degC
)

var (
	flagDark  = flag.Bool("dark", false, "start in dark mode (default: follow the system light/dark setting)")
	flagTreat = flag.Bool("treat", false, "start a treatment automatically once connected")
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
	mu          sync.Mutex
	dev         *device.Device
	connected   bool
	connectedAt time.Time

	status device.Status // live device status

	// settings
	tempBase  int
	sensitive bool
	durLevel  int

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
	return nil
}

func (c *ctrl) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
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
		c.mu.Unlock()
		return
	}
	treating := c.treating
	c.mu.Unlock()

	st, err := dev.GetStatus()
	if err != nil {
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
// the effective target temperature.
func (c *ctrl) settingsSnapshot() (tempBase int, sensitive bool, durLevel int, target float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tempBase, c.sensitive, c.durLevel, device.TemperatureLevels[c.tempLevel()]
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
	// top bar
	modeSeg   *segmented
	darkCheck *widget.Check

	// device / status
	connIcon   *widget.Icon
	devLabel   *widget.Label
	connectBtn *widget.Button
	tempText   *canvas.Text
	phaseLabel *widget.Label
	progress   *widget.ProgressBar

	// treatment controls
	profileSeg     *segmented
	sensitiveCheck *widget.Check
	durSeg         *segmented
	targetLabel    *widget.Label
	startBtn       *widget.Button
	abortBtn       *widget.Button

	// advanced mode (hidden in Normal mode)
	statsCard    *widget.Card
	statsLabel   *widget.RichText
	debugCard    *widget.Card
	rawEntry     *widget.Entry
	rawResult    *widget.Label
	versionLabel *widget.Label
	trafficLabel *widget.RichText

	followSystem bool
}

// segmented is a compact row of buttons where the active one is highlighted,
// used as a segmented control (Normal/Advanced, profile, duration). It wraps a
// plain *fyne.Container (HBox) which is what gets placed in the layout tree —
// Fyne only recurses into concrete *fyne.Container when painting, so the
// wrapper itself must not be the canvas object.
type segmented struct {
	box   *fyne.Container
	btns  []*widget.Button
	cur   int
	onChg func(int)
}

// Obj returns the container to place in the layout.
func (s *segmented) Obj() fyne.CanvasObject { return s.box }

func newSegmented(labels []string, initial int, onChg func(int)) *segmented {
	s := &segmented{cur: initial, onChg: onChg}
	objects := make([]fyne.CanvasObject, 0, len(labels))
	for i, l := range labels {
		b := widget.NewButton(l, func() {
			s.cur = i
			s.refresh()
			if s.onChg != nil {
				s.onChg(i)
			}
		})
		s.btns = append(s.btns, b)
		objects = append(objects, b)
	}
	s.box = container.NewHBox(objects...)
	s.refresh()
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

func main() {
	flag.Parse()

	a := app.NewWithID(appID)
	if *flagDark {
		a.Settings().SetTheme(theme.DarkTheme())
	}
	// No explicit theme otherwise: Fyne uses the default theme with the
	// system's light/dark variant and follows live system changes.

	c := &ctrl{tempBase: baseChild, durLevel: 0, log: &trafficLog{cap: 120}}

	u := &ui{}
	content := buildUI(a, c, u)

	w := a.NewWindow("heatstick")
	w.Resize(fyne.NewSize(620, 820))
	w.SetContent(content)
	w.CenterOnScreen()

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
				u.trafficLabel.Segments = monoseg(u.trafficText(c))
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

	w.Show()
	a.Run()
}

func monoseg(text string) []widget.RichTextSegment {
	if text == "" {
		text = "(no traffic yet)"
	}
	return []widget.RichTextSegment{
		&widget.TextSegment{Text: text, Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Monospace: true}}},
	}
}

func (u *ui) trafficText(c *ctrl) string { return c.log.text() }

func buildUI(a fyne.App, c *ctrl, u *ui) fyne.CanvasObject {
	// --- Top bar: title, mode switch, dark toggle ---
	u.modeSeg = newSegmented([]string{"Normal", "Advanced"}, 0, func(i int) {
		u.setMode(i == 1)
	})
	u.followSystem = !*flagDark
	u.darkCheck = widget.NewCheck("Dark", func(on bool) {
		u.followSystem = false
		if on {
			a.Settings().SetTheme(theme.DarkTheme())
		} else {
			a.Settings().SetTheme(theme.LightTheme())
		}
	})
	u.darkCheck.SetChecked(*flagDark || a.Settings().ThemeVariant() == theme.VariantDark)

	title := widget.NewLabelWithStyle("heatstick", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	right := container.NewHBox(u.modeSeg.Obj(), u.darkCheck)
	topBar := container.NewBorder(nil, nil, title, right, layout.NewSpacer())
	top := container.NewVBox(topBar, widget.NewSeparator())

	// --- Device / status card ---
	u.connIcon = widget.NewIcon(theme.WarningIcon())
	u.devLabel = widget.NewLabel("Checking device…")
	u.connectBtn = widget.NewButton("Connect", func() {
		go func() {
			if err := c.connect(); err != nil {
				fyne.Do(func() {
					u.devLabel.Text = "No device found"
					u.devLabel.Refresh()
				})
			}
		}()
	})
	u.tempText = canvas.NewText("—", color.Black)
	u.tempText.TextSize = 42
	u.tempText.TextStyle = fyne.TextStyle{Bold: true}
	u.tempText.Alignment = fyne.TextAlignCenter
	u.phaseLabel = widget.NewLabel("no device")
	u.phaseLabel.Alignment = fyne.TextAlignCenter
	u.progress = widget.NewProgressBar()
	u.progress.SetValue(0)

	deviceCard := widget.NewCard("Device", "", container.NewVBox(
		container.NewHBox(u.connIcon, u.devLabel, layout.NewSpacer(), u.connectBtn),
		u.tempText,
		u.phaseLabel,
		u.progress,
	))

	// --- Treatment card ---
	u.profileSeg = newSegmented([]string{
		fmt.Sprintf("Child (%.1f °C)", device.TemperatureLevels[baseChild]),
		fmt.Sprintf("Adult (%.1f °C)", device.TemperatureLevels[baseAdult]),
	}, 0, func(i int) {
		if i == 0 {
			c.tempBase = baseChild
		} else {
			c.tempBase = baseAdult
		}
	})
	u.sensitiveCheck = widget.NewCheck("Sensitive (−1.5 °C)", func(on bool) { c.sensitive = on })
	u.durSeg = newSegmented([]string{"Short (4 s)", "Medium (7 s)", "Long (9 s)"}, 0, func(i int) {
		c.durLevel = i
	})
	u.targetLabel = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})

	treatmentGrid := container.NewGridWithColumns(2,
		widget.NewLabelWithStyle("Profile", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		u.profileSeg.Obj(),
		widget.NewLabelWithStyle("Duration", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		u.durSeg.Obj(),
	)
	treatmentCard := widget.NewCard("Treatment", "", container.NewVBox(
		treatmentGrid,
		u.sensitiveCheck,
		u.targetLabel,
	))

	// --- Actions ---
	u.startBtn = widget.NewButtonWithIcon("Start Treatment", theme.MediaPlayIcon(), func() {
		go func() {
			if err := c.startTreatment(); err != nil {
				c.mu.Lock()
				c.lastError = err.Error()
				c.mu.Unlock()
			}
		}()
	})
	u.startBtn.Importance = widget.HighImportance
	u.abortBtn = widget.NewButtonWithIcon("Abort", theme.MediaStopIcon(), func() {
		go c.abort()
	})
	actionCard := widget.NewCard("", "", container.NewVBox(u.startBtn, u.abortBtn))

	// --- Statistics (advanced mode) ---
	u.statsLabel = widget.NewRichTextWithText("")
	u.statsLabel.Wrapping = fyne.TextWrapWord
	u.statsCard = widget.NewCard("Statistics", "", container.NewVBox(
		widget.NewButtonWithIcon("Read statistics", theme.HistoryIcon(), func() {
			go func() {
				c.readStats()
				fyne.Do(func() { refreshStats(u, c.debugSnapshot()) })
			}()
		}),
		u.statsLabel,
	))

	// --- Debug (advanced mode) ---
	u.rawEntry = widget.NewEntry()
	u.rawEntry.PlaceHolder = "ff 0a ff ff ff 03   (12 bytes hex)"
	u.rawResult = widget.NewLabel("")
	u.rawResult.Wrapping = fyne.TextWrapWord
	rawRow := container.NewHBox(u.rawEntry,
		widget.NewButton("Send", func() {
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
		}))

	u.versionLabel = widget.NewLabel("")
	u.versionLabel.Wrapping = fyne.TextWrapWord

	ledBtn := func(label string, r, g, b, mod byte) *widget.Button {
		btn := widget.NewButton(label, func() { c.setLED(r, g, b, mod) })
		btn.Importance = widget.LowImportance
		return btn
	}
	ledRow := container.NewHBox(
		ledBtn("Standby", 255, 255, 255, 1),
		ledBtn("Off", 0, 0, 0, 0),
		ledBtn("Green", 0, 255, 0, 4),
		ledBtn("Red", 255, 0, 0, 4),
		ledBtn("Blue", 0, 0, 255, 4),
		ledBtn("White", 255, 255, 255, 4),
	)

	u.trafficLabel = widget.NewRichTextWithText("")
	u.trafficLabel.Wrapping = fyne.TextWrapWord

	u.debugCard = widget.NewCard("Debug", "", container.NewVBox(
		widget.NewLabelWithStyle("Raw frame send", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		rawRow,
		u.rawResult,
		widget.NewSeparator(),
		container.NewHBox(
			widget.NewButtonWithIcon("Read version", theme.InfoIcon(), func() {
				go func() {
					c.readVersion()
					fyne.Do(func() { refreshVersion(u, c.debugSnapshot()) })
				}()
			}),
			layout.NewSpacer(),
		),
		u.versionLabel,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("LED", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		ledRow,
		widget.NewSeparator(),
		container.NewHBox(
			widget.NewLabelWithStyle("Traffic", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			layout.NewSpacer(),
			widget.NewButton("Clear", func() { c.log.clear() }),
		),
		u.trafficLabel,
	))

	// --- Body: user cards always, advanced cards toggled by mode ---
	body := container.NewVBox(
		deviceCard,
		treatmentCard,
		actionCard,
		u.statsCard,
		u.debugCard,
	)
	// Default to Normal mode: hide the advanced cards (modeSeg starts at 0).
	u.setMode(false)
	return container.NewBorder(top, nil, nil, nil, container.NewScroll(container.NewPadded(body)))
}

// setMode shows the advanced cards in Advanced mode and hides them in Normal.
func (u *ui) setMode(advanced bool) {
	if advanced {
		u.statsCard.Show()
		u.debugCard.Show()
	} else {
		u.statsCard.Hide()
		u.debugCard.Hide()
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
		u.statsLabel.Segments = monoseg(d.statsErr)
	} else if d.stats != nil {
		text := d.stats.String() + fmt.Sprintf("raw: %s", device.FrameHex(d.statsRaw))
		u.statsLabel.Segments = monoseg(text)
	} else {
		u.statsLabel.Segments = monoseg("")
	}
	u.statsLabel.Refresh()
}

// refresh updates the UI from the controller state (main goroutine).
func refresh(a fyne.App, c *ctrl, u *ui) {
	// Keep the "Dark" check in sync while following the system setting.
	if u.followSystem {
		u.darkCheck.SetChecked(a.Settings().ThemeVariant() == theme.VariantDark)
	}

	connected, treating, st, preheat, duration, start, err := c.snapshot()

	// Device / status
	if connected {
		u.connIcon.SetResource(theme.CheckButtonCheckedIcon())
		u.devLabel.Text = "Device connected"
	} else if err != "" {
		u.connIcon.SetResource(theme.WarningIcon())
		u.devLabel.Text = "No device found"
	} else {
		u.connIcon.SetResource(theme.WarningIcon())
		u.devLabel.Text = "Not connected"
	}
	u.connIcon.Refresh()
	u.devLabel.Refresh()
	setButtonEnabled(u.connectBtn, !connected)

	if connected {
		u.tempText.Text = fmt.Sprintf("%.1f °C", st.Temperature)
		if treating {
			u.phaseLabel.Text = "phase: " + st.Phase()
			total := preheat + duration
			if total > 0 {
				elapsed := time.Since(start).Seconds()
				p := elapsed / total
				if p < 0 {
					p = 0
				}
				if p > 1 {
					p = 1
				}
				u.progress.SetValue(p)
			}
		} else {
			u.phaseLabel.Text = "idle"
			u.progress.SetValue(0)
		}
	} else {
		u.tempText.Text = "—"
		u.phaseLabel.Text = "no device"
		u.progress.SetValue(0)
	}
	u.tempText.Color = a.Settings().Theme().Color(theme.ColorNameForeground, a.Settings().ThemeVariant())
	u.tempText.Refresh()
	u.phaseLabel.Refresh()

	// Treatment settings (sync controls to controller state)
	tempBase, sensitive, durLevel, target := c.settingsSnapshot()
	if tempBase == baseAdult {
		u.profileSeg.set(1)
	} else {
		u.profileSeg.set(0)
	}
	u.durSeg.set(durLevel)
	if u.sensitiveCheck.Checked != sensitive {
		u.sensitiveCheck.SetChecked(sensitive)
	}
	u.targetLabel.Text = fmt.Sprintf("Target %.1f °C", target)
	u.targetLabel.Refresh()

	// Actions
	setButtonEnabled(u.startBtn, connected && !treating)
	setButtonEnabled(u.abortBtn, treating)
}

// setButtonEnabled enables or disables a button based on the flag.
func setButtonEnabled(b *widget.Button, enabled bool) {
	if enabled {
		b.Enable()
	} else {
		b.Disable()
	}
}

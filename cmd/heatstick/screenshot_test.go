package main

import (
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/test"

	"heatstick/device"
)

// TestScreenshots renders the real app UI with Fyne's deterministic software
// (test) painter and writes PNGs to docs/screenshots.
//
// The GL driver's Canvas.Capture() is unreliable on desktop: it does
// glReadPixels on the non-preserved front buffer, which returns noise. The
// software painter renders the same buildUI() tree in memory, so this is a
// pixel-faithful substitute for a window screenshot.
//
// If the dongle is connected, real status/version/statistics data is used;
// otherwise plausible simulated values are used, so the test passes
// headless.
//
// Regenerate with: go test ./cmd/heatstick/ -run TestScreenshots -v
func TestScreenshots(t *testing.T) {
	outDir := filepath.Join("..", "..", "docs", "screenshots")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}

	shoot := func(name string, dark, treating, done, advanced bool, size fyne.Size) {
		t.Run(name, func(t *testing.T) {
			a := test.NewApp()
			a.Settings().SetTheme(heatThemeFor(dark))

			c := &ctrl{tempBase: baseAdult, durLevel: 2, sensitive: true, log: &trafficLog{cap: 120}}
			populateCtrl(c)

			if treating {
				c.status = device.Status{Temperature: 50.3, ExternalStatus: 0x02}
				c.treating = true
				c.treatStart = time.Now().Add(-4 * time.Second)
				c.preheatSec = 3.0
				c.durationSec = 7.0
			}

			u := &ui{}
			w := a.NewWindow("heatstick")
			w.SetContent(buildUI(a, c, u, appSettings{sounds: true}))
			// Silence the sound cues while refresh() runs. SetChecked fires
			// OnChanged in Fyne 2.8, but the callback only writes the test
			// app's in-memory preferences, so it is harmless here; the
			// default-on state is restored before the shot.
			u.soundsCheck.SetChecked(false)
			if done {
				u.lastTreating = true // makes refresh() show the completed state
			}
			if advanced {
				u.setMode(true)
			}
			w.Resize(size)
			if dark {
				u.darkCheck.SetChecked(true)
			}
			w.Canvas().(software.WindowlessCanvas).SetScale(2) // crisp 2x output

			refresh(a, c, u)
			u.soundsCheck.SetChecked(true)
			d := c.debugSnapshot()
			refreshVersion(u, d)
			refreshStats(u, d)
			u.trafficLabel.Segments = monoseg(c.log.text(), u.lang)
			u.trafficLabel.Refresh()

			// The GL driver re-runs container layouts on every render pass
			// (min-size changes bubble up to the parent). The software
			// painter does not, so a widget that refresh() newly shows (e.g.
			// the temperature label) would render at its stale zero size.
			// Nudging the size forces the one genuine re-layout we need.
			w.Resize(fyne.NewSize(size.Width+2, size.Height+2))
			w.Resize(size)

			// The nudge only re-layouts containers whose own size changed;
			// the fixed-size dial GridWrap does not change, so its centered
			// countdown (built with empty text, min width 0) keeps its stale
			// zero width and renders shifted right of the disc center.
			// Refresh() re-runs the layout synchronously and recursively,
			// exactly the extra pass GL would do before the next frame.
			u.stateTreating.Refresh()
			u.stateComplete.Refresh()

			img := w.Canvas().Capture()
			path := filepath.Join(outDir, name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			defer f.Close()
			if err := png.Encode(f, img); err != nil {
				t.Fatalf("encode %s: %v", path, err)
			}
			t.Logf("wrote %s (%dx%d px)", path, img.Bounds().Dx(), img.Bounds().Dy())
		})
	}

	shoot("idle", false, false, false, false, idleWinSize)
	shoot("treating", false, true, false, false, treatingWinSize)
	shoot("complete", false, false, true, false, doneWinSize)
	shoot("dark", true, false, false, false, idleWinSize)
	shoot("advanced", false, false, false, true, fyne.NewSize(520, 1400))
}

// populateCtrl fills the controller with real device data if the dongle is
// connected, otherwise with plausible simulated values.
func populateCtrl(c *ctrl) {
	dev, err := device.Open()
	if err == nil {
		defer dev.Close()
		c.log.clear()
		dev.SetFrameLog(c.log.push)
		c.dev = dev
		c.connected = true
		c.connectedAt = time.Now()
		if st, err := dev.GetStatus(); err == nil {
			c.status = *st
		}
		if v, err := dev.GetVersionInfo(); err == nil {
			c.version = v
		}
		if raw, err := dev.GetStatistics(); err == nil {
			if s, derr := device.DecodeStatistics(raw, true); derr == nil {
				c.stats = s
				c.statsRaw = raw
			}
		}
		return
	}

	// Simulated fallback (no hardware present).
	c.connected = true
	c.connectedAt = time.Now()
	c.status = device.Status{Temperature: 22.4}
	c.version = &device.VersionInfo{Raw: []byte{2, 8, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0}}

	var blob [81]byte
	binary.BigEndian.PutUint16(blob[2:4], 47)
	binary.BigEndian.PutUint16(blob[4:6], 118)
	counts := []uint16{17, 22, 12, 9, 6, 4, 3, 2, 2, 1, 0, 0}
	maxt := []int16{478, 502, 497, 489, 481, 476, 473, 470, 469, 468, 0, 0}
	for i := 0; i < 12; i++ {
		binary.BigEndian.PutUint16(blob[12+4*i:14+4*i], counts[i])
		binary.BigEndian.PutUint16(blob[14+4*i:16+4*i], uint16(int32(maxt[i])))
	}
	binary.BigEndian.PutUint16(blob[60:62], 49)
	binary.BigEndian.PutUint16(blob[62:64], 379)
	if s, err := device.DecodeStatistics(blob[:], true); err == nil {
		c.stats = s
		c.statsRaw = blob[:]
	}

	// A little synthetic traffic so the debug pane looks alive:
	// status request/response (22.4 degC, idle) + statistics page request.
	c.log.push("OUT", []byte{0xff, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	c.log.push("IN", []byte{0xff, 0x00, 0x00, 0xe0, 0x00, 0x00, 0x00, 0x00, 0xe0, 0x00, 0x00, 0x00})
	c.log.push("OUT", []byte{0xff, 0x07, 0x00, 0x07, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
}

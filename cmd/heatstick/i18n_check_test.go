package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// TestI18nDE builds the UI in German, verifies the translated labels, and
// checks that switching the language rebuilds the UI in English.
func TestI18nDE(t *testing.T) {
	a := test.NewApp()
	a.Settings().SetTheme(heatThemeFor(false))
	c := &ctrl{tempBase: baseAdult, durLevel: 2, sensitive: true, log: &trafficLog{cap: 120}}
	populateCtrl(c)
	w := test.NewWindow(fyne.NewContainer())
	u := &ui{}
	u.win = w
	buildUI(a, c, u, appSettings{lang: "de"})

	checks := []struct {
		got, want string
	}{
		{u.darkCheck.Text, "Dunkelmodus"},
		{u.autostartCheck.Text, "App starten, wenn Dongle eingesteckt wird"},
		{u.sensLabel.Text, "Empfindlich"},
		{u.cta.text, "heatstick einstecken zum Starten"},
		{u.stopCta.text, "Behandlung stoppen"},
		{u.doneLabel.Text, "Behandlung abgeschlossen"},
		{u.treatSub.Text, "Erwärmt…"},
		{u.durRow.tiles[0].text, "Kurz"},
		{u.durRow.tiles[2].text, "Lang"},
		{u.profRow.tiles[0].text, "Kind"},
		{u.profRow.tiles[1].text, "Erwachsene"},
	}
	for i, k := range checks {
		if k.got != k.want {
			t.Errorf("check %d: got %q want %q", i, k.got, k.want)
		}
	}
	if t.Failed() {
		return
	}
	t.Logf("German labels OK")

	// Switch to English: the handler rebuilds the whole UI.
	test.Tap(u.langSeg.btns[2])
	if u.lang != "en" {
		t.Fatalf("language after switch: got %q want en", u.lang)
	}
	if u.darkCheck.Text != "Dark mode" {
		t.Errorf("rebuild: darkCheck = %q want Dark mode", u.darkCheck.Text)
	}
	if u.cta.text != "Insert heatstick to start" {
		t.Errorf("rebuild: cta = %q", u.cta.text)
	}
}

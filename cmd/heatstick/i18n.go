package main

import (
	"strings"

	"fyne.io/fyne/v2"
)

// UI languages: "de" and "en". The "lang" preference stores "", "de" or
// "en"; "" means follow the system locale (resolved at startup and on
// language switch).

var trEN = map[string]string{
	"modeNormal":      "Normal",
	"modeAdvanced":    "Advanced",
	"darkMode":        "Dark mode",
	"soundsRow":       "Sounds (plug-in, phase, complete, unplug)",
	"autostartRow":    "Start app when dongle is plugged in",
	"volumeRow":       "Volume",
	"languageRow":     "Language",
	"langSystem":      "System",
	"durShort":        "Short",
	"durMedium":       "Medium",
	"durLong":         "Long",
	"personChild":     "Child",
	"personAdult":     "Adult",
	"sensitiveRow":    "Sensitive",
	"targetFor":       "%.1f °C for %.0f s",
	"ctaInsert":       "Insert heatstick to start",
	"ctaNoDevice":     "No device found",
	"ctaStart":        "Start treatment",
	"ctaStop":         "Stop treatment",
	"treatProgress":   "Treatment in Progress",
	"warmingUp":       "Warming up…",
	"treatDone":       "Treatment Completed",
	"statsCard":       "Statistics",
	"readStats":       "Read statistics",
	"debugCard":       "Debug",
	"rawSend":         "Raw frame send",
	"rawPlaceholder":  "ff 0a ff ff ff 03   (12 bytes hex)",
	"sendBtn":         "Send",
	"readVersion":     "Read version",
	"ledRow":          "LED",
	"trafficRow":      "Traffic",
	"clearBtn":        "Clear",
	"ledStandby":      "Standby",
	"ledOff":          "Off",
	"ledGreen":        "Green",
	"ledRed":          "Red",
	"ledBlue":         "Blue",
	"ledWhite":        "White",
	"noTraffic":       "(no traffic yet)",
	"disclaimerTitle": "Important notice",
	"disclaimerBody":  "heatstick is an independent open-source project (Unlicense) and is not affiliated with or endorsed by Kamedi GmbH or the “heat it” brand. It has not been reviewed by Kamedi. Use at your own risk.",
	"autostartTitle":  "Autostart enabled",
	"autostartBody":   "A small background service is now running. When you plug in the dongle, the app starts automatically. You can turn this off at any time in this menu.",
	"autoStartTitle":  "Started automatically",
	"autoStartBody":   "The dongle was plugged in, so the app started automatically. How it works: plug in the dongle, the app opens, you start your treatment.",
}

var trDE = map[string]string{
	"modeNormal":      "Normal",
	"modeAdvanced":    "Erweitert",
	"darkMode":        "Dunkelmodus",
	"soundsRow":       "Geräusche (Einstecken, Phase, Ende, Ausstecken)",
	"autostartRow":    "App starten, wenn Dongle eingesteckt wird",
	"volumeRow":       "Lautstärke",
	"languageRow":     "Sprache",
	"langSystem":      "System",
	"durShort":        "Kurz",
	"durMedium":       "Mittel",
	"durLong":         "Lang",
	"personChild":     "Kind",
	"personAdult":     "Erwachsene",
	"sensitiveRow":    "Empfindlich",
	"targetFor":       "%.1f °C für %.0f s",
	"ctaInsert":       "heatstick einstecken zum Starten",
	"ctaNoDevice":     "Kein Gerät gefunden",
	"ctaStart":        "Behandlung starten",
	"ctaStop":         "Behandlung stoppen",
	"treatProgress":   "Behandlung läuft",
	"warmingUp":       "Erwärmt…",
	"treatDone":       "Behandlung abgeschlossen",
	"statsCard":       "Statistiken",
	"readStats":       "Statistiken lesen",
	"debugCard":       "Debug",
	"rawSend":         "Roh-Frame senden",
	"rawPlaceholder":  "ff 0a ff ff ff 03   (12 Bytes hex)",
	"sendBtn":         "Senden",
	"readVersion":     "Version lesen",
	"ledRow":          "LED",
	"trafficRow":      "Datenaustausch",
	"clearBtn":        "Leeren",
	"ledStandby":      "Bereitschaft",
	"ledOff":          "Aus",
	"ledGreen":        "Grün",
	"ledRed":          "Rot",
	"ledBlue":         "Blau",
	"ledWhite":        "Weiß",
	"noTraffic":       "(noch kein Datenaustausch)",
	"disclaimerTitle": "Wichtiger Hinweis",
	"disclaimerBody":  "heatstick ist ein unabhängiges Open-Source-Projekt (Unlicense) und steht in keinem Zusammenhang mit Kamedi GmbH oder der Marke „heat it“. Es wurde nicht von Kamedi geprüft oder freigegeben. Nutzung auf eigene Verantwortung.",
	"autostartTitle":  "Autostart aktiviert",
	"autostartBody":   "Ein kleiner Hintergrund-Dienst läuft jetzt. Wenn du den Dongle einsteckst, startet die App automatisch. Du kannst das jederzeit in diesem Menü wieder ausschalten.",
	"autoStartTitle":  "Automatisch gestartet",
	"autoStartBody":   "Der Dongle wurde eingesteckt, daher hat sich die App automatisch gestartet. So geht’s: Dongle einstecken, die App öffnet sich, dann Behandlung starten.",
}

// t returns the translation of key for lang ("de"; anything else -> "en").
// Unknown keys fall back to the key itself so missing strings are visible.
func t(lang, key string) string {
	if lang == "de" {
		if s, ok := trDE[key]; ok {
			return s
		}
	}
	if s, ok := trEN[key]; ok {
		return s
	}
	return key
}

// resolveLang maps a stored "lang" preference to a concrete language,
// following the system locale when unset.
func resolveLang(pref string) string {
	if pref == "de" || pref == "en" {
		return pref
	}
	if l := fyne.CurrentDevice().Locale().LanguageString(); strings.HasPrefix(l, "de") {
		return "de"
	}
	return "en"
}

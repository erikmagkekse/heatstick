package main

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Custom theme color names for the mockup palette, so canvas objects and the
// custom widgets can resolve them through the active theme (they adapt to the
// Dark toggle and the treating background).
const (
	heatColorBg              fyne.ThemeColorName = "heatBg"              // navy app background (always)
	heatColorTile            fyne.ThemeColorName = "heatTile"            // tile / segment / gear background
	heatColorTileEdge        fyne.ThemeColorName = "heatTileEdge"        // tile / segment border
	heatColorGreen           fyne.ThemeColorName = "heatGreen"           // accent green
	heatColorGreenSoft       fyne.ThemeColorName = "heatGreenSoft"       // light green (treating background, completed circle)
	heatColorGreenDeep       fyne.ThemeColorName = "heatGreenDeep"       // deep green (sub texts, check mark)
	heatColorMuted           fyne.ThemeColorName = "heatMuted"           // muted secondary text
	heatColorChip            fyne.ThemeColorName = "heatChip"            // logo chip background
	heatColorNavy            fyne.ThemeColorName = "heatNavy"            // text and icons on white
	heatColorCtaOff          fyne.ThemeColorName = "heatCtaOff"          // disabled CTA background
	heatColorCtaOffTxt       fyne.ThemeColorName = "heatCtaOffTxt"       // disabled CTA text
	heatColorCtaStop         fyne.ThemeColorName = "heatCtaStop"         // stop CTA background
	heatColorToggleOff       fyne.ThemeColorName = "heatToggleOff"       // toggle track (off)
	heatColorPhaseWarmup     fyne.ThemeColorName = "heatPhaseWarmup"     // treating disc while warm-up (stick LED: violet)
	heatColorPhaseWarmupRing fyne.ThemeColorName = "heatPhaseWarmupRing" // warm-up disc ring + sub text
	heatColorPhaseActive     fyne.ThemeColorName = "heatPhaseActive"     // treating disc when active (stick LED: blue)
	heatColorPhaseActiveRing fyne.ThemeColorName = "heatPhaseActiveRing" // active disc ring + sub text
)

// heatTheme wraps Fyne's built-in dark theme with the Kamedi navy palette
// (dark navy, white active elements, meadow green) sampled from the original
// vendor app's screenshots. The background is always navy; while a treatment
// runs, only the treating disc takes the stick's LED color (violet warm-up,
// blue active).
type heatTheme struct {
	base fyne.Theme
	dark bool
}

// heatThemeFor returns the Kamedi navy theme; dark selects the deeper-navy
// variant.
func heatThemeFor(dark bool) heatTheme {
	return heatTheme{base: theme.DarkTheme(), dark: dark}
}

func (t heatTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameBackground {
		if t.dark {
			return heatHex("1B2340")
		}
		return heatHex("2B3A61")
	}
	if c, ok := t.customColor(n); ok {
		return c
	}
	pal := heatPalette
	if t.dark {
		pal = heatDarkPalette
	}
	if c, ok := pal[n]; ok {
		return c
	}
	return t.base.Color(n, theme.VariantDark)
}

// customColor resolves the mockup-specific color names.
func (t heatTheme) customColor(n fyne.ThemeColorName) (color.Color, bool) {
	switch n {
	case heatColorBg:
		if t.dark {
			return heatHex("1B2340"), true
		}
		return heatHex("2B3A61"), true
	case heatColorTile:
		if t.dark {
			return heatHex("151C36"), true
		}
		return heatHex("242C54"), true
	case heatColorTileEdge:
		return heatHex("33406B"), true
	case heatColorGreen:
		return heatHex("9DBD4F"), true
	case heatColorGreenSoft:
		return heatHex("D9E5AC"), true
	case heatColorGreenDeep:
		return heatHex("55701C"), true
	case heatColorMuted:
		if t.dark {
			return heatHex("6E7A9E"), true
		}
		return heatHex("8E9BC0"), true
	case heatColorChip:
		return heatHex("9FC1E7"), true
	case heatColorNavy:
		return heatHex("242C54"), true
	case heatColorCtaOff:
		return heatHex("C9CDD6"), true
	case heatColorCtaOffTxt:
		return heatHex("4A5578"), true
	case heatColorCtaStop:
		return heatHex("D9534F"), true
	case heatColorToggleOff:
		return heatHex("4A5578"), true
	case heatColorPhaseWarmup:
		return heatHex("7B5FC7"), true
	case heatColorPhaseWarmupRing:
		return heatHex("A98FE8"), true
	case heatColorPhaseActive:
		return heatHex("4A7BD0"), true
	case heatColorPhaseActiveRing:
		return heatHex("8FB0EC"), true
	}
	return nil, false
}

func (t heatTheme) Font(s fyne.TextStyle) fyne.Resource     { return t.base.Font(s) }
func (t heatTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return t.base.Icon(n) }
func (t heatTheme) Size(n fyne.ThemeSizeName) float32       { return t.base.Size(n) }

func heatHex(s string) color.Color {
	v, _ := strconv.ParseUint(s, 16, 32)
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}

// heatPalette is the default (Normal) variant: dark navy background, white
// active elements, navy text on white, meadow-green accent.
var heatPalette = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:                heatHex("2B3A61"),
	theme.ColorNameForeground:                heatHex("FFFFFF"),
	theme.ColorNamePrimary:                   heatHex("FFFFFF"),
	theme.ColorNameForegroundOnPrimary:       heatHex("242C54"),
	theme.ColorNameButton:                    heatHex("242C54"),
	theme.ColorNameHover:                     heatHex("3A4C7A"),
	theme.ColorNamePressed:                   heatHex("46598C"),
	theme.ColorNameFocus:                     heatHex("9DBD4F"),
	theme.ColorNameSelection:                 heatHex("9DBD4F"),
	theme.ColorNameInputBackground:           heatHex("242C54"),
	theme.ColorNameInputBorder:               heatHex("4A5B85"),
	theme.ColorNameMenuBackground:            heatHex("2B3A61"),
	theme.ColorNameHeaderBackground:          heatHex("2F4067"),
	theme.ColorNameSeparator:                 heatHex("4A5B85"),
	theme.ColorNameScrollBar:                 heatHex("4A5B85"),
	theme.ColorNameScrollBarBackground:       heatHex("242C54"),
	theme.ColorNamePlaceHolder:               heatHex("8A97B8"),
	theme.ColorNameDisabled:                  heatHex("6B7699"),
	theme.ColorNameDisabledButton:            heatHex("1E2745"),
	theme.ColorNameShadow:                    color.RGBA{0, 0, 0, 90},
	theme.ColorNameHyperlink:                 heatHex("7FB4E8"),
	theme.ColorNameInnerWindowBorder:         heatHex("4A5B85"),
	theme.ColorNameInnerWindowBorderInactive: heatHex("3A4C7A"),
	theme.ColorNameOverlayBackground:         color.RGBA{16, 22, 44, 190},
}

// heatDarkPalette is the Dark-toggle variant: the same design in a deeper navy.
var heatDarkPalette = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:                heatHex("1B2340"),
	theme.ColorNameForeground:                heatHex("FFFFFF"),
	theme.ColorNamePrimary:                   heatHex("FFFFFF"),
	theme.ColorNameForegroundOnPrimary:       heatHex("1B2340"),
	theme.ColorNameButton:                    heatHex("151C36"),
	theme.ColorNameHover:                     heatHex("253055"),
	theme.ColorNamePressed:                   heatHex("2E3A66"),
	theme.ColorNameInputBackground:           heatHex("151C36"),
	theme.ColorNameInputBorder:               heatHex("33406B"),
	theme.ColorNameMenuBackground:            heatHex("1B2340"),
	theme.ColorNameHeaderBackground:          heatHex("1F2848"),
	theme.ColorNameSeparator:                 heatHex("33406B"),
	theme.ColorNameScrollBar:                 heatHex("33406B"),
	theme.ColorNameScrollBarBackground:       heatHex("151C36"),
	theme.ColorNamePlaceHolder:               heatHex("6E7A9E"),
	theme.ColorNameDisabled:                  heatHex("525E82"),
	theme.ColorNameDisabledButton:            heatHex("10162C"),
	theme.ColorNameInnerWindowBorder:         heatHex("33406B"),
	theme.ColorNameInnerWindowBorderInactive: heatHex("253055"),
}

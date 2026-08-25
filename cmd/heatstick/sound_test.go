package main

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestSoundDecode verifies the bundled WAV files are 16-bit PCM in the
// expected format.
func TestSoundDecode(t *testing.T) {
	for _, name := range []string{"treat-start.wav", "treat-unplug.wav", "phase-active.wav", "treat-complete.wav"} {
		t.Run(name, func(t *testing.T) {
			data, err := soundFS.ReadFile("assets/sounds/" + name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			pcm, rate, channels, err := decodeWAV(data)
			if err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			if rate != soundSampleRate || channels != soundChannels {
				t.Fatalf("%s: unexpected format rate=%d channels=%d", name, rate, channels)
			}
			secs := float64(len(pcm)) / 2 / float64(rate)
			if secs <= 0.5 || secs > 5 {
				t.Fatalf("%s: unexpected duration %.2fs", name, secs)
			}
			fmt.Printf("  %s: %.2fs OK\n", name, secs)
		})
	}
}

// TestPlaySound plays the start chime and the unplug cue (its notes in
// reverse order) for real; opt in with
// HEATSTICK_PLAY_TEST=1 so CI and regular test runs stay silent.
func TestPlaySound(t *testing.T) {
	if os.Getenv("HEATSTICK_PLAY_TEST") == "" {
		t.Skip("set HEATSTICK_PLAY_TEST=1 to play sounds")
	}
	if _, err := otoContext(); err != nil {
		t.Fatalf("oto context: %v", err)
	}
	playSound("treat-start.wav")
	time.Sleep(2 * time.Second)
	playSound("treat-unplug.wav")
	time.Sleep(2 * time.Second)
}

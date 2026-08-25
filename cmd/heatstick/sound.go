package main

import (
	"bytes"
	"embed"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

//go:embed assets/sounds
var soundFS embed.FS

// All bundled sounds share one format, which is also the oto context format.
const (
	soundSampleRate    = 22050
	soundChannels      = 1
	soundBitsPerSample = 16
)

var (
	soundOnce sync.Once
	otoCtx    *oto.Context
	otoErr    error

	// soundMu guards soundVol and the set of currently playing players, so a
	// volume change also applies to a cue that is already sounding.
	soundMu  sync.Mutex
	soundVol float64 = 1.0
	playing          = map[*oto.Player]struct{}{}
)

// SetSoundVolume sets the playback volume for all sound cues to f, clamped to
// [0,1]. It also updates any cue that is currently playing, so the slider is
// heard immediately on every sound (plug-in, phase, complete, unplug).
func SetSoundVolume(f float64) {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	soundMu.Lock()
	soundVol = f
	for p := range playing {
		p.SetVolume(f)
	}
	soundMu.Unlock()
}

func currentVolume() float64 {
	soundMu.Lock()
	defer soundMu.Unlock()
	return soundVol
}

func otoContext() (*oto.Context, error) {
	soundOnce.Do(func() {
		otoCtx, _, otoErr = oto.NewContext(&oto.NewContextOptions{
			SampleRate:   soundSampleRate,
			ChannelCount: soundChannels,
			Format:       oto.FormatSignedInt16LE,
			BufferSize:   100 * time.Millisecond,
		})
		if otoErr != nil {
			fmt.Fprintf(os.Stderr, "heatstick: audio: %v (sounds disabled)\n", otoErr)
		}
	})
	return otoCtx, otoErr
}

// playSound plays the named embedded sound (treat-start.wav,
// treat-unplug.wav, phase-active.wav, treat-complete.wav) without blocking
// the caller. If no audio backend is available it fails silently (once
// reported on stderr). The unplug cue is not a sample-reversed recording
// (that sounds wrong); it is the plug-in chime's notes in reverse order,
// rendered as natural bell strikes (treat-unplug.wav).
func playSound(name string) {
	c, err := otoContext()
	if err != nil {
		return
	}
	go func() {
		data, err := soundFS.ReadFile("assets/sounds/" + name)
		if err != nil {
			return
		}
		pcm, rate, channels, err := decodeWAV(data)
		if err != nil || rate != soundSampleRate || channels != soundChannels {
			return
		}
		player := c.NewPlayer(bytes.NewReader(pcm))
		player.SetVolume(currentVolume())
		soundMu.Lock()
		playing[player] = struct{}{}
		soundMu.Unlock()
		player.Play()
		// Forget the player once its cue has finished so a later volume change
		// doesn't touch it and it can be collected.
		for player.IsPlaying() {
			time.Sleep(50 * time.Millisecond)
		}
		soundMu.Lock()
		delete(playing, player)
		soundMu.Unlock()
	}()
}

// decodeWAV parses a 16-bit PCM WAV file and returns the raw samples plus
// the file's sample rate and channel count.
func decodeWAV(data []byte) (pcm []byte, sampleRate, channels int, err error) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("not a RIFF/WAVE file")
	}
	if audioFormat := binary.LittleEndian.Uint16(data[20:22]); audioFormat != 1 {
		return nil, 0, 0, fmt.Errorf("not PCM (format %d)", audioFormat)
	}
	channels = int(binary.LittleEndian.Uint16(data[22:24]))
	sampleRate = int(binary.LittleEndian.Uint32(data[24:28]))
	if bits := int(binary.LittleEndian.Uint16(data[34:36])); bits != 16 {
		return nil, 0, 0, fmt.Errorf("only 16-bit PCM supported, got %d bits", bits)
	}
	off := 12
	for off+8 <= len(data) {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		if id == "data" {
			off += 8
			if off+size > len(data) {
				size = len(data) - off
			}
			return data[off : off+size], sampleRate, channels, nil
		}
		off += 8 + size + size%2 // chunks are word-aligned
	}
	return nil, 0, 0, fmt.Errorf("no data chunk")
}

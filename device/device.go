// Package device implements the USB transport for the "heat it" dongle
// (Kamedi GmbH), VID/PID 32f9:0001. The device exposes two USB
// configurations; the application protocol only works on configuration 2
// ("USB Interface"), which this package always selects.
package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	gousb "github.com/google/gousb"
)

const (
	Vid = gousb.ID(0x32f9)
	Pid = gousb.ID(0x0001)

	// AppConfig is the USB configuration number the app protocol requires.
	AppConfig = 2

	frameSize = 12
	header    = 0xFF

	inEpNum  = 2 // bulk IN  0x82
	outEpNum = 2 // bulk OUT 0x02

	defaultTimeout = 1500 * time.Millisecond
)

// Device is an open handle to the dongle in application mode.
type Device struct {
	ctx   *gousb.Context
	dev   *gousb.Device
	cfg   *gousb.Config
	intf  *gousb.Interface
	inEp  *gousb.InEndpoint
	outEp *gousb.OutEndpoint

	// logFn, if set, receives every frame written or read.
	logFn func(dir string, frame []byte)
}

// Open connects to the dongle and switches it to the application
// configuration. It returns an error if the device is not attached.
func Open() (*Device, error) {
	ctx := gousb.NewContext()
	dev, err := ctx.OpenDeviceWithVIDPID(Vid, Pid)
	// gousb returns (nil, nil) when no device matches, so guard both.
	if err != nil || dev == nil {
		_ = ctx.Close()
		if err == nil {
			err = fmt.Errorf("no heat it dongle (VID %04x PID %04x) attached", Vid, Pid)
		}
		return nil, fmt.Errorf("open device: %w", err)
	}
	cfg, err := dev.Config(AppConfig)
	if err != nil {
		_ = dev.Close()
		_ = ctx.Close()
		return nil, fmt.Errorf("select app config %d: %w", AppConfig, err)
	}
	intf, err := cfg.Interface(0, 0)
	if err != nil {
		_ = cfg.Close()
		_ = dev.Close()
		_ = ctx.Close()
		return nil, fmt.Errorf("claim interface: %w", err)
	}
	inEp, err := intf.InEndpoint(inEpNum)
	if err != nil {
		intf.Close()
		_ = cfg.Close()
		_ = dev.Close()
		_ = ctx.Close()
		return nil, fmt.Errorf("in endpoint: %w", err)
	}
	outEp, err := intf.OutEndpoint(outEpNum)
	if err != nil {
		intf.Close()
		_ = cfg.Close()
		_ = dev.Close()
		_ = ctx.Close()
		return nil, fmt.Errorf("out endpoint: %w", err)
	}
	return &Device{ctx: ctx, dev: dev, cfg: cfg, intf: intf, inEp: inEp, outEp: outEp}, nil
}

// Close releases the device and all USB resources.
func (d *Device) Close() error {
	if d == nil {
		return nil
	}
	if d.intf != nil {
		d.intf.Close()
	}
	if d.cfg != nil {
		_ = d.cfg.Close()
	}
	if d.dev != nil {
		_ = d.dev.Close()
	}
	if d.ctx != nil {
		_ = d.ctx.Close()
	}
	return nil
}

// write sends a full 12-byte frame on the OUT endpoint.
func (d *Device) write(frame []byte) error {
	if len(frame) != frameSize {
		return fmt.Errorf("frame must be %d bytes, got %d", frameSize, len(frame))
	}
	if _, err := d.outEp.Write(frame); err != nil {
		return fmt.Errorf("usb write: %w", err)
	}
	if d.logFn != nil {
		d.logFn("OUT", frame)
	}
	return nil
}

// read receives a single 12-byte frame from the IN endpoint.
func (d *Device) read(timeout time.Duration) ([]byte, error) {
	buf := make([]byte, frameSize)
	c, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	n, err := d.inEp.ReadContext(c, buf)
	if err != nil {
		if c.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("read timeout after %s", timeout)
		}
		return nil, fmt.Errorf("usb read: %w", err)
	}
	if n != frameSize {
		return nil, fmt.Errorf("short read: got %d bytes, want %d", n, frameSize)
	}
	if d.logFn != nil {
		d.logFn("IN", buf)
	}
	return buf, nil
}

// SetFrameLog sets a callback invoked for every frame written or read.
// Set it before the first Request to see all traffic.
func (d *Device) SetFrameLog(fn func(dir string, frame []byte)) {
	d.logFn = fn
}

// WriteRaw writes a frame without reading a response.
func (d *Device) WriteRaw(frame []byte) error {
	return d.write(frame)
}

// Request sends a request frame and returns the device's response frame.
func (d *Device) Request(frame []byte) ([]byte, error) {
	if err := d.write(frame); err != nil {
		return nil, err
	}
	return d.read(defaultTimeout)
}

// FrameHex renders a frame as a hex string (e.g. "ff 00 84 02 …").
func FrameHex(frame []byte) string {
	return strings.Join(strings.Fields(fmt.Sprintf("% x", frame)), " ")
}

// Read drains a single frame without sending anything (used for async events).
func (d *Device) Read(timeout time.Duration) ([]byte, error) {
	return d.read(timeout)
}

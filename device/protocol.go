package device

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Message identifiers (verified on the wire).
const (
	MsgStatus            byte = 0x00 // resp: [s16 temp, u8 internal, u8 external, u16]
	MsgGetStatus         byte = 0x02 // req:  query status
	MsgGetStatistics     byte = 0x07 // req:  [u8 offset] (9 pages, 0-8)
	MsgStatistics        byte = 0x07 // resp: [9 raw data bytes]
	MsgStartHeating      byte = 0x08 // req:  [u8 tempLevel, u8 durLevel]
	MsgPreheatingTime    byte = 0x08 // resp: [u16 preheat time, 0.1 s]
	MsgSetLedScale       byte = 0x0A // req:  [u8 R, u8 G, u8 B, u8 modifier]
	MsgAbort             byte = 0x0C // req:  abort treatment
	MsgGetFlash          byte = 0x0D // req:  [u16 addr, u8 len] resp: [u16 addr, u8 len, 6 data]
	MsgNewHeatingTime    byte = 0x18 // resp: [u16 heating time, 0.1 s]
	MsgUpdateHeatingTime byte = 0x18 // req:  query current heating time

	Ack  byte = 0xF0 // resp: ff F0 00 00 ... (no checksum)
	Nack byte = 0xF1
)

// responsePayloadSize is the payload length (in bytes, after the id) of
// each response message, mirroring the field schemas of the official app's
// protocol classes. ACK/NACK carry no payload; unknown ids (0) fall back
// to a checksum search.
var responsePayloadSize = map[byte]int{
	MsgStatus:         6, // s16 + u8 + u8 + u16
	MsgStatistics:     9, // raw
	MsgPreheatingTime: 2, // u16
	MsgGetFlash:       9, // u16 addr + u8 len + 6 data
	MsgNewHeatingTime: 2, // u16
}

// Treatment temperature levels in degrees Celsius (index 0-3).
var TemperatureLevels = []float64{47.0, 48.5, 50.0, 51.5}

// Treatment duration levels in seconds (index 0-2).
var DurationLevels = []float64{4, 7, 9}

// StatisticsPages is the number of statistics pages (0-8) to read.
const StatisticsPages = 9

// buildRequest constructs a 12-byte request frame:
// [0xFF][id][field0..fieldN-1][checksum][zero-pad]
// where checksum = (sum of field bytes) % 256.
func buildRequest(id byte, fields ...byte) []byte {
	frame := make([]byte, frameSize)
	frame[0] = header
	frame[1] = id
	copy(frame[2:], fields)
	sum := 0
	for _, f := range fields {
		sum += int(f)
	}
	frame[2+len(fields)] = byte(sum)
	return frame
}

// findCkIdx returns the frame index of the checksum byte of a response
// frame, or -1 when the message carries no checksum (ACK/NACK).
//
// The checksum position is variable: it sits directly after the last
// payload byte and equals (sum of id + payload bytes) % 256. Payload
// lengths are schema-driven (see responsePayloadSize); for unknown message
// ids the rightmost validating position is used.
func findCkIdx(b []byte) int {
	if len(b) < 2 {
		return -1
	}
	if b[1] == Ack || b[1] == Nack {
		return -1
	}
	if n := responsePayloadSize[b[1]]; n > 0 {
		if 2+n <= len(b) {
			return 2 + n
		}
	}
	sum := int(b[1])
	idx := -1
	for i := 2; i < len(b); i++ {
		if byte(sum) == b[i] {
			idx = i
		}
		sum += int(b[i])
	}
	return idx
}

// parseResponse splits a 12-byte response frame into its id and the
// payload fields (everything between the id and the checksum byte).
func parseResponse(b []byte) (id byte, fields []byte, err error) {
	if len(b) != frameSize {
		return 0, nil, fmt.Errorf("response must be %d bytes, got %d", frameSize, len(b))
	}
	if b[0] != header {
		return 0, nil, fmt.Errorf("bad response header: 0x%02x", b[0])
	}
	id = b[1]
	end := len(b)
	if ck := findCkIdx(b); ck > 2 {
		end = ck
	}
	fields = b[2:end]
	return id, fields, nil
}

// ChecksumOK reports whether the frame's checksum validates. ACK/NACK
// frames (all-zero payload) are valid by definition.
func ChecksumOK(b []byte) bool {
	if len(b) < frameSize || b[0] != header {
		return false
	}
	ck := findCkIdx(b)
	if ck < 0 {
		for i := 2; i < len(b); i++ {
			if b[i] != 0 {
				return false
			}
		}
		return true
	}
	if ck >= len(b) {
		return false
	}
	sum := int(b[1])
	for i := 2; i < ck; i++ {
		sum += int(b[i])
	}
	return byte(sum) == b[ck]
}

// Status is the decoded MSG_STATUS frame.
type Status struct {
	Temperature    float64 // degrees Celsius
	InternalStatus byte
	ExternalStatus byte
	Value          uint16
}

// Phase reports the high-level treatment phase derived from ExternalStatus.
func (s Status) Phase() string {
	switch s.ExternalStatus {
	case 0x00:
		return "idle"
	case 0x01:
		return "warmup"
	case 0x02:
		return "active"
	default:
		return "idle"
	}
}

// GetStatus queries the current device status.
func (d *Device) GetStatus() (*Status, error) {
	resp, err := d.Request(buildRequest(MsgGetStatus))
	if err != nil {
		return nil, err
	}
	id, fields, err := parseResponse(resp)
	if err != nil {
		return nil, err
	}
	if id != MsgStatus {
		return nil, fmt.Errorf("expected status (0x%02x), got 0x%02x", MsgStatus, id)
	}
	temp := int16(binary.BigEndian.Uint16(fields[0:2]))
	return &Status{
		Temperature:    float64(temp) / 10.0,
		InternalStatus: fields[2],
		ExternalStatus: fields[3],
		Value:          binary.BigEndian.Uint16(fields[4:6]),
	}, nil
}

// StartHeating starts a treatment for the given temperature level (0-3) and
// duration level (0-2). It returns the reported preheat time in seconds.
func (d *Device) StartHeating(tempLevel, durLevel byte) (float64, error) {
	resp, err := d.Request(buildRequest(MsgStartHeating, tempLevel, durLevel))
	if err != nil {
		return 0, err
	}
	id, fields, err := parseResponse(resp)
	if err != nil {
		return 0, err
	}
	switch id {
	case MsgPreheatingTime:
		return float64(binary.BigEndian.Uint16(fields[0:2])) / 10.0, nil
	case Nack:
		return 0, fmt.Errorf("start heating NACKed")
	case Ack:
		return 0, nil
	default:
		return 0, fmt.Errorf("unexpected response 0x%02x", id)
	}
}

// UpdateHeatingTime queries the current preheat/heating time in seconds.
// It returns -1 when no update is available.
func (d *Device) UpdateHeatingTime() (float64, error) {
	resp, err := d.Request(buildRequest(MsgUpdateHeatingTime))
	if err != nil {
		return -1, err
	}
	id, fields, err := parseResponse(resp)
	if err != nil {
		return -1, err
	}
	if id == MsgNewHeatingTime {
		v := float64(binary.BigEndian.Uint16(fields[0:2])) / 10.0
		if v == 0 {
			return -1, nil
		}
		return v, nil
	}
	return -1, nil
}

// Abort stops the current treatment.
func (d *Device) Abort() error {
	resp, err := d.Request(buildRequest(MsgAbort))
	if err != nil {
		return err
	}
	id, _, err := parseResponse(resp)
	if err != nil {
		return err
	}
	if id != Ack {
		return fmt.Errorf("abort: expected ACK, got 0x%02x", id)
	}
	return nil
}

// SetLed sets the LED colour (0-255 per channel) and modifier bits.
func (d *Device) SetLed(r, g, b, modifier byte) error {
	resp, err := d.Request(buildRequest(MsgSetLedScale, r, g, b, modifier))
	if err != nil {
		return err
	}
	id, _, err := parseResponse(resp)
	if err != nil {
		return err
	}
	if id != Ack {
		return fmt.Errorf("set led: expected ACK, got 0x%02x", id)
	}
	return nil
}

// GetStatistics reads all 9 statistics pages (9 bytes each) and returns
// the concatenated 81-byte raw blob.
func (d *Device) GetStatistics() ([]byte, error) {
	var all []byte
	for page := 0; page < StatisticsPages; page++ {
		resp, err := d.Request(buildRequest(MsgGetStatistics, byte(page)))
		if err != nil {
			return nil, fmt.Errorf("statistics page %d: %w", page, err)
		}
		id, fields, err := parseResponse(resp)
		if err != nil {
			return nil, err
		}
		if id != MsgStatistics {
			return nil, fmt.Errorf("statistics page %d: expected 0x%02x, got 0x%02x", page, MsgStatistics, id)
		}
		if len(fields) != 9 {
			return nil, fmt.Errorf("statistics page %d: expected 9 bytes, got %d", page, len(fields))
		}
		all = append(all, fields...)
	}
	return all, nil
}

// VersionInfo is the decoded 12-byte version block read from flash
// addresses 0-11 (see research/protocol.md).
type VersionInfo struct {
	Raw []byte // 12 bytes
}

// IsLegacy reports whether the device uses the aligned "legacy" statistics
// layout (6 u8 header flags, treatment pairs at byte 12). This is the layout
// actually emitted by the dongle (verified on the wire); the app's
// version-based heuristic selects a 7-u8 variant that does not match the
// real data, so we default to the aligned layout.
func (v *VersionInfo) IsLegacy() bool {
	return true
}

func (v *VersionInfo) String() string {
	if len(v.Raw) < 11 {
		return fmt.Sprintf("version block %x", v.Raw)
	}
	r := v.Raw
	variant := ""
	if r[6] != 0 {
		variant = fmt.Sprintf("-a%d", r[6])
	}
	return fmt.Sprintf("tuple[0-2] %d.%d.%d, tuple[3-5] %d.%d.%d%s, flag[7]=%d, tuple[8-10] %d.%d.%d, raw %x",
		r[0], r[1], r[2], r[3], r[4], r[5], variant, r[7], r[8], r[9], r[10], r)
}

// GetVersionInfo reads the version block (flash addresses 0-11) in two
// 6-byte reads, matching the official app.
func (d *Device) GetVersionInfo() (*VersionInfo, error) {
	var raw [12]byte
	for off := 0; off < 12; off += 6 {
		resp, err := d.Request(buildRequest(MsgGetFlash, byte(off>>8), byte(off), 6))
		if err != nil {
			return nil, fmt.Errorf("version flash %d: %w", off, err)
		}
		id, fields, err := parseResponse(resp)
		if err != nil {
			return nil, err
		}
		if id != MsgGetFlash {
			return nil, fmt.Errorf("version flash %d: expected 0x%02x, got 0x%02x", off, MsgGetFlash, id)
		}
		if len(fields) != 9 {
			return nil, fmt.Errorf("version flash %d: expected 9 bytes, got %d", off, len(fields))
		}
		copy(raw[off:off+6], fields[3:9]) // skip addr (2) + len (1)
	}
	return &VersionInfo{Raw: raw[:]}, nil
}

// Statistics is the decoded INTERNAL_MEMORY blob (9 pages x 9 bytes = 81
// raw bytes; the schema uses the first 64 (aligned) or 65 (extended) bytes).
// Field semantics follow the app's local InternalMemory database table
// (best-effort labels; the raw blob is the ground truth).
type Statistics struct {
	Legacy bool // aligned layout (6 u8 flags, pairs at byte 12)

	Blacklisted        uint16     // u16 @0
	CounterBoots       uint16     // u16 @2
	CounterTreatments  uint16     // u16 @4
	CounterReprog      uint8      // u8 @6
	CounterWatchdog    uint8      // u8 @7
	ErrHeatingElement  uint8      // u8 @8
	ErrOverTemperature uint8      // u8 @9
	ErrTempSwitch      uint8      // u8 @10
	ErrTempSensor      uint8      // u8 @11
	FlashWriteCounter  uint8      // u8 @12, extended layout only
	TreatmentCounter   [12]uint16 // (u16,s16) pairs @12 (aligned)
	TreatmentMaxTemp   [12]int16  // 0.1 degC
	Tail               [2]uint16  // trailing u16 pair @60
}

// DecodeStatistics decodes an 81-byte statistics blob. legacy=true selects
// the aligned layout (6 u8 flags, 12 (u16,s16) pairs at byte 12) as emitted
// by the dongle; legacy=false selects the extended layout (extra u8 at @12,
// pairs at byte 13).
func DecodeStatistics(data []byte, legacy bool) (*Statistics, error) {
	if len(data) < 81 {
		return nil, fmt.Errorf("statistics blob must be 81 bytes, got %d", len(data))
	}
	s := &Statistics{Legacy: legacy}
	u16 := func(off int) uint16 { return binary.BigEndian.Uint16(data[off : off+2]) }
	s16 := func(off int) int16 { return int16(binary.BigEndian.Uint16(data[off : off+2])) }

	s.Blacklisted = u16(0)
	s.CounterBoots = u16(2)
	s.CounterTreatments = u16(4)
	s.CounterReprog = data[6]
	s.CounterWatchdog = data[7]
	s.ErrHeatingElement = data[8]
	s.ErrOverTemperature = data[9]
	s.ErrTempSwitch = data[10]
	s.ErrTempSensor = data[11]
	base := 12 // first treatment pair offset
	if !legacy {
		s.FlashWriteCounter = data[12]
		base = 13
	}
	for i := 0; i < 12; i++ {
		off := base + 4*i
		s.TreatmentCounter[i] = u16(off)
		s.TreatmentMaxTemp[i] = s16(off + 2)
	}
	s.Tail[0] = u16(base + 48)
	s.Tail[1] = u16(base + 50)
	return s, nil
}

// String renders a compact, human-readable statistics dump.
func (s *Statistics) String() string {
	layout := "new"
	if s.Legacy {
		layout = "legacy (fw<=0.0.9)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "layout: %s\n", layout)
	fmt.Fprintf(&b, "blacklisted=%d boots=%d treatments=%d reprog=%d watchdog=%d\n",
		s.Blacklisted, s.CounterBoots, s.CounterTreatments, s.CounterReprog, s.CounterWatchdog)
	fmt.Fprintf(&b, "errors: heating=%d over-temp=%d temp-switch=%d temp-sensor=%d",
		s.ErrHeatingElement, s.ErrOverTemperature, s.ErrTempSwitch, s.ErrTempSensor)
	if !s.Legacy {
		fmt.Fprintf(&b, " flash-writes=%d", s.FlashWriteCounter)
	}
	fmt.Fprintf(&b, "\ntail: %d %d\n", s.Tail[0], s.Tail[1])
	for i := 0; i < 12; i++ {
		if s.TreatmentCounter[i] == 0 && s.TreatmentMaxTemp[i] == 0 {
			continue
		}
		fmt.Fprintf(&b, "treatment %02d: count=%d max=%.1f degC\n", i, s.TreatmentCounter[i], float64(s.TreatmentMaxTemp[i])/10.0)
	}
	return b.String()
}

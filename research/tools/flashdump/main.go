// flashdump - dumps the heat it dongle flash (8 KiB, 0x0000-0x1FFF) via
// MsgGetFlash (6-byte reads) into a raw .bin file.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"heatstick/device"
)

const (
	maxAddr   = 0xFFFF
	flashSize = 0x2000 // 8 KiB; a read crossing 0x2000 resets the device
)

func main() {
	out := flag.String("out", "research/flash_dump.bin", "output file")
	start := flag.Int("start", 0, "first address to read (hex ok)")
	end := flag.Int("end", flashSize-1, "last address to read (hex ok)")
	delay := flag.Duration("delay", time.Millisecond, "delay between reads")
	flag.Parse()

	if *start < 0 || *end > maxAddr || *start > *end {
		fmt.Fprintln(os.Stderr, "bad address range")
		os.Exit(1)
	}

	dev, err := device.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer dev.Close()

	buf := make([]byte, *end-*start+1)
	reads, errs := 0, 0
	consec := 0
	firstErr := -1
	// The main scan only starts reads whose full 6-byte window fits inside the
	// physical flash (a read that crosses 0x2000 resets the device). The final
	// unaligned bytes are picked up by the tail read below.
	mainEnd := *end
	if mainEnd > flashSize-6 {
		mainEnd = flashSize - 6
	}
	for addr := *start; addr <= mainEnd; addr += 6 {
		data, err := dev.ReadFlash(addr)
		if err != nil {
			errs++
			consec++
			if firstErr < 0 {
				firstErr = addr
				fmt.Fprintf(os.Stderr, "first error at addr 0x%04x: %v\n", addr, err)
			}
			if consec >= 10 {
				fmt.Fprintln(os.Stderr, "10 consecutive errors, stopping")
				break
			}
			continue
		}
		consec = 0
		for i, b := range data {
			if pos := addr + i; pos <= *end {
				buf[pos-*start] = b
			}
		}
		reads++
		if reads%1000 == 0 {
			fmt.Printf("  %d/%d reads (addr 0x%04x), %d errors\n", reads, (mainEnd-*start)/6+1, addr, errs)
		}
		if *delay > 0 {
			time.Sleep(*delay)
		}
	}

	// Capture an unaligned tail: a 6-byte-aligned scan can't reach the final
	// bytes when the range length isn't a multiple of 6, without a read that
	// starts too close to the flash boundary. One overlapping read at the end
	// fills them in.
	if (*end-*start)%6 != 0 {
		tail := *end - 5
		if data, err := dev.ReadFlash(tail); err == nil {
			for i, b := range data {
				if pos := tail + i; pos <= *end {
					buf[pos-*start] = b
				}
			}
			reads++
		}
	}

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	f.Close()

	fmt.Printf("done: %d reads, %d errors, first error addr %s -> %s (%d bytes)\n",
		reads, errs, hexAddr(firstErr), *out, len(buf))
}

func hexAddr(a int) string {
	if a < 0 {
		return "none"
	}
	return fmt.Sprintf("0x%04x", a)
}

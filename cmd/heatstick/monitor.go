package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"heatstick/device"
)

const pollInterval = 1500 * time.Millisecond

// runMonitor is the headless hotplug watcher: it polls for the dongle and
// starts the app whenever the dongle is attached. It keeps running until it
// receives SIGTERM or SIGINT.
func runMonitor() {
	hideConsoleWindow()

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "heatstick monitor:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "heatstick monitor: watching for dongle (poll every %s)\n", pollInterval)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	present := false
	for {
		found := device.Present()
		if found && !present {
			fmt.Fprintln(os.Stderr, "heatstick monitor: dongle attached, starting app")
			startApp(exe)
		}
		present = found
		select {
		case <-sig:
			fmt.Fprintln(os.Stderr, "heatstick monitor: shutting down")
			return
		case <-time.After(pollInterval):
		}
	}
}

// startApp launches the GUI with -auto so it knows it was started by the
// monitor (one-time explanation popup on the first auto-start). If an
// instance is already running, its single-instance guard activates that
// window instead of starting a second one.
func startApp(exe string) {
	cmd := exec.Command(exe, "-auto")
	_ = cmd.Start()
}

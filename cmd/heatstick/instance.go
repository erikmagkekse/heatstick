package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
)

// instanceSocket returns the network and address for the single-instance
// socket: a Unix domain socket on Linux/macOS, a named pipe on Windows.
func instanceSocket() (network, addr string) {
	if runtime.GOOS == "windows" {
		// The Go net package uses the "npipe" network for Windows named
		// pipes (address \\.\pipe\name).
		return "npipe", `\\.\pipe\heatstick`
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return "unix", filepath.Join(dir, "heatstick.sock")
	}
	home, _ := os.UserHomeDir()
	return "unix", filepath.Join(home, ".local", "state", "heatstick", "heatstick.sock")
}

// becomeInstance ensures only one GUI instance runs. If another instance is
// active it sends "activate" to it and reports false; otherwise it binds the
// socket and returns the listener plus true. If neither joining nor binding
// works it returns (nil, true) so the app still starts without the guard.
func becomeInstance() (net.Listener, bool) {
	network, addr := instanceSocket()
	dial := func() net.Conn {
		conn, err := net.DialTimeout(network, addr, 500*time.Millisecond)
		if err != nil {
			return nil
		}
		return conn
	}
	if conn := dial(); conn != nil {
		_, _ = conn.Write([]byte("activate"))
		_ = conn.Close()
		return nil, false
	}

	if network == "unix" {
		if dir := filepath.Dir(addr); dir != "" {
			_ = os.MkdirAll(dir, 0o700)
		}
		_ = os.Remove(addr) // stale socket from a crashed run
	}
	l, err := net.Listen(network, addr)
	if err != nil {
		// Someone else bound the socket in between; try to activate again.
		if conn := dial(); conn != nil {
			_, _ = conn.Write([]byte("activate"))
			_ = conn.Close()
			return nil, false
		}
		fmt.Fprintf(os.Stderr, "heatstick: instance socket: %v (continuing without single-instance guard)\n", err)
		return nil, true
	}
	return l, true
}

// serveActivation accepts connections until the listener is closed and calls
// activate for every "activate" request.
func serveActivation(l net.Listener, activate func()) {
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return // listener closed
			}
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 32)
			n, _ := conn.Read(buf)
			_ = conn.Close()
			if n > 0 && string(buf[:n]) == "activate" {
				fyne.Do(activate)
			}
		}
	}()
}

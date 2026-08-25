//go:build windows

package main

import "syscall"

var (
	procGetConsoleWindow = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
	procShowWindow       = syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow")
)

// hideConsoleWindow hides the console window that Windows shows when the
// monitor is started by the task scheduler, so it runs invisibly.
func hideConsoleWindow() {
	h, _, _ := procGetConsoleWindow.Call()
	if h != 0 {
		procShowWindow.Call(h, 0) // SW_HIDE
	}
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	hotplugTask    = "heatstick-monitor"
	hotplugPlist   = "com.erikmagkekse.heatstick-monitor"
	hotplugService = "heatstick-monitor.service"
)

// runHotplugInstall sets up the per-user autostart that launches the monitor
// watcher (no admin rights required). It is idempotent.
func runHotplugInstall() {
	if err := hotplugInstall(); err != nil {
		fatal("%v", err)
	}
}

// hotplugInstall is the error-returning core used by both the CLI (which
// exits on failure) and the GUI autostart checkbox (which must keep the app
// alive and only report the error).
func hotplugInstall() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot resolve binary path: %w", err)
	}
	switch runtime.GOOS {
	case "linux":
		err = installLinux(exe)
	case "windows":
		err = installWindows(exe)
	case "darwin":
		err = installDarwin(exe)
	default:
		return fmt.Errorf("hotplug auto-start is not supported on %s", runtime.GOOS)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Installed: %s will start heatstick whenever the dongle is attached.\n",
		runtime.GOOS+" autostart")
	return nil
}

// runHotplugUninstall removes the autostart setup. It is idempotent.
func runHotplugUninstall() {
	if err := hotplugUninstall(); err != nil {
		fatal("%v", err)
	}
}

func hotplugUninstall() error {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = uninstallLinux()
	case "windows":
		err = uninstallWindows()
	case "darwin":
		err = uninstallDarwin()
	default:
		return fmt.Errorf("hotplug auto-start is not supported on %s", runtime.GOOS)
	}
	if err != nil {
		return err
	}
	fmt.Println("Uninstalled: autostart removed.")
	return nil
}

// hotplugInstalled reports whether the per-user autostart entry exists.
func hotplugInstalled() bool {
	switch runtime.GOOS {
	case "linux":
		_, err := os.Stat(linuxUnitPath())
		return err == nil
	case "windows":
		return exec.Command("schtasks", "/Query", "/TN", hotplugTask).Run() == nil
	case "darwin":
		_, err := os.Stat(macPlistPath())
		return err == nil
	}
	return false
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "heatstick: "+format+"\n", args...)
	os.Exit(1)
}

func execErr(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runOrWarn(name string, args ...string) error {
	_, err := exec.Command(name, args...).CombinedOutput()
	return err
}

// --- Linux: systemd user service ---

func linuxUnitPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", hotplugService)
}

func installLinux(exe string) error {
	unit := linuxUnitPath()
	if err := os.MkdirAll(filepath.Dir(unit), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(unit), err)
	}
	content := fmt.Sprintf(`[Unit]
Description=heatstick hotplug monitor
After=graphical-session.target

[Service]
ExecStart=%s monitor
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
`, exe)
	if err := os.WriteFile(unit, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	if err := execErr("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return execErr("systemctl", "--user", "enable", "--now", hotplugService)
}

func uninstallLinux() error {
	err := runOrWarn("systemctl", "--user", "disable", "--now", hotplugService)
	_ = os.Remove(linuxUnitPath())
	if err2 := runOrWarn("systemctl", "--user", "daemon-reload"); err2 != nil && err == nil {
		err = err2
	}
	return err
}

// --- Windows: per-user scheduled task at log on ---

func installWindows(exe string) error {
	return execErr("schtasks", "/Create", "/F", "/IT", "/SC", "ONLOGON",
		"/TN", hotplugTask, "/TR", `"`+exe+`" monitor`)
}

func uninstallWindows() error {
	return runOrWarn("schtasks", "/Delete", "/F", "/TN", hotplugTask)
}

// --- macOS: LaunchAgent ---

func macPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", hotplugPlist+".plist")
}

func installDarwin(exe string) error {
	p := macPlistPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(p), err)
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>monitor</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
</dict>
</plist>
`, hotplugPlist, exe)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	uid := strconv.Itoa(os.Getuid())
	if out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, p).CombinedOutput(); err != nil {
		// Fall back to the legacy verb (covers "already loaded" and older macOS).
		runOrWarn("launchctl", "load", "-w", p)
		fmt.Fprintf(os.Stderr, "heatstick: launchctl bootstrap: %v\n%s\n",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uninstallDarwin() error {
	uid := strconv.Itoa(os.Getuid())
	err := runOrWarn("launchctl", "bootout", "gui/"+uid+"/"+hotplugPlist)
	runOrWarn("launchctl", "unload", macPlistPath())
	if rmErr := os.Remove(macPlistPath()); rmErr != nil && err == nil {
		err = rmErr
	}
	return err
}

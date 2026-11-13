//go:build !windows

package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func isWindows() bool { return false }

func hideWindow(cmd *exec.Cmd) {}

func clearReadOnly(p string) {
	if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o200 == 0 {
		_ = os.Chmod(p, st.Mode().Perm()|0o200)
	}
}

func copyAttributes(src, dst string, info fs.FileInfo) error {
	return os.Chmod(dst, info.Mode().Perm()|0o200)
}

func copyPermissions(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		_ = os.Lchown(dst, int(sys.Uid), int(sys.Gid))
	}
	return os.Chmod(dst, st.Mode().Perm())
}

func hasArchiveAttr(p string) bool { return true }
func clearArchiveAttr(p string)    {}

func listProcesses() []string {
	if runtime.GOOS == "linux" {
		entries, _ := os.ReadDir("/proc")
		var out []string
		for _, e := range entries {
			if b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm")); err == nil {
				out = append(out, strings.TrimSpace(string(b)))
			}
		}
		return out
	}
	out, err := exec.Command("ps", "-axo", "comm=").Output()
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func onBattery() bool {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("pmset", "-g", "batt").Output()
		return err == nil && strings.Contains(string(out), "'Battery Power'")
	}
	b, err := os.ReadFile("/sys/class/power_supply/AC/online")
	return err == nil && strings.TrimSpace(string(b)) == "0"
}

func preventSleep() func() {
	if runtime.GOOS != "darwin" {
		return func() {}
	}
	cmd := exec.Command("caffeinate", "-i", "-w", fmt.Sprint(os.Getpid()))
	if cmd.Start() != nil {
		return func() {}
	}
	return func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
}

func setLowPriority(on bool) {
	if on {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
	} else {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 0)
	}
}

func playSound(file string) {
	if runtime.GOOS == "darwin" {
		if file == "" {
			file = "/System/Library/Sounds/Glass.aiff"
		}
		_ = exec.Command("afplay", file).Run()
		return
	}
	if file != "" {
		_ = exec.Command("aplay", "-q", file).Run()
	}
}

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	label := "ar.claraboya.resguardo"
	if instanceName != "" {
		label += "." + sanitizeName(instanceName)
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func setAutostart(on bool) error {
	if runtime.GOOS != "darwin" {
		home, _ := os.UserHomeDir()
		p := filepath.Join(home, ".config", "autostart", "resguardo.desktop")
		if !on {
			_ = os.Remove(p)
			return nil
		}
		exe, _ := os.Executable()
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		return os.WriteFile(p, []byte(fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Resguardo\nExec=\"%s\" --inicio\n", exe)), 0o644)
	}
	p := launchAgentPath()
	if !on {
		_ = exec.Command("launchctl", "unload", p).Run()
		_ = os.Remove(p)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := fmt.Sprintf("<string>%s</string><string>--inicio</string>", exe)
	if instanceName != "" {
		args += fmt.Sprintf("<string>--instancia</string><string>%s</string>", instanceName)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array>%s</array>
<key>RunAtLoad</key><true/>
<key>ProcessType</key><string>Background</string>
</dict></plist>
`, strings.TrimSuffix(filepath.Base(p), ".plist"), args)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	return os.WriteFile(p, []byte(plist), 0o644)
}

func messageBox(title, msg string) {
	if runtime.GOOS == "darwin" {
		script := fmt.Sprintf(`display dialog %q with title %q buttons {"OK"} default button 1`, msg, title)
		_ = exec.Command("osascript", "-e", script).Start()
		return
	}
	fmt.Fprintln(os.Stderr, title+": "+msg)
}

func runImpersonated(ra RunAs, log *Logger, fn func() error) error {
	if ra.Enabled {
		log.Warn("\"Ejecutar como otro usuario\" sólo funciona en Windows; se ignora")
	}
	return fn()
}

func closeProgram(target string, timeout time.Duration, log *Logger) error {
	target = strings.TrimSpace(target)
	if runtime.GOOS == "darwin" {
		app := strings.TrimSuffix(filepath.Base(target), ".app")
		if err := exec.Command("osascript", "-e", fmt.Sprintf(`tell application %q to quit`, app)).Run(); err != nil {
			return err
		}
	} else {
		_ = exec.Command("pkill", "-TERM", "-f", target).Run()
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		alive := false
		for _, n := range listProcesses() {
			if strings.Contains(strings.ToLower(n), strings.ToLower(strings.TrimSuffix(filepath.Base(target), ".app"))) {
				alive = true
				break
			}
		}
		if !alive {
			return nil
		}
	}
	return fmt.Errorf("el programa no se cerró a tiempo (puede estar pidiendo guardar cambios)")
}

func serviceControl(ctx context.Context, name string, start bool) error {
	verb := "stop"
	if start {
		verb = "start"
	}
	if runtime.GOOS == "darwin" {
		return exec.CommandContext(ctx, "launchctl", verb, name).Run()
	}
	return exec.CommandContext(ctx, "systemctl", verb, name).Run()
}

func powerAction(kind string) error {
	if runtime.GOOS == "darwin" {
		m := map[string]string{"apagar": "shut down", "reiniciar": "restart", "cerrar_sesion": "log out", "suspender": "sleep", "hibernar": "sleep"}
		return exec.Command("osascript", "-e", fmt.Sprintf(`tell application "System Events" to %s`, m[kind])).Start()
	}
	m := map[string][]string{"apagar": {"poweroff"}, "reiniciar": {"reboot"}, "suspender": {"suspend"}, "hibernar": {"hibernate"}}
	if a, ok := m[kind]; ok {
		return exec.Command("systemctl", a...).Start()
	}
	return fmt.Errorf("no disponible en este sistema")
}

type VSSSession struct{}

func newVSSSession(ctx context.Context, paths []string, log *Logger) (*VSSSession, error) {
	return nil, fmt.Errorf("las instantáneas de volumen sólo existen en Windows")
}
func (vs *VSSSession) Map(src string) string { return src }
func (vs *VSSSession) Close()                {}

func openUIWindow(url string) { openInBrowser(url) }

func openInBrowser(url string) {
	if runtime.GOOS == "darwin" {
		for _, app := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Chromium"} {
			if _, err := os.Stat("/Applications/" + app + ".app"); err == nil {
				if exec.Command("open", "-na", app, "--args", "--app="+url, "--window-size=1280,860").Run() == nil {
					return
				}
			}
		}
		_ = exec.Command("open", url).Start()
		return
	}
	_ = exec.Command("xdg-open", url).Start()
}

func openFolder(p string) {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("open", p).Start()
		return
	}
	_ = exec.Command("xdg-open", p).Start()
}

func listDrives() []string {
	if runtime.GOOS == "darwin" {
		out := []string{"/"}
		entries, _ := os.ReadDir("/Volumes")
		for _, e := range entries {
			out = append(out, filepath.Join("/Volumes", e.Name()))
		}
		home, _ := os.UserHomeDir()
		return append([]string{home}, out...)
	}
	home, _ := os.UserHomeDir()
	return []string{home, "/"}
}

// detach separa el proceso hijo para que siga vivo cuando el padre termina.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func waitProcessExit(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func closeAppWindows(time.Duration) {}

func syncInstallInfo() {}

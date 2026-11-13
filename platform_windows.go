//go:build windows

package main

import (
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

//go:embed embed/7za-x64.exe
var embedded7z []byte

var (
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	user32                       = windows.NewLazySystemDLL("user32.dll")
	advapi32                     = windows.NewLazySystemDLL("advapi32.dll")
	winmm                        = windows.NewLazySystemDLL("winmm.dll")
	powrprof                     = windows.NewLazySystemDLL("powrprof.dll")
	procGetSystemPowerStatus     = kernel32.NewProc("GetSystemPowerStatus")
	procSetThreadExecutionState  = kernel32.NewProc("SetThreadExecutionState")
	procSetPriorityClass         = kernel32.NewProc("SetPriorityClass")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procLogonUserW               = advapi32.NewProc("LogonUserW")
	procImpersonateLoggedOnUser  = advapi32.NewProc("ImpersonateLoggedOnUser")
	procRevertToSelf             = advapi32.NewProc("RevertToSelf")
	procPlaySoundW               = winmm.NewProc("PlaySoundW")
	procSetSuspendState          = powrprof.NewProc("SetSuspendState")
)

func isWindows() bool { return true }

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func clearReadOnly(p string) {
	ptr, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return
	}
	a, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return
	}
	if a&windows.FILE_ATTRIBUTE_READONLY != 0 {
		_ = windows.SetFileAttributes(ptr, a&^windows.FILE_ATTRIBUTE_READONLY)
	}
}

func copyAttributes(src, dst string, info fs.FileInfo) error {
	sp, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	a, err := windows.GetFileAttributes(sp)
	if err != nil {
		return err
	}
	keep := a & (windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_SYSTEM | windows.FILE_ATTRIBUTE_ARCHIVE)
	if keep == 0 {
		keep = windows.FILE_ATTRIBUTE_NORMAL
	}
	dp, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.SetFileAttributes(dp, keep)
}

func copyPermissions(src, dst string) error {
	sd, err := windows.GetNamedSecurityInfo(src, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dst, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func hasArchiveAttr(p string) bool {
	ptr, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return true
	}
	a, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return true
	}
	return a&windows.FILE_ATTRIBUTE_ARCHIVE != 0
}

func clearArchiveAttr(p string) {
	ptr, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return
	}
	a, err := windows.GetFileAttributes(ptr)
	if err != nil {
		return
	}
	if a&windows.FILE_ATTRIBUTE_ARCHIVE != 0 {
		na := a &^ windows.FILE_ATTRIBUTE_ARCHIVE
		if na == 0 {
			na = windows.FILE_ATTRIBUTE_NORMAL
		}
		_ = windows.SetFileAttributes(ptr, na)
	}
}

func listProcesses() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []string
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil
	}
	for {
		out = append(out, windows.UTF16ToString(pe.ExeFile[:]))
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return out
}

type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

func onBattery() bool {
	var s systemPowerStatus
	r, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return false
	}
	return s.ACLineStatus == 0
}

// preventSleep mantiene el equipo despierto mientras dure el respaldo.
func preventSleep() func() {
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		const esContinuous, esSystemRequired = 0x80000000, 0x00000001
		procSetThreadExecutionState.Call(uintptr(esContinuous | esSystemRequired))
		<-done
		procSetThreadExecutionState.Call(uintptr(esContinuous))
	}()
	return func() { close(done) }
}

func setLowPriority(on bool) {
	h, _ := windows.GetCurrentProcess()
	if on {
		procSetPriorityClass.Call(uintptr(h), 0x00100000) // PROCESS_MODE_BACKGROUND_BEGIN
	} else {
		procSetPriorityClass.Call(uintptr(h), 0x00200000) // PROCESS_MODE_BACKGROUND_END
	}
}

func playSound(file string) {
	const sndSync, sndAlias, sndFilename, sndNoDefault = 0x0000, 0x00010000, 0x00020000, 0x0002
	if file != "" {
		if p, err := windows.UTF16PtrFromString(file); err == nil {
			r, _, _ := procPlaySoundW.Call(uintptr(unsafe.Pointer(p)), 0, sndFilename|sndSync|sndNoDefault)
			if r != 0 {
				return
			}
		}
	}
	p, _ := windows.UTF16PtrFromString("SystemAsterisk")
	procPlaySoundW.Call(uintptr(unsafe.Pointer(p)), 0, sndAlias|sndSync)
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func setAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	name := AppName
	if instanceName != "" {
		name += " " + instanceName
	}
	if !on {
		err := k.DeleteValue(name)
		if err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	val := fmt.Sprintf(`"%s" --inicio`, exe)
	if instanceName != "" {
		val += fmt.Sprintf(` --instancia "%s"`, instanceName)
	}
	return k.SetStringValue(name, val)
}

func messageBox(title, msg string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(msg)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40|0x10000) // MB_ICONINFORMATION | MB_SETFOREGROUND
}

// runImpersonated ejecuta fn con las credenciales de otro usuario (para carpetas de red).
func runImpersonated(ra RunAs, log *Logger, fn func() error) error {
	if !ra.Enabled || ra.User == "" {
		return fn()
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u, _ := windows.UTF16PtrFromString(ra.User)
	dom := ra.Domain
	if dom == "" {
		dom = "."
	}
	d, _ := windows.UTF16PtrFromString(dom)
	p, _ := windows.UTF16PtrFromString(ra.Password)
	var token windows.Handle
	const logonNewCredentials, providerWinNT50 = 9, 3
	r, _, e := procLogonUserW.Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(p)),
		logonNewCredentials, providerWinNT50, uintptr(unsafe.Pointer(&token)))
	if r == 0 {
		if ra.CancelIfFails {
			return fmt.Errorf("no se pudo iniciar sesión como %s: %v", ra.User, e)
		}
		log.Warn("No se pudo iniciar sesión como %s (%v); se sigue con el usuario actual", ra.User, e)
		return fn()
	}
	defer windows.CloseHandle(token)
	r, _, e = procImpersonateLoggedOnUser.Call(uintptr(token))
	if r == 0 {
		if ra.CancelIfFails {
			return fmt.Errorf("no se pudo actuar como %s: %v", ra.User, e)
		}
		log.Warn("No se pudo actuar como %s (%v)", ra.User, e)
		return fn()
	}
	defer procRevertToSelf.Call()
	log.Info("Ejecutando como %s\\%s", dom, ra.User)
	return fn()
}

// closeProgram cierra con amabilidad (como si se tocara la X) un programa por nombre de .exe o por título de ventana.
func closeProgram(target string, timeout time.Duration, log *Logger) error {
	target = strings.TrimSpace(target)
	byExe := strings.HasSuffix(strings.ToLower(target), ".exe")
	pids := map[uint32]bool{}
	if byExe {
		snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
		if err == nil {
			var pe windows.ProcessEntry32
			pe.Size = uint32(unsafe.Sizeof(pe))
			if windows.Process32First(snap, &pe) == nil {
				for {
					if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), filepath.Base(target)) {
						pids[pe.ProcessID] = true
					}
					if windows.Process32Next(snap, &pe) != nil {
						break
					}
				}
			}
			windows.CloseHandle(snap)
		}
		if len(pids) == 0 {
			log.Info("%s no estaba abierto", target)
			return nil
		}
	}
	found := 0
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1
		}
		if byExe {
			var pid uint32
			procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
			if !pids[pid] {
				return 1
			}
		} else {
			buf := make([]uint16, 512)
			procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 512)
			if !strings.Contains(strings.ToLower(windows.UTF16ToString(buf)), strings.ToLower(target)) {
				return 1
			}
		}
		procPostMessageW.Call(hwnd, 0x0010, 0, 0) // WM_CLOSE
		found++
		return 1
	})
	procEnumWindows.Call(cb, 0)
	if found == 0 {
		if byExe {
			return fmt.Errorf("%s está abierto pero no tiene ventanas para cerrar", target)
		}
		log.Info("No hay ninguna ventana con %q", target)
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if byExe {
			alive := false
			for _, n := range listProcesses() {
				if strings.EqualFold(n, filepath.Base(target)) {
					alive = true
					break
				}
			}
			if !alive {
				return nil
			}
		} else {
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
	cmd := exec.CommandContext(ctx, "net", verb, name)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if strings.Contains(s, "2182") || strings.Contains(strings.ToLower(s), "already") || strings.Contains(strings.ToLower(s), "ya se") {
			return nil
		}
		return fmt.Errorf("%v %s", err, s)
	}
	return nil
}

func powerAction(kind string) error {
	switch kind {
	case "apagar":
		return exec.Command("shutdown", "/s", "/t", "30", "/c", "Resguardo terminó los respaldos y apaga el equipo").Start()
	case "reiniciar":
		return exec.Command("shutdown", "/r", "/t", "30", "/c", "Resguardo terminó los respaldos y reinicia el equipo").Start()
	case "hibernar":
		return exec.Command("shutdown", "/h").Start()
	case "cerrar_sesion":
		return exec.Command("shutdown", "/l").Start()
	case "suspender":
		r, _, e := procSetSuspendState.Call(0, 0, 0)
		if r == 0 {
			return e
		}
		return nil
	}
	return fmt.Errorf("acción desconocida")
}

func extractEmbedded7z(dir string) (string, error) {
	p := filepath.Join(dir, "7za.exe")
	if st, err := os.Stat(p); err == nil && st.Size() == int64(len(embedded7z)) {
		return p, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p+".tmp", embedded7z, 0o755); err != nil {
		return "", err
	}
	return p, os.Rename(p+".tmp", p)
}

// ---------- Instantáneas de volumen (VSS) ----------

type VSSSession struct {
	links map[string]string // "C:\" -> carpeta enlace
	ids   []string
	log   *Logger
}

func newVSSSession(ctx context.Context, paths []string, log *Logger) (*VSSSession, error) {
	vs := &VSSSession{links: map[string]string{}, log: log}
	vols := map[string]bool{}
	for _, p := range paths {
		v := filepath.VolumeName(p)
		if len(v) == 2 && v[1] == ':' {
			vols[strings.ToUpper(v)+`\`] = true
		}
	}
	if len(vols) == 0 {
		return nil, fmt.Errorf("los orígenes no están en unidades locales")
	}
	base := filepath.Join(dataDir(), "vss")
	_ = os.MkdirAll(base, 0o755)
	for vol := range vols {
		script := fmt.Sprintf(`$ErrorActionPreference='Stop'; $r = Invoke-CimMethod -ClassName Win32_ShadowCopy -MethodName Create -Arguments @{Volume='%s'; Context='ClientAccessible'}; if ($r.ReturnValue -ne 0) { Write-Output ('ERR ' + $r.ReturnValue); exit 1 }; $s = Get-CimInstance -ClassName Win32_ShadowCopy | Where-Object { $_.ID -eq $r.ShadowID }; Write-Output ('OK ' + $s.ID + ' ' + $s.DeviceObject)`, vol)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		line := strings.TrimSpace(string(out))
		if err != nil || !strings.HasPrefix(line, "OK ") {
			vs.Close()
			if strings.Contains(line, "Access") || strings.Contains(line, "acceso") || strings.Contains(line, "denied") {
				return nil, fmt.Errorf("hace falta ejecutar Resguardo como administrador para usar VSS")
			}
			return nil, fmt.Errorf("%v %s", err, line)
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			vs.Close()
			return nil, fmt.Errorf("respuesta inesperada de VSS: %s", line)
		}
		id, dev := parts[1], parts[2]
		vs.ids = append(vs.ids, id)
		link := filepath.Join(base, string(vol[0])+"_"+strings.Trim(id, "{}")[:8])
		_ = os.Remove(link)
		mk := exec.CommandContext(ctx, "cmd", "/c", "mklink", "/d", link, dev+`\`)
		hideWindow(mk)
		if out, err := mk.CombinedOutput(); err != nil {
			vs.Close()
			return nil, fmt.Errorf("no se pudo enlazar la instantánea: %v %s", err, strings.TrimSpace(string(out)))
		}
		vs.links[vol] = link
		log.Info("Instantánea de volumen creada para %s", vol)
	}
	return vs, nil
}

func (vs *VSSSession) Map(src string) string {
	v := strings.ToUpper(filepath.VolumeName(src)) + `\`
	if link, ok := vs.links[v]; ok {
		rest := strings.TrimPrefix(src[len(filepath.VolumeName(src)):], `\`)
		return filepath.Join(link, rest)
	}
	return src
}

func (vs *VSSSession) Close() {
	for _, link := range vs.links {
		_ = os.Remove(link)
	}
	for _, id := range vs.ids {
		script := fmt.Sprintf(`Get-CimInstance -ClassName Win32_ShadowCopy | Where-Object { $_.ID -eq '%s' } | Remove-CimInstance`, id)
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
		hideWindow(cmd)
		_ = cmd.Run()
	}
}

// openUIWindow abre la ventana de la app (otro proceso con WebView2).
func openUIWindow(url string) {
	exe, err := os.Executable()
	if err == nil {
		args := []string{"--ventana", url}
		if instanceName != "" {
			args = append(args, "--instancia", instanceName)
		}
		cmd := exec.Command(exe, args...)
		if cmd.Start() == nil {
			return
		}
	}
	openInBrowser(url)
}

func openInBrowser(url string) {
	for _, edge := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
	} {
		if _, err := os.Stat(edge); err == nil {
			if exec.Command(edge, "--app="+url, "--window-size=1280,860").Start() == nil {
				return
			}
		}
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func openFolder(p string) {
	_ = exec.Command("explorer", p).Start()
}

func listDrives() []string {
	var out []string
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return []string{`C:\`}
	}
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) != 0 {
			out = append(out, string(rune('A'+i))+`:\`)
		}
	}
	return out
}

// detach: en Windows el proceso hijo ya sigue vivo cuando el padre termina.
func detach(cmd *exec.Cmd) {}

func waitProcessExit(pid int, timeout time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // ya no existe
	}
	defer windows.CloseHandle(h)
	ms := uint32(timeout / time.Millisecond)
	if timeout <= 0 {
		ms = 0
	}
	_, _ = windows.WaitForSingleObject(h, ms)
}

// closeAppWindows cierra las ventanas de otros procesos Resguardo.exe (la
// ventana de la interfaz es un proceso aparte) y espera a que terminen.
func closeAppWindows(timeout time.Duration) {
	self := uint32(os.Getpid())
	pids := map[uint32]bool{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if windows.Process32First(snap, &pe) == nil {
		for {
			if pe.ProcessID != self && strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "Resguardo.exe") {
				pids[pe.ProcessID] = true
			}
			if windows.Process32Next(snap, &pe) != nil {
				break
			}
		}
	}
	windows.CloseHandle(snap)
	if len(pids) == 0 {
		return
	}
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pids[pid] {
			procPostMessageW.Call(hwnd, 0x0010, 0, 0) // WM_CLOSE
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	deadline := time.Now().Add(timeout)
	for pid := range pids {
		waitProcessExit(int(pid), time.Until(deadline))
	}
}

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\Resguardo`

// syncInstallInfo: si Resguardo se instaló con el instalador y este es el
// programa instalado, actualiza la versión que muestra Windows (después de
// una actualización automática quedaría la vieja).
func syncInstallInfo() {
	if instanceName != "" || serviceMode {
		return
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	loc, _, err := k.GetStringValue("InstallLocation")
	exe, err2 := os.Executable()
	if err != nil || err2 != nil || !strings.EqualFold(filepath.Clean(loc), filepath.Dir(exe)) {
		return
	}
	if v, _, _ := k.GetStringValue("DisplayVersion"); v != AppVersion {
		_ = k.SetStringValue("DisplayVersion", AppVersion)
	}
}

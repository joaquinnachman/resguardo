package main

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"resguardo/internal/updatefmt"
)

const updateManifestURL = "https://raw.githubusercontent.com/joaquinnachman/resguardo/main/version.json"
const updatePublicKey = "6x1ZslBELo1zBI+Hsmoh4hTVGWMvTXVbx8cNxTUZ4Vc="
const updateEvery = 6 * time.Hour

func manifestURL() string {
	// Sólo para pruebas: igual se exige la firma, así que no abre ninguna puerta.
	if u := os.Getenv("RESGUARDO_ACTUALIZACIONES"); u != "" {
		return u
	}
	return updateManifestURL
}

func updDir() string { return filepath.Join(dataDir(), "actualizacion") }

// lastUpdate se guarda en actualizacion/ultima.json para mostrar las novedades
// después de reiniciar y para no reintentar una versión que falló.
type lastUpdate struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Notes  []string  `json:"notes"`
	At     time.Time `json:"at"`
	Estado string    `json:"estado"` // instalando | ok | fallida
	Visto  bool      `json:"visto"`
	Error  string    `json:"error,omitempty"`
}

func readLastUpdate() (lastUpdate, bool) {
	var l lastUpdate
	b, err := os.ReadFile(filepath.Join(updDir(), "ultima.json"))
	if err != nil || json.Unmarshal(b, &l) != nil {
		return l, false
	}
	return l, true
}

func writeLastUpdate(l lastUpdate) {
	_ = os.MkdirAll(updDir(), 0o755)
	b, _ := json.MarshalIndent(l, "", "  ")
	_ = writeFileAtomic(filepath.Join(updDir(), "ultima.json"), b)
}

type UpdateStatus struct {
	State      string    `json:"state"` // "" | buscando | al_dia | disponible | bajando | lista | esperando | instalando | error
	Current    string    `json:"current"`
	Latest     string    `json:"latest"`
	Date       string    `json:"date"`
	Notes      []string  `json:"notes"`
	Checked    time.Time `json:"checked"`
	Error      string    `json:"error"`
	Done       int64     `json:"done"`
	Total      int64     `json:"total"`
	CanSelf    bool      `json:"canSelf"`
	Why        string    `json:"why"`
	Download   string    `json:"download"`
	WaitingFor string    `json:"waitingFor"`
}

type Updater struct {
	app  *App
	mu   sync.Mutex
	st   UpdateStatus
	man  *updatefmt.Manifest
	file string // zip descargado y verificado
	want bool   // pidieron instalar ahora
	kick chan bool
	skip string // versión que falló al instalarse
}

func NewUpdater(app *App) *Updater {
	u := &Updater{app: app, kick: make(chan bool, 1)}
	u.st.Current = AppVersion
	u.st.CanSelf, u.st.Why = canSelfUpdate()
	if l, ok := readLastUpdate(); ok && l.Estado == "fallida" {
		u.skip = l.To
	}
	return u
}

func (u *Updater) Status() UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.st
	s.Notes = append([]string{}, s.Notes...)
	return s
}

func (u *Updater) set(fn func(s *UpdateStatus)) {
	u.mu.Lock()
	fn(&u.st)
	u.mu.Unlock()
	u.app.engine.notify()
}

// Check pide una búsqueda ya (install=true: además instalar en cuanto se pueda).
func (u *Updater) Check(install bool) {
	if install {
		u.mu.Lock()
		u.want = true
		u.mu.Unlock()
	}
	select {
	case u.kick <- true:
	default:
	}
}

func (u *Updater) Run() {
	// al arrancar: registrar el resultado de una actualización recién hecha
	if l, ok := readLastUpdate(); ok && l.Estado == "instalando" && l.To == AppVersion {
		l.Estado, l.Visto = "ok", false
		writeLastUpdate(l)
		u.app.log.Info("Resguardo se actualizó de la versión %s a la %s", l.From, l.To)
	} else if ok && l.Estado == "fallida" && !l.Visto {
		u.app.log.Error("No se pudo instalar la versión %s (%s); sigue funcionando la %s", l.To, l.Error, AppVersion)
	}
	go u.cleanup()
	first := time.After(20 * time.Second)
	for {
		manual := false
		select {
		case <-first:
		case manual = <-u.kick:
		case <-time.After(time.Minute):
		case <-u.app.done:
			return
		}
		s := u.app.store.GetSettings()
		st := u.Status()
		if manual || (s.UpdateAuto && time.Since(st.Checked) >= updateEvery) {
			u.check(manual)
		}
		u.maybeInstall(s)
	}
}

func (u *Updater) check(manual bool) {
	u.mu.Lock()
	prev := u.st.State
	u.mu.Unlock()
	u.set(func(s *UpdateStatus) { s.State, s.Error = "buscando", "" })
	m, err := fetchManifest()
	if err != nil {
		u.set(func(s *UpdateStatus) {
			s.Checked = time.Now()
			if u.file != "" {
				s.State = prev // ya está bajada: se puede instalar igual
				return
			}
			s.State, s.Error = "error", err.Error()
		})
		if manual {
			u.app.log.Warn("No se pudo buscar actualizaciones: %v", err)
		}
		return
	}
	newer := updatefmt.Compare(m.Version, AppVersion) > 0
	if newer && m.Version == u.skip && !manual {
		newer = false
	}
	if manual && m.Version == u.skip {
		u.skip = ""
	}
	fresh := false
	u.mu.Lock()
	u.st.Checked = time.Now()
	u.st.Latest, u.st.Date, u.st.Notes = m.Version, m.Date, m.Notes
	u.st.Download = assetFor(m).URL
	switch {
	case !newer:
		u.st.State = "al_dia"
		u.man, u.file = nil, ""
	case u.man == nil || u.man.Version != m.Version || assetFor(*u.man).SHA256 != assetFor(m).SHA256:
		fresh = u.man == nil || u.man.Version != m.Version
		u.man, u.file = &m, ""
		u.st.State = "disponible"
	case u.file != "":
		u.st.State = "lista"
		if prev == "esperando" {
			u.st.State = prev
		}
	default:
		u.st.State = "disponible"
	}
	u.mu.Unlock()
	u.app.engine.notify()
	if newer && (manual || fresh) {
		u.app.log.Info("Hay una versión nueva de Resguardo: %s (tenés la %s)", m.Version, AppVersion)
	}
}

func fetchManifest() (updatefmt.Manifest, error) {
	var m updatefmt.Manifest
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL(), nil)
	req.Header.Set("User-Agent", "Resguardo/"+AppVersion)
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return m, fmt.Errorf("sin conexión con el servidor de actualizaciones")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("el servidor de actualizaciones respondió %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return m, err
	}
	pub, _ := base64.StdEncoding.DecodeString(updatePublicKey)
	return updatefmt.Parse(raw, ed25519.PublicKey(pub))
}

func assetFor(m updatefmt.Manifest) updatefmt.Asset {
	if runtime.GOOS == "darwin" {
		return m.Mac
	}
	return m.Windows
}

// maybeInstall baja la versión nueva y la instala cuando no molesta.
func (u *Updater) maybeInstall(s Settings) {
	u.mu.Lock()
	man, file, want, can := u.man, u.file, u.want, u.st.CanSelf
	u.mu.Unlock()
	if man == nil || !can || !(s.UpdateInstall || want) {
		return
	}
	if file == "" {
		f, err := u.download(*man)
		if err != nil {
			u.set(func(st *UpdateStatus) { st.State, st.Error = "error", err.Error() })
			u.app.log.Warn("No se pudo bajar la versión %s: %v", man.Version, err)
			u.mu.Lock()
			u.want = false
			u.mu.Unlock()
			return
		}
		u.mu.Lock()
		u.file = f
		u.mu.Unlock()
		file = f
		u.set(func(st *UpdateStatus) { st.State = "lista" })
	}
	// No se reinicia en medio de un respaldo; si es automático, tampoco con un DAW abierto.
	u.app.engine.hold.Store(true)
	busy, daw := u.app.engine.busyState()
	waitFor := ""
	switch {
	case busy:
		waitFor = "que termine el respaldo en curso"
	case daw != "" && !want:
		waitFor = "que cierres " + daw
	}
	if waitFor != "" {
		u.app.engine.hold.Store(false)
		u.set(func(st *UpdateStatus) { st.State, st.WaitingFor = "esperando", waitFor })
		return
	}
	u.set(func(st *UpdateStatus) { st.State, st.WaitingFor = "instalando", "" })
	if err := u.install(*man, file); err != nil {
		u.app.engine.hold.Store(false)
		u.app.log.Error("No se pudo instalar la versión %s: %v", man.Version, err)
		u.set(func(st *UpdateStatus) { st.State, st.Error = "error", err.Error() })
		u.mu.Lock()
		u.want = false
		u.skip = man.Version
		u.mu.Unlock()
	}
}

func (u *Updater) download(m updatefmt.Manifest) (string, error) {
	a := assetFor(m)
	if a.URL == "" || a.SHA256 == "" {
		return "", errors.New("la versión publicada no tiene archivo para este sistema")
	}
	if err := os.MkdirAll(updDir(), 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(updDir(), "descarga-"+sanitizeName(m.Version)+".zip")
	if f, err := os.Open(dst); err == nil {
		h := sha256.New()
		_, _ = io.Copy(h, f)
		f.Close()
		if hex.EncodeToString(h.Sum(nil)) == strings.ToLower(a.SHA256) {
			return dst, nil
		}
	}
	u.set(func(st *UpdateStatus) { st.State, st.Done, st.Total = "bajando", 0, a.Size })
	u.app.log.Info("Bajando Resguardo %s…", m.Version)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	req.Header.Set("User-Agent", "Resguardo/"+AppVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("no se pudo bajar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("el servidor respondió %s", resp.Status)
	}
	part := dst + ".part"
	out, err := os.Create(part)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	buf := make([]byte, 256<<10)
	var n int64
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			n += int64(k)
			if a.Size > 0 && n > a.Size {
				out.Close()
				os.Remove(part)
				return "", errors.New("el archivo bajado es más grande de lo anunciado")
			}
			h.Write(buf[:k])
			if _, werr := out.Write(buf[:k]); werr != nil {
				out.Close()
				os.Remove(part)
				return "", werr
			}
			u.mu.Lock()
			u.st.Done = n
			u.mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(part)
			return "", fmt.Errorf("se cortó la descarga: %w", rerr)
		}
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if a.Size > 0 && n != a.Size {
		os.Remove(part)
		return "", fmt.Errorf("la descarga quedó incompleta (%d de %d bytes)", n, a.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(a.SHA256) {
		os.Remove(part)
		return "", errors.New("el archivo bajado no coincide con el publicado (sha256)")
	}
	if err := os.Rename(part, dst); err != nil {
		return "", err
	}
	u.app.log.Info("Resguardo %s bajado y verificado", m.Version)
	return dst, nil
}

// install reemplaza el programa, arranca la versión nueva y, si no responde, vuelve atrás.
func (u *Updater) install(m updatefmt.Manifest, zipPath string) error {
	exe, err := currentExe()
	if err != nil {
		return err
	}
	u.app.log.Info("Instalando Resguardo %s…", m.Version)
	rollback, err := swapProgram(exe, zipPath)
	if err != nil {
		return err
	}
	writeLastUpdate(lastUpdate{From: AppVersion, To: m.Version, Notes: m.Notes, At: time.Now(), Estado: "instalando"})
	port := u.app.server.port
	u.app.stopForUpdate()

	child, err := spawnSelf(exe, port)
	if err == nil {
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			if v := runningInstanceVersion(); v == m.Version {
				u.app.handedOff = true
				u.app.Quit()
				return nil
			}
		}
		err = errors.New("la versión nueva no arrancó")
		_ = child.Kill()
		time.Sleep(time.Second)
	}
	// Volver a la versión anterior.
	u.app.log.Error("Resguardo %s no arrancó (%v): se vuelve a la %s", m.Version, err, AppVersion)
	if rerr := rollback(); rerr != nil {
		u.app.log.Error("No se pudo volver a la versión anterior: %v", rerr)
	}
	writeLastUpdate(lastUpdate{From: AppVersion, To: m.Version, Notes: m.Notes, At: time.Now(), Estado: "fallida", Error: err.Error()})
	if _, serr := spawnSelf(exe, port); serr == nil {
		u.app.handedOff = true
	}
	u.app.Quit()
	return nil
}

func currentExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

func spawnSelf(exe string, port int) (*os.Process, error) {
	args := []string{"--tras-actualizar", "--puerto", strconv.Itoa(port)}
	if runtime.GOOS == "darwin" {
		args = append(args, "--daemon")
	} else {
		args = append(args, "--inicio")
	}
	if instanceName != "" {
		args = append(args, "--instancia", instanceName)
	}
	cmd := exec.Command(exe, args...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// runningInstanceVersion devuelve la versión de la instancia que responde ("" si ninguna).
func runningInstanceVersion() string {
	b, err := os.ReadFile(instancePath())
	if err != nil {
		return ""
	}
	var inst instanceInfo
	if json.Unmarshal(b, &inst) != nil || inst.Port == 0 {
		return ""
	}
	c := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping?t=%s", inst.Port, inst.Token))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var p map[string]any
	if json.NewDecoder(resp.Body).Decode(&p) != nil {
		return ""
	}
	v, _ := p["version"].(string)
	return v
}

// ---------- Reemplazo del programa ----------

// canSelfUpdate dice si esta instalación se puede actualizar sola.
func canSelfUpdate() (bool, string) {
	if serviceMode {
		return false, "Resguardo corre como servicio: actualizalo a mano."
	}
	if instanceName != "" {
		return false, "Las instancias con nombre no se actualizan solas: lo hace la instancia principal."
	}
	exe, err := currentExe()
	if err != nil {
		return false, err.Error()
	}
	switch runtime.GOOS {
	case "windows":
		dir := filepath.Dir(exe)
		if err := probeWritable(dir); err != nil {
			return false, "No se puede escribir en la carpeta de Resguardo (" + dir + "). Movelo a una carpeta tuya, por ejemplo Documentos\\Resguardo."
		}
		return true, ""
	case "darwin":
		bundle := macBundle(exe)
		if bundle == "" {
			return false, "Resguardo no está dentro de su app (.app)."
		}
		if strings.Contains(bundle, "/AppTranslocation/") {
			return false, "Mové Resguardo a la carpeta Aplicaciones para que se pueda actualizar solo."
		}
		if err := probeWritable(filepath.Dir(bundle)); err != nil {
			return false, "No se puede escribir en " + filepath.Dir(bundle) + "."
		}
		return true, ""
	}
	return false, "Las actualizaciones automáticas son para Windows y Mac."
}

func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".resguardo-prueba-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func macBundle(exe string) string {
	b := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // .../Resguardo.app/Contents/MacOS/Resguardo
	if strings.HasSuffix(b, ".app") {
		return b
	}
	return ""
}

func swapProgram(exe, zipPath string) (func() error, error) {
	switch runtime.GOOS {
	case "windows":
		return swapExe(exe, zipPath)
	case "darwin":
		return swapBundle(macBundle(exe), zipPath)
	}
	return nil, errors.New("las actualizaciones automáticas son para Windows y Mac")
}

// swapExe reemplaza el .exe en uso: Windows deja renombrar un programa abierto, no borrarlo.
func swapExe(exe, zipPath string) (func() error, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("el archivo bajado no se puede abrir: %w", err)
	}
	defer zr.Close()
	var src *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), "Resguardo.exe") {
			src = f
		}
	}
	if src == nil {
		return nil, errors.New("el archivo bajado no trae Resguardo.exe")
	}
	nuevo, viejo := exe+".nuevo", exe+".viejo"
	if err := extractZipFile(src, nuevo, 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(viejo)
	if _, err := os.Stat(viejo); err == nil {
		// quedó en uso de una actualización anterior: se usa otro nombre
		viejo = fmt.Sprintf("%s.viejo-%d", exe, time.Now().Unix())
	}
	if err := os.Rename(exe, viejo); err != nil {
		os.Remove(nuevo)
		return nil, fmt.Errorf("no se pudo apartar el programa actual: %w", err)
	}
	if err := os.Rename(nuevo, exe); err != nil {
		_ = os.Rename(viejo, exe)
		os.Remove(nuevo)
		return nil, fmt.Errorf("no se pudo poner la versión nueva: %w", err)
	}
	return func() error {
		var err error
		for i := 0; i < 20; i++ {
			_ = os.Rename(exe, exe+".fallida")
			if err = os.Rename(viejo, exe); err == nil {
				return nil
			}
			time.Sleep(500 * time.Millisecond)
		}
		return err
	}, nil
}

// swapBundle reemplaza la app de Mac (Resguardo.app) por la del zip, en la misma carpeta.
func swapBundle(bundle, zipPath string) (func() error, error) {
	if bundle == "" {
		return nil, errors.New("Resguardo no está dentro de su app (.app)")
	}
	parent := filepath.Dir(bundle)
	stage := filepath.Join(parent, ".Resguardo-nuevo")
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		os.RemoveAll(stage)
		return nil, fmt.Errorf("el archivo bajado no se puede abrir: %w", err)
	}
	for _, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		if !strings.HasPrefix(f.Name, "Resguardo.app/") || strings.Contains(f.Name, "..") {
			continue
		}
		target := filepath.Join(stage, name)
		if strings.HasSuffix(f.Name, "/") {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		if err := extractZipFile(f, target, mode); err != nil {
			zr.Close()
			os.RemoveAll(stage)
			return nil, err
		}
	}
	zr.Close()
	newApp := filepath.Join(stage, "Resguardo.app")
	bin := filepath.Join(newApp, "Contents", "MacOS", "Resguardo")
	if st, err := os.Stat(bin); err != nil || st.Mode().Perm()&0o100 == 0 {
		os.RemoveAll(stage)
		return nil, errors.New("el archivo bajado no trae una app válida")
	}
	old := filepath.Join(parent, ".Resguardo-viejo.app")
	_ = os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		os.RemoveAll(stage)
		return nil, fmt.Errorf("no se pudo apartar la app actual: %w", err)
	}
	if err := os.Rename(newApp, bundle); err != nil {
		_ = os.Rename(old, bundle)
		os.RemoveAll(stage)
		return nil, fmt.Errorf("no se pudo poner la versión nueva: %w", err)
	}
	os.RemoveAll(stage)
	return func() error {
		_ = os.RemoveAll(bundle + ".fallida")
		_ = os.Rename(bundle, bundle+".fallida")
		err := os.Rename(old, bundle)
		_ = os.RemoveAll(bundle + ".fallida")
		return err
	}, nil
}

func extractZipFile(f *zip.File, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		os.Remove(target)
		return err
	}
	return out.Close()
}

// cleanup borra lo que quedó de actualizaciones anteriores (cuando ya no está en uso).
func (u *Updater) cleanup() {
	select {
	case <-time.After(30 * time.Second):
	case <-u.app.done:
		return
	}
	if exe, err := currentExe(); err == nil {
		if runtime.GOOS == "windows" {
			matches, _ := filepath.Glob(exe + ".viejo*")
			matches = append(matches, exe+".fallida", exe+".nuevo")
			for _, m := range matches {
				_ = os.Remove(m)
			}
		}
		if b := macBundle(exe); b != "" {
			_ = os.RemoveAll(filepath.Join(filepath.Dir(b), ".Resguardo-viejo.app"))
			_ = os.RemoveAll(filepath.Join(filepath.Dir(b), ".Resguardo-nuevo"))
			_ = os.RemoveAll(b + ".fallida")
		}
	}
	entries, _ := os.ReadDir(updDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "descarga-") {
			_ = os.Remove(filepath.Join(updDir(), e.Name()))
		}
	}
}

// News devuelve la última actualización si todavía no se mostró.
func (u *Updater) News() *lastUpdate {
	l, ok := readLastUpdate()
	if !ok || l.Visto || (l.Estado != "ok" && l.Estado != "fallida") {
		return nil
	}
	return &l
}

func (u *Updater) AckNews() {
	if l, ok := readLastUpdate(); ok {
		l.Visto = true
		writeLastUpdate(l)
	}
}

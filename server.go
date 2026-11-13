package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui
var uiFS embed.FS

type Server struct {
	store    *Store
	engine   *Engine
	log      *Logger
	token    string
	port     int
	sessions sync.Map
	quit     func()
	srv      *http.Server
	upd      *Updater
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashPassword(pw, salt string) string {
	h := sha256.Sum256([]byte(salt + "|" + pw))
	for i := 0; i < 50000; i++ {
		h = sha256.Sum256(h[:])
	}
	return salt + ":" + hex.EncodeToString(h[:])
}

func checkPassword(pw, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashPassword(pw, parts[0])), []byte(stored)) == 1
}

func NewServer(store *Store, engine *Engine, log *Logger) *Server {
	return &Server{store: store, engine: engine, log: log, token: sessionToken(store.Dir)}
}

// sessionToken se guarda en disco para que la ventana abierta siga autorizada
// cuando Resguardo se reinicia (por ejemplo, después de actualizarse).
func sessionToken(dir string) string {
	p := filepath.Join(dir, "sesion.token")
	if b, err := os.ReadFile(p); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) == 32 {
			return t
		}
	}
	t := randHex(16)
	_ = os.WriteFile(p, []byte(t), 0o600)
	return t
}

func (s *Server) Listen() (net.Listener, error) {
	st := s.store.GetSettings()
	host := "127.0.0.1"
	if st.RemoteAccess && st.UIPasswordHash != "" {
		host = "0.0.0.0"
	}
	base := st.Port
	if base == 0 {
		base = 47321
	}
	var lastErr error
	if preferredPort > 0 {
		// después de actualizar: esperar a que la versión anterior suelte su puerto
		for i := 0; i < 40; i++ {
			ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(preferredPort)))
			if err == nil {
				s.port = preferredPort
				return ln, nil
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	for p := base; p < base+20; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p)))
		if err == nil {
			s.port = p
			return ln, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *Server) URL() string { return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", s.port, s.token) }

func (s *Server) Serve(ln net.Listener) error {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(uiFS, "ui")
	static := http.FileServer(http.FS(sub))
	mux.HandleFunc("/api/", s.api)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("t"); t != "" {
			if subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1 {
				http.SetCookie(w, &http.Cookie{Name: "rsg_t", Value: s.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			}
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		static.ServeHTTP(w, r)
	})
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 20 * time.Second}
	return s.srv.Serve(ln)
}

func (s *Server) Shutdown() {
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(ctx)
	}
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// authLevel: 0 sin acceso, 1 sólo login, 2 acceso completo.
func (s *Server) authLevel(r *http.Request) int {
	st := s.store.GetSettings()
	tokenOK := false
	if c, err := r.Cookie("rsg_t"); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.token)) == 1 {
		tokenOK = true
	}
	if r.Header.Get("X-Resguardo-Token") == s.token {
		tokenOK = true
	}
	sessionOK := false
	if c, err := r.Cookie("rsg_s"); err == nil {
		if _, ok := s.sessions.Load(c.Value); ok {
			sessionOK = true
		}
	}
	if isLoopback(r) {
		if !tokenOK && !sessionOK {
			if st.ProtectUI && st.UIPasswordHash != "" {
				return 1
			}
			return 0
		}
		if st.ProtectUI && st.UIPasswordHash != "" && !sessionOK {
			return 1
		}
		return 2
	}
	if !st.RemoteAccess || st.UIPasswordHash == "" {
		return 0
	}
	if sessionOK {
		return 2
	}
	return 1
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readBody(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	return dec.Decode(v)
}

type TaskView struct {
	Task
	NextRun time.Time     `json:"nextRun"`
	LastRun time.Time     `json:"lastRun"`
	Last    *BackupRecord `json:"last"`
	Backups int           `json:"backups"`
	Running bool          `json:"running"`
	Queued  bool          `json:"queued"`
}

func (s *Server) taskViews() []TaskView {
	st := s.engine.Status()
	out := []TaskView{}
	for _, t := range s.store.GetTasks() {
		ts := s.store.State(t.ID)
		v := TaskView{Task: t, NextRun: ts.NextRun, LastRun: ts.LastRun}
		h := s.store.History(t.ID)
		v.Backups = len(h)
		if len(h) > 0 {
			l := h[len(h)-1]
			v.Last = &l
		}
		if st.Running != nil && st.Running.TaskID == t.ID {
			v.Running = true
		}
		for _, q := range st.Queue {
			if q.TaskID == t.ID {
				v.Queued = true
			}
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	if path == "ping" {
		if r.URL.Query().Get("t") == s.token || r.Header.Get("X-Resguardo-Token") == s.token {
			writeJSON(w, map[string]any{"app": AppName, "version": AppVersion, "pid": os.Getpid()})
			return
		}
		writeJSON(w, map[string]any{"app": AppName})
		return
	}
	if r.Method == http.MethodPost && r.Header.Get("X-Requested-With") != "resguardo" {
		writeErr(w, http.StatusForbidden, "solicitud no permitida")
		return
	}
	lvl := s.authLevel(r)
	if path == "login" && r.Method == http.MethodPost && lvl >= 1 {
		var b struct{ Password string }
		_ = readBody(r, &b)
		st := s.store.GetSettings()
		if st.UIPasswordHash == "" || !checkPassword(b.Password, st.UIPasswordHash) {
			time.Sleep(700 * time.Millisecond)
			writeErr(w, http.StatusUnauthorized, "contraseña incorrecta")
			return
		}
		id := randHex(24)
		s.sessions.Store(id, time.Now())
		http.SetCookie(w, &http.Cookie{Name: "rsg_s", Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if path == "whoami" {
		writeJSON(w, map[string]any{"level": lvl})
		return
	}
	if lvl < 2 {
		writeErr(w, http.StatusUnauthorized, "hace falta iniciar sesión")
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			s.log.Error("Error interno en %s: %v", path, rec)
			writeErr(w, 500, fmt.Sprint(rec))
		}
	}()
	switch path {
	case "logout":
		if c, err := r.Cookie("rsg_s"); err == nil {
			s.sessions.Delete(c.Value)
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "state":
		st := s.store.GetSettings()
		st.UIPasswordHash = map[bool]string{true: "set", false: ""}[st.UIPasswordHash != ""]
		var daws []map[string]string
		for _, d := range DAWs {
			daws = append(daws, map[string]string{"id": d.ID, "name": d.Name, "exts": strings.Join(d.Exts, " ")})
		}
		writeJSON(w, map[string]any{
			"version": AppVersion, "os": runtime.GOOS, "dataDir": s.store.Dir, "settings": st,
			"tasks": s.taskViews(), "lists": s.store.ListNames(), "status": s.engine.Status(), "daws": daws,
			"instance": instanceName, "home": userHome(), "update": s.upd.Status(),
		})
	case "status":
		since, _ := strconv.ParseInt(r.URL.Query().Get("log"), 10, 64)
		writeJSON(w, map[string]any{"status": s.engine.Status(), "log": s.log.Since(since), "tasks": s.taskViews(),
			"version": AppVersion, "update": s.upd.Status()})
	case "update/check":
		var b struct {
			Install bool `json:"install"`
		}
		_ = readBody(r, &b)
		s.upd.Check(b.Install)
		writeJSON(w, s.upd.Status())
	case "update/news":
		if r.Method == http.MethodPost {
			s.upd.AckNews()
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
		writeJSON(w, map[string]any{"news": s.upd.News()})
	case "task/template":
		t := NewTask()
		if typ := r.URL.Query().Get("type"); typ != "" {
			t.Type = typ
			if typ == TypeProjects {
				t.Name = "Mis proyectos"
				t.Schedule = Schedule{Kind: "diario", Time: "03:00", Weekdays: []int{1, 2, 3, 4, 5}, MonthDay: 1, Month: 1, EveryMinutes: 60}
				t.Separated = false
				t.Projects.KeepDays = 0
			}
		}
		writeJSON(w, t)
	case "task/save":
		var t Task
		if err := readBody(r, &t); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if strings.TrimSpace(t.Name) == "" {
			writeErr(w, 400, "la tarea necesita un nombre")
			return
		}
		if t.Created.IsZero() {
			t.Created = time.Now()
		}
		if err := s.store.PutTask(t); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		s.engine.ResetSchedule(t.ID)
		go s.engine.DiscoverTask(t)
		s.log.Info("Tarea guardada: %s", t.Name)
		writeJSON(w, t)
	case "task/delete":
		var b struct {
			ID    string `json:"id"`
			Files bool   `json:"files"`
		}
		_ = readBody(r, &b)
		t, ok := s.store.GetTask(b.ID)
		if !ok {
			writeErr(w, 404, "no existe la tarea")
			return
		}
		if b.Files {
			for _, rec := range s.store.History(b.ID) {
				if rec.Project == "" {
					deleteRecordFiles(t, rec, s.store.GetSettings(), s.log)
				}
			}
		}
		_ = s.store.DeleteTask(b.ID)
		s.log.Info("Tarea borrada: %s", t.Name)
		writeJSON(w, map[string]bool{"ok": true})
	case "task/clone":
		var b struct{ ID string }
		_ = readBody(r, &b)
		t, ok := s.store.GetTask(b.ID)
		if !ok {
			writeErr(w, 404, "no existe la tarea")
			return
		}
		raw, _ := json.Marshal(t)
		var c Task
		_ = json.Unmarshal(raw, &c)
		c.ID, c.Name, c.Created = newID(), t.Name+" (copia)", time.Now()
		_ = s.store.PutTask(c)
		writeJSON(w, c)
	case "task/toggle":
		var b struct{ IDs []string }
		_ = readBody(r, &b)
		for _, id := range b.IDs {
			_ = s.store.UpdateTask(id, func(t *Task) { t.Enabled = !t.Enabled })
			s.engine.ResetSchedule(id)
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "task/reset":
		var b struct{ ID string }
		_ = readBody(r, &b)
		s.store.ResetState(b.ID)
		_ = s.store.SaveState(b.ID)
		if t, ok := s.store.GetTask(b.ID); ok {
			s.log.Info("Tarea reiniciada: el próximo respaldo de %q será completo", t.Name)
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "task/move":
		var b struct {
			ID    string
			Delta int
		}
		_ = readBody(r, &b)
		_ = s.store.MoveTask(b.ID, b.Delta)
		writeJSON(w, map[string]bool{"ok": true})
	case "run":
		var b struct {
			IDs     []string `json:"ids"`
			All     bool     `json:"all"`
			Group   *string  `json:"group"`
			Force   bool     `json:"force"`
			Project string   `json:"project"`
		}
		_ = readBody(r, &b)
		n := 0
		for _, t := range s.store.GetTasks() {
			sel := b.All && t.Enabled
			if b.Group != nil && t.Group == *b.Group && t.Enabled {
				sel = true
			}
			for _, id := range b.IDs {
				if id == t.ID {
					sel = true
				}
			}
			if sel && s.engine.Enqueue(t.ID, b.Force, b.Project, "manual") {
				n++
			}
		}
		writeJSON(w, map[string]int{"queued": n})
	case "cancel":
		var b struct{ All bool }
		_ = readBody(r, &b)
		if b.All {
			s.engine.CancelAll()
		} else {
			s.engine.Cancel()
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "pause":
		writeJSON(w, map[string]bool{"paused": s.engine.TogglePause()})
	case "scheduler":
		var b struct{ Off bool }
		_ = readBody(r, &b)
		s.engine.SetSchedulerOff(b.Off)
		writeJSON(w, map[string]bool{"ok": true})
	case "queue/remove":
		var b struct{ ID string }
		_ = readBody(r, &b)
		s.engine.RemoveQueued(b.ID)
		writeJSON(w, map[string]bool{"ok": true})
	case "ack":
		s.engine.AckErrors()
		writeJSON(w, map[string]bool{"ok": true})
	case "history":
		id := r.URL.Query().Get("task")
		h := s.store.History(id)
		sort.Slice(h, func(i, j int) bool { return h[i].Started.After(h[j].Started) })
		limit := s.store.GetSettings().HistoryItems
		if limit > 0 && len(h) > limit && r.URL.Query().Get("todo") == "" {
			h = h[:limit]
		}
		writeJSON(w, h)
	case "history/park":
		var b struct {
			Task   string   `json:"task"`
			IDs    []string `json:"ids"`
			Parked bool     `json:"parked"`
		}
		_ = readBody(r, &b)
		_ = s.store.UpdateRecords(b.Task, func(h []BackupRecord) []BackupRecord {
			for i := range h {
				for _, id := range b.IDs {
					if h[i].ID == id {
						h[i].Parked = b.Parked
					}
				}
			}
			return h
		})
		writeJSON(w, map[string]bool{"ok": true})
	case "history/delete":
		var b struct {
			Task  string   `json:"task"`
			IDs   []string `json:"ids"`
			Files bool     `json:"files"`
		}
		_ = readBody(r, &b)
		t, _ := s.store.GetTask(b.Task)
		del := map[string]bool{}
		for _, id := range b.IDs {
			del[id] = true
		}
		n := 0
		_ = s.store.UpdateRecords(b.Task, func(h []BackupRecord) []BackupRecord {
			out := h[:0]
			for _, rec := range h {
				if (del[rec.ID] || len(b.IDs) == 0) && !rec.Parked {
					if b.Files && rec.Project == "" {
						deleteRecordFiles(t, rec, s.store.GetSettings(), s.log)
					}
					n++
					continue
				}
				out = append(out, rec)
			}
			return out
		})
		writeJSON(w, map[string]int{"deleted": n})
	case "history/optimize":
		n := 0
		for _, t := range s.store.GetTasks() {
			_ = s.store.UpdateRecords(t.ID, func(h []BackupRecord) []BackupRecord {
				out := h[:0]
				for _, rec := range h {
					gone := len(rec.Outputs) > 0
					for _, o := range rec.Outputs {
						if o.Dest < len(t.Destinations) && t.Destinations[o.Dest].Kind != "" && t.Destinations[o.Dest].Kind != "local" {
							gone = false
							break
						}
						if _, err := os.Stat(o.Path); err == nil || (o.Kind != "proyecto" && rec.Project == "" && fileExistsAnyPart(o.Path)) {
							gone = false
							break
						}
					}
					if gone && !rec.Parked {
						n++
						continue
					}
					out = append(out, rec)
				}
				return out
			})
		}
		s.log.Info("Historial optimizado: %d registros huérfanos quitados", n)
		writeJSON(w, map[string]int{"removed": n})
	case "history/restore":
		var b struct {
			Task     string `json:"task"`
			ID       string `json:"id"`
			Dest     int    `json:"dest"`
			Target   string `json:"target"`
			Password string `json:"password"`
		}
		_ = readBody(r, &b)
		t, ok := s.store.GetTask(b.Task)
		if !ok {
			writeErr(w, 404, "no existe la tarea")
			return
		}
		if strings.TrimSpace(b.Target) == "" {
			writeErr(w, 400, "elegí una carpeta donde restaurar")
			return
		}
		pw := b.Password
		if pw == "" {
			pw = t.Password
		}
		s.engine.EnqueueFunc("Restaurar "+t.Name, func(ctx context.Context, prog *Progress) error {
			_, err := RestoreRecord(ctx, s.store, t, b.ID, b.Dest, b.Target, pw, prog, s.log)
			if err == nil {
				s.log.Info("Restauración terminada en %s", b.Target)
				s.engine.mu.Lock()
				s.engine.lastRestore = b.Target
				s.engine.mu.Unlock()
			}
			return err
		})
		writeJSON(w, map[string]bool{"ok": true})
	case "log/clear":
		s.log.ClearView()
		writeJSON(w, map[string]bool{"ok": true})
	case "log/add":
		var b struct{ Text string }
		_ = readBody(r, &b)
		s.log.Info("Nota: %s", b.Text)
		writeJSON(w, map[string]bool{"ok": true})
	case "log/deleteall":
		s.log.DeleteAll()
		writeJSON(w, map[string]bool{"ok": true})
	case "log/open":
		openFolder(filepath.Join(s.store.Dir, "registro"))
		writeJSON(w, map[string]bool{"ok": true})
	case "log/files":
		out := []map[string]any{}
		for _, f := range s.log.Files() {
			st, _ := os.Stat(f)
			out = append(out, map[string]any{"name": filepath.Base(f), "size": st.Size()})
		}
		writeJSON(w, out)
	case "log/file":
		name := filepath.Base(r.URL.Query().Get("name"))
		b, err := os.ReadFile(filepath.Join(s.store.Dir, "registro", name))
		if err != nil {
			writeErr(w, 404, "no existe")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(b)
	case "projects":
		t, ok := s.store.GetTask(r.URL.Query().Get("task"))
		if !ok {
			writeErr(w, 404, "no existe la tarea")
			return
		}
		writeJSON(w, s.engine.ProjectViews(t))
	case "projects/rescan":
		var b struct{ Task string }
		_ = readBody(r, &b)
		t, ok := s.store.GetTask(b.Task)
		if !ok {
			writeErr(w, 404, "no existe la tarea")
			return
		}
		s.engine.DiscoverTask(t)
		writeJSON(w, s.engine.ProjectViews(t))
	case "projects/analyze":
		t, ok := s.store.GetTask(r.URL.Query().Get("task"))
		key := r.URL.Query().Get("key")
		if !ok {
			writeErr(w, 404, "no existe la tarea")
			return
		}
		for _, p := range s.engine.ProjectViews(t) {
			if p.Key == key {
				writeJSON(w, AnalyzeProject(p.Project, s.store.GetSettings().SampleSearchFolders))
				return
			}
		}
		writeErr(w, 404, "no se encontró el proyecto")
	case "projects/override":
		var b struct {
			Task     string          `json:"task"`
			Key      string          `json:"key"`
			Override ProjectOverride `json:"override"`
		}
		_ = readBody(r, &b)
		err := s.store.UpdateTask(b.Task, func(t *Task) {
			if t.Projects.Overrides == nil {
				t.Projects.Overrides = map[string]ProjectOverride{}
			}
			if !b.Override.Excluded && len(b.Override.Destinations) == 0 && b.Override.Note == "" {
				delete(t.Projects.Overrides, b.Key)
			} else {
				t.Projects.Overrides[b.Key] = b.Override
			}
		})
		if err != nil {
			writeErr(w, 404, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "projects/snapshots":
		t, ok := s.store.GetTask(r.URL.Query().Get("task"))
		if !ok {
			writeErr(w, 404, "no existe la tarea")
			return
		}
		di, _ := strconv.Atoi(r.URL.Query().Get("dest"))
		snaps, err := ProjectSnapshots(t, s.store.GetSettings(), r.URL.Query().Get("key"), di)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, snaps)
	case "projects/park":
		var b struct {
			Task   string `json:"task"`
			Key    string `json:"key"`
			Dest   int    `json:"dest"`
			Snap   string `json:"snap"`
			Parked bool   `json:"parked"`
		}
		_ = readBody(r, &b)
		t, _ := s.store.GetTask(b.Task)
		if err := SetSnapshotParked(t, s.store.GetSettings(), b.Key, b.Dest, b.Snap, b.Parked); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "projects/restore":
		var b struct {
			Task string         `json:"task"`
			Key  string         `json:"key"`
			Dest int            `json:"dest"`
			Snap string         `json:"snap"`
			Opt  RestoreOptions `json:"opt"`
		}
		_ = readBody(r, &b)
		t, ok := s.store.GetTask(b.Task)
		if !ok {
			writeErr(w, 404, "no existe la tarea")
			return
		}
		s.engine.EnqueueFunc("Restaurar "+filepath.Base(b.Key)+" ("+b.Snap+")", func(ctx context.Context, prog *Progress) error {
			target, n, err := RestoreSnapshot(ctx, t, s.store.GetSettings(), b.Key, b.Dest, b.Snap, b.Opt, prog, s.log)
			if err == nil {
				s.log.Info("Restauración terminada: %d archivos en %s", n, target)
				s.engine.mu.Lock()
				s.engine.lastRestore = target
				s.engine.mu.Unlock()
			}
			return err
		})
		writeJSON(w, map[string]bool{"ok": true})
	case "fs/list":
		writeJSON(w, listDir(r.URL.Query().Get("path"), r.URL.Query().Get("files") == "1"))
	case "fs/test":
		var loc Location
		_ = readBody(r, &loc)
		fsys, base, err := openFS(loc, s.store.GetSettings())
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		defer fsys.Close()
		base = expandTokens(base, nil)
		if _, err := fsys.Stat(base); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				writeJSON(w, map[string]any{"ok": true, "msg": "Conecta bien. La carpeta todavía no existe: se crea en el primer respaldo."})
				return
			}
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "msg": "Conecta bien y la carpeta existe."})
	case "fs/open":
		var b struct{ Path string }
		_ = readBody(r, &b)
		openFolder(b.Path)
		writeJSON(w, map[string]bool{"ok": true})
	case "settings":
		if r.Method == http.MethodPost {
			var st Settings
			if err := readBody(r, &st); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			old := s.store.GetSettings()
			st.UIPasswordHash = old.UIPasswordHash
			if err := s.store.SaveSettings(st); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			s.log.SetDetailed(st.LogDetailed || st.LogIndividualFiles)
			s.log.SetPerDay(st.LogPerDay)
			if st.Autostart != old.Autostart {
				if err := setAutostart(st.Autostart); err != nil {
					s.log.Error("No se pudo cambiar el inicio automático: %v", err)
				}
			}
			s.engine.notify()
			writeJSON(w, map[string]bool{"ok": true, "restart": st.Port != old.Port || st.RemoteAccess != old.RemoteAccess})
			return
		}
		writeJSON(w, s.store.GetSettings())
	case "settings/password":
		var b struct {
			Old string `json:"old"`
			New string `json:"new"`
		}
		_ = readBody(r, &b)
		st := s.store.GetSettings()
		if st.UIPasswordHash != "" && !checkPassword(b.Old, st.UIPasswordHash) {
			writeErr(w, 400, "la contraseña actual no coincide")
			return
		}
		if b.New == "" {
			st.UIPasswordHash = ""
			st.ProtectUI = false
			st.RemoteAccess = false
		} else {
			st.UIPasswordHash = hashPassword(b.New, randHex(8))
		}
		_ = s.store.SaveSettings(st)
		writeJSON(w, map[string]bool{"ok": true})
	case "mail/test":
		var cfg SMTPSettings
		_ = readBody(r, &cfg)
		if err := sendMail(cfg, "Resguardo: correo de prueba", "Si te llegó este correo, Resguardo puede enviarte los registros.", nil); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "tools/decrypt":
		var b struct{ Src, Dst, Password string }
		_ = readBody(r, &b)
		s.engine.EnqueueFunc("Descifrar "+filepath.Base(b.Src), func(ctx context.Context, prog *Progress) error {
			n, err := DecryptPath(b.Src, b.Dst, b.Password)
			if err == nil {
				s.log.Info("Descifrados %d archivos en %s", n, b.Dst)
			}
			return err
		})
		writeJSON(w, map[string]bool{"ok": true})
	case "tools/extract":
		var b struct{ Archive, Dst, Password string }
		_ = readBody(r, &b)
		s.engine.EnqueueFunc("Descomprimir "+filepath.Base(b.Archive), func(ctx context.Context, prog *Progress) error {
			low := strings.ToLower(b.Archive)
			if strings.HasSuffix(low, ".7z") || strings.Contains(low, ".7z.") {
				if err := Extract7z(ctx, b.Archive, b.Dst, b.Password); err != nil {
					return err
				}
				s.log.Info("Descomprimido en %s", b.Dst)
				return nil
			}
			n, err := ExtractZip(b.Archive, b.Dst, b.Password, nil)
			if err == nil {
				s.log.Info("Descomprimidos %d archivos en %s", n, b.Dst)
			}
			return err
		})
		writeJSON(w, map[string]bool{"ok": true})
	case "tools/delete":
		var b struct{ Path string }
		_ = readBody(r, &b)
		if b.Path == "" || len(filepath.Clean(b.Path)) <= 3 {
			writeErr(w, 400, "ruta inválida")
			return
		}
		s.engine.EnqueueFunc("Borrar "+filepath.Base(b.Path), func(ctx context.Context, prog *Progress) error {
			if err := (LocalFS{}).RemoveAll(b.Path); err != nil {
				return err
			}
			s.log.Info("Borrado: %s", b.Path)
			return nil
		})
		writeJSON(w, map[string]bool{"ok": true})
	case "lists/switch":
		var b struct{ Name string }
		_ = readBody(r, &b)
		if err := s.store.SwitchList(b.Name); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		s.log.Info("Lista de tareas: %s", b.Name)
		go s.engine.discoverAll()
		writeJSON(w, map[string]bool{"ok": true})
	case "lists/saveas":
		var b struct{ Name string }
		_ = readBody(r, &b)
		if strings.TrimSpace(b.Name) == "" {
			writeErr(w, 400, "falta el nombre")
			return
		}
		_ = s.store.SaveListAs(b.Name)
		writeJSON(w, map[string]bool{"ok": true})
	case "lists/new":
		var b struct{ Name string }
		_ = readBody(r, &b)
		if strings.TrimSpace(b.Name) == "" {
			writeErr(w, 400, "falta el nombre")
			return
		}
		if err := s.store.SwitchList(b.Name); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "export":
		w.Header().Set("Content-Disposition", `attachment; filename="resguardo-tareas.json"`)
		writeJSON(w, s.store.GetTasks())
	case "import":
		var tasks []Task
		if err := readBody(r, &tasks); err != nil {
			writeErr(w, 400, "el archivo no es una lista de tareas de Resguardo")
			return
		}
		for _, t := range tasks {
			t.ID = newID()
			_ = s.store.PutTask(t)
		}
		s.log.Info("Se importaron %d tareas", len(tasks))
		writeJSON(w, map[string]int{"imported": len(tasks)})
	case "import/foreign/find":
		writeJSON(w, findForeignLists())
	case "import/foreign/preview":
		var b struct {
			Path string `json:"path"`
			Name string `json:"name"`
			Data string `json:"data"` // base64
		}
		if err := readBody(r, &b); err != nil {
			writeErr(w, 400, "solicitud inválida")
			return
		}
		var data []byte
		var err error
		if b.Data != "" {
			data, err = base64.StdEncoding.DecodeString(b.Data)
		} else {
			data, err = os.ReadFile(b.Path)
			b.Name = filepath.Base(b.Path)
		}
		if err != nil {
			writeErr(w, 400, "no se pudo leer el archivo: "+err.Error())
			return
		}
		p, err := parseForeignList(b.Name, data)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		prev := map[string]string{}
		for _, t := range s.store.GetTasks() {
			if t.ImportedFrom != "" {
				prev[t.ImportedFrom] = t.Name
			}
		}
		for i := range p.Tasks {
			p.Tasks[i].Existing = prev[p.Tasks[i].Task.ImportedFrom]
		}
		writeJSON(w, p)
	case "import/foreign/apply":
		var b struct {
			Program string `json:"program"`
			Tasks   []Task `json:"tasks"`
		}
		if err := readBody(r, &b); err != nil {
			writeErr(w, 400, "solicitud inválida")
			return
		}
		for _, t := range b.Tasks {
			t.ID = newID()
			t.Created = time.Now()
			fillSlices(&t)
			if err := s.store.PutTask(t); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
		}
		s.log.Info("Se importaron %d tareas de %s", len(b.Tasks), b.Program)
		writeJSON(w, map[string]int{"imported": len(b.Tasks)})
	case "autostart":
		var b struct{ On bool }
		_ = readBody(r, &b)
		if err := setAutostart(b.On); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		st := s.store.GetSettings()
		st.Autostart = b.On
		_ = s.store.SaveSettings(st)
		writeJSON(w, map[string]bool{"ok": true})
	case "lastrestore":
		s.engine.mu.Lock()
		p := s.engine.lastRestore
		s.engine.mu.Unlock()
		writeJSON(w, map[string]string{"path": p})
	case "quit":
		writeJSON(w, map[string]bool{"ok": true})
		go func() {
			time.Sleep(300 * time.Millisecond)
			if s.quit != nil {
				s.quit()
			}
		}()
	default:
		writeErr(w, 404, "no existe")
	}
}

func fileExistsAnyPart(p string) bool {
	if _, err := os.Stat(p); err == nil {
		return true
	}
	_, err := os.Stat(p + ".001")
	return err == nil
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

type dirItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

func listDir(p string, files bool) map[string]any {
	drives := listDrives()
	if p == "" {
		var items []dirItem
		for _, d := range drives {
			items = append(items, dirItem{Name: d, Path: d, Dir: true})
		}
		return map[string]any{"path": "", "parent": "", "items": items, "drives": drives}
	}
	p = filepath.Clean(p)
	entries, err := os.ReadDir(p)
	var items []dirItem
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "$") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(p, e.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		if !isDir && !files {
			continue
		}
		var size int64
		if !isDir {
			if fi, err := e.Info(); err == nil {
				size = fi.Size()
			}
		}
		items = append(items, dirItem{Name: e.Name(), Path: filepath.Join(p, e.Name()), Dir: isDir, Size: size})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Dir != items[j].Dir {
			return items[i].Dir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	res := map[string]any{"path": p, "parent": parent, "items": items, "drives": drives}
	if err != nil {
		res["error"] = err.Error()
	}
	return res
}

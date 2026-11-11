package main

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Store guarda la configuración, las listas de tareas, el historial y el estado
// en la carpeta de datos del usuario (%APPDATA%\Resguardo o ~/Library/Application Support/Resguardo).
type Store struct {
	mu       sync.Mutex
	Dir      string
	Settings Settings
	Tasks    []Task
	history  map[string][]BackupRecord
	states   map[string]*TaskState
}

var instanceName string

func dataDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	if serviceMode && isWindows() {
		pd := os.Getenv("ProgramData")
		if pd != "" {
			base = pd
		}
	}
	name := AppName
	if instanceName != "" {
		name = AppName + "-" + sanitizeName(instanceName)
	}
	return filepath.Join(base, name)
}

func OpenStore() (*Store, error) {
	s := &Store{Dir: dataDir(), history: map[string][]BackupRecord{}, states: map[string]*TaskState{}}
	for _, d := range []string{"", "listas", "historial", "estado", "registro", "bin", "tmp"} {
		if err := os.MkdirAll(filepath.Join(s.Dir, d), 0o755); err != nil {
			return nil, err
		}
	}
	s.Settings = DefaultSettings()
	if b, err := os.ReadFile(filepath.Join(s.Dir, "opciones.json")); err == nil {
		_ = json.Unmarshal(b, &s.Settings)
	}
	if s.Settings.CurrentList == "" {
		s.Settings.CurrentList = "Principal"
	}
	if s.Settings.SampleSearchFolders == nil {
		s.Settings.SampleSearchFolders = []string{}
	}
	if s.Settings.SMTP.To == nil {
		s.Settings.SMTP.To = []string{}
	}
	if err := s.loadList(s.Settings.CurrentList); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) listPath(name string) string {
	return filepath.Join(s.Dir, "listas", sanitizeName(name)+".json")
}

func (s *Store) loadList(name string) error {
	s.Tasks = nil
	b, err := os.ReadFile(s.listPath(name))
	if errors.Is(err, os.ErrNotExist) {
		s.Tasks = []Task{}
		return s.saveTasksLocked()
	}
	if err != nil {
		return err
	}
	var tasks []Task
	if err := json.Unmarshal(b, &tasks); err != nil {
		return fmt.Errorf("la lista %q está dañada: %w", name, err)
	}
	for i := range tasks {
		normalizeTask(&tasks[i])
	}
	s.Tasks = tasks
	return nil
}

func normalizeTask(t *Task) {
	defer fillSlices(t)
	if t.ID == "" {
		t.ID = newID()
	}
	if t.Type == "" {
		t.Type = TypeIncr
	}
	if t.Compression == "" {
		t.Compression = "ninguna"
	}
	if t.Format == "" {
		t.Format = "zip"
	}
	if t.Schedule.Kind == "" {
		t.Schedule.Kind = "manual"
	}
	if t.Projects.Overrides == nil {
		t.Projects.Overrides = map[string]ProjectOverride{}
	}
	if t.Projects.MaxDepth <= 0 {
		t.Projects.MaxDepth = 6
	}
	if len(t.Projects.DAWs) == 0 {
		t.Projects.DAWs = []string{"ableton", "cubase", "protools"}
	}
}

func (s *Store) ListNames() []string {
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "listas"))
	names := []string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(names)
	return names
}

func (s *Store) SwitchList(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadList(name); err != nil {
		return err
	}
	s.Settings.CurrentList = name
	return s.saveSettingsLocked()
}

func (s *Store) SaveListAs(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Settings.CurrentList = name
	if err := s.saveTasksLocked(); err != nil {
		return err
	}
	return s.saveSettingsLocked()
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) saveTasksLocked() error {
	b, err := json.MarshalIndent(s.Tasks, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.listPath(s.Settings.CurrentList), b)
}

func (s *Store) saveSettingsLocked() error {
	b, err := json.MarshalIndent(s.Settings, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.Dir, "opciones.json"), b)
}

func (s *Store) SaveSettings(st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.CurrentList = s.Settings.CurrentList
	s.Settings = st
	return s.saveSettingsLocked()
}

func (s *Store) GetSettings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Settings
}

func (s *Store) GetTasks() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, len(s.Tasks))
	copy(out, s.Tasks)
	return out
}

func (s *Store) GetTask(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

func (s *Store) PutTask(t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalizeTask(&t)
	for i := range s.Tasks {
		if s.Tasks[i].ID == t.ID {
			s.Tasks[i] = t
			return s.saveTasksLocked()
		}
	}
	s.Tasks = append(s.Tasks, t)
	return s.saveTasksLocked()
}

func (s *Store) UpdateTask(id string, fn func(t *Task)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			fn(&s.Tasks[i])
			return s.saveTasksLocked()
		}
	}
	return fmt.Errorf("no existe la tarea")
}

func (s *Store) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			s.Tasks = append(s.Tasks[:i], s.Tasks[i+1:]...)
			_ = os.Remove(filepath.Join(s.Dir, "historial", id+".json"))
			_ = os.Remove(filepath.Join(s.Dir, "estado", id+".json.gz"))
			delete(s.history, id)
			delete(s.states, id)
			return s.saveTasksLocked()
		}
	}
	return nil
}

func (s *Store) MoveTask(id string, delta int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			j := i + delta
			if j < 0 || j >= len(s.Tasks) {
				return nil
			}
			s.Tasks[i], s.Tasks[j] = s.Tasks[j], s.Tasks[i]
			return s.saveTasksLocked()
		}
	}
	return nil
}

// ---------- Historial ----------

func (s *Store) historyLocked(taskID string) []BackupRecord {
	if h, ok := s.history[taskID]; ok {
		return h
	}
	var h []BackupRecord
	if b, err := os.ReadFile(filepath.Join(s.Dir, "historial", taskID+".json")); err == nil {
		_ = json.Unmarshal(b, &h)
	}
	s.history[taskID] = h
	return h
}

func (s *Store) History(taskID string) []BackupRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.historyLocked(taskID)
	out := make([]BackupRecord, len(h))
	copy(out, h)
	return out
}

func (s *Store) saveHistoryLocked(taskID string) error {
	b, err := json.Marshal(s.history[taskID])
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.Dir, "historial", taskID+".json"), b)
}

func (s *Store) AddRecord(r BackupRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.historyLocked(r.TaskID)
	s.history[r.TaskID] = append(h, r)
	return s.saveHistoryLocked(r.TaskID)
}

func (s *Store) UpdateRecords(taskID string, fn func(h []BackupRecord) []BackupRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.historyLocked(taskID)
	s.history[taskID] = fn(h)
	return s.saveHistoryLocked(taskID)
}

// ---------- Estado ----------

func (s *Store) State(taskID string) *TaskState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[taskID]; ok {
		return st
	}
	st := &TaskState{Last: map[string]FileStamp{}, Full: map[string]FileStamp{}}
	if f, err := os.Open(filepath.Join(s.Dir, "estado", taskID+".json.gz")); err == nil {
		if zr, err := gzip.NewReader(f); err == nil {
			_ = json.NewDecoder(zr).Decode(st)
			zr.Close()
		}
		f.Close()
	}
	if st.Last == nil {
		st.Last = map[string]FileStamp{}
	}
	if st.Full == nil {
		st.Full = map[string]FileStamp{}
	}
	s.states[taskID] = st
	return st
}

func (s *Store) SaveState(taskID string) error {
	s.mu.Lock()
	st := s.states[taskID]
	s.mu.Unlock()
	if st == nil {
		return nil
	}
	path := filepath.Join(s.Dir, "estado", taskID+".json.gz")
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	s.mu.Lock()
	err = json.NewEncoder(zw).Encode(st)
	s.mu.Unlock()
	if err == nil {
		err = zw.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) ResetState(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[taskID]
	if st == nil {
		st = &TaskState{}
		s.states[taskID] = st
	}
	st.Last = map[string]FileStamp{}
	st.Full = map[string]FileStamp{}
	st.LastFull = st.LastFull.AddDate(-100, 0, 0)
	st.SinceFull = 0
}

// ---------- utilidades ----------

var badName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func sanitizeName(n string) string {
	n = strings.TrimSpace(badName.ReplaceAllString(n, "_"))
	n = strings.TrimRight(n, ". ")
	if n == "" {
		n = "_"
	}
	return n
}

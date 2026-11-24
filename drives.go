package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Acción al conectar un disco: cuando aparece un disco que una tarea usa como
// origen o destino, la tarea copia sola, muestra la vista previa o pregunta.

type ConnectAsk struct {
	ID     string    `json:"id"`
	TaskID string    `json:"taskId"`
	Task   string    `json:"task"`
	Kind   string    `json:"kind"` // comparar | preguntar
	Drive  string    `json:"drive"`
	At     time.Time `json:"at"`
}

// rootOfPath se puede cambiar en las pruebas.
var rootOfPath = volumeRoot

// volumeRoot devuelve la raíz del disco donde está la ruta ("" si no se sabe).
func volumeRoot(p string) string {
	p = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(p), "?D?"), "?F?")
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if v := filepath.VolumeName(p); v != "" {
		if strings.HasPrefix(v, `\\`) {
			return v // recurso de red
		}
		return v + string(filepath.Separator)
	}
	for q := p; ; q = filepath.Dir(q) {
		if isDriveRoot(q) {
			if q == "/" {
				return "" // el disco del sistema nunca se "conecta"
			}
			return q
		}
		if filepath.Dir(q) == q {
			return ""
		}
	}
}

func taskDriveRoots(t Task) []string {
	seen := map[string]bool{}
	var out []string
	add := func(l Location) {
		if l.Kind != "" && l.Kind != "local" {
			return
		}
		if r := rootOfPath(l.Path); r != "" && !seen[strings.ToLower(r)] {
			seen[strings.ToLower(r)] = true
			out = append(out, r)
		}
	}
	for _, s := range t.Sources {
		add(s)
	}
	for _, d := range t.Destinations {
		add(d)
	}
	return out
}

func driveConnected(root string) bool {
	fi, err := os.Stat(root)
	return err == nil && fi.IsDir()
}

func (e *Engine) driveWatcher() {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		e.checkDrives()
		select {
		case <-tick.C:
		case <-e.quit:
			return
		}
	}
}

func (e *Engine) checkDrives() {
	first := e.drives == nil
	if first {
		e.drives = map[string]bool{}
	}
	now := map[string]bool{}
	var newly []string
	for _, t := range e.store.GetTasks() {
		if !t.Enabled || t.OnConnect == "" {
			continue
		}
		for _, r := range taskDriveRoots(t) {
			k := strings.ToLower(r)
			if _, done := now[k]; done {
				continue
			}
			c := driveConnected(r)
			now[k] = c
			if was, known := e.drives[k]; c && known && !was && !first {
				newly = append(newly, r)
			}
		}
	}
	e.drives = now
	for _, r := range newly {
		e.driveArrived(r)
	}
}

func (e *Engine) driveArrived(root string) {
	serial := volumeSerial(root)
	for _, t := range e.store.GetTasks() {
		if !t.Enabled || t.OnConnect == "" {
			continue
		}
		roots := taskDriveRoots(t)
		uses, ready := false, true
		for _, r := range roots {
			if strings.EqualFold(r, root) {
				uses = true
			}
			if !driveConnected(r) {
				ready = false
			}
		}
		if !uses {
			continue
		}
		st := e.store.State(t.ID)
		if serial != "" {
			if st.Volumes == nil {
				st.Volumes = map[string]string{}
			}
			k := strings.ToLower(root)
			if old := st.Volumes[k]; old != "" && old != serial {
				e.log.Warn("Se conectó un disco en %s, pero no es el de la tarea %q: no se copia nada", root, t.Name)
				continue
			}
			st.Volumes[k] = serial
			_ = e.store.SaveState(t.ID)
		}
		if !ready {
			e.log.Info("Se conectó %s (tarea %q), pero falta otro disco de la tarea", root, t.Name)
			continue
		}
		if t.OnConnectHours > 0 && !st.LastRun.IsZero() && time.Since(st.LastRun) < time.Duration(t.OnConnectHours)*time.Hour {
			e.log.Info("Se conectó %s: la tarea %q ya corrió hace menos de %d h", root, t.Name, t.OnConnectHours)
			continue
		}
		switch t.OnConnect {
		case "copiar":
			e.log.Info("Se conectó %s: se ejecuta la tarea %q", root, t.Name)
			e.Enqueue(t.ID, false, "", "al conectar el disco")
		case "comparar", "preguntar":
			e.log.Info("Se conectó %s: se pregunta por la tarea %q", root, t.Name)
			e.mu.Lock()
			kept := e.asks[:0]
			for _, a := range e.asks {
				if a.TaskID != t.ID {
					kept = append(kept, a)
				}
			}
			e.asks = append(kept, ConnectAsk{ID: fmt.Sprintf("%d", time.Now().UnixNano()), TaskID: t.ID, Task: t.Name, Kind: t.OnConnect, Drive: root, At: time.Now()})
			e.mu.Unlock()
			if e.OnAsk != nil {
				go e.OnAsk()
			}
		}
	}
	e.notify()
}

// AskDone quita una pregunta pendiente (la contestó la interfaz).
func (e *Engine) AskDone(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	kept := e.asks[:0]
	for _, a := range e.asks {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	e.asks = kept
}

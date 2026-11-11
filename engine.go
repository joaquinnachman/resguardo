package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Job struct {
	ID       string    `json:"id"`
	TaskID   string    `json:"taskId"`
	TaskName string    `json:"taskName"`
	Force    bool      `json:"force"`
	Project  string    `json:"project"`
	Reason   string    `json:"reason"`
	Queued   time.Time `json:"queued"`
	Priority int       `json:"priority"`
	Kind     string    `json:"kind"` // tarea | restaurar
	run      func(ctx context.Context, prog *Progress) error
}

type pstate struct {
	LastMain time.Time `json:"lastMain"`
	DirtyAt  time.Time `json:"dirtyAt"`
	LastSnap time.Time `json:"lastSnap"`
	Pending  bool      `json:"pending"`
}

type Engine struct {
	store *Store
	log   *Logger

	mu        sync.Mutex
	queue     []*Job
	current   *Job
	cancel    context.CancelFunc
	prog      *Progress
	paused    atomic.Bool
	hold      atomic.Bool // retiene la cola mientras se instala una actualización
	doneCount atomic.Int64
	schedOff  atomic.Bool
	wake      chan struct{}
	startTime time.Time

	projects     map[string][]Project
	pstates      map[string]map[string]*pstate
	dawOpen      map[string]bool
	closeTimer   map[string]*time.Timer
	lastRun      map[string]BackupRecord
	lastResult   string // ok | errores | ""
	ranSinceMail bool
	lastRestore  string

	OnState func(state string, tip string)
	quit    chan struct{}
}

func NewEngine(store *Store, log *Logger) *Engine {
	e := &Engine{store: store, log: log, wake: make(chan struct{}, 1), startTime: time.Now(),
		projects: map[string][]Project{}, pstates: map[string]map[string]*pstate{}, dawOpen: map[string]bool{},
		closeTimer: map[string]*time.Timer{}, lastRun: map[string]BackupRecord{}, quit: make(chan struct{})}
	return e
}

func (e *Engine) Start() {
	s := e.store.GetSettings()
	e.log.SetDetailed(s.LogDetailed || s.LogIndividualFiles)
	e.log.SetPerDay(s.LogPerDay)
	e.log.DeleteOlderThan(s.DeleteLogsDays)
	e.log.Info("Resguardo %s iniciado (lista de tareas: %s)", AppVersion, s.CurrentList)
	e.loadProjectStates()
	go e.worker()
	go e.scheduler()
	go e.watchers()
	go e.discoverAll()
	// tareas que corren al iniciar
	for _, t := range e.store.GetTasks() {
		if !t.Enabled {
			continue
		}
		if s.RunAllAtStartup || t.Schedule.Kind == "inicio" || t.Schedule.AtStartup {
			e.Enqueue(t.ID, false, "", "al iniciar")
		}
	}
	e.notify()
}

func (e *Engine) Stop() {
	select {
	case <-e.quit:
	default:
		close(e.quit)
	}
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
}

// ---------- cola ----------

func (e *Engine) Enqueue(taskID string, force bool, project, reason string) bool {
	t, ok := e.store.GetTask(taskID)
	if !ok {
		return false
	}
	s := e.store.GetSettings()
	e.mu.Lock()
	if s.NoDuplicateQueue {
		for _, j := range e.queue {
			if j.TaskID == taskID && j.Project == project && j.Kind == "tarea" {
				if force {
					j.Force = true
				}
				e.mu.Unlock()
				return false
			}
		}
	}
	j := &Job{ID: newID(), TaskID: taskID, TaskName: t.Name, Force: force, Project: project, Reason: reason,
		Queued: time.Now(), Priority: t.Priority, Kind: "tarea"}
	e.queue = append(e.queue, j)
	sort.SliceStable(e.queue, func(a, b int) bool { return e.queue[a].Priority > e.queue[b].Priority })
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
	e.notify()
	return true
}

// EnqueueFunc agrega un trabajo especial (por ejemplo, una restauración).
func (e *Engine) EnqueueFunc(name string, fn func(ctx context.Context, prog *Progress) error) {
	e.mu.Lock()
	e.queue = append(e.queue, &Job{ID: newID(), TaskName: name, Reason: "pedido", Queued: time.Now(), Priority: 10, Kind: "restaurar", run: fn})
	sort.SliceStable(e.queue, func(a, b int) bool { return e.queue[a].Priority > e.queue[b].Priority })
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
	e.notify()
}

func (e *Engine) RemoveQueued(id string) {
	e.mu.Lock()
	for i, j := range e.queue {
		if j.ID == id {
			e.queue = append(e.queue[:i], e.queue[i+1:]...)
			break
		}
	}
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) Cancel() {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
}

func (e *Engine) CancelAll() {
	e.mu.Lock()
	e.queue = nil
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	e.notify()
}

func (e *Engine) TogglePause() bool {
	v := !e.paused.Load()
	e.paused.Store(v)
	if v {
		e.log.Info("Respaldo en pausa")
	} else {
		e.log.Info("Respaldo reanudado")
	}
	e.notify()
	return v
}

func (e *Engine) worker() {
	for {
		e.mu.Lock()
		var j *Job
		if len(e.queue) > 0 && !e.paused.Load() && !e.hold.Load() {
			j = e.queue[0]
			e.queue = e.queue[1:]
		}
		e.mu.Unlock()
		if j == nil {
			e.maybeMailAfterBackup()
			select {
			case <-e.wake:
			case <-time.After(2 * time.Second):
			case <-e.quit:
				return
			}
			continue
		}
		e.execute(j)
	}
}

type Status struct {
	Running    *Job          `json:"running"`
	Progress   *ProgressView `json:"progress"`
	Queue      []*Job        `json:"queue"`
	Paused     bool          `json:"paused"`
	SchedOff   bool          `json:"schedOff"`
	DAWs       []string      `json:"daws"`
	Errors     int           `json:"errors"`
	LastResult string        `json:"lastResult"`
	Done       int64         `json:"done"`
}

func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := Status{Paused: e.paused.Load(), SchedOff: e.schedOff.Load(), Errors: e.log.ErrorCount(), LastResult: e.lastResult, Done: e.doneCount.Load()}
	st.Queue = append([]*Job{}, e.queue...)
	if e.current != nil {
		c := *e.current
		st.Running = &c
		if e.prog != nil {
			v := e.prog.View()
			st.Progress = &v
		}
	}
	for id, open := range e.dawOpen {
		if open {
			if d := dawByID(id); d != nil {
				st.DAWs = append(st.DAWs, d.Name)
			}
		}
	}
	sort.Strings(st.DAWs)
	return st
}

func (e *Engine) notify() {
	if e.OnState == nil {
		return
	}
	st := e.Status()
	state, tip := "ok", "Resguardo: todo al día"
	switch {
	case st.Running != nil && st.Paused:
		state, tip = "pausado", "Resguardo: en pausa"
	case st.Running != nil:
		state, tip = "trabajando", "Resguardo: respaldando "+st.Running.TaskName
	case len(st.Queue) > 0 && st.Paused:
		state, tip = "pausado", "Resguardo: en pausa"
	case e.lastResult == "errores" && e.store.GetSettings().TrayErrorSignal:
		state, tip = "error", "Resguardo: el último respaldo tuvo errores"
	}
	if len(st.DAWs) > 0 && state == "ok" {
		tip += " · " + strings.Join(st.DAWs, ", ") + " abierto"
	}
	e.OnState(state, tip)
}

func (e *Engine) AckErrors() {
	e.mu.Lock()
	e.lastResult = ""
	e.mu.Unlock()
	e.log.ResetErrors()
	e.notify()
}

// limitSpeed devuelve el límite de velocidad actual (MB/s).
func (e *Engine) limitSpeed() int {
	s := e.store.GetSettings()
	limit := s.MaxSpeedMBps
	if s.ThrottleWhileDAW && s.ThrottleMBps > 0 {
		e.mu.Lock()
		open := false
		for _, v := range e.dawOpen {
			open = open || v
		}
		e.mu.Unlock()
		if open && (limit == 0 || s.ThrottleMBps < limit) {
			limit = s.ThrottleMBps
		}
	}
	return limit
}

func (e *Engine) execute(j *Job) {
	ctx, cancel := context.WithCancel(context.Background())
	prog := &Progress{Started: time.Now(), lastTime: time.Now()}
	e.mu.Lock()
	e.current, e.cancel, e.prog = j, cancel, prog
	e.mu.Unlock()
	e.notify()
	defer func() {
		cancel()
		e.mu.Lock()
		e.current, e.cancel, e.prog = nil, nil, nil
		e.mu.Unlock()
		e.doneCount.Add(1)
		e.notify()
	}()

	if j.run != nil {
		e.log.Info("── %s", j.TaskName)
		if err := j.run(ctx, prog); err != nil {
			e.log.Error("%s: %v", j.TaskName, err)
		}
		return
	}

	task, ok := e.store.GetTask(j.TaskID)
	if !ok {
		return
	}
	s := e.store.GetSettings()
	if s.NoRunOnBattery && j.Reason != "manual" && onBattery() {
		e.log.Warn("La tarea %q no se ejecuta: la computadora está a batería", task.Name)
		return
	}
	stopSleep := func() {}
	if s.PreventSleep {
		stopSleep = preventSleep()
	}
	defer stopSleep()
	if s.LowPriority {
		setLowPriority(true)
		defer setLowPriority(false)
	}
	what := task.Name
	if j.Project != "" {
		what += " → " + filepath.Base(j.Project)
	}
	e.log.Info("── Inicio: %s (%s)", what, j.Reason)
	rc := &RunCtx{ctx: ctx, store: e.store, log: e.log, prog: prog, task: task, settings: s, reason: j.Reason, force: j.Force, project: j.Project}
	rc.copier = &Copier{ctx: ctx, paused: &e.paused, limit: e.limitSpeed, prog: prog, settings: s, log: e.log}

	var rec *BackupRecord
	err := runImpersonated(task.RunAs, e.log, func() error {
		preFailed := false
		if len(task.PreEvents) > 0 {
			prog.Set("Eventos previos")
			if perr := runEvents(ctx, task.PreEvents, &task, s, e.log); perr != nil {
				preFailed = true
				rc.errors++
				e.log.Error("Evento previo: %v", perr)
				if task.CancelIfPreFails {
					return fmt.Errorf("se cancela la tarea porque falló un evento previo")
				}
			}
		}
		var rerr error
		switch task.Type {
		case TypeDummy:
			e.log.Info("Tarea ficticia: sólo eventos")
		case TypeProjects:
			rec, rerr = runProjects(rc, e)
		default:
			rec, rerr = runClassic(rc)
		}
		if len(task.PostEvents) > 0 && !(preFailed && task.SkipPostIfPreFailed) && ctx.Err() == nil {
			prog.Set("Eventos posteriores")
			if perr := runEvents(ctx, task.PostEvents, &task, s, e.log); perr != nil {
				rc.errors++
				e.log.Error("Evento posterior: %v", perr)
			}
		}
		return rerr
	})
	dur := time.Since(prog.Started).Round(time.Second)
	result := "ok"
	switch {
	case ctx.Err() != nil:
		e.log.Warn("── Cancelado: %s", what)
		result = "cancelado"
	case err != nil:
		e.log.Error("── Falló: %s: %v", what, err)
		result = "errores"
	case rc.errors > 0:
		e.log.Warn("── Terminado con %d errores: %s · %d archivos · %s · %s", rc.errors, what, rc.files, humanBytes(rc.bytes), dur)
		result = "errores"
	default:
		e.log.Info("── Terminado: %s · %d archivos · %s · %s", what, rc.files, humanBytes(rc.bytes), dur)
	}
	e.mu.Lock()
	if result != "cancelado" {
		e.lastResult = result
	}
	e.ranSinceMail = true
	if rec != nil {
		e.lastRun[task.ID] = *rec
	}
	e.mu.Unlock()
	if s.PlaySound {
		go playSound(s.SoundFile)
	}
	if s.MailEnabled && s.MailWhen == "tarea" {
		go e.mailLog(fmt.Sprintf("%s · %s", task.Name, map[string]string{"ok": "correcto", "errores": "con errores", "cancelado": "cancelado"}[result]), result == "ok")
	}
}

// ---------- programador ----------

func (e *Engine) scheduler() {
	s := e.store.GetSettings()
	// respaldos perdidos mientras la computadora estaba apagada
	for _, t := range e.store.GetTasks() {
		st := e.store.State(t.ID)
		missed := t.Enabled && s.RunMissed && scheduledKind(t.Schedule.Kind) && !st.NextRun.IsZero() && st.NextRun.Before(time.Now())
		if missed {
			e.log.Info("La tarea %q no se ejecutó el %s (equipo apagado): se ejecuta ahora", t.Name, st.NextRun.Format("02/01 15:04"))
			e.Enqueue(t.ID, false, "", "respaldo perdido")
		}
		st.NextRun = time.Time{}
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	lastMailDay := ""
	for {
		e.scheduleTick()
		s := e.store.GetSettings()
		if s.MailEnabled && s.MailWhen == "diario" {
			now := time.Now()
			if now.Format("15:04") >= s.MailTime && lastMailDay != now.Format("2006-01-02") {
				lastMailDay = now.Format("2006-01-02")
				go e.mailLog("Registro diario", false)
			}
		}
		select {
		case <-tick.C:
		case <-e.quit:
			return
		}
	}
}

func scheduledKind(k string) bool {
	switch k {
	case "unavez", "diario", "semanal", "mensual", "anual", "temporizador":
		return true
	}
	return false
}

func (e *Engine) scheduleTick() {
	if e.schedOff.Load() {
		return
	}
	now := time.Now()
	for _, t := range e.store.GetTasks() {
		if !t.Enabled || !scheduledKind(t.Schedule.Kind) {
			continue
		}
		st := e.store.State(t.ID)
		if st.NextRun.IsZero() {
			base := st.LastRun
			if base.Before(e.startTime) {
				base = e.startTime
			}
			st.NextRun = NextRun(t.Schedule, now, base, st.LastRun)
			_ = e.store.SaveState(t.ID)
			continue
		}
		if now.Before(st.NextRun) {
			continue
		}
		if inWindow(t.Schedule, now) {
			e.Enqueue(t.ID, false, "", "programado")
		} else {
			e.log.Info("La tarea %q quedó fuera de su franja horaria; se saltea", t.Name)
		}
		st.NextRun = NextRun(t.Schedule, now.Add(time.Minute), now, now)
		_ = e.store.SaveState(t.ID)
	}
}

// ResetSchedule recalcula la próxima ejecución (después de editar la tarea).
func (e *Engine) ResetSchedule(taskID string) {
	st := e.store.State(taskID)
	st.NextRun = time.Time{}
	_ = e.store.SaveState(taskID)
}

func (e *Engine) SetSchedulerOff(off bool) {
	e.schedOff.Store(off)
	if off {
		e.log.Info("Programador en pausa: no se ejecutan tareas programadas")
	} else {
		e.log.Info("Programador activo")
	}
	e.notify()
}

// ---------- vigilancia de DAW y de proyectos ----------

func (e *Engine) watchers() {
	dawTick := time.NewTicker(4 * time.Second)
	saveTick := time.NewTicker(10 * time.Second)
	defer dawTick.Stop()
	defer saveTick.Stop()
	lastRescan := time.Now()
	for {
		select {
		case <-e.quit:
			return
		case <-dawTick.C:
			if e.store.GetSettings().DAWWatch {
				e.checkDAWs()
			}
		case <-saveTick.C:
			s := e.store.GetSettings()
			if s.DAWWatch {
				e.checkSaves()
			}
			if s.RescanMinutes > 0 && time.Since(lastRescan) > time.Duration(s.RescanMinutes)*time.Minute {
				lastRescan = time.Now()
				go e.discoverAll()
			}
		}
	}
}

func (e *Engine) checkDAWs() {
	now := RunningDAWs()
	e.mu.Lock()
	var closed, opened []string
	for id := range e.dawOpen {
		if e.dawOpen[id] && !now[id] {
			closed = append(closed, id)
		}
	}
	for id := range now {
		if !e.dawOpen[id] {
			opened = append(opened, id)
		}
	}
	e.dawOpen = now
	e.mu.Unlock()
	for _, id := range opened {
		if d := dawByID(id); d != nil {
			e.log.Info("Se abrió %s", d.Name)
		}
	}
	for _, id := range closed {
		d := dawByID(id)
		if d == nil {
			continue
		}
		delay := time.Duration(e.store.GetSettings().CloseDelaySeconds) * time.Second
		e.log.Info("Se cerró %s: se revisan sus proyectos en %s", d.Name, delay)
		dawID := id
		time.AfterFunc(delay, func() { e.onDAWClosed(dawID) })
	}
	if len(opened)+len(closed) > 0 {
		e.notify()
	}
}

func (e *Engine) onDAWClosed(dawID string) {
	for _, t := range e.store.GetTasks() {
		if !t.Enabled {
			continue
		}
		if t.Type == TypeProjects && t.Projects.TriggerOnClose && contains(t.Projects.DAWs, dawID) {
			e.mu.Lock()
			projects := append([]Project{}, e.projects[t.ID]...)
			states := e.pstates[t.ID]
			e.mu.Unlock()
			for _, p := range projects {
				if p.DAW != dawID {
					continue
				}
				_, _, mt := projectFilesIn(p.Dir, []string{p.DAW})
				e.mu.Lock()
				ps := states[p.Key]
				need := ps == nil || mt.After(ps.LastSnap) || ps.Pending || !ps.DirtyAt.IsZero()
				if ps != nil {
					ps.DirtyAt = time.Time{}
				}
				e.mu.Unlock()
				if need {
					e.Enqueue(t.ID, false, p.Key, "se cerró el DAW")
				}
			}
		}
		if t.Type != TypeProjects && t.Schedule.OnDAWClose {
			e.Enqueue(t.ID, false, "", "se cerró el DAW")
		}
	}
}

func (e *Engine) checkSaves() {
	s := e.store.GetSettings()
	quiet := time.Duration(s.SaveQuietSeconds) * time.Second
	for _, t := range e.store.GetTasks() {
		if !t.Enabled {
			continue
		}
		watchSave := (t.Type == TypeProjects && t.Projects.TriggerOnSave) || (t.Type != TypeProjects && t.Schedule.OnDAWSave)
		if !watchSave {
			continue
		}
		e.mu.Lock()
		projects := append([]Project{}, e.projects[t.ID]...)
		if e.pstates[t.ID] == nil {
			e.pstates[t.ID] = map[string]*pstate{}
		}
		states := e.pstates[t.ID]
		e.mu.Unlock()
		for _, p := range projects {
			_, _, mt := projectFilesIn(p.Dir, []string{p.DAW})
			e.mu.Lock()
			ps := states[p.Key]
			if ps == nil {
				ps = &pstate{LastMain: mt}
				states[p.Key] = ps
			}
			if mt.After(ps.LastMain) {
				ps.LastMain = mt
				ps.DirtyAt = time.Now()
				e.mu.Unlock()
				e.log.Info("Se guardó %s (%s)", p.Name, p.DAWName)
				continue
			}
			ready := !ps.DirtyAt.IsZero() && time.Since(ps.DirtyAt) >= quiet
			if ready {
				ps.DirtyAt = time.Time{}
			}
			e.mu.Unlock()
			if ready {
				if t.Type == TypeProjects {
					e.Enqueue(t.ID, false, p.Key, "se guardó el proyecto")
				} else {
					e.Enqueue(t.ID, false, "", "se guardó un proyecto")
				}
			}
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// discoverAll busca proyectos en todas las tareas que lo necesitan.
func (e *Engine) discoverAll() {
	for _, t := range e.store.GetTasks() {
		e.DiscoverTask(t)
	}
}

func (e *Engine) DiscoverTask(t Task) []Project {
	if t.Type != TypeProjects && !t.Schedule.OnDAWSave {
		return nil
	}
	cfg := t.Projects
	if t.Type != TypeProjects {
		cfg.DAWs = []string{"ableton", "cubase", "protools", "fl", "reaper", "studioone", "bitwig", "logic"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	projects := DiscoverProjects(ctx, projectSourcePaths(t), cfg)
	e.setProjects(t.ID, projects)
	return projects
}

func (e *Engine) setProjects(taskID string, projects []Project) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.projects[taskID] = projects
	if e.pstates[taskID] == nil {
		e.pstates[taskID] = map[string]*pstate{}
	}
	states := e.pstates[taskID]
	t, _ := e.store.GetTask(taskID)
	for _, p := range projects {
		ps := states[p.Key]
		if ps == nil {
			ps = &pstate{}
			states[p.Key] = ps
		}
		if ps.LastMain.IsZero() {
			ps.LastMain = p.Modified
			// nunca respaldado, o cambiado mientras Resguardo estaba cerrado
			if t.Type == TypeProjects && t.Projects.TriggerOnSave && (ps.LastSnap.IsZero() || p.Modified.After(ps.LastSnap)) {
				ps.DirtyAt = time.Now()
			}
		}
	}
}

func (e *Engine) projectDone(taskID string, p Project, pending bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pstates[taskID] == nil {
		e.pstates[taskID] = map[string]*pstate{}
	}
	ps := e.pstates[taskID][p.Key]
	if ps == nil {
		ps = &pstate{}
		e.pstates[taskID][p.Key] = ps
	}
	ps.LastSnap = time.Now()
	ps.Pending = pending
	if p.Modified.After(ps.LastMain) {
		ps.LastMain = p.Modified
	}
	if pending {
		ps.DirtyAt = time.Now()
	} else {
		ps.DirtyAt = time.Time{}
	}
}

func (e *Engine) loadProjectStates() {
	for _, t := range e.store.GetTasks() {
		if t.Type != TypeProjects {
			continue
		}
		m := map[string]*pstate{}
		for _, r := range e.store.History(t.ID) {
			if r.Project == "" {
				continue
			}
			ps := m[r.Project]
			if ps == nil {
				ps = &pstate{}
				m[r.Project] = ps
			}
			if r.Finished.After(ps.LastSnap) {
				ps.LastSnap = r.Finished
			}
		}
		e.mu.Lock()
		e.pstates[t.ID] = m
		e.mu.Unlock()
	}
}

type ProjectView struct {
	Project
	LastSnap  time.Time  `json:"lastSnap"`
	Dirty     bool       `json:"dirty"`
	Pending   bool       `json:"pending"`
	Excluded  bool       `json:"excluded"`
	Custom    bool       `json:"custom"`
	Dests     []Location `json:"dests"`
	Snapshots int        `json:"snapshots"`
}

func (e *Engine) ProjectViews(t Task) []ProjectView {
	e.mu.Lock()
	projects := append([]Project{}, e.projects[t.ID]...)
	states := e.pstates[t.ID]
	e.mu.Unlock()
	counts := map[string]int{}
	for _, r := range e.store.History(t.ID) {
		if r.Project != "" {
			counts[r.Project]++
		}
	}
	out := []ProjectView{}
	for _, p := range projects {
		v := ProjectView{Project: p, Snapshots: counts[p.Key]}
		e.mu.Lock()
		if ps := states[p.Key]; ps != nil {
			v.LastSnap, v.Dirty, v.Pending = ps.LastSnap, !ps.DirtyAt.IsZero() || p.Modified.After(ps.LastSnap), ps.Pending
		} else {
			v.Dirty = true
		}
		e.mu.Unlock()
		if o, ok := t.Projects.Overrides[p.Key]; ok {
			v.Excluded = o.Excluded
			v.Custom = len(o.Destinations) > 0
		}
		v.Dests = projectDestinations(t, p.Key)
		out = append(out, v)
	}
	return out
}

// ---------- correo ----------

func (e *Engine) maybeMailAfterBackup() {
	s := e.store.GetSettings()
	e.mu.Lock()
	ran := e.ranSinceMail
	if ran && s.MailWhen == "respaldo" {
		e.ranSinceMail = false
	}
	e.mu.Unlock()
	if ran && s.MailEnabled && s.MailWhen == "respaldo" {
		go e.mailLog("Fin de respaldos", false)
	}
}

func (e *Engine) mailLog(subject string, okOnly bool) {
	s := e.store.GetSettings()
	files := e.log.Files()
	if len(files) == 0 {
		return
	}
	if s.MailLatestOnly || s.LogPerDay {
		files = files[len(files)-1:]
	}
	hasErr := false
	var body strings.Builder
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), " ERROR ") {
			hasErr = true
		}
		if !s.MailAttach {
			body.Write(b)
		}
	}
	if s.MailOnlyErrors && !hasErr {
		return
	}
	text := s.SMTP.Body
	if !s.MailAttach {
		text += "\r\n\r\n" + body.String()
	}
	var attach []string
	if s.MailAttach {
		attach = files
	}
	subj := s.SMTP.Subject
	if subj == "" {
		subj = "Resguardo"
	}
	if subject != "" {
		subj += " · " + subject
	}
	if err := sendMail(s.SMTP, subj, text, attach); err != nil {
		e.log.Error("No se pudo enviar el registro por correo: %v", err)
		return
	}
	e.log.Info("Registro enviado por correo")
	if s.MailDeleteIfSent {
		for _, f := range files {
			_ = os.Remove(f)
		}
	}
}

// busyState dice si hay un respaldo en curso o en cola, y qué DAW está abierto.
func (e *Engine) busyState() (bool, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	busy := e.current != nil || len(e.queue) > 0
	daw := ""
	for id, open := range e.dawOpen {
		if open {
			if d := dawByID(id); d != nil {
				daw = d.Name
			}
		}
	}
	return busy, daw
}

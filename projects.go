package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// runProjects ejecuta una tarea de tipo "proyectos": una instantánea por proyecto con cambios.
func runProjects(rc *RunCtx, e *Engine) (*BackupRecord, error) {
	task := rc.task
	start := time.Now()
	filter, err := NewFilter(&task)
	if err != nil {
		return nil, err
	}
	var sources []string
	for _, s := range task.Sources {
		if s.Kind == "ftp" || s.Kind == "sftp" {
			rc.log.Warn("Las tareas de proyectos sólo leen carpetas locales o de red; se ignora %s", describeLocation(s))
			continue
		}
		p := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s.Path), "?D?"), "?F?")
		sources = append(sources, expandTokens(p, &task))
	}
	var readRoot func(string) string
	if task.UseVSS && isWindows() {
		rc.prog.Set("Creando instantánea de volumen (VSS)")
		vs, err := newVSSSession(rc.ctx, sources, rc.log)
		if err != nil {
			if rc.settings.CancelIfVSSFails {
				return nil, fmt.Errorf("no se pudo crear la instantánea de volumen: %w", err)
			}
			rc.log.Warn("No se pudo usar la instantánea de volumen (%v); se copia sin VSS", err)
		} else {
			defer vs.Close()
			readRoot = vs.Map
		}
	}
	rc.prog.Set("Buscando proyectos")
	projects := DiscoverProjects(rc.ctx, sources, task.Projects)
	if e != nil {
		e.setProjects(task.ID, projects)
	}
	if rc.project != "" {
		var only []Project
		for _, p := range projects {
			if p.Key == rc.project {
				only = append(only, p)
			}
		}
		if len(only) == 0 {
			return nil, fmt.Errorf("no se encontró el proyecto %s (¿se movió o se renombró?)", rc.project)
		}
		projects = only
	}
	rc.log.Info("%d proyectos encontrados", len(projects))
	snaps := 0
	for _, p := range projects {
		if rc.ctx.Err() != nil {
			break
		}
		if o, ok := task.Projects.Overrides[p.Key]; ok && o.Excluded {
			continue
		}
		dests := projectDestinations(task, p.Key)
		if len(dests) == 0 {
			rc.fail("%s: no tiene destino configurado", p.Name)
			continue
		}
		rc.prog.Set("Revisando " + p.Name)
		files, externals, err := rc.scanProject(p, filter, readRoot)
		if err != nil {
			rc.fail("%s: %v", p.Name, err)
			continue
		}
		anyPending := false
		for di, d := range dests {
			res, pending, err := rc.snapshotProject(p, files, externals, di, d)
			ts := res.TS
			if pending {
				anyPending = true
			}
			if err != nil {
				if rc.ctx.Err() != nil {
					break
				}
				rc.fail("%s → %s: %v", p.Name, describeLocation(d), err)
				anyPending = true
				continue
			}
			if ts == "" {
				continue
			}
			snaps++
			nfiles, nbytes := res.Changed, res.Bytes
			rec := BackupRecord{ID: newID(), TaskID: task.ID, Type: "instantanea", Started: start, Finished: time.Now(),
				Status: "ok", Project: p.Key, Snapshot: ts, Reason: rc.reason, Files: nfiles, Bytes: nbytes,
				Outputs: []Output{{Dest: di, Path: projectRoot(LocalFS{}, expandTokens(d.Path, &task), p.Key), Kind: "proyecto"}}}
			if d.Kind != "" && d.Kind != "local" {
				rec.Outputs[0].Path = describeLocation(d) + "/" + p.Key
			}
			if pending {
				rec.Status = "errores"
			}
			_ = rc.store.AddRecord(rec)
		}
		if e != nil {
			e.projectDone(task.ID, p, anyPending)
		}
	}
	st := rc.store.State(task.ID)
	st.LastRun = time.Now()
	_ = rc.store.SaveState(task.ID)
	rec := &BackupRecord{ID: newID(), TaskID: task.ID, Type: TypeProjects, Started: start, Finished: time.Now(),
		Files: rc.files, Bytes: rc.bytes, Errors: rc.errors}
	switch {
	case rc.ctx.Err() != nil:
		rec.Status = "cancelado"
	case rc.errors > 0:
		rec.Status = "errores"
	default:
		rec.Status = "ok"
	}
	if snaps == 0 && rc.ctx.Err() == nil {
		rc.log.Info("Ningún proyecto tenía cambios")
	}
	return rec, nil
}

func projectSourcePaths(task Task) []string {
	var out []string
	for _, s := range task.Sources {
		if s.Kind == "ftp" || s.Kind == "sftp" {
			continue
		}
		p := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s.Path), "?D?"), "?F?")
		out = append(out, filepath.Clean(expandTokens(p, &task)))
	}
	return out
}

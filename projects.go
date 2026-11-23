package main

import (
	"errors"
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
	allProjects := projects
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
				Outputs: []Output{{Dest: di, Path: projectPaths(LocalFS{}, expandTokens(d.Path, &task), p.Key, task.Projects).open(), Kind: "proyecto"}}}
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
	// "Copiar toda la carpeta": lo que no es de ningún proyecto, en las
	// pasadas completas (no cuando se guarda un solo proyecto).
	if task.Projects.CopyAll && rc.project == "" && rc.ctx.Err() == nil {
		rc.copyLooseFiles(sources, allProjects, filter, readRoot)
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

// pathWithin: p es dir o está adentro de dir.
func pathWithin(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	if isWindows() {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
	}
	if p == dir {
		return true
	}
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return strings.HasPrefix(p, dir)
}

// copyLooseFiles copia tal cual lo que está en las carpetas de proyectos pero
// fuera de cualquier proyecto (textos, PDFs, referencias, carpetas sueltas),
// en la misma estructura que el origen. Sin versiones: si cambió, se
// reemplaza; no borra nada del destino.
func (rc *RunCtx) copyLooseFiles(sources []string, projects []Project, filter *Filter, readRoot func(string) string) {
	rc.prog.Set("Copiando el resto de las carpetas")
	entries, warns, err := ScanSources(rc.ctx, sources, ScanOptions{Subdirs: true, Filter: filter, ReadRoot: readRoot,
		FollowReparse: !rc.settings.IgnoreReparsePoints}, rc.log)
	if err != nil {
		rc.fail("Resto de las carpetas: %v", err)
		return
	}
	for _, w := range warns {
		rc.fail("%s", w)
	}
	inProject := func(real string) bool {
		for _, p := range projects {
			if pathWithin(real, p.Dir) {
				return true
			}
		}
		return false
	}
	var loose []Entry
	for _, e := range entries {
		if !e.Dir && !inProject(e.Real) {
			loose = append(loose, e)
		}
	}
	if len(loose) == 0 {
		return
	}
	for _, d := range rc.task.Destinations {
		if rc.ctx.Err() != nil {
			return
		}
		fsys, base, err := openFS(d, rc.settings)
		if err != nil {
			rc.fail("Resto de las carpetas → %s: %v", describeLocation(d), err)
			continue
		}
		base = expandTokens(base, &rc.task)
		var todo []Entry
		var bytes int64
		target := func(e Entry) string {
			parts := strings.Split(e.Rel, "/")
			for i := range parts {
				parts[i] = sanitizeName(parts[i])
			}
			return fsys.Join(append([]string{base}, parts...)...)
		}
		for _, e := range loose {
			if st, err := fsys.Stat(target(e)); err == nil && st.Size() == e.Size && absDur(st.ModTime().Sub(e.MTime)) <= 2*time.Second {
				continue
			}
			todo = append(todo, e)
			bytes += e.Size
		}
		rc.prog.AddTotals(len(todo), bytes)
		copied := 0
		for _, e := range todo {
			if rc.ctx.Err() != nil {
				break
			}
			dst := target(e)
			rc.prog.SetCurrent(e.Real)
			err := fsys.MkdirAll(fsys.Dir(dst))
			if err == nil {
				err = rc.copier.CopyFile(fsys, e.Abs, dst, entryInfo(e), rc.task.Projects.Verify)
			}
			if err != nil && !errors.Is(err, errChangedDuringCopy) {
				rc.fail("%s: %v", e.Real, err)
			} else {
				copied++
				rc.files++
				rc.bytes += e.Size
			}
			rc.prog.FileDone()
		}
		fsys.Close()
		if copied > 0 {
			rc.log.Info("Resto de las carpetas → %s: %d archivos copiados (%s)", describeLocation(d), copied, humanBytes(bytes))
		}
	}
}

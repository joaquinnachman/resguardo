package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Estructura en el destino, por proyecto:
//
//	<destino>/<ruta del proyecto>/Actual/...                       copia actual, se abre directo con el DAW
//	<destino>/<ruta del proyecto>/Versiones anteriores/<fecha>/... lo que se reemplazó o borró en esa fecha
//	<destino>/<ruta del proyecto>/_resguardo/instantaneas/<fecha>.json.gz  lista exacta de cada versión
const (
	dirActual    = "Actual"
	dirVersiones = "Versiones anteriores"
	dirMeta      = "_resguardo"
	dirExternos  = "_Samples externos"
)

type MFile struct {
	Size  int64 `json:"s"`
	MTime int64 `json:"m"`
}

type Manifest struct {
	Version   int               `json:"version"`
	Time      time.Time         `json:"time"`
	Key       string            `json:"key"`
	Name      string            `json:"name"`
	DAW       string            `json:"daw"`
	SourceDir string            `json:"sourceDir"`
	Main      string            `json:"main"`
	Reason    string            `json:"reason"`
	Files     map[string]MFile  `json:"files"`
	Externals map[string]string `json:"externals"`
	Changed   int               `json:"changed"`
	Removed   int               `json:"removed"`
	Bytes     int64             `json:"bytes"`
}

type SnapshotInfo struct {
	Name    string    `json:"name"`
	Time    time.Time `json:"time"`
	Files   int       `json:"files"`
	Changed int       `json:"changed"`
	Removed int       `json:"removed"`
	Bytes   int64     `json:"bytes"`
	Total   int64     `json:"total"`
	Reason  string    `json:"reason"`
	Main    string    `json:"main"`
	Parked  bool      `json:"parked"`
}

func mtimeClose(a, b int64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= int64(2*time.Second)
}

func (m MFile) matches(info fs.FileInfo) bool {
	return info.Size() == m.Size && mtimeClose(info.ModTime().UnixNano(), m.MTime)
}

func projectRoot(f FS, base, key string) string {
	parts := strings.Split(key, "/")
	for i := range parts {
		parts[i] = sanitizeName(parts[i])
	}
	return f.Join(append([]string{base}, parts...)...)
}

func readManifest(f FS, p string) (*Manifest, error) {
	r, err := f.Open(p)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var m Manifest
	if err := json.NewDecoder(zr).Decode(&m); err != nil {
		return nil, err
	}
	if m.Files == nil {
		m.Files = map[string]MFile{}
	}
	return &m, nil
}

func writeManifest(f FS, p string, m *Manifest) error {
	if err := f.MkdirAll(f.Dir(p)); err != nil {
		return err
	}
	w, err := f.Create(p + ".rsgtmp")
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(w)
	err = json.NewEncoder(zw).Encode(m)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = f.Remove(p + ".rsgtmp")
		return err
	}
	if f.Kind() == "ftp" {
		_ = f.Remove(p)
	}
	return f.Rename(p+".rsgtmp", p)
}

func listSnapshots(f FS, root string) []string {
	entries, err := f.ReadDir(f.Join(root, dirMeta, "instantaneas"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".json.gz") {
			out = append(out, strings.TrimSuffix(n, ".json.gz"))
		}
	}
	sort.Strings(out)
	return out
}

func readParked(f FS, root string) map[string]bool {
	out := map[string]bool{}
	r, err := f.Open(f.Join(root, dirMeta, "fijadas.json"))
	if err != nil {
		return out
	}
	defer r.Close()
	var l []string
	_ = json.NewDecoder(r).Decode(&l)
	for _, s := range l {
		out[s] = true
	}
	return out
}

func writeParked(f FS, root string, m map[string]bool) error {
	var l []string
	for k, v := range m {
		if v {
			l = append(l, k)
		}
	}
	sort.Strings(l)
	b, _ := json.Marshal(l)
	p := f.Join(root, dirMeta, "fijadas.json")
	if err := f.MkdirAll(f.Dir(p)); err != nil {
		return err
	}
	w, err := f.Create(p)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}

// listTree devuelve todos los archivos bajo dir (rutas relativas con "/").
func listTree(f FS, dir string) map[string]fs.FileInfo {
	out := map[string]fs.FileInfo{}
	var walk func(p, rel string)
	walk = func(p, rel string) {
		entries, err := f.ReadDir(p)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.Name() == "." || e.Name() == ".." {
				continue
			}
			r := e.Name()
			if rel != "" {
				r = rel + "/" + e.Name()
			}
			if e.IsDir() {
				walk(f.Join(p, e.Name()), r)
				continue
			}
			if strings.HasSuffix(e.Name(), ".rsgtmp") {
				continue
			}
			out[r] = e
		}
	}
	walk(dir, "")
	return out
}

type projFile struct {
	abs  string
	real string
	info fs.FileInfo
}

// scanProject arma la lista de archivos del proyecto (más los samples externos).
func (rc *RunCtx) scanProject(p Project, filter *Filter, readRoot func(string) string) (map[string]projFile, map[string]string, error) {
	entries, warns, err := ScanSources(rc.ctx, []string{p.Dir}, ScanOptions{Subdirs: true, Filter: filter, ReadRoot: readRoot,
		FollowReparse: !rc.settings.IgnoreReparsePoints}, rc.log)
	if err != nil {
		return nil, nil, err
	}
	for _, w := range warns {
		rc.fail("%s", w)
	}
	files := map[string]projFile{}
	for _, e := range entries {
		if e.Dir {
			continue
		}
		parts := strings.SplitN(e.Rel, "/", 2)
		if len(parts) < 2 {
			continue
		}
		files[parts[1]] = projFile{abs: e.Abs, real: e.Real, info: entryInfo(e)}
	}
	externals := map[string]string{}
	if rc.task.Projects.IncludeExternal {
		rep := AnalyzeProject(p, rc.settings.SampleSearchFolders)
		for _, x := range rep.External {
			if x.Library && !rc.task.Projects.IncludeFactory {
				continue
			}
			st, err := os.Stat(x.Path)
			if err != nil {
				continue
			}
			rel := dirExternos + "/" + relForAbsolute(x.Path)
			files[rel] = projFile{abs: x.Path, real: x.Path, info: st}
			externals[rel] = x.Path
		}
		if len(rep.Missing) > 0 {
			shown := rep.Missing
			if len(shown) > 5 {
				shown = shown[:5]
			}
			rc.log.Warn("%s: el proyecto usa %d archivos que no se encuentran (%s%s)", p.Name, len(rep.Missing), strings.Join(shown, ", "), map[bool]string{true: "…", false: ""}[len(rep.Missing) > 5])
		}
		if n := len(externals); n > 0 {
			rc.log.Info("%s: %d samples externos incluidos en el respaldo", p.Name, n)
		}
	}
	return files, externals, nil
}

type snapResult struct {
	TS      string
	Changed int
	Bytes   int64
}

// snapshotProject crea una instantánea de un proyecto en un destino.
// Devuelve la instantánea (TS vacío si no había cambios) y si quedó algo pendiente.
func (rc *RunCtx) snapshotProject(p Project, files map[string]projFile, externals map[string]string, di int, dest Location) (snapResult, bool, error) {
	fsys, base, err := openFS(dest, rc.settings)
	if err != nil {
		return snapResult{}, true, err
	}
	defer fsys.Close()
	base = expandTokens(base, &rc.task)
	root := projectRoot(fsys, base, p.Key)
	actual := fsys.Join(root, dirActual)
	if err := fsys.MkdirAll(actual); err != nil {
		return snapResult{}, true, fmt.Errorf("no se pudo crear %s: %w", actual, err)
	}
	have := listTree(fsys, actual)
	snaps := listSnapshots(fsys, root)
	// La referencia principal es la lista de la versión anterior (fechas del origen);
	// así funciona igual en destinos que no conservan la fecha de los archivos (algunos FTP).
	var prev *Manifest
	if len(snaps) > 0 {
		prev, _ = readManifest(fsys, fsys.Join(root, dirMeta, "instantaneas", snaps[len(snaps)-1]+".json.gz"))
	}
	var changed, removed []string
	var changedBytes int64
	for rel, pf := range files {
		size, mt := pf.info.Size(), pf.info.ModTime().UnixNano()
		h, inActual := have[rel]
		same := inActual && h.Size() == size
		if same {
			if prev != nil {
				pm, ok := prev.Files[rel]
				same = ok && pm.Size == size && mtimeClose(pm.MTime, mt)
			} else {
				same = mtimeClose(h.ModTime().UnixNano(), mt)
			}
		}
		if same {
			continue
		}
		changed = append(changed, rel)
		changedBytes += size
	}
	for rel := range have {
		if _, ok := files[rel]; !ok {
			removed = append(removed, rel)
		}
	}
	if len(changed) == 0 && len(removed) == 0 && len(snaps) > 0 {
		rc.log.Detail("%s: sin cambios en %s", p.Name, describeLocation(dest))
		return snapResult{}, false, nil
	}
	sort.Strings(changed)
	sort.Strings(removed)
	now := time.Now()
	ts := snapName(now)
	if len(snaps) > 0 && snaps[len(snaps)-1] >= ts {
		now = now.Add(time.Second)
		ts = snapName(now)
	}
	rc.prog.AddTotals(len(changed), changedBytes)
	verDir := fsys.Join(root, dirVersiones, ts)
	moveOld := func(rel string) bool {
		if _, ok := have[rel]; !ok {
			return false
		}
		src := fsys.Join(actual, rel)
		dst := fsys.Join(verDir, rel)
		if err := fsys.MkdirAll(fsys.Dir(dst)); err != nil {
			rc.fail("%s: no se pudo guardar la versión anterior de %s: %v", p.Name, rel, err)
			return false
		}
		if err := fsys.Rename(src, dst); err != nil {
			rc.fail("%s: no se pudo guardar la versión anterior de %s: %v", p.Name, rel, err)
			return false
		}
		return true
	}
	for _, rel := range removed {
		moveOld(rel)
	}
	pending := false
	failedRel := map[string]bool{}
	for _, rel := range changed {
		if rc.ctx.Err() != nil {
			return snapResult{}, true, rc.ctx.Err()
		}
		pf := files[rel]
		movedOld := moveOld(rel)
		rc.prog.SetCurrent(pf.real)
		err := rc.copier.CopyFile(fsys, pf.abs, fsys.Join(actual, rel), pf.info, rc.task.Projects.Verify)
		if errors.Is(err, errChangedDuringCopy) {
			rc.log.Warn("%s: %s cambió mientras se copiaba (¿grabando?); se vuelve a copiar en el próximo respaldo", p.Name, rel)
			pending = true
			err = nil
		}
		if err != nil {
			// se devuelve la versión anterior a "Actual" para que la copia siga completa
			if movedOld {
				_ = fsys.Remove(fsys.Join(actual, rel))
				_ = fsys.Rename(fsys.Join(verDir, rel), fsys.Join(actual, rel))
			}
			if errors.Is(err, context.Canceled) {
				return snapResult{}, true, err
			}
			rc.fail("%s: %s: %v", p.Name, rel, err)
			pending = true
			failedRel[rel] = true
		} else {
			rc.files++
			rc.bytes += pf.info.Size()
			if rc.settings.LogIndividualFiles {
				rc.log.Detail("%s: %s", p.Name, rel)
			}
		}
		rc.prog.FileDone()
	}
	removeEmptyDirs(fsys, verDir)
	// La lista de la versión refleja lo que realmente quedó guardado.
	m := &Manifest{Version: 1, Time: now, Key: p.Key, Name: p.Name, DAW: p.DAW, SourceDir: p.Dir, Main: p.Main, Reason: rc.reason,
		Files: map[string]MFile{}, Externals: externals, Changed: len(changed) - len(failedRel), Removed: len(removed), Bytes: changedBytes}
	for rel, pf := range files {
		if failedRel[rel] {
			if prev != nil {
				if old, ok := prev.Files[rel]; ok {
					m.Files[rel] = old
				}
			} else if h, ok := have[rel]; ok {
				m.Files[rel] = MFile{Size: h.Size(), MTime: h.ModTime().UnixNano()}
			}
			continue
		}
		m.Files[rel] = MFile{Size: pf.info.Size(), MTime: pf.info.ModTime().UnixNano()}
	}
	if err := writeManifest(fsys, fsys.Join(root, dirMeta, "instantaneas", ts+".json.gz"), m); err != nil {
		return snapResult{}, true, fmt.Errorf("no se pudo guardar la lista de la instantánea: %w", err)
	}
	info := map[string]any{"clave": p.Key, "nombre": p.Name, "daw": p.DAWName, "origen": p.Dir, "actualizado": now,
		"aviso": "Carpeta creada por Resguardo. 'Actual' es la copia al día del proyecto; 'Versiones anteriores' guarda lo que se fue reemplazando. Para volver a una versión usá Restaurar dentro de Resguardo."}
	if b, err := json.MarshalIndent(info, "", "  "); err == nil {
		if w, err := fsys.Create(fsys.Join(root, dirMeta, "proyecto.json")); err == nil {
			_, _ = w.Write(b)
			_ = w.Close()
		}
	}
	rc.log.Info("%s → %s: versión %s (%d archivos nuevos o cambiados, %d quitados, %s)", p.Name, describeLocation(dest), ts,
		len(changed)-len(failedRel), len(removed), humanBytes(changedBytes))
	rc.retainProject(fsys, root, append(snaps, ts), p.Key, di)
	return snapResult{TS: ts, Changed: len(changed) - len(failedRel), Bytes: changedBytes}, pending, nil
}

// retainProject aplica la política de versiones y limpia lo que ya no hace falta.
func (rc *RunCtx) retainProject(fsys FS, root string, snaps []string, key string, di int) {
	cfg := rc.task.Projects
	if len(snaps) <= 1 || (cfg.KeepVersions <= 0 && cfg.KeepDays <= 0 && !cfg.Thinning) {
		return
	}
	parked := readParked(fsys, root)
	keep := selectKeep(snaps, parked, cfg, time.Now())
	var drop []string
	for _, s := range snaps {
		if !keep[s] {
			drop = append(drop, s)
		}
	}
	if len(drop) == 0 {
		return
	}
	for _, s := range drop {
		_ = fsys.Remove(fsys.Join(root, dirMeta, "instantaneas", s+".json.gz"))
	}
	rc.log.Info("Se descartaron %d versiones viejas según la política de versiones", len(drop))
	gcVersions(fsys, root, rc.log)
	dropped := map[string]bool{}
	for _, s := range drop {
		dropped[s] = true
	}
	_ = rc.store.UpdateRecords(rc.task.ID, func(h []BackupRecord) []BackupRecord {
		out := h[:0]
		for _, r := range h {
			if r.Project == key && dropped[r.Snapshot] && len(r.Outputs) > 0 && r.Outputs[0].Dest == di {
				continue
			}
			out = append(out, r)
		}
		return out
	})
}

// selectKeep decide qué versiones conservar.
func selectKeep(snaps []string, parked map[string]bool, cfg ProjectsConfig, now time.Time) map[string]bool {
	keep := map[string]bool{}
	cands := map[string]bool{}
	for _, s := range snaps {
		cands[s] = true
	}
	if cfg.Thinning {
		// todas las de las últimas 24 h, una por día el último mes, una por semana después
		cands = map[string]bool{}
		dayBest := map[string]string{}
		weekBest := map[string]string{}
		for _, s := range snaps {
			t, ok := parseSnap(s)
			if !ok {
				cands[s] = true
				continue
			}
			age := now.Sub(t)
			switch {
			case age <= 24*time.Hour:
				cands[s] = true
			case age <= 30*24*time.Hour:
				k := t.Format("2006-01-02")
				if s > dayBest[k] {
					dayBest[k] = s
				}
			default:
				y, w := t.ISOWeek()
				k := fmt.Sprintf("%d-%02d", y, w)
				if s > weekBest[k] {
					weekBest[k] = s
				}
			}
		}
		for _, s := range dayBest {
			cands[s] = true
		}
		for _, s := range weekBest {
			cands[s] = true
		}
	}
	if cfg.KeepDays > 0 {
		limit := now.AddDate(0, 0, -cfg.KeepDays)
		for s := range cands {
			if t, ok := parseSnap(s); ok && t.Before(limit) {
				delete(cands, s)
			}
		}
	}
	var list []string
	for s := range cands {
		list = append(list, s)
	}
	sort.Strings(list)
	if cfg.KeepVersions > 0 && len(list) > cfg.KeepVersions {
		list = list[len(list)-cfg.KeepVersions:]
	}
	for _, s := range list {
		keep[s] = true
	}
	for s := range parked {
		keep[s] = true
	}
	if len(snaps) > 0 {
		keep[snaps[len(snaps)-1]] = true
	}
	return keep
}

// gcVersions borra de "Versiones anteriores" lo que ninguna versión conservada necesita.
// Regla: el archivo que una versión M necesita está en la primera carpeta de versiones
// posterior a M que lo contenga (y si no hay ninguna, en "Actual").
func gcVersions(fsys FS, root string, log *Logger) {
	snaps := listSnapshots(fsys, root)
	manifests := map[string]*Manifest{}
	for _, s := range snaps {
		if m, err := readManifest(fsys, fsys.Join(root, dirMeta, "instantaneas", s+".json.gz")); err == nil {
			manifests[s] = m
		}
	}
	verRoot := fsys.Join(root, dirVersiones)
	entries, err := fsys.ReadDir(verRoot)
	if err != nil {
		return
	}
	var vers []string
	for _, e := range entries {
		if e.IsDir() {
			vers = append(vers, e.Name())
		}
	}
	sort.Strings(vers)
	trees := map[string]map[string]fs.FileInfo{}
	for _, v := range vers {
		trees[v] = listTree(fsys, fsys.Join(verRoot, v))
	}
	needed := map[string]map[string]bool{}
	for ms, m := range manifests {
		for rel := range m.Files {
			for _, v := range vers {
				if v <= ms {
					continue
				}
				if _, ok := trees[v][rel]; ok {
					if needed[v] == nil {
						needed[v] = map[string]bool{}
					}
					needed[v][rel] = true
					break
				}
			}
		}
	}
	var freed int64
	for _, v := range vers {
		vdir := fsys.Join(verRoot, v)
		keepAny := false
		for rel, info := range trees[v] {
			if needed[v][rel] {
				keepAny = true
				continue
			}
			if err := fsys.Remove(fsys.Join(vdir, rel)); err == nil {
				freed += info.Size()
			} else {
				keepAny = true
			}
		}
		if !keepAny {
			_ = fsys.RemoveAll(vdir)
		} else {
			removeEmptyDirs(fsys, vdir)
		}
	}
	if freed > 0 && log != nil {
		log.Info("Se liberaron %s de versiones viejas", humanBytes(freed))
	}
}

func removeEmptyDirs(fsys FS, dir string) bool {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return false
	}
	empty := true
	for _, e := range entries {
		if e.IsDir() {
			if !removeEmptyDirs(fsys, fsys.Join(dir, e.Name())) {
				empty = false
			}
		} else {
			empty = false
		}
	}
	if empty {
		_ = fsys.Remove(dir)
	}
	return empty
}

// ProjectSnapshots lista las versiones guardadas de un proyecto en un destino.
func ProjectSnapshots(task Task, settings Settings, key string, di int) ([]SnapshotInfo, error) {
	dests := projectDestinations(task, key)
	if di < 0 || di >= len(dests) {
		return nil, fmt.Errorf("destino inválido")
	}
	fsys, base, err := openFS(dests[di], settings)
	if err != nil {
		return nil, err
	}
	defer fsys.Close()
	root := projectRoot(fsys, expandTokens(base, &task), key)
	parked := readParked(fsys, root)
	out := []SnapshotInfo{}
	for _, s := range listSnapshots(fsys, root) {
		m, err := readManifest(fsys, fsys.Join(root, dirMeta, "instantaneas", s+".json.gz"))
		if err != nil {
			continue
		}
		var total int64
		for _, f := range m.Files {
			total += f.Size
		}
		out = append(out, SnapshotInfo{Name: s, Time: m.Time, Files: len(m.Files), Changed: m.Changed, Removed: m.Removed,
			Bytes: m.Bytes, Total: total, Reason: m.Reason, Main: m.Main, Parked: parked[s]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

func SetSnapshotParked(task Task, settings Settings, key string, di int, snap string, parked bool) error {
	dests := projectDestinations(task, key)
	if di < 0 || di >= len(dests) {
		return fmt.Errorf("destino inválido")
	}
	fsys, base, err := openFS(dests[di], settings)
	if err != nil {
		return err
	}
	defer fsys.Close()
	root := projectRoot(fsys, expandTokens(base, &task), key)
	m := readParked(fsys, root)
	m[snap] = parked
	return writeParked(fsys, root, m)
}

func projectDestinations(task Task, key string) []Location {
	if o, ok := task.Projects.Overrides[key]; ok && len(o.Destinations) > 0 {
		return o.Destinations
	}
	return task.Destinations
}

type RestoreOptions struct {
	Target            string `json:"target"`
	OnlyProjectFiles  bool   `json:"onlyProjectFiles"`
	ExternalsToOrigin bool   `json:"externalsToOrigin"`
}

// RestoreSnapshot reconstruye una versión de un proyecto en una carpeta (nunca pisa el original).
func RestoreSnapshot(ctx context.Context, task Task, settings Settings, key string, di int, snap string, opt RestoreOptions, prog *Progress, log *Logger) (string, int, error) {
	dests := projectDestinations(task, key)
	if di < 0 || di >= len(dests) {
		return "", 0, fmt.Errorf("destino inválido")
	}
	fsys, base, err := openFS(dests[di], settings)
	if err != nil {
		return "", 0, err
	}
	defer fsys.Close()
	root := projectRoot(fsys, expandTokens(base, &task), key)
	m, err := readManifest(fsys, fsys.Join(root, dirMeta, "instantaneas", snap+".json.gz"))
	if err != nil {
		return "", 0, fmt.Errorf("no se encontró la versión %s: %w", snap, err)
	}
	// Índice de dónde está cada archivo: primero "Actual", después las versiones posteriores.
	actualTree := listTree(fsys, fsys.Join(root, dirActual))
	verEntries, _ := fsys.ReadDir(fsys.Join(root, dirVersiones))
	var vers []string
	for _, e := range verEntries {
		if e.IsDir() {
			vers = append(vers, e.Name())
		}
	}
	sort.Strings(vers)
	verTrees := map[string]map[string]fs.FileInfo{}
	tree := func(v string) map[string]fs.FileInfo {
		t, ok := verTrees[v]
		if !ok {
			t = listTree(fsys, fsys.Join(root, dirVersiones, v))
			verTrees[v] = t
		}
		return t
	}
	locate := func(rel string, mf MFile) string {
		// 1) regla de estructura: primera carpeta de versiones posterior a esta versión que lo tenga
		for _, v := range vers {
			if v <= snap {
				continue
			}
			if info, ok := tree(v)[rel]; ok {
				if info.Size() == mf.Size {
					return fsys.Join(root, dirVersiones, v, rel)
				}
				break
			}
		}
		if info, ok := actualTree[rel]; ok && info.Size() == mf.Size {
			return fsys.Join(root, dirActual, rel)
		}
		// 2) por fecha y tamaño en cualquier lugar
		for _, v := range vers {
			if info, ok := tree(v)[rel]; ok && mf.matches(info) {
				return fsys.Join(root, dirVersiones, v, rel)
			}
		}
		// 3) por tamaño
		for _, v := range vers {
			if info, ok := tree(v)[rel]; ok && info.Size() == mf.Size {
				return fsys.Join(root, dirVersiones, v, rel)
			}
		}
		return ""
	}

	rels := make([]string, 0, len(m.Files))
	for rel := range m.Files {
		if opt.OnlyProjectFiles {
			if strings.Contains(rel, "/") || dawForFile(rel, []string{m.DAW}) == nil {
				continue
			}
		}
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	if len(rels) == 0 {
		return "", 0, fmt.Errorf("la versión no tiene archivos para restaurar")
	}
	target := opt.Target
	if opt.OnlyProjectFiles && target == "" {
		target = m.SourceDir
	}
	if target == "" {
		home, _ := os.UserHomeDir()
		target = filepath.Join(home, "Desktop", "Resguardo - restaurados", sanitizeName(m.Name)+" ("+snap+")")
	}
	if !opt.OnlyProjectFiles {
		if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
			target = filepath.Join(target, sanitizeName(m.Name)+" ("+snap+")")
		}
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", 0, err
	}
	var total int64
	for _, rel := range rels {
		total += m.Files[rel].Size
	}
	if prog != nil {
		prog.SetTotals(len(rels), total)
	}
	cp := &Copier{ctx: ctx, prog: prog, settings: settings, log: log}
	count := 0
	var missing []string
	for _, rel := range rels {
		if ctx.Err() != nil {
			return target, count, ctx.Err()
		}
		mf := m.Files[rel]
		src := locate(rel, mf)
		if src == "" {
			missing = append(missing, rel)
			continue
		}
		out := filepath.Join(target, filepath.FromSlash(rel))
		if opt.OnlyProjectFiles {
			ext := filepath.Ext(rel)
			out = filepath.Join(target, strings.TrimSuffix(rel, ext)+" (resguardo "+snap+")"+ext)
		}
		if prog != nil {
			prog.SetCurrent(rel)
		}
		if err := downloadTo(cp, fsys, src, out, time.Unix(0, mf.MTime)); err != nil {
			log.Error("No se pudo restaurar %s: %v", rel, err)
			continue
		}
		count++
		if prog != nil {
			prog.FileDone()
		}
		if opt.ExternalsToOrigin {
			if orig, ok := m.Externals[rel]; ok {
				if _, err := os.Stat(orig); errors.Is(err, os.ErrNotExist) {
					if err := copyLocalFile(out, orig, mustStat(out)); err == nil {
						log.Info("Sample externo devuelto a su lugar: %s", orig)
					}
				}
			}
		}
	}
	if !opt.OnlyProjectFiles {
		var b strings.Builder
		fmt.Fprintf(&b, "Proyecto restaurado por Resguardo\r\n\r\nProyecto: %s\r\nVersión: %s\r\nCarpeta original: %s\r\nArchivos: %d\r\n", m.Name, snap, m.SourceDir, count)
		if len(m.Externals) > 0 {
			b.WriteString("\r\nSamples que estaban fuera de la carpeta del proyecto (quedaron en \"_Samples externos\"):\r\n")
			var ks []string
			for k := range m.Externals {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			for _, k := range ks {
				fmt.Fprintf(&b, "  %s  ←  %s\r\n", k, m.Externals[k])
			}
			b.WriteString("\r\nSi el DAW no los encuentra al abrir, indicale que busque dentro de esta carpeta.\r\n")
		}
		if len(missing) > 0 {
			b.WriteString("\r\nNo se encontraron en el respaldo:\r\n")
			for _, r := range missing {
				fmt.Fprintf(&b, "  %s\r\n", r)
			}
		}
		_ = os.WriteFile(filepath.Join(target, "LEEME - Resguardo.txt"), []byte(b.String()), 0o644)
	}
	if len(missing) > 0 {
		log.Warn("Restauración: %d archivos no se encontraron en el respaldo", len(missing))
	}
	return target, count, nil
}

func mustStat(p string) fs.FileInfo {
	st, _ := os.Stat(p)
	return st
}

func downloadTo(cp *Copier, fsys FS, src, out string, mtime time.Time) error {
	r, err := fsys.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	w, err := os.Create(out + ".rsgtmp")
	if err != nil {
		return err
	}
	if cp != nil && cp.prog != nil {
		_, err = cp.Stream(w, r, nil)
	} else {
		_, err = io.Copy(w, r)
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out + ".rsgtmp")
		return err
	}
	if err := os.Rename(out+".rsgtmp", out); err != nil {
		return err
	}
	return os.Chtimes(out, mtime, mtime)
}

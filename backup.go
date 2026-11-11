package main

import (
	"context"
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

// RunCtx agrupa todo lo que necesita una ejecución de tarea.
type RunCtx struct {
	ctx      context.Context
	store    *Store
	log      *Logger
	prog     *Progress
	copier   *Copier
	task     Task
	settings Settings
	reason   string
	force    bool
	project  string // tareas de proyectos: sólo este proyecto ("" = todos)
	errors   int
	files    int
	bytes    int64
}

func (rc *RunCtx) fail(format string, a ...any) {
	rc.errors++
	rc.log.Error(format, a...)
}

func typeLabel(t string) string {
	switch t {
	case TypeFull:
		return "Completo"
	case TypeIncr:
		return "Incremental"
	case TypeDiff:
		return "Diferencial"
	case TypeDummy:
		return "Ficticio"
	case TypeProjects:
		return "Proyectos"
	}
	return t
}

func stampOf(e Entry) FileStamp { return FileStamp{Size: e.Size, MTime: e.MTime.UnixNano()} }

func lastFullRecord(h []BackupRecord) *BackupRecord {
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Type == TypeFull && (h[i].Status == "ok" || h[i].Status == "errores") {
			return &h[i]
		}
	}
	return nil
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// effectiveType decide si esta corrida es completa, incremental o diferencial.
func (rc *RunCtx) effectiveType(st *TaskState, h []BackupRecord) (string, string) {
	t := rc.task.Type
	if t != TypeIncr && t != TypeDiff {
		return t, ""
	}
	now := time.Now()
	switch {
	case rc.force:
		return TypeFull, "se forzó un respaldo completo"
	case lastFullRecord(h) == nil && (rc.settings.FirstBackupFull || len(st.Full) == 0):
		return TypeFull, "es el primer respaldo de la tarea"
	case rc.task.FullEvery > 0 && st.SinceFull >= rc.task.FullEvery-1:
		return TypeFull, fmt.Sprintf("toca el completo (uno cada %d)", rc.task.FullEvery)
	case rc.task.FixedFullWeekday >= 0 && int(now.Weekday()) == rc.task.FixedFullWeekday && !sameDay(st.LastFull, now):
		return TypeFull, "es el día fijo del respaldo completo"
	}
	return t, ""
}

// runClassic ejecuta una tarea al estilo Cobian (completo / incremental / diferencial).
func runClassic(rc *RunCtx) (*BackupRecord, error) {
	task := rc.task
	st := rc.store.State(task.ID)
	hist := rc.store.History(task.ID)
	typ, why := rc.effectiveType(st, hist)
	start := time.Now()
	rec := &BackupRecord{ID: newID(), TaskID: task.ID, Type: typ, Started: start, Reason: rc.reason,
		Compression: task.Compression, Format: task.Format, Encrypted: task.Encrypt && task.Password != ""}
	if why != "" {
		rc.log.Info("Respaldo %s (%s)", strings.ToLower(typeLabel(typ)), why)
	} else {
		rc.log.Info("Respaldo %s", strings.ToLower(typeLabel(typ)))
	}

	filter, err := NewFilter(&task)
	if err != nil {
		return nil, err
	}

	// Orígenes: los remotos se bajan primero a una carpeta temporal.
	tmpRoot := tempDir(rc.settings)
	var cleanup []string
	defer func() {
		for _, d := range cleanup {
			_ = os.RemoveAll(d)
		}
	}()
	var srcPaths []string
	for _, s := range task.Sources {
		if s.Kind == "ftp" || s.Kind == "sftp" {
			rc.prog.Set("Bajando origen remoto " + describeLocation(s))
			local, err := downloadRemote(rc, s, tmpRoot, filter)
			if err != nil {
				rc.fail("No se pudo bajar el origen %s: %v", describeLocation(s), err)
				continue
			}
			cleanup = append(cleanup, filepath.Dir(local))
			srcPaths = append(srcPaths, local)
			continue
		}
		p := strings.TrimSpace(s.Path)
		p = strings.TrimPrefix(strings.TrimPrefix(p, "?D?"), "?F?")
		srcPaths = append(srcPaths, expandTokens(p, &task))
	}

	// Instantáneas de volumen (VSS) para copiar archivos abiertos.
	var readRoot func(string) string
	if task.UseVSS && isWindows() {
		rc.prog.Set("Creando instantánea de volumen (VSS)")
		vs, err := newVSSSession(rc.ctx, srcPaths, rc.log)
		if err != nil {
			if rc.settings.CancelIfVSSFails {
				return nil, fmt.Errorf("no se pudo crear la instantánea de volumen: %w", err)
			}
			rc.log.Warn("No se pudo usar la instantánea de volumen (%v). Se copia sin VSS; ¿el programa se ejecuta como administrador?", err)
		} else {
			defer vs.Close()
			readRoot = vs.Map
		}
	}

	rc.prog.Set("Buscando archivos")
	entries, warns, err := ScanSources(rc.ctx, srcPaths, ScanOptions{
		Subdirs: task.IncludeSubdirs, IgnoreEmptyDirs: task.IgnoreEmptyDirs && !(typ == TypeFull && rc.settings.NeverIgnoreEmptyFull),
		AbsolutePaths: task.AbsolutePaths, FollowReparse: !rc.settings.IgnoreReparsePoints, Filter: filter, ReadRoot: readRoot,
		OnProgress: func(n int, b int64) { rc.prog.Set(fmt.Sprintf("Buscando archivos (%d)", n)) },
	}, rc.log)
	if err != nil {
		return nil, err
	}
	for _, w := range warns {
		rc.fail("%s", w)
	}

	useAttr := task.UseArchiveAttr && isWindows()
	var selected []Entry
	allFiles := map[string]Entry{}
	for _, e := range entries {
		if e.Dir {
			if typ == TypeFull {
				selected = append(selected, e)
			}
			continue
		}
		allFiles[e.Rel] = e
		changed := true
		switch typ {
		case TypeIncr:
			if useAttr {
				changed = hasArchiveAttr(e.Real)
			} else {
				changed = st.Last[e.Real] != stampOf(e)
			}
		case TypeDiff:
			if useAttr {
				changed = hasArchiveAttr(e.Real)
			} else {
				changed = st.Full[e.Real] != stampOf(e)
			}
		}
		if changed {
			selected = append(selected, e)
		}
	}
	var nfiles int
	var nbytes int64
	for _, e := range selected {
		if !e.Dir {
			nfiles++
			nbytes += e.Size
		}
	}
	rc.log.Info("%d archivos para respaldar (%s)", nfiles, humanBytes(nbytes))
	if nfiles == 0 && typ != TypeFull && !rc.settings.SaveEmptyBackups {
		rc.log.Info("No hay cambios desde el último respaldo: no se crea un respaldo vacío")
		st.LastRun = time.Now()
		_ = rc.store.SaveState(task.ID)
		return nil, nil
	}
	rc.prog.SetTotals(nfiles*max(1, len(task.Destinations)), nbytes*int64(max(1, len(task.Destinations))))

	failed := map[string]bool{}
	tsName := formatTS(start, rc.settings.TimestampFormat)
	for di, dest := range task.Destinations {
		if rc.ctx.Err() != nil {
			break
		}
		out, err := rc.toDestination(di, dest, typ, tsName, selected, allFiles, failed)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				break
			}
			rc.fail("Destino %s: %v", describeLocation(dest), err)
			for _, e := range selected {
				failed[e.Rel] = true
			}
			continue
		}
		rec.Outputs = append(rec.Outputs, out...)
	}

	// Actualizar el estado sólo con lo que se copió bien a todos los destinos.
	if rc.ctx.Err() == nil || len(rec.Outputs) > 0 {
		for _, e := range selected {
			if e.Dir || failed[e.Rel] {
				continue
			}
			rc.files++
			rc.bytes += e.Size
			s := stampOf(e)
			st.Last[e.Real] = s
			if typ == TypeFull {
				st.Full[e.Real] = s
			}
			if useAttr && task.ClearArchiveAttr && typ != TypeDiff {
				clearArchiveAttr(e.Real)
			}
		}
		if typ == TypeFull && rc.ctx.Err() == nil {
			// en un completo, el estado completo es exactamente lo que hay ahora
			nf := map[string]FileStamp{}
			for _, e := range allFiles {
				if !failed[e.Rel] {
					nf[e.Real] = stampOf(e)
				}
			}
			st.Full = nf
			st.Last = copyStamps(nf)
		}
	}
	now := time.Now()
	st.LastRun = now
	if typ == TypeFull && rc.ctx.Err() == nil {
		st.LastFull = now
		st.SinceFull = 0
	} else if typ != TypeFull {
		st.SinceFull++
	}
	_ = rc.store.SaveState(task.ID)

	rec.Finished = time.Now()
	rec.Files, rec.Bytes, rec.Errors = rc.files, rc.bytes, rc.errors
	switch {
	case rc.ctx.Err() != nil:
		rec.Status = "cancelado"
	case rc.errors > 0:
		rec.Status = "errores"
	default:
		rec.Status = "ok"
	}
	if typ != TypeFull {
		if lf := lastFullRecord(hist); lf != nil {
			rec.Parent = lf.ID
		}
	}
	if len(hist) == 0 && rc.settings.ParkFirstBackup {
		rec.Parked = true
	}
	if len(rec.Outputs) > 0 {
		_ = rc.store.AddRecord(*rec)
	}
	if task.Separated && rc.ctx.Err() == nil {
		rc.applyRetention()
	}
	return rec, nil
}

func copyStamps(m map[string]FileStamp) map[string]FileStamp {
	o := make(map[string]FileStamp, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

// toDestination escribe la selección en un destino según el modo de compresión.
func (rc *RunCtx) toDestination(di int, dest Location, typ, tsName string, selected []Entry, all map[string]Entry, failed map[string]bool) ([]Output, error) {
	task := rc.task
	fsys, base, err := openFS(dest, rc.settings)
	if err != nil {
		return nil, err
	}
	defer fsys.Close()
	base = expandTokens(base, &task)
	if base == "" {
		return nil, fmt.Errorf("el destino no tiene ruta")
	}
	rc.log.Info("Destino: %s", describeLocation(Location{Kind: dest.Kind, Host: dest.Host, User: dest.User, Path: base}))
	name := sanitizeName(task.Name)
	if task.Separated {
		name += " " + tsName
		if task.TypeInName {
			name += " (" + typeLabel(typ) + ")"
		}
	}
	parent := base
	if task.TopParentDir {
		parent = fsys.Join(base, sanitizeName(task.Name))
	}
	if err := fsys.MkdirAll(parent); err != nil {
		return nil, fmt.Errorf("no se pudo crear la carpeta de destino: %w", err)
	}
	ext := ".zip"
	if task.Format == "7z" {
		ext = ".7z"
	}
	spec := ArchiveSpec{Format: task.Format, Level: task.Level, Comment: task.Comment,
		Encryption: rc.settings.ZipEncryption, StoredExt: parseStoredExt(rc.settings.StoredExtensions),
		SplitBytes: int64(task.SplitMB) * 1024 * 1024, AbsPaths: task.AbsolutePaths}
	if task.Encrypt {
		spec.Password = task.Password
	}
	markFail := func(e Entry, err error) {
		if err == nil {
			return
		}
		failed[e.Rel] = true
		if errors.Is(err, context.Canceled) {
			return
		}
		rc.fail("%s: %v", e.Real, err)
	}

	switch task.Compression {
	case "", "ninguna":
		root := fsys.Join(parent, name)
		if err := fsys.MkdirAll(root); err != nil {
			return nil, err
		}
		for _, e := range selected {
			if rc.ctx.Err() != nil {
				return nil, rc.ctx.Err()
			}
			target := fsys.Join(root, e.Rel)
			if e.Dir {
				_ = fsys.MkdirAll(target)
				continue
			}
			rc.prog.SetCurrent(e.Real)
			info := entryInfo(e)
			if task.Encrypt && task.Password != "" {
				markFail(e, rc.copyEncrypted(fsys, e.Abs, target+".rsg", info))
			} else {
				if !task.Separated {
					if dst, err := fsys.Stat(target); err == nil && dst.Size() == e.Size && dst.ModTime().Unix() == e.MTime.Unix() {
						rc.prog.addBytes(e.Size)
						rc.prog.FileDone()
						continue
					}
				}
				err := rc.copier.CopyFile(fsys, e.Abs, target, info, false)
				if errors.Is(err, errChangedDuringCopy) {
					rc.log.Warn("%s cambió durante la copia; se copió igual (se volverá a copiar la próxima vez)", e.Real)
					failed[e.Rel] = true
					err = nil
				}
				markFail(e, err)
			}
			if rc.settings.LogIndividualFiles {
				rc.log.Detail("Copiado %s", e.Rel)
			}
			rc.prog.FileDone()
		}
		if task.Mirror && !task.Separated && rc.ctx.Err() == nil {
			rc.mirrorClean(fsys, root, all)
		}
		return []Output{{Dest: di, Path: root, Kind: "carpeta"}}, nil

	case "individual":
		root := fsys.Join(parent, name)
		for _, e := range selected {
			if rc.ctx.Err() != nil {
				return nil, rc.ctx.Err()
			}
			if e.Dir {
				continue
			}
			target := fsys.Join(root, e.Rel) + ext
			info := entryInfo(e)
			var err error
			if task.Format == "7z" {
				dir := filepath.Dir(e.Abs)
				_, err = rc.copier.Write7z(fsys, target, map[string][]string{dir: {filepath.Base(e.Abs)}}, e.Size, spec, tempDir(rc.settings))
				rc.prog.FileDone()
			} else {
				_, err = rc.copier.WriteZip(fsys, target, []ArchiveItem{{Abs: e.Abs, Name: filepath.Base(e.Rel), Info: info}}, spec, nil)
			}
			markFail(e, err)
		}
		return []Output{{Dest: di, Path: root, Kind: "individual"}}, nil

	case "separado", "global":
		groups := map[int][]Entry{}
		for _, e := range selected {
			k := 0
			if task.Compression == "separado" {
				k = e.Src
			}
			groups[k] = append(groups[k], e)
		}
		keys := make([]int, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		var outs []Output
		for _, k := range keys {
			list := groups[k]
			archName := name
			if task.Compression == "separado" && len(list) > 0 {
				top := strings.SplitN(list[0].Rel, "/", 2)[0]
				archName = name + " - " + sanitizeName(top)
			}
			target := fsys.Join(parent, archName+ext)
			var created []string
			var err error
			if task.Format == "7z" {
				byDir := map[string][]string{}
				var total int64
				for _, e := range list {
					if e.Dir {
						continue
					}
					total += e.Size
					if task.AbsolutePaths {
						byDir[""] = append(byDir[""], e.Abs)
						continue
					}
					// carpeta de trabajo = carpeta padre del origen, así el nombre interno es e.Rel
					srcRoot := strings.SplitN(e.Rel, "/", 2)[0]
					rootAbs := e.Abs
					for i := strings.Count(e.Rel, "/"); i > 0; i-- {
						rootAbs = filepath.Dir(rootAbs)
					}
					parentDir := filepath.Dir(rootAbs)
					if filepath.Base(rootAbs) != srcRoot {
						// nombre renombrado (duplicado): se usa la carpeta del origen
						parentDir = rootAbs
						byDir[parentDir] = append(byDir[parentDir], filepath.FromSlash(strings.SplitN(e.Rel, "/", 2)[len(strings.SplitN(e.Rel, "/", 2))-1]))
						continue
					}
					byDir[parentDir] = append(byDir[parentDir], filepath.FromSlash(e.Rel))
				}
				created, err = rc.copier.Write7z(fsys, target, byDir, total, spec, tempDir(rc.settings))
				for _, e := range list {
					if !e.Dir {
						rc.prog.FileDone()
						if err != nil {
							failed[e.Rel] = true
						}
					}
				}
			} else {
				items := make([]ArchiveItem, 0, len(list))
				for _, e := range list {
					items = append(items, ArchiveItem{Abs: e.Abs, Name: e.Rel, Info: entryInfo(e), Dir: e.Dir})
				}
				created, err = rc.copier.WriteZip(fsys, target, items, spec, func(it ArchiveItem, ferr error) {
					if ferr != nil {
						e := all[it.Name]
						e.Real = it.Abs
						e.Rel = it.Name
						markFail(e, ferr)
					}
				})
			}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return outs, err
				}
				rc.fail("No se pudo crear %s: %v", target, err)
				for _, e := range list {
					failed[e.Rel] = true
				}
				continue
			}
			if rc.settings.TestArchives && fsys.Kind() == "local" && len(created) > 0 {
				rc.prog.Set("Probando el archivo creado")
				var terr error
				if task.Format == "7z" {
					terr = Test7z(rc.ctx, created[0], spec.Password)
				} else {
					terr = TestZip(created[0], spec.Password)
				}
				if terr != nil {
					rc.fail("La prueba de %s falló: %v", created[0], terr)
				} else {
					rc.log.Info("Archivo verificado: %s", filepath.Base(created[0]))
				}
			}
			kind := "zip"
			if task.Format == "7z" {
				kind = "7z"
			}
			outs = append(outs, Output{Dest: di, Path: created[0], Kind: kind})
		}
		return outs, nil
	}
	return nil, fmt.Errorf("modo de compresión desconocido: %s", task.Compression)
}

type entryFileInfo struct{ e Entry }

func (i entryFileInfo) Name() string       { return filepath.Base(i.e.Real) }
func (i entryFileInfo) Size() int64        { return i.e.Size }
func (i entryFileInfo) Mode() fs.FileMode  { return i.e.Mode }
func (i entryFileInfo) ModTime() time.Time { return i.e.MTime }
func (i entryFileInfo) IsDir() bool        { return i.e.Dir }
func (i entryFileInfo) Sys() any           { return nil }

func entryInfo(e Entry) fs.FileInfo { return entryFileInfo{e} }

func (rc *RunCtx) copyEncrypted(fsys FS, src, target string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := fsys.MkdirAll(fsys.Dir(target)); err != nil {
		return err
	}
	tmp := target + ".rsgtmp"
	w, err := fsys.Create(tmp)
	if err != nil {
		return err
	}
	ew, err := newRSGWriter(w, rc.task.Password)
	if err != nil {
		w.Close()
		return err
	}
	_, cerr := rc.copier.Stream(ew, in, nil)
	if cerr == nil {
		cerr = ew.Close()
	}
	if err := w.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = fsys.Remove(tmp)
		return cerr
	}
	if fsys.Kind() == "ftp" {
		_ = fsys.Remove(target)
	}
	if err := fsys.Rename(tmp, target); err != nil {
		_ = fsys.Remove(target)
		if err := fsys.Rename(tmp, target); err != nil {
			return err
		}
	}
	_ = fsys.Chtimes(target, info.ModTime())
	return nil
}

// mirrorClean borra (o aparta) del destino lo que ya no existe en el origen.
func (rc *RunCtx) mirrorClean(fsys FS, root string, all map[string]Entry) {
	rc.prog.Set("Espejo: limpiando archivos borrados en el origen")
	keepDirs := map[string]bool{}
	for rel := range all {
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts); i++ {
			keepDirs[strings.Join(parts[:i], "/")] = true
		}
	}
	safeRoot := ""
	if rc.settings.SafeMirror {
		if rc.settings.SafeMirrorDir != "" && fsys.Kind() == "local" {
			safeRoot = filepath.Join(rc.settings.SafeMirrorDir, sanitizeName(rc.task.Name), snapName(time.Now()))
		} else {
			safeRoot = fsys.Join(fsys.Dir(root), "_Espejo seguro ("+sanitizeName(rc.task.Name)+")", snapName(time.Now()))
		}
	}
	removed := 0
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			r := e.Name()
			if rel != "" {
				r = rel + "/" + e.Name()
			}
			p := fsys.Join(dir, e.Name())
			if e.IsDir() {
				if keepDirs[r] {
					walk(p, r)
					continue
				}
				if _, isFile := all[r]; isFile {
					continue
				}
			} else {
				name := e.Name()
				if strings.HasSuffix(name, ".rsg") {
					if _, ok := all[strings.TrimSuffix(r, ".rsg")]; ok {
						continue
					}
				}
				if _, ok := all[r]; ok {
					continue
				}
			}
			removed++
			if safeRoot != "" {
				target := fsys.Join(safeRoot, r)
				_ = fsys.MkdirAll(fsys.Dir(target))
				if err := fsys.Rename(p, target); err != nil {
					rc.log.Warn("Espejo: no se pudo apartar %s: %v", p, err)
				} else {
					rc.log.Detail("Espejo: apartado %s", r)
				}
			} else {
				if err := fsys.RemoveAll(p); err != nil {
					rc.log.Warn("Espejo: no se pudo borrar %s: %v", p, err)
				} else {
					rc.log.Detail("Espejo: borrado %s", r)
				}
			}
		}
	}
	walk(root, "")
	if removed > 0 {
		if safeRoot != "" {
			rc.log.Info("Espejo: %d elementos borrados en el origen se apartaron en %s", removed, safeRoot)
		} else {
			rc.log.Info("Espejo: se borraron %d elementos que ya no estaban en el origen", removed)
		}
	}
}

// applyRetention borra los respaldos más viejos según "copias completas a conservar".
func (rc *RunCtx) applyRetention() {
	task := rc.task
	h := rc.store.History(task.ID)
	var toDelete []BackupRecord
	if task.FullCopiesToKeep > 0 {
		var fulls []BackupRecord
		for _, r := range h {
			if r.Type == TypeFull && r.Project == "" {
				fulls = append(fulls, r)
			}
		}
		if len(fulls) > task.FullCopiesToKeep {
			old := fulls[:len(fulls)-task.FullCopiesToKeep]
			for _, f := range old {
				if f.Parked {
					continue
				}
				toDelete = append(toDelete, f)
				for _, r := range h {
					if r.Parent == f.ID && !r.Parked {
						toDelete = append(toDelete, r)
					}
				}
			}
		}
	}
	if task.DiffCopiesToKeep > 0 {
		var diffs []BackupRecord
		for _, r := range h {
			if r.Type == TypeDiff {
				diffs = append(diffs, r)
			}
		}
		if len(diffs) > task.DiffCopiesToKeep {
			for _, d := range diffs[:len(diffs)-task.DiffCopiesToKeep] {
				if !d.Parked {
					toDelete = append(toDelete, d)
				}
			}
		}
	}
	if len(toDelete) == 0 {
		return
	}
	ids := map[string]bool{}
	for _, r := range toDelete {
		if ids[r.ID] {
			continue
		}
		ids[r.ID] = true
		rc.log.Info("Se borra el respaldo viejo del %s (%s)", r.Started.Format("02/01/2006 15:04"), typeLabel(r.Type))
		deleteRecordFiles(rc.task, r, rc.settings, rc.log)
	}
	_ = rc.store.UpdateRecords(task.ID, func(h []BackupRecord) []BackupRecord {
		out := h[:0]
		for _, r := range h {
			if !ids[r.ID] {
				out = append(out, r)
			}
		}
		return out
	})
}

func deleteRecordFiles(task Task, r BackupRecord, settings Settings, log *Logger) {
	for _, o := range r.Outputs {
		if o.Dest >= len(task.Destinations) {
			continue
		}
		fsys, _, err := openFS(task.Destinations[o.Dest], settings)
		if err != nil {
			log.Warn("No se pudo abrir el destino para borrar %s: %v", o.Path, err)
			continue
		}
		paths := []string{o.Path}
		if strings.HasSuffix(o.Path, ".001") {
			base := strings.TrimSuffix(o.Path, ".001")
			for i := 2; i < 1000; i++ {
				p := fmt.Sprintf("%s.%03d", base, i)
				if !existsFS(fsys, p) {
					break
				}
				paths = append(paths, p)
			}
		}
		for _, p := range paths {
			if err := fsys.RemoveAll(p); err != nil {
				log.Warn("No se pudo borrar %s: %v", p, err)
			}
		}
		fsys.Close()
	}
}

// downloadRemote baja un origen FTP/SFTP completo a una carpeta temporal.
func downloadRemote(rc *RunCtx, loc Location, tmpRoot string, filter *Filter) (string, error) {
	fsys, base, err := openFS(loc, rc.settings)
	if err != nil {
		return "", err
	}
	defer fsys.Close()
	work, err := os.MkdirTemp(tmpRoot, "origen-")
	if err != nil {
		return "", err
	}
	name := sanitizeName(filepath.Base(strings.TrimRight(strings.ReplaceAll(base, "\\", "/"), "/")))
	if name == "_" || name == "" {
		name = sanitizeName(loc.Host)
	}
	local := filepath.Join(work, name)
	st, err := fsys.Stat(base)
	if err != nil {
		return "", err
	}
	get := func(rp, lp string, info fs.FileInfo) error {
		r, err := fsys.Open(rp)
		if err != nil {
			return err
		}
		defer r.Close()
		if err := os.MkdirAll(filepath.Dir(lp), 0o755); err != nil {
			return err
		}
		w, err := os.Create(lp)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		w.Close()
		if err == nil && !info.ModTime().IsZero() {
			_ = os.Chtimes(lp, info.ModTime(), info.ModTime())
		}
		return err
	}
	if !st.IsDir() {
		return local, get(base, local, st)
	}
	var walk func(rp, lp string) error
	walk = func(rp, lp string) error {
		if rc.ctx.Err() != nil {
			return rc.ctx.Err()
		}
		if err := os.MkdirAll(lp, 0o755); err != nil {
			return err
		}
		entries, err := fsys.ReadDir(rp)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if filter != nil && !filter.Allowed(e.Name(), e, "") {
				continue
			}
			if e.IsDir() {
				if rc.task.IncludeSubdirs {
					if err := walk(fsys.Join(rp, e.Name()), filepath.Join(lp, e.Name())); err != nil {
						return err
					}
				}
				continue
			}
			rc.prog.SetCurrent(fsys.Join(rp, e.Name()))
			if err := get(fsys.Join(rp, e.Name()), filepath.Join(lp, e.Name()), e); err != nil {
				rc.fail("No se pudo bajar %s: %v", fsys.Join(rp, e.Name()), err)
			}
		}
		return nil
	}
	return local, walk(base, local)
}

func tempDir(s Settings) string {
	if s.TempDir != "" {
		if err := os.MkdirAll(s.TempDir, 0o755); err == nil {
			return s.TempDir
		}
	}
	d := filepath.Join(dataDir(), "tmp")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func humanBytes(b int64) string {
	const u = 1024
	if b < u {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(u), 0
	for n := b / u; n >= u; n /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

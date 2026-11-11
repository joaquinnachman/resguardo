package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RestoreRecord restaura un respaldo clásico (completo + incrementales, o completo + diferencial) en una carpeta.
func RestoreRecord(ctx context.Context, store *Store, task Task, recID string, dest int, target, password string, prog *Progress, log *Logger) (int, error) {
	h := store.History(task.ID)
	sort.Slice(h, func(i, j int) bool { return h[i].Started.Before(h[j].Started) })
	var rec *BackupRecord
	for i := range h {
		if h[i].ID == recID {
			rec = &h[i]
		}
	}
	if rec == nil {
		return 0, fmt.Errorf("no se encontró el respaldo")
	}
	var chain []BackupRecord
	switch rec.Type {
	case TypeFull:
		chain = []BackupRecord{*rec}
	case TypeDiff, TypeIncr:
		var parent *BackupRecord
		for i := range h {
			if h[i].ID == rec.Parent {
				parent = &h[i]
			}
		}
		if parent == nil {
			log.Warn("No se encontró el respaldo completo base; se restaura sólo este respaldo")
		} else {
			chain = append(chain, *parent)
		}
		if rec.Type == TypeIncr && parent != nil {
			for _, r := range h {
				if r.Type == TypeIncr && r.Parent == parent.ID && r.Started.Before(rec.Started) {
					chain = append(chain, r)
				}
			}
		}
		chain = append(chain, *rec)
	default:
		return 0, fmt.Errorf("este registro no se puede restaurar desde acá")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return 0, err
	}
	if password == "" {
		password = task.Password
	}
	settings := store.GetSettings()
	cp := &Copier{ctx: ctx, prog: prog, settings: settings, log: log}
	total := 0
	for _, r := range chain {
		for _, o := range r.Outputs {
			if o.Dest != dest || o.Dest >= len(task.Destinations) {
				continue
			}
			prog.Set(fmt.Sprintf("Restaurando el respaldo del %s", r.Started.Format("02/01/2006 15:04")))
			n, err := restoreOutput(ctx, cp, task.Destinations[o.Dest], settings, o, target, password, r.Encrypted, log)
			total += n
			if err != nil {
				return total, err
			}
		}
	}
	return total, nil
}

func restoreOutput(ctx context.Context, cp *Copier, loc Location, settings Settings, o Output, target, password string, encrypted bool, log *Logger) (int, error) {
	fsys, _, err := openFS(loc, settings)
	if err != nil {
		return 0, err
	}
	defer fsys.Close()
	// Si es remoto, primero se baja a una carpeta temporal.
	localOf := func(p string) (string, func(), error) {
		if fsys.Kind() == "local" {
			return p, func() {}, nil
		}
		tmp, err := os.MkdirTemp(tempDir(settings), "rest-")
		if err != nil {
			return "", nil, err
		}
		lp := filepath.Join(tmp, filepath.Base(strings.ReplaceAll(p, "\\", "/")))
		st, err := fsys.Stat(p)
		if err != nil {
			os.RemoveAll(tmp)
			return "", nil, err
		}
		if err := downloadTo(cp, fsys, p, lp, st.ModTime()); err != nil {
			os.RemoveAll(tmp)
			return "", nil, err
		}
		return lp, func() { os.RemoveAll(tmp) }, nil
	}
	count := 0
	switch o.Kind {
	case "zip", "7z":
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
		var locals []string
		var cleanups []func()
		defer func() {
			for _, c := range cleanups {
				c()
			}
		}()
		for _, p := range paths {
			lp, cl, err := localOf(p)
			if err != nil {
				return 0, err
			}
			locals = append(locals, lp)
			cleanups = append(cleanups, cl)
		}
		if o.Kind == "7z" {
			if err := Extract7z(ctx, locals[0], target, password); err != nil {
				return 0, err
			}
			return 1, nil
		}
		return ExtractZip(locals[0], target, password, func(name string) { prog(cp).FileDone() })
	case "carpeta", "individual":
		var walk func(p, rel string) error
		walk = func(p, rel string) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			entries, err := fsys.ReadDir(p)
			if err != nil {
				return err
			}
			for _, e := range entries {
				src := fsys.Join(p, e.Name())
				r := filepath.Join(rel, e.Name())
				if e.IsDir() {
					if err := os.MkdirAll(filepath.Join(target, r), 0o755); err != nil {
						return err
					}
					if err := walk(src, r); err != nil {
						return err
					}
					continue
				}
				if strings.HasSuffix(e.Name(), ".rsgtmp") {
					continue
				}
				if err := restoreOneFile(ctx, cp, fsys, src, filepath.Join(target, r), e, password, localOf, o.Kind == "individual", encrypted); err != nil {
					log.Error("No se pudo restaurar %s: %v", r, err)
					continue
				}
				count++
			}
			return nil
		}
		return count, walk(o.Path, "")
	}
	return 0, fmt.Errorf("tipo de salida desconocido")
}

func prog(cp *Copier) *Progress {
	if cp.prog == nil {
		return &Progress{}
	}
	return cp.prog
}

func restoreOneFile(ctx context.Context, cp *Copier, fsys FS, src, out string, info fs.FileInfo, password string, localOf func(string) (string, func(), error), individual, encrypted bool) error {
	low := strings.ToLower(src)
	switch {
	case encrypted && strings.HasSuffix(low, ".rsg"):
		lp, cl, err := localOf(src)
		if err != nil {
			return err
		}
		defer cl()
		_, err = DecryptPath(lp, filepath.Dir(out), password)
		return err
	case individual && strings.HasSuffix(low, ".zip"):
		lp, cl, err := localOf(src)
		if err != nil {
			return err
		}
		defer cl()
		_, err = ExtractZip(lp, filepath.Dir(out), password, nil)
		return err
	case individual && strings.HasSuffix(low, ".7z"):
		lp, cl, err := localOf(src)
		if err != nil {
			return err
		}
		defer cl()
		return Extract7z(ctx, lp, filepath.Dir(out), password)
	}
	prog(cp).SetCurrent(out)
	return downloadTo(cp, fsys, src, out, info.ModTime())
}

var _ = time.Now

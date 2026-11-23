package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Vista previa de una copia: qué se copiaría (y qué quitaría el espejo) en
// cada destino, sin copiar nada. Para las tareas de copia sin comprimir.

type PreviewItem struct {
	Rel  string `json:"rel"`
	Size int64  `json:"size"`
	Kind string `json:"kind"` // nuevo | modificado | quitar
}

type PreviewDest struct {
	Dest    string        `json:"dest"`
	New     int           `json:"new"`
	Changed int           `json:"changed"`
	Removed int           `json:"removed"`
	Bytes   int64         `json:"bytes"`
	Items   []PreviewItem `json:"items"`
	More    int           `json:"more"`
	Error   string        `json:"error,omitempty"`
}

type TaskPreview struct {
	Supported bool          `json:"supported"`
	Why       string        `json:"why,omitempty"`
	Files     int           `json:"files"`
	Dests     []PreviewDest `json:"dests"`
}

const previewMaxItems = 300

func previewSupported(t Task) (bool, string) {
	if t.Type != TypeFull || t.Separated || !(t.Compression == "" || t.Compression == "ninguna") {
		return false, "La vista previa es para las tareas de copia sin comprimir."
	}
	for _, s := range t.Sources {
		if s.Kind == "ftp" || s.Kind == "sftp" {
			return false, "La vista previa no está disponible con orígenes remotos."
		}
	}
	return true, ""
}

func PreviewTask(ctx context.Context, st *Store, task Task, settings Settings, log *Logger) (TaskPreview, error) {
	out := TaskPreview{Dests: []PreviewDest{}}
	if ok, why := previewSupported(task); !ok {
		out.Why = why
		return out, nil
	}
	out.Supported = true
	rc := &RunCtx{ctx: ctx, store: st, task: task, settings: settings, log: log}
	filter, err := NewFilter(&task)
	if err != nil {
		return out, err
	}
	var srcPaths []string
	for _, s := range task.Sources {
		p := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s.Path), "?D?"), "?F?")
		srcPaths = append(srcPaths, expandTokens(p, &task))
	}
	entries, _, err := ScanSources(ctx, srcPaths, ScanOptions{Subdirs: task.IncludeSubdirs, AbsolutePaths: task.AbsolutePaths,
		ContentsOnly: isContentsOnly(task), FollowReparse: !settings.IgnoreReparsePoints, Filter: filter}, log)
	if err != nil {
		return out, err
	}
	files := map[string]Entry{}
	tops := map[string]bool{}
	for _, e := range entries {
		if !strings.Contains(e.Rel, "/") {
			tops[e.Rel] = e.Dir
		}
		if !e.Dir {
			files[e.Rel] = e
		}
	}
	out.Files = len(files)
	rels := make([]string, 0, len(files))
	for r := range files {
		rels = append(rels, r)
	}
	sort.Strings(rels)
	suffix := ""
	if task.Encrypt && task.Password != "" {
		suffix = ".rsg"
	}
	for di, dest := range task.Destinations {
		pd := PreviewDest{Dest: describeLocation(dest), Items: []PreviewItem{}}
		add := func(it PreviewItem) {
			if len(pd.Items) < previewMaxItems {
				pd.Items = append(pd.Items, it)
			} else {
				pd.More++
			}
		}
		fsys, base, err := openFS(dest, settings)
		if err != nil {
			pd.Error = err.Error()
			out.Dests = append(out.Dests, pd)
			continue
		}
		base = expandTokens(base, &task)
		_, root, _ := rc.destLayout(fsys, base, TypeFull, "")
		for _, r := range rels {
			if ctx.Err() != nil {
				break
			}
			e := files[r]
			st, err := fsys.Stat(fsys.Join(root, r) + suffix)
			switch {
			case err != nil:
				pd.New++
				pd.Bytes += e.Size
				add(PreviewItem{Rel: r, Size: e.Size, Kind: "nuevo"})
			case suffix == "" && (st.Size() != e.Size || absDur(st.ModTime().Sub(e.MTime)) > 2*time.Second):
				pd.Changed++
				pd.Bytes += e.Size
				add(PreviewItem{Rel: r, Size: e.Size, Kind: "modificado"})
			}
		}
		if task.Mirror {
			// lo que el espejo quitaría: archivos dentro de las carpetas del origen que ya no están
			var walk func(dir, rel string)
			walk = func(dir, rel string) {
				ents, err := fsys.ReadDir(dir)
				if err != nil {
					return
				}
				for _, de := range ents {
					n := de.Name()
					if n == "." || n == ".." {
						continue
					}
					r := rel + "/" + n
					if de.IsDir() {
						walk(fsys.Join(dir, n), r)
						continue
					}
					if _, ok := files[strings.TrimSuffix(r, suffix)]; !ok {
						pd.Removed++
						add(PreviewItem{Rel: r, Size: de.Size(), Kind: "quitar"})
					}
				}
			}
			for t, isDir := range tops {
				if isDir {
					walk(fsys.Join(root, t), t)
				}
			}
			// Con "sólo lo de adentro": lo que Resguardo había copiado al primer
			// nivel y ya no está en el origen.
			if isContentsOnly(task) && rc.sourceReadable() {
				managed := rc.managedTops(di)
				if ents, err := fsys.ReadDir(root); err == nil {
					for _, de := range ents {
						n := de.Name()
						if _, ok := tops[n]; ok || !managed[strings.ToLower(n)] {
							continue
						}
						pd.Removed++
						add(PreviewItem{Rel: n, Size: de.Size(), Kind: "quitar"})
					}
				}
			}
		}
		fsys.Close()
		out.Dests = append(out.Dests, pd)
	}
	if ctx.Err() != nil {
		return out, fmt.Errorf("se canceló")
	}
	return out, nil
}

package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// DAW describe cómo reconocer un programa y sus proyectos.
type DAW struct {
	ID        string
	Name      string
	Exts      []string // extensiones del archivo de proyecto
	SkipDirs  []string // carpetas internas donde un archivo de proyecto no marca un proyecto nuevo
	WinProc   *regexp.Regexp
	MacProc   *regexp.Regexp
	LinuxProc *regexp.Regexp
	Refs      bool // sabemos leer qué archivos usa el proyecto
}

var DAWs = []DAW{
	{ID: "ableton", Name: "Ableton Live", Exts: []string{".als"}, SkipDirs: []string{"Backup", "Ableton Project Info"},
		WinProc: regexp.MustCompile(`(?i)^ableton live.*\.exe$`), MacProc: regexp.MustCompile(`(?i)/Ableton Live[^/]*\.app/`), LinuxProc: regexp.MustCompile(`(?i)ableton`), Refs: true},
	{ID: "cubase", Name: "Cubase / Nuendo", Exts: []string{".cpr", ".npr"}, SkipDirs: []string{"Auto Saves"},
		WinProc: regexp.MustCompile(`(?i)^(cubase|nuendo)[^\\]*\.exe$`), MacProc: regexp.MustCompile(`(?i)/(Cubase|Nuendo)[^/]*\.app/`), LinuxProc: regexp.MustCompile(`(?i)^(cubase|nuendo)`), Refs: true},
	{ID: "protools", Name: "Pro Tools", Exts: []string{".ptx", ".ptf", ".pts"}, SkipDirs: []string{"Session File Backups"},
		WinProc: regexp.MustCompile(`(?i)^protools\.exe$`), MacProc: regexp.MustCompile(`(?i)/Pro Tools[^/]*\.app/`), LinuxProc: regexp.MustCompile(`(?i)^protools`), Refs: true},
	{ID: "fl", Name: "FL Studio", Exts: []string{".flp"}, SkipDirs: []string{"Backup"},
		WinProc: regexp.MustCompile(`(?i)^fl(64)?\.exe$`), MacProc: regexp.MustCompile(`(?i)/FL Studio[^/]*\.app/`), LinuxProc: regexp.MustCompile(`(?i)^fl(64)?$`)},
	{ID: "reaper", Name: "REAPER", Exts: []string{".rpp"}, SkipDirs: []string{"Backups"},
		WinProc: regexp.MustCompile(`(?i)^reaper\.exe$`), MacProc: regexp.MustCompile(`(?i)/REAPER[^/]*\.app/`), LinuxProc: regexp.MustCompile(`(?i)^reaper$`)},
	{ID: "studioone", Name: "Studio One", Exts: []string{".song"}, SkipDirs: []string{"History"},
		WinProc: regexp.MustCompile(`(?i)^studio one.*\.exe$`), MacProc: regexp.MustCompile(`(?i)/Studio One[^/]*\.app/`), LinuxProc: regexp.MustCompile(`(?i)studio one`)},
	{ID: "bitwig", Name: "Bitwig Studio", Exts: []string{".bwproject"}, SkipDirs: []string{"autosaves"},
		WinProc: regexp.MustCompile(`(?i)^bitwig studio\.exe$`), MacProc: regexp.MustCompile(`(?i)/Bitwig Studio\.app/`), LinuxProc: regexp.MustCompile(`(?i)bitwig`)},
	{ID: "logic", Name: "Logic Pro", Exts: []string{".logicx"},
		MacProc: regexp.MustCompile(`(?i)/Logic Pro[^/]*\.app/`)},
}

func dawByID(id string) *DAW {
	for i := range DAWs {
		if DAWs[i].ID == id {
			return &DAWs[i]
		}
	}
	return nil
}

func dawForFile(name string, enabled []string) *DAW {
	ext := strings.ToLower(filepath.Ext(name))
	for _, id := range enabled {
		d := dawByID(id)
		if d == nil {
			continue
		}
		for _, e := range d.Exts {
			if e == ext {
				return d
			}
		}
	}
	return nil
}

// Project es una carpeta de proyecto detectada dentro de un origen.
type Project struct {
	Key       string    `json:"key"`  // ruta relativa al origen, con "/" (incluye el nombre del origen)
	Name      string    `json:"name"` // nombre de la carpeta
	Dir       string    `json:"dir"`  // ruta absoluta
	DAW       string    `json:"daw"`
	DAWName   string    `json:"dawName"`
	Files     []string  `json:"files"` // archivos de proyecto (sólo nombres)
	Main      string    `json:"main"`  // el más reciente
	Modified  time.Time `json:"modified"`
	SourceIdx int       `json:"sourceIdx"`
}

func isProjectFileName(name string) bool {
	n := strings.ToLower(name)
	return !strings.HasPrefix(n, "._") && !strings.HasPrefix(n, ".")
}

// projectFilesIn devuelve los archivos de proyecto que hay directamente en dir.
func projectFilesIn(dir string, enabled []string) (*DAW, []string, time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, time.Time{}
	}
	var found *DAW
	var files []string
	var newest time.Time
	for _, e := range entries {
		name := e.Name()
		if !isProjectFileName(name) {
			continue
		}
		d := dawForFile(name, enabled)
		if d == nil {
			continue
		}
		// Logic guarda el proyecto como "paquete" (carpeta .logicx); el resto son archivos.
		if e.IsDir() != (d.ID == "logic") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if found == nil {
			found = d
		}
		if d.ID != found.ID {
			continue
		}
		files = append(files, name)
		mt := info.ModTime()
		if d.ID == "logic" {
			mt = newestIn(filepath.Join(dir, name))
		}
		if mt.After(newest) {
			newest = mt
		}
	}
	return found, files, newest
}

func newestIn(dir string) time.Time {
	var newest time.Time
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return nil
	})
	return newest
}

func mainProjectFile(dir string, files []string) string {
	best := ""
	var bt time.Time
	for _, f := range files {
		if st, err := os.Stat(filepath.Join(dir, f)); err == nil {
			mt := st.ModTime()
			if st.IsDir() {
				mt = newestIn(filepath.Join(dir, f))
			}
			if best == "" || mt.After(bt) {
				best, bt = f, mt
			}
		}
	}
	return best
}

// DiscoverProjects busca carpetas de proyecto dentro de los orígenes.
func DiscoverProjects(ctx context.Context, sources []string, cfg ProjectsConfig) []Project {
	var out []Project
	depth := cfg.MaxDepth
	if depth <= 0 {
		depth = 6
	}
	for si, src := range sources {
		src = filepath.Clean(src)
		top := filepath.Base(src)
		var walk func(dir, rel string, level int)
		walk = func(dir, rel string, level int) {
			if ctx.Err() != nil {
				return
			}
			if d, files, mt := projectFilesIn(dir, cfg.DAWs); d != nil {
				out = append(out, Project{Key: rel, Name: filepath.Base(dir), Dir: dir, DAW: d.ID, DAWName: d.Name,
					Files: files, Main: mainProjectFile(dir, files), Modified: mt, SourceIdx: si})
				return
			}
			if level >= depth {
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "$") {
					continue
				}
				if strings.HasSuffix(strings.ToLower(e.Name()), ".logicx") {
					continue
				}
				if e.Type()&os.ModeSymlink != 0 {
					continue
				}
				walk(filepath.Join(dir, e.Name()), rel+"/"+e.Name(), level+1)
			}
		}
		walk(src, top, 0)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// procMatchesDAW decide si un nombre de proceso (o ruta) corresponde al DAW.
func procMatchesDAW(d *DAW, name string) bool {
	switch runtime.GOOS {
	case "windows":
		return d.WinProc != nil && d.WinProc.MatchString(filepath.Base(name))
	case "darwin":
		return d.MacProc != nil && d.MacProc.MatchString(name)
	default:
		return d.LinuxProc != nil && d.LinuxProc.MatchString(filepath.Base(name))
	}
}

// RunningDAWs devuelve los ids de los DAW abiertos ahora.
func RunningDAWs() map[string]bool {
	names := listProcesses()
	out := map[string]bool{}
	for _, n := range names {
		for i := range DAWs {
			if procMatchesDAW(&DAWs[i], n) {
				out[DAWs[i].ID] = true
			}
		}
	}
	return out
}

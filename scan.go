package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ---------- Filtros ----------

type compiledItem struct {
	kind  string
	masks []string
	re    *regexp.Regexp
	path  string
}

type Filter struct {
	include   []compiledItem
	exclude   []compiledItem
	date      DateFilter
	dateLimit time.Time
	size      SizeFilter
	smart     bool
}

func normCase(s string) string {
	if runtime.GOOS == "linux" {
		return s
	}
	return strings.ToLower(s)
}

func compileItems(items []FilterItem) ([]compiledItem, error) {
	var out []compiledItem
	for _, it := range items {
		v := strings.TrimSpace(it.Value)
		if v == "" {
			continue
		}
		switch it.Kind {
		case "regex":
			re, err := regexp.Compile("(?i)" + v)
			if err != nil {
				return nil, fmt.Errorf("expresión regular inválida %q: %w", v, err)
			}
			out = append(out, compiledItem{kind: "regex", re: re})
		case "archivo", "carpeta":
			out = append(out, compiledItem{kind: it.Kind, path: normCase(filepath.ToSlash(filepath.Clean(v)))})
		default:
			var masks []string
			for _, m := range strings.Split(v, ",") {
				m = strings.TrimSpace(m)
				if m != "" {
					masks = append(masks, normCase(filepath.ToSlash(m)))
				}
			}
			out = append(out, compiledItem{kind: "mascara", masks: masks})
		}
	}
	return out, nil
}

func parseRelativeDate(s string, now time.Time) (time.Time, bool) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "hoy", "1000-01-01":
		if s == "" {
			return time.Time{}, false
		}
		return day, true
	case "ayer", "999-01-01", "0999-01-01":
		return day.AddDate(0, 0, -1), true
	case "manana", "mañana", "1001-01-01":
		return day.AddDate(0, 0, 1), true
	}
	if strings.HasPrefix(s, "-") {
		if n, err := strconv.Atoi(s[1:]); err == nil {
			return day.AddDate(0, 0, -n), true
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func NewFilter(t *Task) (*Filter, error) {
	f := &Filter{date: t.DateFilter, size: t.SizeFilter, smart: t.Projects.SmartExclusions}
	var err error
	if f.include, err = compileItems(t.Include); err != nil {
		return nil, err
	}
	if f.exclude, err = compileItems(t.Exclude); err != nil {
		return nil, err
	}
	if f.date.Mode != "" {
		if d, ok := parseRelativeDate(f.date.Date, time.Now()); ok {
			f.dateLimit = d
		} else {
			f.date.Mode = ""
		}
	}
	return f, nil
}

func (c compiledItem) match(absSlash, name string, isDir bool) bool {
	switch c.kind {
	case "regex":
		return c.re.MatchString(absSlash)
	case "archivo":
		return !isDir && absSlash == c.path
	case "carpeta":
		return absSlash == c.path || strings.HasPrefix(absSlash, c.path+"/")
	default:
		for _, m := range c.masks {
			target := name
			if strings.Contains(m, "/") {
				target = absSlash
				if !strings.HasPrefix(m, "/") && !strings.Contains(m, ":") {
					// patrón relativo: comparar contra el final de la ruta
					parts := strings.Split(absSlash, "/")
					n := strings.Count(m, "/") + 1
					if len(parts) >= n {
						target = strings.Join(parts[len(parts)-n:], "/")
					}
				}
			}
			if ok, _ := filepath.Match(m, target); ok {
				return true
			}
		}
	}
	return false
}

// smartExcluded: cachés que los DAW regeneran solos.
func smartExcluded(name, parentName string) bool {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".asd"): // Ableton: análisis de samples
		return true
	case n == "wavecache.wfm": // Pro Tools: caché de formas de onda
		return true
	case strings.HasSuffix(n, ".csh") && strings.EqualFold(parentName, "Images"): // Cubase: imágenes de onda
		return true
	case strings.HasSuffix(n, ".reapeaks"): // REAPER: picos
		return true
	case n == ".ds_store", n == "thumbs.db", n == "desktop.ini":
		return true
	case strings.HasSuffix(n, ".rsgtmp"):
		return true
	}
	return false
}

// Allowed decide si un archivo o carpeta entra en el respaldo.
func (f *Filter) Allowed(abs string, info fs.FileInfo, parentName string) bool {
	name := info.Name()
	isDir := info.IsDir()
	if strings.HasSuffix(name, ".rsgtmp") {
		return false
	}
	if f.smart && !isDir && smartExcluded(name, parentName) {
		return false
	}
	absSlash := normCase(filepath.ToSlash(abs))
	lname := normCase(name)
	for _, c := range f.exclude {
		if c.match(absSlash, lname, isDir) {
			return false
		}
	}
	if isDir {
		return true
	}
	if len(f.include) > 0 {
		ok := false
		for _, c := range f.include {
			if c.match(absSlash, lname, false) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.date.Mode != "" {
		newer := info.ModTime().After(f.dateLimit) || info.ModTime().Equal(f.dateLimit)
		cond := newer
		if f.date.Op == "mas_viejos" {
			cond = !newer
		}
		if (f.date.Mode == "incluir" && !cond) || (f.date.Mode == "excluir" && cond) {
			return false
		}
	}
	if f.size.Mode != "" && f.size.MB > 0 {
		limit := int64(f.size.MB * 1024 * 1024)
		cond := info.Size() > limit
		if f.size.Op == "menores" {
			cond = info.Size() < limit
		}
		if (f.size.Mode == "incluir" && !cond) || (f.size.Mode == "excluir" && cond) {
			return false
		}
	}
	return true
}

// ---------- Recorrido de orígenes ----------

type Entry struct {
	Abs   string // ruta desde donde se lee (puede ser una instantánea VSS)
	Real  string // ruta real en el disco
	Rel   string // con "/"
	Size  int64
	MTime time.Time
	Dir   bool
	Src   int
	Mode  fs.FileMode
}

// relForAbsolute arma la ruta relativa "absoluta" (C/Datos/... o Users/...).
func relForAbsolute(abs string) string {
	abs = filepath.ToSlash(abs)
	if len(abs) >= 2 && abs[1] == ':' {
		return string(abs[0]) + strings.TrimPrefix(abs[2:], "")
	}
	return strings.TrimLeft(abs, "/")
}

type ScanOptions struct {
	Subdirs         bool
	IgnoreEmptyDirs bool
	AbsolutePaths   bool
	FollowReparse   bool
	Filter          *Filter
	ReadRoot        func(src string) string // para VSS: ruta de lectura alternativa
	OnProgress      func(files int, bytes int64)
}

// ScanSources recorre los orígenes locales y devuelve la lista de entradas.
func ScanSources(ctx context.Context, sources []string, opt ScanOptions, log *Logger) ([]Entry, []string, error) {
	var out []Entry
	var warnings []string
	usedTop := map[string]int{}
	var nfiles int
	var nbytes int64
	for si, src := range sources {
		src = filepath.Clean(src)
		readRoot := src
		if opt.ReadRoot != nil {
			readRoot = opt.ReadRoot(src)
		}
		info, err := os.Stat(readRoot)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("No se encontró el origen %s: %v", src, err))
			continue
		}
		var top string
		if opt.AbsolutePaths {
			top = relForAbsolute(src)
		} else {
			top = filepath.Base(src)
			if top == "." || top == string(filepath.Separator) || strings.HasSuffix(top, ":") || top == "" {
				top = strings.TrimSuffix(strings.ReplaceAll(src, ":", ""), string(filepath.Separator))
				top = sanitizeName(top)
			}
			if n := usedTop[strings.ToLower(top)]; n > 0 {
				usedTop[strings.ToLower(top)] = n + 1
				top = fmt.Sprintf("%s (%d)", top, n+1)
			} else {
				usedTop[strings.ToLower(top)] = 1
			}
		}
		if !info.IsDir() {
			if opt.Filter != nil && !opt.Filter.Allowed(src, info, filepath.Base(filepath.Dir(src))) {
				continue
			}
			out = append(out, Entry{Abs: readRoot, Real: src, Rel: filepath.ToSlash(top), Size: info.Size(), MTime: info.ModTime(), Src: si, Mode: info.Mode()})
			nfiles++
			nbytes += info.Size()
			continue
		}
		visited := map[string]bool{}
		var walk func(dirRead, dirReal, rel string) (int, error)
		walk = func(dirRead, dirReal, rel string) (int, error) {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if real, err := filepath.EvalSymlinks(dirRead); err == nil {
				if visited[real] {
					return 0, nil
				}
				visited[real] = true
			}
			entries, err := os.ReadDir(dirRead)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("No se pudo leer %s: %v", dirReal, err))
				return 0, nil
			}
			count := 0
			parentName := filepath.Base(dirReal)
			for _, d := range entries {
				name := d.Name()
				pRead := filepath.Join(dirRead, name)
				pReal := filepath.Join(dirReal, name)
				r := rel + "/" + name
				isLink := d.Type()&fs.ModeSymlink != 0
				var fi fs.FileInfo
				if isLink {
					fi, err = os.Stat(pRead)
				} else {
					fi, err = d.Info()
				}
				if err != nil {
					continue
				}
				if opt.Filter != nil && !opt.Filter.Allowed(pReal, fi, parentName) {
					continue
				}
				if fi.IsDir() {
					if isLink && !opt.FollowReparse {
						continue
					}
					if !opt.Subdirs {
						continue
					}
					idx := len(out)
					out = append(out, Entry{Abs: pRead, Real: pReal, Rel: r, Dir: true, MTime: fi.ModTime(), Src: si, Mode: fi.Mode()})
					n, err := walk(pRead, pReal, r)
					if err != nil {
						return count, err
					}
					if n == 0 && opt.IgnoreEmptyDirs {
						out = out[:idx]
					} else {
						count += n + 1
					}
					continue
				}
				if !fi.Mode().IsRegular() {
					continue
				}
				out = append(out, Entry{Abs: pRead, Real: pReal, Rel: r, Size: fi.Size(), MTime: fi.ModTime(), Src: si, Mode: fi.Mode()})
				count++
				nfiles++
				nbytes += fi.Size()
				if opt.OnProgress != nil && nfiles%500 == 0 {
					opt.OnProgress(nfiles, nbytes)
				}
			}
			return count, nil
		}
		out = append(out, Entry{Abs: readRoot, Real: src, Rel: filepath.ToSlash(top), Dir: true, MTime: info.ModTime(), Src: si, Mode: info.Mode()})
		if _, err := walk(readRoot, src, filepath.ToSlash(top)); err != nil {
			return nil, warnings, err
		}
	}
	return out, warnings, nil
}

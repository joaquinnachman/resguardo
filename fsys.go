package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// FS abstrae un destino (o un origen remoto): disco local / red, FTP o SFTP.
type FS interface {
	Kind() string
	Join(elem ...string) string
	Dir(p string) string
	Stat(p string) (fs.FileInfo, error)
	ReadDir(p string) ([]fs.FileInfo, error)
	MkdirAll(p string) error
	Create(p string) (io.WriteCloser, error)
	Open(p string) (io.ReadCloser, error)
	Remove(p string) error
	RemoveAll(p string) error
	Rename(oldp, newp string) error
	Chtimes(p string, mtime time.Time) error
	Reconnect() error
	Close() error
}

// ---------- Local ----------

type LocalFS struct{}

func (LocalFS) Kind() string                         { return "local" }
func (LocalFS) Join(elem ...string) string           { return filepath.Join(elem...) }
func (LocalFS) Dir(p string) string                  { return filepath.Dir(p) }
func (LocalFS) Stat(p string) (fs.FileInfo, error)   { return os.Stat(p) }
func (LocalFS) MkdirAll(p string) error              { return os.MkdirAll(p, 0o755) }
func (LocalFS) Open(p string) (io.ReadCloser, error) { return os.Open(p) }
func (LocalFS) Remove(p string) error                { clearReadOnly(p); return os.Remove(p) }
func (LocalFS) Reconnect() error                     { return nil }
func (LocalFS) Close() error                         { return nil }
func (LocalFS) Chtimes(p string, t time.Time) error  { return os.Chtimes(p, t, t) }

func (LocalFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			out = append(out, fi)
		}
	}
	return out, nil
}

func (LocalFS) Create(p string) (io.WriteCloser, error) {
	clearReadOnly(p)
	return os.Create(p)
}

func (LocalFS) RemoveAll(p string) error {
	_ = filepath.WalkDir(p, func(sp string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			clearReadOnly(sp)
		}
		return nil
	})
	return os.RemoveAll(p)
}

func (LocalFS) Rename(a, b string) error {
	if err := os.Rename(a, b); err != nil {
		// Puede ser otra unidad: copiar y borrar.
		var le *os.LinkError
		if errors.As(err, &le) {
			if cerr := copyLocalTree(a, b); cerr != nil {
				return cerr
			}
			return os.RemoveAll(a)
		}
		return err
	}
	return nil
}

func copyLocalTree(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return copyLocalFile(src, dst, st)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return copyLocalFile(p, target, fi)
	})
}

func copyLocalFile(src, dst string, fi fs.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

// ---------- Apertura según ubicación ----------

func expandTokens(p string, task *Task) string {
	now := time.Now()
	host, _ := os.Hostname()
	user := os.Getenv("USERNAME")
	if user == "" {
		user = os.Getenv("USER")
	}
	name := ""
	if task != nil {
		name = sanitizeName(task.Name)
	}
	r := strings.NewReplacer(
		"{tarea}", name,
		"{fecha}", now.Format("2006-01-02"),
		"{año}", now.Format("2006"), "{anio}", now.Format("2006"),
		"{mes}", now.Format("01"),
		"{dia}", now.Format("02"), "{día}", now.Format("02"),
		"{hora}", now.Format("15-04"),
		"{equipo}", host,
		"{usuario}", user,
	)
	return r.Replace(p)
}

func openFS(loc Location, settings Settings) (FS, string, error) {
	switch loc.Kind {
	case "", "local":
		p := strings.TrimSpace(loc.Path)
		p = strings.TrimPrefix(strings.TrimPrefix(p, "?D?"), "?F?")
		return LocalFS{}, p, nil
	case "ftp":
		f, err := dialFTP(loc, settings)
		if err != nil {
			return nil, "", err
		}
		p := loc.Path
		if p == "" {
			p = "/"
		}
		return f, p, nil
	case "sftp":
		f, err := dialSFTP(loc)
		if err != nil {
			return nil, "", err
		}
		p := loc.Path
		if p == "" {
			p = "."
		}
		return f, p, nil
	}
	return nil, "", fmt.Errorf("tipo de ubicación desconocido: %s", loc.Kind)
}

func describeLocation(loc Location) string {
	switch loc.Kind {
	case "ftp":
		return fmt.Sprintf("ftp://%s@%s%s", loc.User, loc.Host, loc.Path)
	case "sftp":
		return fmt.Sprintf("sftp://%s@%s%s", loc.User, loc.Host, loc.Path)
	}
	return loc.Path
}

// remotePathJoin para FS remotos (siempre con "/").
func remoteJoin(elem ...string) string {
	for i := range elem {
		elem[i] = strings.ReplaceAll(elem[i], "\\", "/")
	}
	return path.Join(elem...)
}

// removeAllGeneric borra recursivamente usando sólo la interfaz FS.
func removeAllGeneric(f FS, p string) error {
	st, err := f.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !st.IsDir() {
		return f.Remove(p)
	}
	entries, err := f.ReadDir(p)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "." || e.Name() == ".." {
			continue
		}
		if err := removeAllGeneric(f, f.Join(p, e.Name())); err != nil {
			return err
		}
	}
	return f.Remove(p)
}

// existsFS: true si la ruta existe.
func existsFS(f FS, p string) bool {
	_, err := f.Stat(p)
	return err == nil
}

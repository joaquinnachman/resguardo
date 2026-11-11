package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	yzip "resguardo/internal/aeszip"
)

type ArchiveItem struct {
	Abs  string
	Name string // nombre dentro del archivo, con "/"
	Info fs.FileInfo
	Dir  bool
}

type ArchiveSpec struct {
	Format     string
	Level      int
	Password   string
	Encryption string
	Comment    string
	SplitBytes int64
	StoredExt  map[string]bool
	AbsPaths   bool
}

func parseStoredExt(s string) map[string]bool {
	m := map[string]bool{}
	for _, e := range strings.Split(s, ",") {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e != "" {
			m[e] = true
		}
	}
	return m
}

func (s ArchiveSpec) stored(name string) bool {
	if s.Level == 0 {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	return s.StoredExt[ext]
}

// ---------- Escritor en partes (.001, .002 …) ----------

type splitWriter struct {
	fs      FS
	base    string
	size    int64
	cur     io.WriteCloser
	written int64
	part    int
	Parts   []string
}

func (w *splitWriter) next() error {
	if w.cur != nil {
		if err := w.cur.Close(); err != nil {
			return err
		}
	}
	w.part++
	name := fmt.Sprintf("%s.%03d", w.base, w.part)
	c, err := w.fs.Create(name)
	if err != nil {
		return err
	}
	w.cur, w.written = c, 0
	w.Parts = append(w.Parts, name)
	return nil
}

func (w *splitWriter) Write(b []byte) (int, error) {
	total := 0
	for len(b) > 0 {
		if w.cur == nil || w.written >= w.size {
			if err := w.next(); err != nil {
				return total, err
			}
		}
		room := w.size - w.written
		chunk := b
		if int64(len(chunk)) > room {
			chunk = chunk[:room]
		}
		n, err := w.cur.Write(chunk)
		total += n
		w.written += int64(n)
		if err != nil {
			return total, err
		}
		b = b[n:]
	}
	return total, nil
}

func (w *splitWriter) Close() error {
	if w.cur != nil {
		return w.cur.Close()
	}
	return nil
}

// tailCommentWriter retiene los últimos 2 bytes (largo del comentario del ZIP)
// para poder agregar un comentario a archivos creados con la librería de cifrado.
type tailCommentWriter struct {
	w       io.Writer
	hold    []byte
	comment string
}

func (t *tailCommentWriter) Write(b []byte) (int, error) {
	t.hold = append(t.hold, b...)
	if len(t.hold) > 2 {
		n := len(t.hold) - 2
		if _, err := t.w.Write(t.hold[:n]); err != nil {
			return 0, err
		}
		t.hold = append([]byte{}, t.hold[n:]...)
	}
	return len(b), nil
}

func (t *tailCommentWriter) Finish() error {
	c := []byte(t.comment)
	if len(c) > 65535 {
		c = c[:65535]
	}
	if len(t.hold) == 2 && len(c) > 0 {
		var lb [2]byte
		binary.LittleEndian.PutUint16(lb[:], uint16(len(c)))
		if _, err := t.w.Write(lb[:]); err != nil {
			return err
		}
		_, err := t.w.Write(c)
		return err
	}
	_, err := t.w.Write(t.hold)
	return err
}

// ---------- ZIP ----------

var yzipMu sync.Mutex

// WriteZip crea un .zip (o partes .zip.001…) en el destino. Devuelve los archivos creados.
func (c *Copier) WriteZip(dst FS, outPath string, items []ArchiveItem, spec ArchiveSpec, onFile func(ArchiveItem, error)) ([]string, error) {
	if err := dst.MkdirAll(dst.Dir(outPath)); err != nil {
		return nil, err
	}
	var sink io.WriteCloser
	var sw *splitWriter
	var created []string
	tmpOut := outPath + ".rsgtmp"
	if spec.SplitBytes > 0 {
		sw = &splitWriter{fs: dst, base: outPath, size: spec.SplitBytes}
		sink = sw
	} else {
		w, err := dst.Create(tmpOut)
		if err != nil {
			return nil, err
		}
		sink = w
	}
	bw := bufio.NewWriterSize(sink, 1<<20)
	fail := func(err error) ([]string, error) {
		sink.Close()
		if sw != nil {
			for _, p := range sw.Parts {
				_ = dst.Remove(p)
			}
		} else {
			_ = dst.Remove(tmpOut)
		}
		return nil, err
	}

	if spec.Password == "" {
		zw := zip.NewWriter(bw)
		lvl := spec.Level
		zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
			return flate.NewWriter(out, lvl)
		})
		if spec.Comment != "" {
			_ = zw.SetComment(spec.Comment)
		}
		for _, it := range items {
			if err := c.waitIfPaused(); err != nil {
				return fail(err)
			}
			if it.Dir {
				fh := &zip.FileHeader{Name: strings.TrimSuffix(it.Name, "/") + "/", Method: zip.Store}
				if it.Info != nil {
					fh.Modified = it.Info.ModTime()
				}
				if _, err := zw.CreateHeader(fh); err != nil {
					return fail(err)
				}
				continue
			}
			fh, err := zip.FileInfoHeader(it.Info)
			if err != nil {
				return fail(err)
			}
			fh.Name = it.Name
			fh.Method = zip.Deflate
			if spec.stored(it.Name) {
				fh.Method = zip.Store
			}
			c.prog.SetCurrent(it.Abs)
			w, err := zw.CreateHeader(fh)
			if err != nil {
				return fail(err)
			}
			ferr := c.copyInto(w, it.Abs)
			if onFile != nil {
				onFile(it, ferr)
			}
			if ferr != nil {
				if errors.Is(ferr, context.Canceled) {
					return fail(ferr)
				}
				// el archivo queda incompleto dentro del zip: se informa como error
			}
			c.prog.FileDone()
		}
		if err := zw.Close(); err != nil {
			return fail(err)
		}
	} else {
		yzipMu.Lock()
		defer yzipMu.Unlock()
		yzip.SetDeflateLevel(spec.Level)
		enc := yzip.AES256Encryption
		switch spec.Encryption {
		case "aes128":
			enc = yzip.AES128Encryption
		case "zipcrypto":
			enc = yzip.StandardEncryption
		}
		tw := &tailCommentWriter{w: bw, comment: spec.Comment}
		zw := yzip.NewWriter(tw)
		for _, it := range items {
			if err := c.waitIfPaused(); err != nil {
				return fail(err)
			}
			if it.Dir {
				fh := &yzip.FileHeader{Name: strings.TrimSuffix(it.Name, "/") + "/", Method: yzip.Store}
				if it.Info != nil {
					fh.SetModTime(it.Info.ModTime())
				}
				if _, err := zw.CreateHeader(fh); err != nil {
					return fail(err)
				}
				continue
			}
			fh := &yzip.FileHeader{Name: it.Name, Method: yzip.Deflate}
			if spec.stored(it.Name) {
				fh.Method = yzip.Store
			}
			if !isASCII(it.Name) {
				fh.Flags |= 0x800
			}
			fh.SetModTime(it.Info.ModTime())
			fh.SetMode(it.Info.Mode())
			fh.UncompressedSize64 = uint64(it.Info.Size())
			fh.SetPassword(spec.Password)
			fh.SetEncryptionMethod(enc)
			c.prog.SetCurrent(it.Abs)
			w, err := zw.CreateHeader(fh)
			if err != nil {
				return fail(err)
			}
			ferr := c.copyInto(w, it.Abs)
			if onFile != nil {
				onFile(it, ferr)
			}
			if ferr != nil && errors.Is(ferr, context.Canceled) {
				return fail(ferr)
			}
			c.prog.FileDone()
		}
		if err := zw.Close(); err != nil {
			return fail(err)
		}
		if err := tw.Finish(); err != nil {
			return fail(err)
		}
	}
	if err := bw.Flush(); err != nil {
		return fail(err)
	}
	if err := sink.Close(); err != nil {
		if sw == nil {
			_ = dst.Remove(tmpOut)
		}
		return nil, err
	}
	if sw != nil {
		created = sw.Parts
	} else {
		if dst.Kind() == "ftp" {
			_ = dst.Remove(outPath)
		}
		if err := dst.Rename(tmpOut, outPath); err != nil {
			_ = dst.Remove(outPath)
			if err := dst.Rename(tmpOut, outPath); err != nil {
				return nil, err
			}
		}
		created = []string{outPath}
	}
	return created, nil
}

func (c *Copier) copyInto(w io.Writer, abs string) error {
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = c.Stream(w, f, nil)
	return err
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// ---------- Lectura de ZIP (también partido y cifrado) ----------

type multiReaderAt struct {
	files []*os.File
	sizes []int64
	total int64
}

func openMulti(paths []string) (*multiReaderAt, error) {
	m := &multiReaderAt{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			m.Close()
			return nil, err
		}
		st, _ := f.Stat()
		m.files = append(m.files, f)
		m.sizes = append(m.sizes, st.Size())
		m.total += st.Size()
	}
	return m, nil
}

func (m *multiReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for i, f := range m.files {
		if off >= m.sizes[i] {
			off -= m.sizes[i]
			continue
		}
		for n < len(p) && off < m.sizes[i] {
			k, err := f.ReadAt(p[n:], off)
			n += k
			off += int64(k)
			if err != nil && err != io.EOF {
				return n, err
			}
			if k == 0 {
				break
			}
		}
		if n == len(p) {
			return n, nil
		}
		off = 0
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *multiReaderAt) Close() {
	for _, f := range m.files {
		f.Close()
	}
}

// archiveParts devuelve las partes de un archivo partido (x.zip.001, x.zip.002…) o el archivo solo.
func archiveParts(p string) []string {
	if regexp.MustCompile(`\.\d{3}$`).MatchString(p) {
		base := p[:len(p)-4]
		var parts []string
		for i := 1; ; i++ {
			q := fmt.Sprintf("%s.%03d", base, i)
			if _, err := os.Stat(q); err != nil {
				break
			}
			parts = append(parts, q)
		}
		return parts
	}
	return []string{p}
}

// ExtractZip extrae un zip local (posiblemente partido y/o cifrado) en destDir.
func ExtractZip(archive, destDir, password string, onFile func(name string)) (int, error) {
	parts := archiveParts(archive)
	m, err := openMulti(parts)
	if err != nil {
		return 0, err
	}
	defer m.Close()
	zr, err := yzip.NewReader(m, m.total)
	if err != nil {
		return 0, fmt.Errorf("no es un ZIP válido: %w", err)
	}
	count := 0
	for _, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		if strings.Contains(name, "..") {
			continue
		}
		target := filepath.Join(destDir, name)
		if strings.HasSuffix(f.Name, "/") {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		if f.IsEncrypted() {
			if password == "" {
				return count, fmt.Errorf("el archivo está cifrado: falta la contraseña")
			}
			f.SetPassword(password)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		rc, err := f.Open()
		if err != nil {
			return count, fmt.Errorf("%s: %w", f.Name, err)
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return count, err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return count, fmt.Errorf("%s: %w (¿contraseña incorrecta?)", f.Name, err)
		}
		_ = os.Chtimes(target, f.ModTime(), f.ModTime())
		count++
		if onFile != nil {
			onFile(f.Name)
		}
	}
	return count, nil
}

// TestZip lee todo el contenido y verifica los CRC.
func TestZip(archive, password string) error {
	parts := archiveParts(archive)
	m, err := openMulti(parts)
	if err != nil {
		return err
	}
	defer m.Close()
	zr, err := yzip.NewReader(m, m.total)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		if f.IsEncrypted() {
			f.SetPassword(password)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		_, err = io.Copy(io.Discard, rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}

// ---------- 7z (con el 7-Zip que viene incluido) ----------

var sevenZipMu sync.Mutex
var sevenZipPath string

func find7z(dataDirPath string) (string, error) {
	sevenZipMu.Lock()
	defer sevenZipMu.Unlock()
	if sevenZipPath != "" {
		return sevenZipPath, nil
	}
	if p, err := extractEmbedded7z(filepath.Join(dataDirPath, "bin")); err == nil && p != "" {
		sevenZipPath = p
		return p, nil
	}
	for _, name := range []string{"7zz", "7z", "7za", "7zr"} {
		if p, err := exec.LookPath(name); err == nil {
			sevenZipPath = p
			return p, nil
		}
	}
	for _, p := range []string{`C:\Program Files\7-Zip\7z.exe`, `C:\Program Files (x86)\7-Zip\7z.exe`, "/opt/homebrew/bin/7zz", "/usr/local/bin/7zz", "/opt/homebrew/bin/7z", "/usr/local/bin/7z"} {
		if _, err := os.Stat(p); err == nil {
			sevenZipPath = p
			return p, nil
		}
	}
	return "", fmt.Errorf("7-Zip no está disponible en este equipo; usá el formato ZIP")
}

var pctRe = regexp.MustCompile(`(\d{1,3})%`)

// run7z ejecuta 7-Zip mostrando el avance.
func (c *Copier) run7z(dir string, args []string, totalBytes int64) error {
	exe, err := find7z(dataDir())
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(c.ctx, exe, args...)
	cmd.Dir = dir
	hideWindow(cmd)
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	base := c.prog.View().DoneBytes
	sc := bufio.NewScanner(stdout)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b == '\r' || b == '\n' || b == '\b' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	var out strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if mm := pctRe.FindAllStringSubmatch(line, -1); len(mm) > 0 {
			pct, _ := strconv.Atoi(mm[len(mm)-1][1])
			c.prog.mu.Lock()
			c.prog.DoneBytes = base + totalBytes*int64(pct)/100
			c.prog.mu.Unlock()
		} else if strings.TrimSpace(line) != "" {
			out.WriteString(line + "\n")
		}
	}
	err = cmd.Wait()
	c.prog.mu.Lock()
	c.prog.DoneBytes = base + totalBytes
	c.prog.mu.Unlock()
	if err != nil {
		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if len(msg) > 600 {
			msg = msg[len(msg)-600:]
		}
		return fmt.Errorf("7-Zip falló: %v %s", err, msg)
	}
	return nil
}

// Write7z crea un .7z en una carpeta temporal local y después lo sube/mueve al destino.
// groups: carpeta de trabajo -> rutas relativas a esa carpeta.
func (c *Copier) Write7z(dst FS, outPath string, groups map[string][]string, totalBytes int64, spec ArchiveSpec, tmpDir string) ([]string, error) {
	work, err := os.MkdirTemp(tmpDir, "7z-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	local := filepath.Join(work, filepath.Base(strings.ReplaceAll(outPath, "\\", "/")))
	dirs := make([]string, 0, len(groups))
	for d := range groups {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for i, d := range dirs {
		list := filepath.Join(work, fmt.Sprintf("lista%d.txt", i))
		if err := os.WriteFile(list, []byte(strings.Join(groups[d], "\n")+"\n"), 0o600); err != nil {
			return nil, err
		}
		args := []string{"a", "-t7z", "-bsp1", "-bso1", "-y", "-scsUTF-8", fmt.Sprintf("-mx=%d", clamp(spec.Level, 0, 9))}
		if spec.Password != "" {
			args = append(args, "-p"+spec.Password, "-mhe=on")
		}
		if spec.SplitBytes > 0 && len(dirs) == 1 {
			args = append(args, fmt.Sprintf("-v%db", spec.SplitBytes))
		}
		if spec.AbsPaths {
			args = append(args, "-spf2")
		}
		args = append(args, local, "@"+list)
		if err := c.run7z(d, args, totalBytes/int64(len(dirs))); err != nil {
			return nil, err
		}
	}
	// Partir después si hubo varios grupos.
	var locals []string
	if spec.SplitBytes > 0 && len(dirs) == 1 {
		locals = archiveParts(local + ".001")
	} else {
		locals = []string{local}
	}
	if spec.SplitBytes > 0 && len(dirs) > 1 {
		var err error
		locals, err = splitLocalFile(local, spec.SplitBytes)
		if err != nil {
			return nil, err
		}
	}
	if err := dst.MkdirAll(dst.Dir(outPath)); err != nil {
		return nil, err
	}
	var created []string
	c.prog.Set("Copiando el archivo 7z al destino")
	for _, lp := range locals {
		suffix := strings.TrimPrefix(filepath.Base(lp), filepath.Base(local))
		target := outPath + suffix
		st, _ := os.Stat(lp)
		if err := c.CopyFile(dst, lp, target, st, false); err != nil && !errors.Is(err, errChangedDuringCopy) {
			return created, err
		}
		created = append(created, target)
	}
	return created, nil
}

func splitLocalFile(p string, size int64) ([]string, error) {
	in, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	var parts []string
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s.%03d", p, i)
		out, err := os.Create(name)
		if err != nil {
			return nil, err
		}
		n, err := io.CopyN(out, in, size)
		out.Close()
		if n == 0 {
			os.Remove(name)
			break
		}
		parts = append(parts, name)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	os.Remove(p)
	return parts, nil
}

func Extract7z(ctx context.Context, archive, destDir, password string) error {
	exe, err := find7z(dataDir())
	if err != nil {
		return err
	}
	args := []string{"x", "-y", "-o" + destDir}
	args = append(args, "-p"+password)
	args = append(args, archive)
	cmd := exec.CommandContext(ctx, exe, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := string(out)
		if len(s) > 500 {
			s = s[len(s)-500:]
		}
		return fmt.Errorf("7-Zip: %v %s", err, s)
	}
	return nil
}

func Test7z(ctx context.Context, archive, password string) error {
	exe, err := find7z(dataDir())
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, "t", "-p"+password, archive)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := string(out)
		if len(s) > 500 {
			s = s[len(s)-500:]
		}
		return fmt.Errorf("7-Zip: %v %s", err, s)
	}
	return nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

package main

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var errChangedDuringCopy = errors.New("el archivo cambió mientras se copiaba (¿se está grabando?)")
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Progress es el estado de avance que ve la interfaz.
type Progress struct {
	mu         sync.Mutex
	Phase      string
	TotalFiles int
	TotalBytes int64
	DoneFiles  int
	DoneBytes  int64
	Current    string
	Started    time.Time
	lastBytes  int64
	lastTime   time.Time
	speed      float64
}

type ProgressView struct {
	Phase      string  `json:"phase"`
	TotalFiles int     `json:"totalFiles"`
	TotalBytes int64   `json:"totalBytes"`
	DoneFiles  int     `json:"doneFiles"`
	DoneBytes  int64   `json:"doneBytes"`
	Current    string  `json:"current"`
	Speed      float64 `json:"speed"` // bytes/s
	Elapsed    float64 `json:"elapsed"`
}

func (p *Progress) View() ProgressView {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if dt := now.Sub(p.lastTime).Seconds(); dt >= 1 {
		inst := float64(p.DoneBytes-p.lastBytes) / dt
		if p.speed == 0 {
			p.speed = inst
		} else {
			p.speed = p.speed*0.6 + inst*0.4
		}
		p.lastBytes, p.lastTime = p.DoneBytes, now
	}
	return ProgressView{Phase: p.Phase, TotalFiles: p.TotalFiles, TotalBytes: p.TotalBytes, DoneFiles: p.DoneFiles,
		DoneBytes: p.DoneBytes, Current: p.Current, Speed: p.speed, Elapsed: now.Sub(p.Started).Seconds()}
}

func (p *Progress) Set(phase string) {
	p.mu.Lock()
	p.Phase = phase
	p.mu.Unlock()
}

func (p *Progress) SetTotals(files int, bytes int64) {
	p.mu.Lock()
	p.TotalFiles, p.TotalBytes = files, bytes
	p.mu.Unlock()
}

func (p *Progress) AddTotals(files int, bytes int64) {
	p.mu.Lock()
	p.TotalFiles += files
	p.TotalBytes += bytes
	p.mu.Unlock()
}

func (p *Progress) FileDone() {
	p.mu.Lock()
	p.DoneFiles++
	p.mu.Unlock()
}

func (p *Progress) SetCurrent(s string) {
	p.mu.Lock()
	p.Current = s
	p.mu.Unlock()
}

func (p *Progress) addBytes(n int64) {
	p.mu.Lock()
	p.DoneBytes += n
	p.mu.Unlock()
}

// Copier copia respetando pausa, cancelación y límite de velocidad.
type Copier struct {
	ctx      context.Context
	paused   *atomic.Bool
	limit    func() int // MB/s, 0 = sin límite
	prog     *Progress
	settings Settings
	log      *Logger

	winStart time.Time
	winBytes int64
}

func (c *Copier) waitIfPaused() error {
	for c.paused != nil && c.paused.Load() {
		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return c.ctx.Err()
}

func (c *Copier) throttle(n int) {
	if c.limit == nil {
		return
	}
	mbps := c.limit()
	if mbps <= 0 {
		return
	}
	now := time.Now()
	if c.winStart.IsZero() || now.Sub(c.winStart) > time.Second {
		c.winStart, c.winBytes = now, 0
	}
	c.winBytes += int64(n)
	allowed := float64(mbps) * 1024 * 1024
	elapsed := now.Sub(c.winStart).Seconds()
	need := float64(c.winBytes) / allowed
	if need > elapsed {
		time.Sleep(time.Duration((need - elapsed) * float64(time.Second)))
	}
}

// Stream copia src a dst actualizando el avance. Si h != nil, calcula el hash.
func (c *Copier) Stream(dst io.Writer, src io.Reader, h hash.Hash) (int64, error) {
	buf := make([]byte, 1<<20)
	var total int64
	for {
		if err := c.waitIfPaused(); err != nil {
			return total, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if h != nil {
				h.Write(buf[:n])
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			if c.prog != nil {
				c.prog.addBytes(int64(n))
			}
			c.throttle(n)
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

// CopyFile copia un archivo local a un FS de destino de forma segura:
// escribe a un temporal y lo renombra, conserva fecha y atributos y, si se pide, verifica.
func (c *Copier) CopyFile(dst FS, srcPath, dstPath string, srcInfo fs.FileInfo, verify bool) error {
	attempt := func() error {
		in, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := dst.MkdirAll(dst.Dir(dstPath)); err != nil {
			return err
		}
		tmp := dstPath + ".rsgtmp"
		w, err := dst.Create(tmp)
		if err != nil {
			return err
		}
		var h hash.Hash32
		if verify {
			h = crc32.New(crcTable)
		}
		var hh hash.Hash
		if h != nil {
			hh = h
		}
		_, cerr := c.Stream(w, in, hh)
		werr := w.Close()
		if cerr == nil {
			cerr = werr
		}
		if cerr != nil {
			_ = dst.Remove(tmp)
			return cerr
		}
		if dst.Kind() == "ftp" {
			_ = dst.Remove(dstPath)
		}
		if err := dst.Rename(tmp, dstPath); err != nil {
			_ = dst.Remove(dstPath)
			if err2 := dst.Rename(tmp, dstPath); err2 != nil {
				_ = dst.Remove(tmp)
				return fmt.Errorf("no se pudo renombrar el temporal: %w", err2)
			}
		}
		c.applyMeta(dst, srcPath, dstPath, srcInfo)
		if verify && h != nil {
			r, err := dst.Open(dstPath)
			if err != nil {
				return fmt.Errorf("verificación: %w", err)
			}
			vh := crc32.New(crcTable)
			_, err = io.Copy(vh, r)
			r.Close()
			if err != nil {
				return fmt.Errorf("verificación: %w", err)
			}
			if vh.Sum32() != h.Sum32() {
				return fmt.Errorf("verificación fallida: la copia no coincide con el original")
			}
		}
		if st, err := os.Stat(srcPath); err == nil && srcInfo != nil {
			if st.Size() != srcInfo.Size() || !st.ModTime().Equal(srcInfo.ModTime()) {
				return errChangedDuringCopy
			}
		}
		return nil
	}
	err := attempt()
	if err != nil && dst.Kind() != "local" && c.settings.FTPReconnect && c.ctx.Err() == nil && !errors.Is(err, errChangedDuringCopy) {
		c.log.Warn("Error de conexión copiando %s (%v); se reconecta y se reintenta", srcPath, err)
		if rerr := dst.Reconnect(); rerr == nil {
			err = attempt()
		}
	}
	return err
}

func (c *Copier) applyMeta(dst FS, srcPath, dstPath string, srcInfo fs.FileInfo) {
	if srcInfo == nil {
		return
	}
	report := func(what string, err error) {
		if err != nil && !c.settings.IgnoreAttrErrors {
			c.log.Warn("No se pudo copiar %s de %s: %v", what, dstPath, err)
		}
	}
	if c.settings.CopyTimestamps || true {
		// La fecha siempre se conserva: el versionado depende de ella.
		if err := dst.Chtimes(dstPath, srcInfo.ModTime()); err != nil && dst.Kind() == "local" {
			report("la fecha", err)
		}
	}
	if dst.Kind() != "local" {
		return
	}
	if c.settings.CopyAttributes {
		report("los atributos", copyAttributes(srcPath, dstPath, srcInfo))
	}
	if c.settings.CopyPermissions {
		report("los permisos", copyPermissions(srcPath, dstPath))
	}
}

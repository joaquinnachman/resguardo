package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type LogLine struct {
	N     int64  `json:"n"`
	Time  string `json:"t"`
	Level string `json:"l"` // info | aviso | error | detalle
	Msg   string `json:"m"`
}

type Logger struct {
	mu       sync.Mutex
	dir      string
	perDay   bool
	ring     []LogLine
	next     int64
	errCount int
	detailed bool
	onError  func()
}

func NewLogger(dir string, perDay bool) *Logger {
	return &Logger{dir: dir, perDay: perDay}
}

func (l *Logger) fileName(t time.Time) string {
	if l.perDay {
		return filepath.Join(l.dir, "registro "+t.Format("2006-01-02")+".txt")
	}
	return filepath.Join(l.dir, "registro.txt")
}

func (l *Logger) write(level, msg string) {
	now := time.Now()
	l.mu.Lock()
	l.next++
	line := LogLine{N: l.next, Time: now.Format("2006-01-02 15:04:05"), Level: level, Msg: msg}
	l.ring = append(l.ring, line)
	if len(l.ring) > 3000 {
		l.ring = l.ring[len(l.ring)-3000:]
	}
	if level == "error" {
		l.errCount++
	}
	cb := l.onError
	tag := map[string]string{"info": "INFO ", "aviso": "AVISO", "error": "ERROR", "detalle": "     "}[level]
	f, err := os.OpenFile(l.fileName(now), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintf(f, "%s %s %s\r\n", line.Time, tag, msg)
		f.Close()
	}
	l.mu.Unlock()
	if level == "error" && cb != nil {
		cb()
	}
}

func (l *Logger) Info(format string, a ...any)  { l.write("info", fmt.Sprintf(format, a...)) }
func (l *Logger) Warn(format string, a ...any)  { l.write("aviso", fmt.Sprintf(format, a...)) }
func (l *Logger) Error(format string, a ...any) { l.write("error", fmt.Sprintf(format, a...)) }
func (l *Logger) Detail(format string, a ...any) {
	l.mu.Lock()
	d := l.detailed
	l.mu.Unlock()
	if d {
		l.write("detalle", fmt.Sprintf(format, a...))
	}
}

func (l *Logger) SetDetailed(d bool) { l.mu.Lock(); l.detailed = d; l.mu.Unlock() }
func (l *Logger) SetPerDay(d bool)   { l.mu.Lock(); l.perDay = d; l.mu.Unlock() }

func (l *Logger) Since(n int64) []LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []LogLine
	for _, ln := range l.ring {
		if ln.N > n {
			out = append(out, ln)
		}
	}
	return out
}

func (l *Logger) ClearView() {
	l.mu.Lock()
	l.ring = nil
	l.mu.Unlock()
}

func (l *Logger) ErrorCount() int { l.mu.Lock(); defer l.mu.Unlock(); return l.errCount }
func (l *Logger) ResetErrors()    { l.mu.Lock(); l.errCount = 0; l.mu.Unlock() }

func (l *Logger) Files() []string {
	entries, _ := os.ReadDir(l.dir)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "registro") && strings.HasSuffix(e.Name(), ".txt") {
			out = append(out, filepath.Join(l.dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

func (l *Logger) DeleteOlderThan(days int) {
	if days <= 0 {
		return
	}
	limit := time.Now().AddDate(0, 0, -days)
	for _, f := range l.Files() {
		if st, err := os.Stat(f); err == nil && st.ModTime().Before(limit) {
			_ = os.Remove(f)
		}
	}
}

func (l *Logger) DeleteAll() {
	for _, f := range l.Files() {
		_ = os.Remove(f)
	}
	l.ClearView()
}

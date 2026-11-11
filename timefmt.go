package main

import (
	"fmt"
	"strings"
	"time"
)

var mesesLargos = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
var mesesCortos = []string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
var diasLargos = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}
var diasCortos = []string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}

// formatTS da formato a una fecha con los códigos de Cobian (yyyy, MM, dd, HH, mm, ss, etc.).
func formatTS(t time.Time, layout string) string {
	if layout == "" {
		layout = "yyyy-MM-dd HH;mm;ss"
	}
	var b strings.Builder
	i := 0
	for i < len(layout) {
		c := layout[i]
		j := i
		for j < len(layout) && layout[j] == c {
			j++
		}
		n := j - i
		switch c {
		case 'y':
			y := t.Year()
			switch {
			case n <= 2:
				fmt.Fprintf(&b, "%02d", y%100)
			case n == 3:
				fmt.Fprintf(&b, "%03d", y%1000)
			case n == 4:
				fmt.Fprintf(&b, "%04d", y)
			default:
				fmt.Fprintf(&b, "%05d", y)
			}
		case 'M':
			m := int(t.Month())
			switch n {
			case 1:
				fmt.Fprintf(&b, "%d", m)
			case 2:
				fmt.Fprintf(&b, "%02d", m)
			case 3:
				b.WriteString(mesesCortos[m-1])
			default:
				b.WriteString(mesesLargos[m-1])
			}
		case 'd':
			switch n {
			case 1:
				fmt.Fprintf(&b, "%d", t.Day())
			case 2:
				fmt.Fprintf(&b, "%02d", t.Day())
			case 3:
				b.WriteString(diasCortos[t.Weekday()])
			default:
				b.WriteString(diasLargos[t.Weekday()])
			}
		case 'H':
			if n == 1 {
				fmt.Fprintf(&b, "%d", t.Hour())
			} else {
				fmt.Fprintf(&b, "%02d", t.Hour())
			}
		case 'h':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			if n == 1 {
				fmt.Fprintf(&b, "%d", h)
			} else {
				fmt.Fprintf(&b, "%02d", h)
			}
		case 'm':
			if n == 1 {
				fmt.Fprintf(&b, "%d", t.Minute())
			} else {
				fmt.Fprintf(&b, "%02d", t.Minute())
			}
		case 's':
			if n == 1 {
				fmt.Fprintf(&b, "%d", t.Second())
			} else {
				fmt.Fprintf(&b, "%02d", t.Second())
			}
		case 'f':
			ms := t.Nanosecond() / 1e6
			switch n {
			case 1:
				fmt.Fprintf(&b, "%d", ms/100)
			case 2:
				fmt.Fprintf(&b, "%02d", ms/10)
			default:
				fmt.Fprintf(&b, "%03d", ms)
			}
		case 't':
			ampm := "AM"
			if t.Hour() >= 12 {
				ampm = "PM"
			}
			if n == 1 {
				b.WriteByte(ampm[0])
			} else {
				b.WriteString(ampm)
			}
		default:
			b.WriteString(layout[i:j])
		}
		i = j
	}
	return sanitizeName(b.String())
}

// Marca de tiempo fija para instantáneas de proyectos (se ordena alfabéticamente).
const snapLayout = "2006-01-02 15.04.05"

func snapName(t time.Time) string { return t.Format(snapLayout) }

func parseSnap(s string) (time.Time, bool) {
	t, err := time.ParseInLocation(snapLayout, s, time.Local)
	return t, err == nil
}

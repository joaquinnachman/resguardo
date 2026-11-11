package main

import (
	"strconv"
	"strings"
	"time"
)

func parseHM(s string) (int, int) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return clamp(h, 0, 23), clamp(m, 0, 59)
}

func atHM(day time.Time, hm string) time.Time {
	h, m := parseHM(hm)
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, day.Location())
}

func daysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.Local).Day()
}

// NextRun calcula la próxima ejecución programada a partir de "from".
// timerBase es el punto desde donde cuenta el temporizador.
func NextRun(s Schedule, from, timerBase, lastRun time.Time) time.Time {
	switch s.Kind {
	case "unavez":
		d, err := time.ParseInLocation("2006-01-02", s.Date, time.Local)
		if err != nil {
			return time.Time{}
		}
		t := atHM(d, s.Time)
		if !lastRun.IsZero() && !lastRun.Before(t) {
			return time.Time{} // ya se ejecutó
		}
		if t.Before(from.Add(-time.Minute)) && lastRun.IsZero() {
			return t // perdida: se ejecuta en cuanto se pueda
		}
		return t
	case "diario":
		t := atHM(from, s.Time)
		if !t.After(from) {
			t = atHM(from.AddDate(0, 0, 1), s.Time)
		}
		return t
	case "semanal":
		days := s.Weekdays
		if len(days) == 0 {
			days = []int{1}
		}
		for i := 0; i <= 7; i++ {
			d := from.AddDate(0, 0, i)
			for _, wd := range days {
				if int(d.Weekday()) == wd {
					t := atHM(d, s.Time)
					if t.After(from) {
						return t
					}
				}
			}
		}
	case "mensual":
		for i := 0; i <= 12; i++ {
			mfirst := time.Date(from.Year(), from.Month()+time.Month(i), 1, 0, 0, 0, 0, from.Location())
			day := s.MonthDay
			if day <= 0 {
				day = 1
			}
			if dim := daysIn(mfirst.Year(), mfirst.Month()); day > dim {
				day = dim
			}
			t := atHM(time.Date(mfirst.Year(), mfirst.Month(), day, 0, 0, 0, 0, from.Location()), s.Time)
			if t.After(from) {
				return t
			}
		}
	case "anual":
		for i := 0; i <= 4; i++ {
			y := from.Year() + i
			m := time.Month(clamp(s.Month, 1, 12))
			day := s.MonthDay
			if day <= 0 {
				day = 1
			}
			if dim := daysIn(y, m); day > dim {
				day = dim
			}
			t := atHM(time.Date(y, m, day, 0, 0, 0, 0, from.Location()), s.Time)
			if t.After(from) {
				return t
			}
		}
	case "temporizador":
		every := s.EveryMinutes
		if every <= 0 {
			every = 60
		}
		t := timerBase.Add(time.Duration(every) * time.Minute)
		for !t.After(from) {
			t = t.Add(time.Duration(every) * time.Minute)
		}
		return t
	}
	return time.Time{}
}

func inWindow(s Schedule, now time.Time) bool {
	if s.WindowFrom == "" || s.WindowTo == "" {
		return true
	}
	cur := now.Format("15:04")
	fh, fm := parseHM(s.WindowFrom)
	th, tm := parseHM(s.WindowTo)
	from := pad2(fh) + ":" + pad2(fm)
	to := pad2(th) + ":" + pad2(tm)
	if from <= to {
		return cur >= from && cur <= to
	}
	return cur >= from || cur <= to // franja que cruza la medianoche
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

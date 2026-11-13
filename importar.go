package main

// Importación de listas de tareas de otros programas de respaldo.
//
// Cobian Backup 11 (Gravity) guarda sus listas en
// "C:\Program Files (x86)\Cobian Backup 11\DB\*.lst": texto (UTF-16 o UTF-8)
// con un bloque por tarea entre "{11//" y "//11}", y líneas "Clave=Valor".
// Las listas (orígenes, destino, exclusiones, eventos) van como CommaText de
// Delphi: elementos separados por comas, entre comillas si hace falta, y cada
// elemento es a su vez "tipo,ruta[,más campos]". Fecha y hora van como los 64
// bits de un TDateTime de Delphi escritos con ceros y unos.
//
// Cobian Reflector guarda "C:\Program Files\Cobian Reflector\Lists\*.lst":
// texto UTF-16 con secciones "<§- id -§> ... <§§- id -§§>" de líneas
// "Clave=Valor"; un valor "{ *§* id *§* }" apunta a otra sección (orígenes,
// destino, horario, filtros). La primera sección enumera las tareas.
//
// Las contraseñas (cifrado, FTP, ejecutar como) nunca se importan.

import (
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

type ImportedTask struct {
	Task     Task     `json:"task"`
	Warnings []string `json:"warnings"`
	Existing string   `json:"existing,omitempty"` // nombre de una tarea importada antes desde el mismo lugar
}

type ImportPreview struct {
	Program string         `json:"program"`
	File    string         `json:"file"`
	Tasks   []ImportedTask `json:"tasks"`
}

type FoundList struct {
	Path     string    `json:"path"`
	Program  string    `json:"program"`
	Tasks    int       `json:"tasks"`
	Modified time.Time `json:"modified"`
	Error    string    `json:"error,omitempty"`
}

// ---------- Texto ----------

// decodeText: UTF-16 (con o sin BOM), UTF-8 o, si no es UTF-8 válido, Windows-1252.
func decodeText(b []byte) string {
	u16 := func(b []byte, be bool) string {
		n := len(b) / 2
		units := make([]uint16, n)
		for i := 0; i < n; i++ {
			if be {
				units[i] = binary.BigEndian.Uint16(b[2*i:])
			} else {
				units[i] = binary.LittleEndian.Uint16(b[2*i:])
			}
		}
		return string(utf16.Decode(units))
	}
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return u16(b[2:], false)
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return u16(b[2:], true)
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	}
	// Sin BOM: si casi todos los bytes impares son 0, es UTF-16 LE.
	if len(b) >= 8 {
		zeros := 0
		for i := 1; i < len(b) && i < 400; i += 2 {
			if b[i] == 0 {
				zeros++
			}
		}
		if zeros*2 > min(len(b), 400)/2 {
			return u16(b, false)
		}
	}
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
		if c >= 0x80 && c < 0xA0 {
			r[i] = cp1252[c-0x80]
		}
	}
	return string(r)
}

var cp1252 = [32]rune{'€', '\u0081', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '\u008d', 'Ž', '\u008f',
	'\u0090', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '\u009d', 'ž', 'Ÿ'}

// splitCommaText separa un TStrings.CommaText de Delphi.
func splitCommaText(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		var b strings.Builder
		if s[i] == '"' {
			i++
			for i < len(s) {
				if s[i] == '"' {
					if i+1 < len(s) && s[i+1] == '"' {
						b.WriteByte('"')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(s[i])
				i++
			}
			// lo que quede hasta la coma (no debería haber nada)
			for i < len(s) && s[i] != ',' {
				i++
			}
		} else {
			for i < len(s) && s[i] != ',' {
				b.WriteByte(s[i])
				i++
			}
		}
		out = append(out, strings.TrimSpace(b.String()))
		if i < len(s) && s[i] == ',' {
			i++
			if i == len(s) {
				out = append(out, "")
			}
		}
	}
	return out
}

// cobianItems devuelve los elementos de una lista "tipo,valor,..." de Cobian 11.
func cobianItems(v string) [][]string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	outer := splitCommaText(v)
	var items [][]string
	for i := 0; i < len(outer); i++ {
		it := outer[i]
		if strings.Contains(it, ",") || strings.Contains(it, `"`) {
			items = append(items, splitCommaText(it))
			continue
		}
		// Lista sin comillas: "1,C:\Dir,1,D:\Otra"
		if isDigits(it) && i+1 < len(outer) {
			items = append(items, []string{it, outer[i+1]})
			i++
			continue
		}
		if it != "" {
			items = append(items, []string{"", it})
		}
	}
	return items
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ---------- Claves ----------

type kvList []struct{ K, V string }

func normKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	return strings.Join(strings.Fields(k), " ")
}

func (l kvList) get(keys ...string) (string, bool) {
	for _, want := range keys {
		w := normKey(want)
		for _, kv := range l {
			if normKey(kv.K) == w {
				return kv.V, true
			}
		}
	}
	return "", false
}

func (l kvList) all(key string) []string {
	var out []string
	w := normKey(key)
	for _, kv := range l {
		if normKey(kv.K) == w {
			out = append(out, kv.V)
		}
	}
	return out
}

func (l kvList) str(keys ...string) string {
	v, _ := l.get(keys...)
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' && !strings.Contains(v[1:len(v)-1], `"`) {
		v = v[1 : len(v)-1]
	}
	return v
}

func (l kvList) flag(def bool, keys ...string) bool {
	v, ok := l.get(keys...)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "-1":
		return true
	case "false", "0", "no":
		return false
	}
	return def
}

func (l kvList) num(def int, keys ...string) int {
	v, ok := l.get(keys...)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		f, err2 := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err2 != nil {
			return def
		}
		return int(f)
	}
	return n
}

// findKey busca una clave que contenga todas las palabras (para claves de
// Cobian Reflector cuyo nombre exacto no conocemos).
func (l kvList) findKey(words ...string) (string, bool) {
	for _, kv := range l {
		k := strings.ToLower(kv.K)
		ok := true
		for _, w := range words {
			if !strings.Contains(k, strings.ToLower(w)) {
				ok = false
				break
			}
		}
		if ok {
			return kv.V, true
		}
	}
	return "", false
}

// ---------- Fechas ----------

// delphiTime: TDateTime como 64 ceros y unos (Cobian 11), como número, o como
// "AAAA-MM-DD HH:MM:SS[:mmm]" (Cobian Reflector).
func delphiTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if len(v) == 64 && strings.Trim(v, "01") == "" {
		bits, err := strconv.ParseUint(v, 2, 64)
		if err != nil {
			return time.Time{}, false
		}
		return fromDelphiDays(math.Float64frombits(bits))
	}
	if f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64); err == nil {
		return fromDelphiDays(f)
	}
	if len(v) >= 16 {
		s := v
		if len(s) > 19 {
			s = s[:19]
		}
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05"} {
			if t, err := time.ParseInLocation(layout, s[:min(len(s), len(layout))], time.Local); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func fromDelphiDays(f float64) (time.Time, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f < -1e6 || f > 1e7 {
		return time.Time{}, false
	}
	days := math.Floor(f)
	frac := math.Abs(f - days)
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.Local)
	t := base.AddDate(0, 0, int(days))
	t = t.Add(time.Duration(math.Round(frac*86400)) * time.Second)
	return t, true
}

// weekdaysFrom: días de la semana de Cobian a 0=domingo..6=sábado. Si viene
// algún 0 se toma como 0=domingo; si no, como Delphi (1=domingo..7=sábado).
func weekdaysFrom(vals []string) []int {
	var nums []int
	for _, v := range vals {
		for _, m := range reDigits.FindAllString(v, -1) {
			n, _ := strconv.Atoi(m)
			nums = append(nums, n)
		}
	}
	zeroBased := false
	for _, n := range nums {
		if n == 0 {
			zeroBased = true
		}
	}
	seen := map[int]bool{}
	var out []int
	for _, n := range nums {
		if !zeroBased {
			n--
		}
		if n >= 0 && n <= 6 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// ---------- Detección ----------

var reDigits = regexp.MustCompile(`\d+`)
var c11Open = regexp.MustCompile(`^\{(\d+)//\s*$`)
var c11Close = regexp.MustCompile(`^//(\d+)\}\s*$`)

func parseForeignList(name string, data []byte) (ImportPreview, error) {
	text := decodeText(data)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	p := ImportPreview{File: name}
	switch {
	case strings.Contains(text, "<§-"):
		p.Program = "Cobian Reflector"
		tasks, err := parseReflector(text)
		if err != nil {
			return p, err
		}
		p.Tasks = tasks
	case hasCobian11Blocks(text):
		p.Program = "Cobian Backup 11"
		p.Tasks = parseCobian11(text)
	default:
		return p, fmt.Errorf("no reconozco este archivo: tiene que ser una lista de tareas de Cobian Backup 11 o de Cobian Reflector (.lst)")
	}
	if len(p.Tasks) == 0 {
		return p, fmt.Errorf("la lista no tiene tareas")
	}
	return p, nil
}

func hasCobian11Blocks(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		if c11Open.MatchString(strings.TrimSpace(l)) {
			return true
		}
	}
	return false
}

// ---------- Cobian Backup 11 ----------

func parseCobian11(text string) []ImportedTask {
	var out []ImportedTask
	var cur kvList
	in := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case c11Open.MatchString(t):
			in, cur = true, nil
		case c11Close.MatchString(t):
			if in && len(cur) > 0 {
				out = append(out, convertCobian11(cur))
			}
			in = false
		case in:
			if k, v, ok := strings.Cut(line, "="); ok {
				cur = append(cur, struct{ K, V string }{strings.TrimSpace(k), strings.TrimRight(v, " \t")})
			}
		}
	}
	return out
}

func convertCobian11(l kvList) ImportedTask {
	var w []string
	warn := func(f string, a ...any) { w = append(w, fmt.Sprintf(f, a...)) }
	t := NewTask()
	t.Name = l.str("Name")
	if t.Name == "" {
		t.Name = "Tarea de Cobian"
	}
	id := l.str("Id")
	if id == "" {
		id = t.Name
	}
	t.ImportedFrom = "cobian11:" + id
	t.Group = l.str("Group")
	t.Enabled = l.flag(true, "Enabled")
	t.IncludeSubdirs = l.flag(true, "Include subdirectories")
	t.Separated = l.flag(true, "Create separated backups")
	t.UseVSS = l.flag(false, "Use VSC", "Use Volume Shadow Copy", "Use VSS")
	if l.flag(false, "Use attributes", "Use file attributes", "Use archive attribute") {
		warn("En Cobian detectaba los cambios con el atributo de archivo. Resguardo compara fecha y tamaño, así los dos programas no se pisan.")
	}
	t.UseArchiveAttr = false
	t.ClearArchiveAttr = l.flag(true, "Clear archive attribute")

	switch l.num(1, "Backup type") {
	case 0:
		t.Type = TypeFull
	case 2:
		t.Type = TypeDiff
	case 3:
		t.Type = TypeDummy
	default:
		t.Type = TypeIncr
	}
	switch l.num(0, "Priority") {
	case 1:
		t.Priority = 1
	case 2:
		t.Priority = -1
	default:
		t.Priority = 0
	}

	src, _ := l.get("Source")
	t.Sources = cobianLocations(src, "origen", &w)
	dst, _ := l.get("Destination")
	t.Destinations = cobianLocations(dst, "destino", &w)
	if len(t.Sources) == 0 {
		warn("No tiene orígenes que se puedan importar.")
	}
	if len(t.Destinations) == 0 && t.Type != TypeDummy {
		warn("No tiene destinos que se puedan importar.")
	}

	t.Schedule = cobian11Schedule(l, &w)

	t.FullCopiesToKeep = max(0, l.num(t.FullCopiesToKeep, "Full copies", "Full copies to keep"))
	t.DiffCopiesToKeep = max(0, l.num(0, "Differental copies", "Differential copies", "Differential copies to keep"))
	t.FullEvery = max(0, l.num(0, "One full every", "Make full backup every"))
	t.FixedFullWeekday = -1
	if l.flag(false, "Use fixed day") {
		if d := weekdaysFrom([]string{l.str("Fixed day")}); len(d) > 0 {
			t.FixedFullWeekday = d[0]
		}
	}

	switch l.num(0, "Compression", "Compression type") {
	case 0:
		t.Compression = "ninguna"
	case 2:
		t.Compression, t.Format = "separado", "7z"
	default:
		t.Compression, t.Format = "separado", "zip"
	}
	if t.Compression != "ninguna" && l.flag(false, "Compress individually") {
		t.Compression = "individual"
	}
	if split := l.num(0, "Split"); split != 0 && t.Compression != "ninguna" {
		if sz, _ := strconv.ParseInt(strings.TrimSpace(l.str("Custom size")), 10, 64); sz > 0 {
			t.SplitMB = int(sz / (1 << 20))
		}
		if t.SplitMB <= 0 {
			t.SplitMB = 4300
		}
		warn("En Cobian el archivo comprimido se dividía en partes. Revisá el tamaño de cada parte (quedó en %d MB).", t.SplitMB)
	}
	t.Comment = l.str("Comment")
	if strings.EqualFold(t.Comment, "Cobian Backup 11 Gravity") {
		t.Comment = ""
	}
	if l.num(0, "Encryption", "Encryption type") != 0 {
		t.Encrypt = true
		warn("Estaba cifrada. La contraseña no se importa: ponela en la tarea (Compresión y cifrado) antes de activarla.")
	}

	inc, _ := l.get("Inclusions")
	t.Include = cobianFilters(inc, "inclusión", &w, &t)
	exc, _ := l.get("Exclusions")
	t.Exclude = cobianFilters(exc, "exclusión", &w, &t)

	for _, ev := range []struct{ key, label string }{{"Pre backup events", "antes"}, {"Post backup events", "después"}} {
		v, _ := l.get(ev.key, strings.Replace(ev.key, " backup", "-backup", 1))
		if items := cobianItems(v); len(items) > 0 {
			var desc []string
			for _, it := range items {
				var parts []string
				for _, p := range it {
					if p != "" && !isDigits(p) && !strings.EqualFold(p, "true") && !strings.EqualFold(p, "false") {
						parts = append(parts, p)
					}
				}
				if len(parts) > 0 {
					desc = append(desc, strings.Join(parts, " "))
				}
			}
			msg := fmt.Sprintf("Tenía %d acción(es) %s del respaldo que no se importan; agregalas en Eventos si las necesitás", len(items), ev.label)
			if len(desc) > 0 {
				msg += ": " + strings.Join(desc, " · ")
			}
			warn(msg + ".")
		}
	}
	t.CancelIfPreFails = l.flag(true, "Abort if pre-event fails")
	t.Mirror = l.flag(false, "Mirror")
	t.AbsolutePaths = l.flag(false, "Absolute paths")
	t.TypeInName = l.flag(true, "Include backup type")
	t.IgnoreEmptyDirs = l.flag(false, "Delete empty directories")
	if l.flag(false, "Impersonate") {
		t.RunAs = RunAs{Enabled: true, User: l.str("Run as User name"), Domain: l.str("Run as Domain"), CancelIfFails: l.flag(false, "Abort if impersonation fails")}
		warn("Corría como otro usuario (%s). La contraseña no se importa: ponela en la tarea.", t.RunAs.User)
	}
	fillSlices(&t)
	if w == nil {
		w = []string{}
	}
	return ImportedTask{Task: t, Warnings: w}
}

// cobianLocations: "tipo,ruta" con tipo 0 = archivo, 1 = carpeta, 2 = FTP.
func cobianLocations(v, what string, w *[]string) []Location {
	var out []Location
	for _, it := range cobianItems(v) {
		kind, path := "", ""
		if len(it) >= 2 {
			kind, path = strings.TrimSpace(it[0]), strings.TrimSpace(it[1])
		} else if len(it) == 1 {
			path = strings.TrimSpace(it[0])
		}
		if path == "" {
			continue
		}
		if loc, ok := urlLocation(path); ok {
			out = append(out, loc)
			*w = append(*w, fmt.Sprintf("El %s %s es remoto: la contraseña no se importa, ponela en la tarea.", what, locText(loc)))
			continue
		}
		switch kind {
		case "0":
			out = append(out, Location{Kind: "local", Path: path, ForceFile: true})
		case "1", "":
			out = append(out, Location{Kind: "local", Path: path})
		default:
			*w = append(*w, fmt.Sprintf("El %s %q es de un tipo que no se pudo importar (FTP u otro): agregalo a mano.", what, path))
		}
	}
	return out
}

func locText(l Location) string {
	if l.Kind == "ftp" || l.Kind == "sftp" {
		u := l.Kind + "://"
		if l.User != "" {
			u += l.User + "@"
		}
		return u + l.Host + l.Path
	}
	return l.Path
}

func urlLocation(s string) (Location, bool) {
	low := strings.ToLower(s)
	if !strings.HasPrefix(low, "ftp://") && !strings.HasPrefix(low, "sftp://") && !strings.HasPrefix(low, "ftps://") {
		return Location{}, false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Location{}, false
	}
	l := Location{Kind: "ftp", Host: u.Hostname(), Path: u.Path, Passive: true}
	if strings.HasPrefix(low, "sftp://") {
		l.Kind = "sftp"
	}
	if strings.HasPrefix(low, "ftps://") {
		l.TLS = "implicito"
	}
	if p, err := strconv.Atoi(u.Port()); err == nil {
		l.Port = p
	}
	if u.User != nil {
		l.User = u.User.Username()
	}
	if l.Path == "" {
		l.Path = "/"
	}
	return l, true
}

// cobianFilters: "tipo,valor,..." con tipo 0 = archivo, 1 = carpeta, 2 = máscara.
// Los filtros por tamaño o fecha no tienen equivalente directo.
func cobianFilters(v, what string, w *[]string, t *Task) []FilterItem {
	var out []FilterItem
	for _, it := range cobianItems(v) {
		kind, val := "", ""
		if len(it) >= 2 {
			kind, val = strings.TrimSpace(it[0]), strings.TrimSpace(it[1])
		} else if len(it) == 1 {
			val = strings.TrimSpace(it[0])
		}
		if val == "" {
			*w = append(*w, fmt.Sprintf("Tenía una %s por tamaño o fecha que no se importa: revisá los filtros.", what))
			continue
		}
		switch {
		case strings.ContainsAny(val, "*?") || !strings.ContainsAny(val, `\/`):
			out = append(out, FilterItem{Kind: "mascara", Value: val})
		case kind == "0":
			out = append(out, FilterItem{Kind: "archivo", Value: val})
		case kind == "1":
			out = append(out, FilterItem{Kind: "carpeta", Value: val})
		case filepath.Ext(val) != "" && !strings.HasSuffix(val, `\`):
			out = append(out, FilterItem{Kind: "archivo", Value: val})
		default:
			out = append(out, FilterItem{Kind: "carpeta", Value: val})
		}
	}
	return out
}

func cobian11Schedule(l kvList, w *[]string) Schedule {
	s := NewTask().Schedule
	at, ok := delphiTime(l.str("Date/Time", "Date time"))
	if ok {
		s.Time = at.Format("15:04")
		s.Date = at.Format("2006-01-02")
	}
	if dm := l.num(0, "Day of the month"); dm >= 1 && dm <= 31 {
		s.MonthDay = dm
	}
	if m := l.num(0, "Month"); m >= 1 && m <= 12 {
		s.Month = m
	}
	switch l.num(1, "Schedule type") {
	case 0:
		s.Kind = "unavez"
		if !ok {
			s.Kind = "manual"
			*w = append(*w, "No se pudo leer la fecha de la ejecución única: quedó manual.")
		}
	case 1:
		s.Kind = "diario"
	case 2:
		s.Kind = "semanal"
		s.Weekdays = weekdaysFrom(l.all("Day of the week"))
		if len(s.Weekdays) == 0 {
			s.Weekdays = []int{1}
			*w = append(*w, "No se pudieron leer los días de la semana: quedó los lunes.")
		}
	case 3:
		s.Kind = "mensual"
		if l.flag(false, "Select days of the week") {
			*w = append(*w, fmt.Sprintf("En Cobian era un día de la semana del mes (por ejemplo, el primer lunes); acá quedó el día %d de cada mes.", s.MonthDay))
		}
	case 4:
		s.Kind = "anual"
	case 5:
		s.Kind = "temporizador"
		if m := l.num(0, "Timer"); m > 0 {
			s.EveryMinutes = m
		}
		from, ok1 := delphiTime(l.str("Timer from"))
		to, ok2 := delphiTime(l.str("Timer to"))
		if ok1 && ok2 {
			f, t := from.Format("15:04"), to.Format("15:04")
			if !(f == "00:00" && (t == "23:59" || t == "00:00")) {
				s.WindowFrom, s.WindowTo = f, t
			}
		}
	case 6:
		s.Kind = "manual"
	case 7:
		s.Kind = "inicio"
	default:
		s.Kind = "manual"
		*w = append(*w, "No se reconoció el horario: quedó manual.")
	}
	if s.Weekdays == nil {
		s.Weekdays = []int{}
	}
	return s
}

// ---------- Cobian Reflector ----------

type rfDoc struct {
	sections map[string]kvList
	root     string
}

func refID(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") {
		return "", false
	}
	v = strings.TrimSpace(v[1 : len(v)-1])
	if !strings.HasPrefix(v, "*§*") || !strings.HasSuffix(v, "*§*") || len(v) < 6 {
		return "", false
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(v, "*§*"), "*§*")), true
}

func (d rfDoc) children(l kvList, key string) []kvList {
	var out []kvList
	for _, v := range l.all(key) {
		if id, ok := refID(v); ok {
			if s, ok := d.sections[id]; ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func parseReflector(text string) ([]ImportedTask, error) {
	d := rfDoc{sections: map[string]kvList{}}
	var curID string
	var cur kvList
	in := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "<§§-") && strings.HasSuffix(t, "-§§>"):
			id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(t, "<§§-"), "-§§>"))
			if in && id == curID {
				d.sections[curID] = cur
			}
			in = false
		case strings.HasPrefix(t, "<§-") && strings.HasSuffix(t, "-§>"):
			curID = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(t, "<§-"), "-§>"))
			if d.root == "" {
				d.root = curID
			}
			cur, in = nil, true
		case in && !strings.HasPrefix(t, "§"):
			if k, v, ok := strings.Cut(line, "="); ok {
				cur = append(cur, struct{ K, V string }{strings.TrimSpace(k), v})
			}
		}
	}
	root, ok := d.sections[d.root]
	if !ok {
		return nil, fmt.Errorf("el archivo no es una lista de tareas de Cobian Reflector")
	}
	var out []ImportedTask
	for _, ts := range d.children(root, "BackupTask") {
		out = append(out, convertReflector(d, ts))
	}
	return out, nil
}

func convertReflector(d rfDoc, l kvList) ImportedTask {
	var w []string
	warn := func(f string, a ...any) { w = append(w, fmt.Sprintf(f, a...)) }
	t := NewTask()
	t.Name = l.str("TaskName")
	if t.Name == "" {
		t.Name = "Tarea de Cobian"
	}
	id := l.str("TaskId")
	if id == "" {
		id = t.Name
	}
	t.ImportedFrom = "reflector:" + id
	t.Group = l.str("TaskGroup")
	t.Enabled = l.flag(true, "TaskEnabled")
	t.IncludeSubdirs = l.flag(true, "TaskIncludeSubdirectories")
	t.IgnoreEmptyDirs = l.flag(false, "TaskIgnoreEmptyDirectories")
	if v, ok := l.findKey("separat"); ok {
		t.Separated = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := l.findKey("shadow"); ok {
		t.UseVSS = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := l.findKey("mirror"); ok && !strings.Contains(v, "*§*") {
		t.Mirror = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := l.findKey("absolute"); ok {
		t.AbsolutePaths = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := l.findKey("attribute"); ok && strings.EqualFold(strings.TrimSpace(v), "true") {
		warn("En Cobian detectaba los cambios con el atributo de archivo. Resguardo compara fecha y tamaño, así los dos programas no se pisan.")
	}
	t.UseArchiveAttr = false

	switch l.num(1, "TaskBackupType") {
	case 0:
		t.Type = TypeFull
	case 2:
		t.Type = TypeDiff
	case 3:
		t.Type = TypeDummy
	default:
		t.Type = TypeIncr
	}

	locs := func(key, what string) []Location {
		var out []Location
		for _, s := range d.children(l, key) {
			path := strings.TrimSpace(s.str("SDPath"))
			if path == "" {
				continue
			}
			if loc, ok := urlLocation(path); ok {
				out = append(out, loc)
				warn("El %s %s es remoto: la contraseña no se importa, ponela en la tarea.", what, locText(loc))
				continue
			}
			switch s.num(1, "SDKind") {
			case 1:
				out = append(out, Location{Kind: "local", Path: path})
			case 0:
				out = append(out, Location{Kind: "local", Path: path, ForceFile: true})
			default:
				warn("El %s %q es de un tipo que no se pudo importar (FTP u otro): agregalo a mano.", what, path)
			}
		}
		return out
	}
	t.Sources = locs("TaskSource", "origen")
	t.Destinations = locs("TaskDestination", "destino")
	if len(t.Sources) == 0 {
		warn("No tiene orígenes que se puedan importar.")
	}
	if len(t.Destinations) == 0 && t.Type != TypeDummy {
		warn("No tiene destinos que se puedan importar.")
	}

	t.Schedule = NewTask().Schedule
	if sch := d.children(l, "TaskSchedule"); len(sch) > 0 {
		s := sch[0]
		if at, ok := delphiTime(s.str("SchDateAndTime")); ok {
			t.Schedule.Time = at.Format("15:04")
			t.Schedule.Date = at.Format("2006-01-02")
		}
		if dm := s.num(0, "SchDayOfMonth"); dm >= 1 && dm <= 31 {
			t.Schedule.MonthDay = dm
		}
		if m := s.num(0, "SchMonth"); m >= 1 && m <= 12 {
			t.Schedule.Month = m
		}
		switch s.num(1, "SchSchedule") {
		case 0:
			t.Schedule.Kind = "unavez"
		case 1:
			t.Schedule.Kind = "diario"
		case 2:
			t.Schedule.Kind = "semanal"
			var days []int
			for _, v := range s.all("SchDaysOfWeek") {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 && n <= 6 {
					days = append(days, n)
				}
			}
			sort.Ints(days)
			t.Schedule.Weekdays = days
			if len(days) == 0 {
				t.Schedule.Weekdays = []int{1}
				warn("No se pudieron leer los días de la semana: quedó los lunes.")
			}
		case 3:
			t.Schedule.Kind = "mensual"
		case 4:
			t.Schedule.Kind = "anual"
		case 5:
			t.Schedule.Kind = "temporizador"
			if m := s.num(0, "SchTimer"); m > 0 {
				t.Schedule.EveryMinutes = m
			}
		case 6:
			t.Schedule.Kind = "manual"
		default:
			t.Schedule.Kind = "manual"
			warn("No se reconoció el horario: quedó manual.")
		}
	} else {
		t.Schedule.Kind = "manual"
		warn("No tenía horario: quedó manual.")
	}
	if t.Schedule.Weekdays == nil {
		t.Schedule.Weekdays = []int{}
	}

	t.FullCopiesToKeep = max(0, l.num(t.FullCopiesToKeep, "TaskFullCopiesToKeep"))
	if v, ok := l.findKey("differential", "keep"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			t.DiffCopiesToKeep = n
		}
	}
	t.FullEvery = max(0, l.num(0, "TaskForceFullBackupCount"))
	t.FixedFullWeekday = -1
	if l.flag(false, "TaskFixedFullBackup") {
		if d := l.num(-1, "TaskFixedFullBackupDay"); d >= 0 && d <= 6 {
			t.FixedFullWeekday = d
		}
	}
	if v, ok := l.findKey("compress"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			t.Compression, t.Format = "separado", "zip"
			if n == 2 {
				t.Format = "7z"
			}
			warn("En Cobian se comprimía. Revisá el formato y la forma de comprimir en la tarea.")
		}
	}
	if v, ok := l.findKey("encrypt"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); (err == nil && n > 0) || strings.EqualFold(strings.TrimSpace(v), "true") {
			t.Encrypt = true
			warn("Estaba cifrada. La contraseña no se importa: ponela en la tarea (Compresión y cifrado) antes de activarla.")
		}
	}

	for _, f := range d.children(l, "TaskIncludeFilters") {
		if fi, ok := reflectorFilter(f, "inclusión", &w); ok {
			t.Include = append(t.Include, fi)
		}
	}
	for _, f := range d.children(l, "TaskExcludeFilters") {
		if fi, ok := reflectorFilter(f, "exclusión", &w); ok {
			t.Exclude = append(t.Exclude, fi)
		}
	}
	for _, k := range []string{"TaskPreBackupEvents", "TaskPostBackupEvents", "TaskPreBackupEvent", "TaskPostBackupEvent"} {
		if n := len(l.all(k)); n > 0 {
			when := "antes"
			if strings.Contains(k, "Post") {
				when = "después"
			}
			warn("Tenía %d acción(es) %s del respaldo que no se importan; agregalas en Eventos si las necesitás.", n, when)
		}
	}
	fillSlices(&t)
	if w == nil {
		w = []string{}
	}
	return ImportedTask{Task: t, Warnings: w}
}

func reflectorFilter(f kvList, what string, w *[]string) (FilterItem, bool) {
	mask := strings.TrimSpace(f.str("FOMask"))
	if mask == "" {
		*w = append(*w, fmt.Sprintf("Tenía una %s por tamaño o fecha que no se importa: revisá los filtros.", what))
		return FilterItem{}, false
	}
	switch {
	case strings.ContainsAny(mask, "*?") || !strings.ContainsAny(mask, `\/`):
		return FilterItem{Kind: "mascara", Value: mask}, true
	case filepath.Ext(mask) != "" && !strings.HasSuffix(mask, `\`):
		return FilterItem{Kind: "archivo", Value: mask}, true
	default:
		return FilterItem{Kind: "carpeta", Value: mask}, true
	}
}

// ---------- Buscar listas en la computadora ----------

func findForeignLists() []FoundList {
	if !isWindows() {
		return []FoundList{}
	}
	var bases []string
	for _, v := range []string{"ProgramFiles(x86)", "ProgramFiles", "ProgramW6432", "ProgramData", "APPDATA", "LOCALAPPDATA"} {
		if p := os.Getenv(v); p != "" {
			bases = append(bases, p)
		}
	}
	bases = append(bases, `C:\Program Files (x86)`, `C:\Program Files`, `C:\ProgramData`)
	seen := map[string]bool{}
	out := []FoundList{}
	for _, b := range bases {
		dirs, _ := filepath.Glob(filepath.Join(b, "Cobian*"))
		for _, d := range dirs {
			for _, sub := range []string{"DB", "Lists", "Settings", ""} {
				files, _ := filepath.Glob(filepath.Join(d, sub, "*.lst"))
				for _, f := range files {
					key := strings.ToLower(f)
					if seen[key] {
						continue
					}
					seen[key] = true
					st, err := os.Stat(f)
					if err != nil || st.IsDir() || st.Size() > 32<<20 {
						continue
					}
					fl := FoundList{Path: f, Modified: st.ModTime(), Program: filepath.Base(d)}
					if data, err := os.ReadFile(f); err != nil {
						fl.Error = "no se pudo leer: " + err.Error()
					} else if p, err := parseForeignList(filepath.Base(f), data); err != nil {
						fl.Error = err.Error()
					} else {
						fl.Program, fl.Tasks = p.Program, len(p.Tasks)
					}
					out = append(out, fl)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		mi := strings.EqualFold(filepath.Base(out[i].Path), "MainList.lst")
		mj := strings.EqualFold(filepath.Base(out[j].Path), "MainList.lst")
		if mi != mj {
			return mi
		}
		return out[i].Modified.After(out[j].Modified)
	})
	return out
}

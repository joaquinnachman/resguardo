package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// ExtRef es un archivo que usa el proyecto y que está fuera de su carpeta.
type ExtRef struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	MTime   time.Time `json:"mtime"`
	Library bool      `json:"library"` // contenido de fábrica / packs (se puede reinstalar)
}

type RefReport struct {
	Supported bool     `json:"supported"`
	Total     int      `json:"total"`
	Internal  int      `json:"internal"`
	External  []ExtRef `json:"external"`
	Missing   []string `json:"missing"`
	Library   int      `json:"library"`
	Note      string   `json:"note"`
}

var audioExts = map[string]bool{".wav": true, ".aif": true, ".aiff": true, ".mp3": true, ".flac": true, ".ogg": true,
	".w64": true, ".rf64": true, ".caf": true, ".m4a": true, ".aac": true, ".mid": true, ".midi": true, ".mxf": true, ".bwf": true, ".sd2": true}

func isAudioName(n string) bool { return audioExts[strings.ToLower(filepath.Ext(n))] }

func pathInside(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	if runtime.GOOS != "linux" {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
	}
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

func looksLibrary(p string) bool {
	l := strings.ToLower(filepath.ToSlash(p))
	return strings.Contains(l, "/ableton/factory packs/") || strings.Contains(l, "/core library/") ||
		strings.Contains(l, "/ableton/live packs/") || strings.Contains(l, "/steinberg/content/") ||
		strings.Contains(l, "/vstsounds/") || strings.Contains(l, ".vstsound")
}

// AnalyzeProject lee el archivo de proyecto y clasifica los archivos que usa.
func AnalyzeProject(p Project, searchFolders []string) RefReport {
	r := analyzeProject(p, searchFolders)
	if r.External == nil {
		r.External = []ExtRef{}
	}
	if r.Missing == nil {
		r.Missing = []string{}
	}
	return r
}

func analyzeProject(p Project, searchFolders []string) RefReport {
	switch p.DAW {
	case "ableton":
		return analyzeALS(filepath.Join(p.Dir, p.Main), p.Dir, p.Files)
	case "cubase":
		return analyzeByNames(p, cprNames(filepath.Join(p.Dir, p.Main)), searchFolders,
			"Cubase guarda sus proyectos en un formato cerrado: la búsqueda de audio externo es aproximada.")
	case "protools":
		names, err := ptxAudioNames(filepath.Join(p.Dir, p.Main))
		if err != nil {
			return RefReport{Supported: false, Note: "No se pudo leer la sesión de Pro Tools: " + err.Error()}
		}
		return analyzeByNamesOpt(p, names, searchFolders, "", true)
	}
	return RefReport{Supported: false, Note: "Para este programa se respalda la carpeta completa del proyecto."}
}

// ---------- Ableton (.als = XML comprimido con gzip) ----------

type alsRef struct {
	path, relPath, name, data, pack string
	relParts, hintParts             []string
	folder                          bool
}

func openMaybeGzip(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(f)
	head, _ := br.Peek(2)
	if len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			f.Close()
			return nil, err
		}
		return struct {
			io.Reader
			io.Closer
		}{zr, f}, nil
	}
	return struct {
		io.Reader
		io.Closer
	}{br, f}, nil
}

func parseALSRefs(path string) ([]alsRef, error) {
	rc, err := openMaybeGzip(path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	dec.Strict = false
	var refs []alsRef
	var cur *alsRef
	var stack []string
	capData := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(refs) > 0 {
				break
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if name == "FileRef" && cur == nil {
				cur = &alsRef{}
				stack = []string{"FileRef"}
				continue
			}
			if cur == nil {
				continue
			}
			stack = append(stack, name)
			parent := stack[len(stack)-2]
			val := attr(t, "Value")
			switch {
			case name == "Path" && parent == "FileRef":
				cur.path = val
			case name == "RelativePath" && parent == "FileRef":
				cur.relPath = val
			case name == "RelativePathElement" && parent == "RelativePath":
				cur.relParts = append(cur.relParts, attr(t, "Dir"))
			case name == "RelativePathElement" && parent == "PathHint":
				cur.hintParts = append(cur.hintParts, attr(t, "Dir"))
			case name == "Name" && parent == "FileRef":
				cur.name = val
			case name == "LivePackName":
				cur.pack = val
			case name == "RefersToFolder":
				cur.folder = strings.EqualFold(val, "true")
			case name == "Data" && parent == "FileRef":
				capData = true
			}
		case xml.CharData:
			if capData && cur != nil {
				cur.data += strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			if cur == nil {
				continue
			}
			if t.Name.Local == "Data" {
				capData = false
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				refs = append(refs, *cur)
				cur = nil
			}
		}
	}
	return refs, nil
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func decodeUTF16Hex(h string) string {
	b, err := hex.DecodeString(strings.Join(strings.Fields(h), ""))
	if err != nil || len(b) < 6 || len(b)%2 != 0 {
		return ""
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	s := strings.TrimRight(string(utf16.Decode(u)), "\x00")
	if (len(s) > 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/')) || strings.HasPrefix(s, `\\`) {
		return s
	}
	return ""
}

func (r alsRef) candidates(projectDir string) []string {
	var c []string
	if r.path != "" {
		c = append(c, filepath.FromSlash(r.path))
	}
	if r.relPath != "" {
		c = append(c, filepath.Join(projectDir, filepath.FromSlash(r.relPath)))
	}
	if r.name != "" {
		if len(r.relParts) > 0 {
			parts := append([]string{projectDir}, r.relParts...)
			c = append(c, filepath.Join(append(parts, r.name)...))
		}
		if len(r.hintParts) > 0 {
			hp := append([]string{}, r.hintParts...)
			var base string
			if len(hp[0]) <= 2 && strings.TrimSuffix(hp[0], ":") != "" && len(strings.TrimSuffix(hp[0], ":")) == 1 {
				base = strings.TrimSuffix(hp[0], ":") + ":" + string(filepath.Separator)
				hp = hp[1:]
			} else {
				base = string(filepath.Separator)
			}
			c = append(c, filepath.Join(append(append([]string{base}, hp...), r.name)...))
			if runtime.GOOS == "windows" {
				// en Windows, Live 9/10 a veces no guarda la unidad
				c = append(c, filepath.Join(append(append([]string{`C:\`}, hp...), r.name)...))
			}
		}
	}
	if s := decodeUTF16Hex(r.data); s != "" {
		c = append(c, s)
	}
	return c
}

func analyzeALS(alsPath, projectDir string, projectFiles []string) RefReport {
	refs, err := parseALSRefs(alsPath)
	if err != nil {
		return RefReport{Supported: false, Note: "No se pudo leer el .als: " + err.Error()}
	}
	// Se consideran todos los .als de la carpeta (versiones del mismo tema).
	for _, f := range projectFiles {
		full := filepath.Join(projectDir, f)
		if full == alsPath {
			continue
		}
		if more, err := parseALSRefs(full); err == nil {
			refs = append(refs, more...)
		}
	}
	rep := RefReport{Supported: true}
	seen := map[string]bool{}
	for _, r := range refs {
		if r.folder {
			continue
		}
		cands := r.candidates(projectDir)
		if len(cands) == 0 {
			continue
		}
		key := strings.ToLower(cands[0])
		if seen[key] {
			continue
		}
		seen[key] = true
		rep.Total++
		found := ""
		var st os.FileInfo
		for _, c := range cands {
			if s, err := os.Stat(c); err == nil && !s.IsDir() {
				found, st = c, s
				break
			}
		}
		if found == "" {
			rep.Missing = append(rep.Missing, cands[0])
			continue
		}
		if abs, err := filepath.Abs(found); err == nil {
			found = abs
		}
		if pathInside(found, projectDir) {
			rep.Internal++
			continue
		}
		lib := r.pack != "" || looksLibrary(found)
		if lib {
			rep.Library++
		}
		rep.External = append(rep.External, ExtRef{Path: found, Size: st.Size(), MTime: st.ModTime(), Library: lib})
	}
	return rep
}

// ---------- Búsqueda por nombre (Pro Tools / Cubase) ----------

type nameIndex struct {
	built time.Time
	roots string
	m     map[string][]string
}

var searchIdxMu sync.Mutex
var searchIdx nameIndex

func indexFolders(folders []string) map[string][]string {
	searchIdxMu.Lock()
	defer searchIdxMu.Unlock()
	key := strings.Join(folders, "|")
	if searchIdx.m != nil && searchIdx.roots == key && time.Since(searchIdx.built) < 10*time.Minute {
		return searchIdx.m
	}
	m := map[string][]string{}
	for _, root := range folders {
		_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !fi.IsDir() && isAudioName(fi.Name()) {
				k := strings.ToLower(fi.Name())
				m[k] = append(m[k], p)
			}
			return nil
		})
	}
	searchIdx = nameIndex{built: time.Now(), roots: key, m: m}
	return m
}

// analyzeByNames recibe nombres o rutas de audio y los busca en la carpeta del proyecto y fuera.
func analyzeByNames(p Project, cands []string, searchFolders []string, note string) RefReport {
	return analyzeByNamesOpt(p, cands, searchFolders, note, false)
}

func analyzeByNamesOpt(p Project, cands []string, searchFolders []string, note string, allAudio bool) RefReport {
	rep := RefReport{Supported: true, Note: note}
	inProject := map[string]bool{}
	_ = filepath.Walk(p.Dir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			inProject[strings.ToLower(fi.Name())] = true
		}
		return nil
	})
	var dirs []string
	var names []string
	for _, c := range cands {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if allAudio || isAudioName(c) {
			names = append(names, c)
		} else if looksAbsDir(c) {
			dirs = append(dirs, c)
		}
	}
	var idx map[string][]string
	seen := map[string]bool{}
	for _, n := range names {
		base := filepath.Base(strings.ReplaceAll(n, `\`, "/"))
		k := strings.ToLower(base)
		if seen[k] {
			continue
		}
		seen[k] = true
		rep.Total++
		if looksAbsPath(n) {
			if st, err := os.Stat(n); err == nil {
				if pathInside(n, p.Dir) {
					rep.Internal++
				} else {
					rep.External = append(rep.External, ExtRef{Path: n, Size: st.Size(), MTime: st.ModTime(), Library: looksLibrary(n)})
				}
				continue
			}
		}
		if inProject[k] {
			rep.Internal++
			continue
		}
		found := ""
		for _, d := range dirs {
			cand := filepath.Join(d, base)
			if _, err := os.Stat(cand); err == nil {
				found = cand
				break
			}
		}
		if found == "" && len(searchFolders) > 0 {
			if idx == nil {
				idx = indexFolders(searchFolders)
			}
			if l := idx[k]; len(l) > 0 {
				found = l[0]
			}
		}
		if found == "" {
			rep.Missing = append(rep.Missing, base)
			continue
		}
		if st, err := os.Stat(found); err == nil {
			rep.External = append(rep.External, ExtRef{Path: found, Size: st.Size(), MTime: st.ModTime(), Library: looksLibrary(found)})
		}
	}
	return rep
}

func looksAbsPath(s string) bool {
	return (len(s) > 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/')) || strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, "/")
}

func looksAbsDir(s string) bool {
	if !looksAbsPath(s) || len(s) > 400 {
		return false
	}
	st, err := os.Stat(s)
	return err == nil && st.IsDir()
}

// ---------- Cubase (.cpr): búsqueda de cadenas ----------

func cprNames(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	add := func(s string) {
		if c := cleanCandidate(strings.TrimSpace(s)); c != "" {
			out = append(out, c)
		}
	}
	// ASCII / UTF-8
	start := -1
	for i := 0; i <= len(b); i++ {
		printable := i < len(b) && (b[i] >= 0x20 && b[i] != 0x7f)
		if printable {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && i-start >= 5 {
			add(string(b[start:i]))
		}
		start = -1
	}
	// UTF-16LE
	for off := 0; off < 2; off++ {
		var cur []uint16
		for i := off; i+1 < len(b); i += 2 {
			u := binary.LittleEndian.Uint16(b[i:])
			if u >= 0x20 && u < 0xd800 {
				cur = append(cur, u)
				continue
			}
			if len(cur) >= 5 {
				add(string(utf16.Decode(cur)))
			}
			cur = cur[:0]
		}
	}
	// UTF-16BE (Cubase usa big endian en partes del formato)
	for off := 0; off < 2; off++ {
		var cur []uint16
		for i := off; i+1 < len(b); i += 2 {
			u := binary.BigEndian.Uint16(b[i:])
			if u >= 0x20 && u < 0xd800 && u != 0x2020 {
				cur = append(cur, u)
				continue
			}
			if len(cur) >= 5 {
				add(string(utf16.Decode(cur)))
			}
			cur = cur[:0]
		}
	}
	// Las cadenas suelen venir pegadas a bytes del formato: se recorta a la parte útil.
	// Se descartan duplicados y recortes (lecturas desalineadas que pierden la primera letra).
	seen := map[string]bool{}
	var uniq []string
	for _, c := range out {
		if !seen[c] {
			seen[c] = true
			uniq = append(uniq, c)
		}
	}
	var cleaned []string
	for _, c := range uniq {
		dup := false
		for _, o := range uniq {
			if o != c && len(o) > len(c) && strings.HasSuffix(o, c) {
				dup = true
				break
			}
		}
		if !dup {
			cleaned = append(cleaned, c)
		}
	}
	return cleaned
}

var absStartRe = regexp.MustCompile(`[A-Za-z]:[\\/]|\\\\[^\\]|/Users/|/Volumes/|/Applications/|/Library/|/tmp/|/home/|/mnt/|/media/`)
var fileTailRe = regexp.MustCompile(`[\p{L}\p{N} _\-\.\(\)\[\]&+,'!#@~]+$`)

func cleanCandidate(s string) string {
	if loc := absStartRe.FindStringIndex(s); loc != nil {
		return strings.TrimSpace(s[loc[0]:])
	}
	if isAudioName(s) {
		t := strings.TrimLeft(fileTailRe.FindString(s), " .-")
		// si el primer carácter es una letra rara pegada (byte del formato), se descarta
		if r := []rune(t); len(r) > 1 && r[0] > 0x2FF {
			t = string(r[1:])
		}
		if len(t) >= 5 {
			return t
		}
	}
	return ""
}

// ---------- Pro Tools (.ptx / .ptf / .pts) ----------
// Basado en el trabajo de ingeniería inversa de ptformat (Damien Zammit, LGPL).

func ptxUnxor(b []byte) ([]byte, error) {
	if len(b) < 0x14 {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, len(b))
	copy(out, b)
	xorType := b[0x12]
	xorValue := b[0x13]
	var delta uint8
	gen := func(mul uint8, negative bool) uint8 {
		for i := 0; i < 256; i++ {
			if uint8(i)*mul == xorValue {
				if negative {
					return uint8(-i)
				}
				return uint8(i)
			}
		}
		return 0
	}
	switch xorType {
	case 0x01:
		delta = gen(53, false)
	case 0x05:
		delta = gen(11, true)
	default:
		return nil, errUnknownPT
	}
	var key [256]byte
	for i := 0; i < 256; i++ {
		key[i] = uint8(i) * delta
	}
	for i := 0x14; i < len(out); i++ {
		var idx int
		if xorType == 0x01 {
			idx = i & 0xff
		} else {
			idx = (i >> 12) & 0xff
		}
		out[i] = b[i] ^ key[idx]
	}
	return out, nil
}

type ptBlock struct {
	typ, content uint16
	size, offset uint32
	child        []ptBlock
}

type ptParser struct {
	b   []byte
	big bool
}

func (p *ptParser) u16(pos uint32) uint16 {
	if p.big {
		return binary.BigEndian.Uint16(p.b[pos:])
	}
	return binary.LittleEndian.Uint16(p.b[pos:])
}

func (p *ptParser) u32(pos uint32) uint32 {
	if p.big {
		return binary.BigEndian.Uint32(p.b[pos:])
	}
	return binary.LittleEndian.Uint32(p.b[pos:])
}

func (p *ptParser) parseAt(pos uint32, max uint32, level int) (ptBlock, bool) {
	var blk ptBlock
	if pos+9 > uint32(len(p.b)) || p.b[pos] != 0x5a || level > 40 {
		return blk, false
	}
	blk.typ = p.u16(pos + 1)
	blk.size = p.u32(pos + 3)
	blk.content = p.u16(pos + 7)
	blk.offset = pos + 7
	if uint64(blk.size)+uint64(blk.offset) > uint64(max) || blk.typ&0xff00 != 0 {
		return blk, false
	}
	own := blk.offset + blk.size
	jump := uint32(0)
	for i := uint32(1); i < blk.size && pos+i+jump < max; {
		jump = 0
		if c, ok := p.parseAt(pos+i, own, level+1); ok {
			blk.child = append(blk.child, c)
			jump = c.size + 7
		}
		if jump != 0 {
			i += jump
		} else {
			i++
		}
	}
	return blk, true
}

type errStr string

func (e errStr) Error() string { return string(e) }

const errUnknownPT = errStr("formato de sesión de Pro Tools desconocido")

func ptxAudioNames(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, err := ptxUnxor(raw)
	if err != nil {
		return nil, err
	}
	if b[0] != 0x03 && !bytes.Contains(b[:min(len(b), 0x100)], []byte("0010111100101011")) {
		return nil, errUnknownPT
	}
	p := &ptParser{b: b, big: b[0x11] != 0}
	var blocks []ptBlock
	for i := uint32(20); i < uint32(len(b)); {
		blk, ok := p.parseAt(i, uint32(len(b)), 0)
		if ok {
			blocks = append(blocks, blk)
			i += blk.size + 7
		} else {
			i++
		}
	}
	var names []string
	for _, blk := range blocks {
		if blk.content != 0x1004 || blk.offset+6 > uint32(len(b)) {
			continue
		}
		nwavs := p.u32(blk.offset + 2)
		for _, c := range blk.child {
			if c.content != 0x103a {
				continue
			}
			pos := c.offset + 11
			end := c.offset + c.size
			for n := uint32(0); pos+4 < end && n < nwavs; {
				l := p.u32(pos)
				if l > 4096 || pos+4+l+4 > uint32(len(b)) {
					break
				}
				name := string(b[pos+4 : pos+4+l])
				pos += 4 + l
				wtype := string(b[pos : pos+4])
				pos += 9
				if strings.Contains(name, ".grp") || name == "Audio Files" || name == "Fade Files" {
					continue
				}
				ok := strings.Contains(wtype, "WAVE") || strings.Contains(wtype, "EVAW") || strings.Contains(wtype, "AIFF") ||
					strings.Contains(wtype, "FFIA") || strings.Contains(wtype, "fFXM") || strings.Contains(wtype, "MXFf")
				if !ok && wtype[0] == 0 {
					ln := strings.ToLower(name)
					ok = strings.HasSuffix(ln, ".wav") || strings.HasSuffix(ln, ".aif") || strings.HasSuffix(ln, ".aiff") || strings.HasSuffix(ln, ".mxf")
				}
				if !ok {
					continue
				}
				names = append(names, name)
				n++
			}
		}
	}
	return names, nil
}

func atoiSafe(s string) int { n, _ := strconv.Atoi(s); return n }

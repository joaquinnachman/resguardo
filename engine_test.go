package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func testEnv(t *testing.T) (*Store, *Logger, *Engine, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	store, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	log := NewLogger(filepath.Join(store.Dir, "registro"), true)
	log.SetDetailed(true)
	e := NewEngine(store, log)
	return store, log, e, root
}

func writeFile(t *testing.T, p string, data []byte, mt time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !mt.IsZero() {
		os.Chtimes(p, mt, mt)
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func run(t *testing.T, e *Engine, taskID string, force bool, project string) {
	t.Helper()
	e.execute(&Job{ID: newID(), TaskID: taskID, Force: force, Project: project, Reason: "prueba", Kind: "tarea"})
}

func treeOf(t *testing.T, dir string) map[string][]byte {
	out := map[string][]byte{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		b, _ := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	return out
}

func dumpLog(t *testing.T, l *Logger) {
	for _, ln := range l.Since(0) {
		t.Logf("[%s] %s", ln.Level, ln.Msg)
	}
}

func TestClassicIncrementalAndRestore(t *testing.T) {
	store, log, e, root := testEnv(t)
	src := filepath.Join(root, "Sesiones")
	base := time.Now().Add(-time.Hour)
	writeFile(t, filepath.Join(src, "a.txt"), []byte("hola"), base)
	writeFile(t, filepath.Join(src, "sub", "b.wav"), randBytes(3<<20), base)
	writeFile(t, filepath.Join(src, "sub", "cache.asd"), []byte("x"), base)
	dst := filepath.Join(root, "Backup")
	task := NewTask()
	task.Name = "Sesiones"
	task.Type = TypeIncr
	task.Sources = []Location{{Kind: "local", Path: src}}
	task.Destinations = []Location{{Kind: "local", Path: dst}}
	task.FullCopiesToKeep = 2
	task.Projects.SmartExclusions = true
	store.PutTask(task)

	run(t, e, task.ID, false, "")
	h := store.History(task.ID)
	if len(h) != 1 || h[0].Type != TypeFull || h[0].Status != "ok" {
		dumpLog(t, log)
		t.Fatalf("primer respaldo: %+v", h)
	}
	firstFull := h[0].Outputs[0].Path
	full := treeOf(t, firstFull)
	if _, ok := full["Sesiones/sub/b.wav"]; !ok || len(full) != 2 {
		t.Fatalf("contenido del completo: %v", keys(full))
	}

	// cambio + archivo nuevo -> incremental
	writeFile(t, filepath.Join(src, "a.txt"), []byte("hola de nuevo"), base.Add(10*time.Minute))
	writeFile(t, filepath.Join(src, "c.txt"), []byte("nuevo"), base.Add(10*time.Minute))
	time.Sleep(1100 * time.Millisecond)
	run(t, e, task.ID, false, "")
	h = store.History(task.ID)
	if len(h) != 2 || h[1].Type != TypeIncr || h[1].Files != 2 {
		dumpLog(t, log)
		t.Fatalf("incremental: %+v", h[len(h)-1])
	}
	inc := treeOf(t, h[1].Outputs[0].Path)
	if len(inc) != 2 || string(inc["Sesiones/a.txt"]) != "hola de nuevo" {
		t.Fatalf("contenido del incremental: %v", keys(inc))
	}
	// sin cambios -> no se crea nada
	run(t, e, task.ID, false, "")
	if len(store.History(task.ID)) != 2 {
		t.Fatalf("no debería crear un respaldo vacío")
	}
	// restaurar la cadena
	target := filepath.Join(root, "Restaurado")
	n, err := RestoreRecord(context.Background(), store, task, h[1].ID, 0, target, "", &Progress{}, log)
	if err != nil {
		t.Fatal(err)
	}
	got := treeOf(t, target)
	if n != 4 || string(got["Sesiones/a.txt"]) != "hola de nuevo" || got["Sesiones/c.txt"] == nil || got["Sesiones/sub/b.wav"] == nil {
		t.Fatalf("restauración: %d %v", n, keys(got))
	}
	// retención: dos completos más -> se borra el primero y su incremental
	time.Sleep(1100 * time.Millisecond)
	run(t, e, task.ID, true, "")
	time.Sleep(1100 * time.Millisecond)
	run(t, e, task.ID, true, "")
	h = store.History(task.ID)
	fulls := 0
	for _, r := range h {
		if r.Type == TypeFull {
			fulls++
		}
	}
	if fulls != 2 || len(h) != 2 {
		t.Fatalf("retención: %d completos, %d registros", fulls, len(h))
	}
	if _, err := os.Stat(firstFull); err == nil {
		t.Fatalf("el completo viejo debería haberse borrado")
	}
	entries, _ := os.ReadDir(dst)
	if len(entries) != 2 {
		t.Fatalf("en el destino quedaron %d carpetas", len(entries))
	}
}

func keys(m map[string][]byte) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func TestArchivesAndEncryption(t *testing.T) {
	store, log, e, root := testEnv(t)
	src := filepath.Join(root, "Mezclas")
	base := time.Now().Add(-time.Hour)
	big := randBytes(2<<20 + 12345)
	writeFile(t, filepath.Join(src, "Tema 1 – master.wav"), big, base)
	writeFile(t, filepath.Join(src, "notas.txt"), bytes.Repeat([]byte("mezcla "), 5000), base)
	cases := []struct {
		name, comp, format string
		enc                bool
		split              int
	}{
		{"zip-global", "global", "zip", false, 0},
		{"zip-aes-partido", "global", "zip", true, 1},
		{"zip-separado", "separado", "zip", false, 0},
		{"zip-individual", "individual", "zip", true, 0},
		{"7z-global-aes", "global", "7z", true, 0},
		{"7z-partido", "global", "7z", false, 1},
		{"cifrado-sin-comprimir", "ninguna", "zip", true, 0},
	}
	s := store.GetSettings()
	s.TestArchives = true
	store.SaveSettings(s)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task := NewTask()
			task.Name = c.name
			task.Type = TypeFull
			task.Compression = c.comp
			task.Format = c.format
			task.Encrypt = c.enc
			task.Password = "clave secreta ñ"
			task.SplitMB = c.split
			task.Comment = "Resguardo prueba"
			task.Sources = []Location{{Path: src}}
			task.Destinations = []Location{{Path: filepath.Join(root, "Destino", c.name)}}
			store.PutTask(task)
			run(t, e, task.ID, false, "")
			h := store.History(task.ID)
			if len(h) != 1 || h[0].Status != "ok" {
				dumpLog(t, log)
				t.Fatalf("respaldo: %+v", h)
			}
			target := filepath.Join(root, "Rest", c.name)
			if _, err := RestoreRecord(context.Background(), store, task, h[0].ID, 0, target, "", &Progress{}, log); err != nil {
				dumpLog(t, log)
				t.Fatal(err)
			}
			got := treeOf(t, target)
			if !bytes.Equal(got["Mezclas/Tema 1 – master.wav"], big) {
				t.Fatalf("contenido restaurado distinto: %v", keys(got))
			}
		})
	}
}

func TestMirrorSafe(t *testing.T) {
	store, log, e, root := testEnv(t)
	src := filepath.Join(root, "Src")
	base := time.Now().Add(-time.Hour)
	writeFile(t, filepath.Join(src, "uno.txt"), []byte("1"), base)
	writeFile(t, filepath.Join(src, "dos.txt"), []byte("2"), base)
	task := NewTask()
	task.Name = "Espejo"
	task.Type = TypeFull
	task.Separated = false
	task.Mirror = true
	task.Sources = []Location{{Path: src}}
	dst := filepath.Join(root, "Dst")
	task.Destinations = []Location{{Path: dst}}
	store.PutTask(task)
	run(t, e, task.ID, false, "")
	os.Remove(filepath.Join(src, "dos.txt"))
	run(t, e, task.ID, false, "")
	if _, err := os.Stat(filepath.Join(dst, "Espejo", "Src", "dos.txt")); err == nil {
		dumpLog(t, log)
		t.Fatal("el espejo no quitó el archivo borrado")
	}
	matches, _ := filepath.Glob(filepath.Join(dst, "_Espejo seguro (Espejo)", "*", "Src", "dos.txt"))
	if len(matches) != 1 {
		dumpLog(t, log)
		t.Fatal("el archivo no quedó apartado en el espejo seguro")
	}
}

func TestProjectsVersioning(t *testing.T) {
	store, log, e, root := testEnv(t)
	src := filepath.Join(root, "Proyectos")
	base := time.Now().Add(-48 * time.Hour)
	// Ableton
	abl := filepath.Join(src, "Motel", "Tema 1 Project")
	voz := randBytes(1 << 20)
	writeFile(t, filepath.Join(abl, "Samples", "Recorded", "voz.wav"), voz, base)
	writeFile(t, filepath.Join(abl, "Samples", "Recorded", "voz.wav.asd"), []byte("cache"), base)
	ext := filepath.Join(root, "Libreria", "kick.wav")
	writeFile(t, ext, []byte("RIFF-kick"), base)
	alsXML := func(extra string) string {
		return `<?xml version="1.0"?><Ableton><LiveSet>
<SampleRef><FileRef><RelativePath Value="Samples/Recorded/voz.wav"/><Path Value="` + filepath.ToSlash(filepath.Join(abl, "Samples/Recorded/voz.wav")) + `"/><LivePackName Value=""/></FileRef></SampleRef>
<SampleRef><FileRef><RelativePath Value=""/><Path Value="` + filepath.ToSlash(ext) + `"/><LivePackName Value=""/></FileRef></SampleRef>
` + extra + `</LiveSet></Ableton>`
	}
	writeALS(t, filepath.Join(abl, "Tema 1.als"), alsXML(""))
	os.Chtimes(filepath.Join(abl, "Tema 1.als"), base, base)
	// Pro Tools
	pt := filepath.Join(src, "Clientes", "Banda X")
	ptx, _ := os.ReadFile("testdata/pt/TestPTX.ptx")
	writeFile(t, filepath.Join(pt, "Banda X.ptx"), ptx, base)
	writeFile(t, filepath.Join(pt, "Audio Files", "monoTone.wav"), randBytes(5000), base)
	writeFile(t, filepath.Join(pt, "WaveCache.wfm"), []byte("cache"), base)
	lib := filepath.Join(root, "Samples PT")
	writeFile(t, filepath.Join(lib, "stereoTone.L.aif"), []byte("FORM-L"), base)
	// algo que no es proyecto
	writeFile(t, filepath.Join(src, "notas.txt"), []byte("nada"), base)

	s := store.GetSettings()
	s.SampleSearchFolders = []string{lib}
	store.SaveSettings(s)

	dst := filepath.Join(root, "Respaldo")
	task := NewTask()
	task.Name = "Proyectos"
	task.Type = TypeProjects
	task.Sources = []Location{{Path: src}}
	task.Destinations = []Location{{Path: dst}}
	task.Projects.KeepVersions = 0
	task.Projects.Thinning = false
	store.PutTask(task)

	run(t, e, task.ID, false, "")
	ablRoot := filepath.Join(dst, "Proyectos", "Motel", "Tema 1 Project")
	actual := treeOf(t, filepath.Join(ablRoot, "Actual"))
	extRel := "_Samples externos/" + relForAbsolute(ext)
	if actual["Tema 1.als"] == nil || !bytes.Equal(actual["Samples/Recorded/voz.wav"], voz) || actual[extRel] == nil {
		dumpLog(t, log)
		t.Fatalf("Actual de Ableton: %v", keys(actual))
	}
	if actual["Samples/Recorded/voz.wav.asd"] != nil {
		t.Fatalf("no debería copiar los .asd")
	}
	ptActual := treeOf(t, filepath.Join(dst, "Proyectos", "Clientes", "Banda X", "Actual"))
	if ptActual["Banda X.ptx"] == nil || ptActual["Audio Files/monoTone.wav"] == nil || ptActual["WaveCache.wfm"] != nil {
		t.Fatalf("Actual de Pro Tools: %v", keys(ptActual))
	}
	foundExt := false
	for k := range ptActual {
		if strings.HasSuffix(k, "stereoTone.L.aif") && strings.HasPrefix(k, dirExternos) {
			foundExt = true
		}
	}
	if !foundExt {
		dumpLog(t, log)
		t.Fatalf("Pro Tools: no encontró el audio externo: %v", keys(ptActual))
	}
	snaps1 := listSnapshots(LocalFS{}, ablRoot)
	if len(snaps1) != 1 {
		t.Fatalf("versiones: %v", snaps1)
	}
	// sin cambios: no crea versión nueva
	run(t, e, task.ID, false, "")
	if len(listSnapshots(LocalFS{}, ablRoot)) != 1 {
		t.Fatal("no debería crear una versión sin cambios")
	}
	// se guarda el proyecto con cambios y se borra una toma
	oldALS, _ := os.ReadFile(filepath.Join(abl, "Tema 1.als"))
	time.Sleep(1100 * time.Millisecond)
	writeALS(t, filepath.Join(abl, "Tema 1.als"), alsXML("<!-- cambio -->"))
	os.Chtimes(filepath.Join(abl, "Tema 1.als"), base.Add(time.Hour), base.Add(time.Hour))
	os.Remove(filepath.Join(abl, "Samples", "Recorded", "voz.wav"))
	run(t, e, task.ID, false, "Proyectos/Motel/Tema 1 Project")
	snaps := listSnapshots(LocalFS{}, ablRoot)
	if len(snaps) != 2 {
		dumpLog(t, log)
		t.Fatalf("versiones después del cambio: %v", snaps)
	}
	old := treeOf(t, filepath.Join(ablRoot, dirVersiones, snaps[1]))
	if !bytes.Equal(old["Tema 1.als"], oldALS) || !bytes.Equal(old["Samples/Recorded/voz.wav"], voz) {
		t.Fatalf("versiones anteriores: %v", keys(old))
	}
	// restaurar la primera versión completa
	target := filepath.Join(root, "Rest")
	dir, n, err := RestoreSnapshot(context.Background(), task, store.GetSettings(), "Proyectos/Motel/Tema 1 Project", 0, snaps[0], RestoreOptions{Target: target}, &Progress{}, log)
	if err != nil {
		t.Fatal(err)
	}
	got := treeOf(t, dir)
	if n != 3 || !bytes.Equal(got["Tema 1.als"], oldALS) || !bytes.Equal(got["Samples/Recorded/voz.wav"], voz) || got[extRel] == nil || got["LEEME - Resguardo.txt"] == nil {
		t.Fatalf("restauración v1 (%d): %v", n, keys(got))
	}
	// restaurar sólo el archivo de proyecto, al lado del original
	_, n, err = RestoreSnapshot(context.Background(), task, store.GetSettings(), "Proyectos/Motel/Tema 1 Project", 0, snaps[0], RestoreOptions{OnlyProjectFiles: true}, &Progress{}, log)
	if err != nil || n != 1 {
		t.Fatalf("restaurar sólo el .als: %d %v", n, err)
	}
	m, _ := filepath.Glob(filepath.Join(abl, "Tema 1 (resguardo *).als"))
	if len(m) != 1 {
		t.Fatal("no apareció el .als restaurado al lado del original")
	}
	os.Remove(m[0])

	// política: conservar 2 versiones -> la tercera borra la primera y limpia
	tk, _ := store.GetTask(task.ID)
	tk.Projects.KeepVersions = 2
	store.PutTask(tk)
	time.Sleep(1100 * time.Millisecond)
	writeALS(t, filepath.Join(abl, "Tema 1.als"), alsXML("<!-- otro cambio -->"))
	os.Chtimes(filepath.Join(abl, "Tema 1.als"), base.Add(2*time.Hour), base.Add(2*time.Hour))
	run(t, e, task.ID, false, "Proyectos/Motel/Tema 1 Project")
	snaps = listSnapshots(LocalFS{}, ablRoot)
	if len(snaps) != 2 {
		t.Fatalf("política de versiones: %v", snaps)
	}
	// la toma borrada sólo la necesitaba la primera versión: tiene que haberse liberado
	all := treeOf(t, filepath.Join(ablRoot, dirVersiones))
	for k := range all {
		if strings.HasSuffix(k, "voz.wav") {
			t.Fatalf("la toma vieja debería haberse liberado: %v", keys(all))
		}
	}
	// y la versión 2 sigue restaurando bien
	dir, _, err = RestoreSnapshot(context.Background(), tk, store.GetSettings(), "Proyectos/Motel/Tema 1 Project", 0, snaps[0], RestoreOptions{Target: filepath.Join(root, "Rest2")}, &Progress{}, log)
	if err != nil {
		t.Fatal(err)
	}
	got = treeOf(t, dir)
	if got["Samples/Recorded/voz.wav"] != nil || got["Tema 1.als"] == nil {
		t.Fatalf("restauración v2: %v", keys(got))
	}
	dumpLog(t, log)
}

func TestDiscoverAndSaveTrigger(t *testing.T) {
	store, _, e, root := testEnv(t)
	src := filepath.Join(root, "P")
	base := time.Now().Add(-time.Hour)
	writeALS(t, filepath.Join(src, "A Project", "A.als"), "<Ableton/>")
	writeFile(t, filepath.Join(src, "A Project", "Backup", "A [2026].als"), []byte("x"), base)
	writeFile(t, filepath.Join(src, "Cubase", "Tema", "Tema.cpr"), []byte("x"), base)
	writeFile(t, filepath.Join(src, "Cubase", "Tema", "Auto Saves", "Tema-01.bak"), []byte("x"), base)
	writeFile(t, filepath.Join(src, "PT", "Sesion", "Sesion.ptx"), []byte("x"), base)
	writeFile(t, filepath.Join(src, "PT", "Sesion", "Session File Backups", "Sesion.bak.001.ptx"), []byte("x"), base)
	task := NewTask()
	task.Type = TypeProjects
	task.Sources = []Location{{Path: src}}
	task.Destinations = []Location{{Path: filepath.Join(root, "D")}}
	store.PutTask(task)
	ps := e.DiscoverTask(task)
	if len(ps) != 3 {
		t.Fatalf("proyectos: %+v", ps)
	}
	for _, p := range ps {
		if strings.Contains(p.Key, "Backup") || strings.Contains(p.Key, "Session File Backups") {
			t.Fatalf("no debería contar carpetas de backup como proyectos: %s", p.Key)
		}
	}
}

func TestFiltersAndSchedule(t *testing.T) {
	task := NewTask()
	task.Include = []FilterItem{{Kind: "mascara", Value: "*.wav, *.als"}}
	task.Exclude = []FilterItem{{Kind: "mascara", Value: "*Bounce*"}, {Kind: "regex", Value: `/tmp_\d+/`}}
	task.SizeFilter = SizeFilter{Mode: "excluir", Op: "mayores", MB: 1}
	f, err := NewFilter(&task)
	if err != nil {
		t.Fatal(err)
	}
	fi := func(name string, size int64) fs.FileInfo {
		return entryInfo(Entry{Real: "/x/" + name, Size: size, MTime: time.Now()})
	}
	cases := map[string]bool{"/p/voz.wav": true, "/p/notas.txt": false, "/p/Bounce final.wav": false, "/p/tmp_12/voz.wav": false}
	for p, want := range cases {
		if got := f.Allowed(p, fi(filepath.Base(p), 100), "p"); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
	if f.Allowed("/p/grande.wav", fi("grande.wav", 2<<20), "p") {
		t.Error("filtro de tamaño")
	}
	loc := time.Local
	now := time.Date(2026, 11, 8, 18, 0, 0, 0, loc) // domingo
	if n := NextRun(Schedule{Kind: "diario", Time: "22:00"}, now, now, time.Time{}); !n.Equal(time.Date(2026, 11, 8, 22, 0, 0, 0, loc)) {
		t.Errorf("diario: %v", n)
	}
	if n := NextRun(Schedule{Kind: "semanal", Time: "03:00", Weekdays: []int{1, 4}}, now, now, time.Time{}); !n.Equal(time.Date(2026, 11, 9, 3, 0, 0, 0, loc)) {
		t.Errorf("semanal: %v", n)
	}
	if n := NextRun(Schedule{Kind: "mensual", Time: "10:00", MonthDay: 31}, now, now, time.Time{}); !n.Equal(time.Date(2026, 11, 30, 10, 0, 0, 0, loc)) {
		t.Errorf("mensual: %v", n)
	}
	if n := NextRun(Schedule{Kind: "temporizador", EveryMinutes: 45}, now, now.Add(-time.Hour), time.Time{}); !n.Equal(now.Add(30 * time.Minute)) {
		t.Errorf("temporizador: %v", n)
	}
	if got := formatTS(time.Date(2026, 1, 2, 3, 4, 5, 0, loc), "yyyy-MM-dd HH;mm;ss (dddd)"); got != "2026-01-02 03;04;05 (viernes)" {
		t.Errorf("formato: %s", got)
	}
}

func TestRSGRoundTrip(t *testing.T) {
	for _, size := range []int{0, 1, rsgChunk - 1, rsgChunk, rsgChunk + 1, 3*rsgChunk + 77} {
		data := randBytes(size)
		var enc bytes.Buffer
		w, _ := newRSGWriter(&enc, "pw")
		w.Write(data)
		w.Close()
		var dec bytes.Buffer
		if err := decryptRSG(bytes.NewReader(enc.Bytes()), &dec, "pw"); err != nil || !bytes.Equal(dec.Bytes(), data) {
			t.Fatalf("tamaño %d: %v", size, err)
		}
		if err := decryptRSG(bytes.NewReader(enc.Bytes()), &bytes.Buffer{}, "mal"); err == nil {
			t.Fatalf("aceptó una contraseña incorrecta")
		}
		if size > 10 {
			if err := decryptRSG(bytes.NewReader(enc.Bytes()[:enc.Len()-5]), &bytes.Buffer{}, "pw"); err == nil {
				t.Fatalf("aceptó un archivo cortado")
			}
		}
	}
}

func TestTriggers(t *testing.T) {
	store, _, e, root := testEnv(t)
	src := filepath.Join(root, "P")
	als := filepath.Join(src, "A Project", "A.als")
	writeALS(t, als, "<Ableton/>")
	old := time.Now().Add(-time.Hour)
	os.Chtimes(als, old, old)
	s := store.GetSettings()
	s.SaveQuietSeconds = 0
	store.SaveSettings(s)
	task := NewTask()
	task.Type = TypeProjects
	task.Sources = []Location{{Path: src}}
	task.Destinations = []Location{{Path: filepath.Join(root, "D")}}
	store.PutTask(task)
	// simula que ya había una versión guardada
	store.AddRecord(BackupRecord{ID: newID(), TaskID: task.ID, Project: "P/A Project", Snapshot: "x", Finished: time.Now()})
	e.loadProjectStates()
	e.DiscoverTask(task)
	e.checkSaves()
	if len(e.Status().Queue) != 0 {
		t.Fatalf("no debería encolar sin cambios: %+v", e.Status().Queue)
	}
	now := time.Now()
	os.Chtimes(als, now, now)
	e.checkSaves() // detecta el guardado
	e.checkSaves() // pasó el tiempo de calma (0 s)
	q := e.Status().Queue
	if len(q) != 1 || q[0].Project != "P/A Project" || q[0].Reason != "se guardó el proyecto" {
		t.Fatalf("cola tras guardar: %+v", q)
	}
	e.RemoveQueued(q[0].ID)
	// al cerrar el DAW, el proyecto modificado vuelve a la cola
	e.onDAWClosed("ableton")
	q = e.Status().Queue
	if len(q) != 1 || q[0].Reason != "se cerró el DAW" {
		t.Fatalf("cola tras cerrar el DAW: %+v", q)
	}
}

func TestDirectMirror(t *testing.T) {
	store, log, e, root := testEnv(t)
	base := time.Now().Add(-time.Hour)
	a := filepath.Join(root, "Proyectos", "Tema A")
	b := filepath.Join(root, "Otros", "Tema B")
	writeFile(t, filepath.Join(a, "Tema A.als"), []byte("a"), base)
	writeFile(t, filepath.Join(a, "Samples", "bombo.wav"), []byte("bombo"), base)
	writeFile(t, filepath.Join(a, "Samples", "viejo.wav"), []byte("viejo"), base)
	writeFile(t, filepath.Join(b, "Tema B.cpr"), []byte("b"), base)
	dst := filepath.Join(root, "PRUEBABK")
	// algo que ya estaba en el destino y no es de la tarea
	writeFile(t, filepath.Join(dst, "Cosas mías", "nota.txt"), []byte("no tocar"), base)
	writeFile(t, filepath.Join(dst, "suelto.txt"), []byte("no tocar"), base)

	task := NewTask()
	task.Name = "Prueba"
	task.Type = TypeIncr
	task.Separated = false
	task.Direct = true
	task.Mirror = true
	task.Sources = []Location{{Path: a}, {Path: b}}
	task.Destinations = []Location{{Path: dst}}
	store.PutTask(task)
	run(t, e, task.ID, false, "")

	got := treeOf(t, dst)
	want := []string{"Cosas mías/nota.txt", "Tema A/Samples/bombo.wav", "Tema A/Samples/viejo.wav", "Tema A/Tema A.als", "Tema B/Tema B.cpr", "suelto.txt"}
	if strings.Join(keys(got), "|") != strings.Join(want, "|") {
		dumpLog(t, log)
		t.Fatalf("destino: %v", keys(got))
	}

	// Sólo se borra un archivo (sin otros cambios): el espejo igual limpia.
	os.Remove(filepath.Join(a, "Samples", "viejo.wav"))
	os.Remove(filepath.Join(a, "Samples", "bombo.wav")) // Samples queda vacía en el origen
	// Un origen que no aparece (disco desconectado): su copia no se toca.
	os.Rename(b, b+" (desconectado)")
	run(t, e, task.ID, false, "")
	if _, err := os.Stat(filepath.Join(dst, "Tema A", "Samples", "viejo.wav")); err == nil {
		dumpLog(t, log)
		t.Fatal("el espejo no quitó el archivo borrado")
	}
	if _, err := os.Stat(filepath.Join(dst, "Tema A", "Samples")); err != nil {
		t.Fatal("se quitó una carpeta que sigue en el origen")
	}
	if m, _ := filepath.Glob(filepath.Join(dst, "_Espejo seguro (Prueba)", "*", "Tema A", "Samples", "viejo.wav")); len(m) != 1 {
		dumpLog(t, log)
		t.Fatal("lo quitado no quedó apartado dentro del destino")
	}
	for _, p := range []string{"Tema B/Tema B.cpr", "Cosas mías/nota.txt", "suelto.txt", "Tema A/Tema A.als"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(p))); err != nil {
			dumpLog(t, log)
			t.Fatalf("se tocó %s", p)
		}
	}
	os.Rename(b+" (desconectado)", b)

	// Restaurar trae sólo lo de la tarea, con sus carpetas.
	h := store.History(task.ID)
	if len(h) == 0 || len(h[0].Outputs) != 1 || h[0].Outputs[0].Path != dst {
		t.Fatalf("registro: %+v", h)
	}
	target := filepath.Join(root, "Restaurado")
	if _, err := RestoreRecord(context.Background(), store, task, h[0].ID, 0, target, "", &Progress{}, log); err != nil {
		t.Fatal(err)
	}
	rt := keys(treeOf(t, target))
	if strings.Join(rt, "|") != "Tema A/Tema A.als|Tema B/Tema B.cpr" {
		t.Fatalf("restaurado: %v", rt)
	}

	// Borrar el respaldo con sus archivos borra sólo las carpetas de la tarea.
	deleteRecordFiles(task, h[0], store.GetSettings(), log)
	left := keys(treeOf(t, dst))
	for _, p := range left {
		if strings.HasPrefix(p, "Tema ") {
			t.Fatalf("quedó %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "Cosas mías", "nota.txt")); err != nil {
		t.Fatal("se borró algo que no era de la tarea")
	}
}

func TestContentsOnlyMirror(t *testing.T) {
	store, log, e, root := testEnv(t)
	base := time.Now().Add(-time.Hour)
	src := filepath.Join(root, "E")
	writeFile(t, filepath.Join(src, "Tema A", "Tema A.als"), []byte("a"), base)
	writeFile(t, filepath.Join(src, "Tema B", "Tema B.cpr"), []byte("b"), base)
	writeFile(t, filepath.Join(src, "notas.txt"), []byte("notas"), base)
	dst := filepath.Join(root, "2026")
	// Lo que ya había copiado otro programa (Cobian) y algo que no es del origen.
	writeFile(t, filepath.Join(dst, "Tema A", "Tema A.als"), []byte("X"), base.Add(time.Second)) // mismo tamaño y fecha (±1 s): no se recopia
	writeFile(t, filepath.Join(dst, "Viejo de Cobian", "x.wav"), []byte("x"), base)

	task := NewTask()
	task.Name = "BK de E"
	task.Type = TypeIncr
	task.Separated = false
	task.Direct = true
	task.ContentsOnly = true
	task.Mirror = true
	task.Sources = []Location{{Path: src}}
	task.Destinations = []Location{{Path: dst}}
	store.PutTask(task)
	run(t, e, task.ID, false, "")

	got := strings.Join(keys(treeOf(t, dst)), "|")
	if got != "Tema A/Tema A.als|Tema B/Tema B.cpr|Viejo de Cobian/x.wav|notas.txt" {
		dumpLog(t, log)
		t.Fatalf("destino: %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "Tema A", "Tema A.als")); string(b) != "X" {
		t.Fatal("se volvió a copiar un archivo igual")
	}

	// Se borra una carpeta del origen: el espejo la aparta; lo de Cobian queda.
	os.RemoveAll(filepath.Join(src, "Tema B"))
	run(t, e, task.ID, false, "")
	if _, err := os.Stat(filepath.Join(dst, "Tema B")); err == nil {
		dumpLog(t, log)
		t.Fatal("el espejo no quitó la carpeta borrada")
	}
	if m, _ := filepath.Glob(filepath.Join(dst, "_Espejo seguro (BK de E)", "*", "Tema B", "Tema B.cpr")); len(m) != 1 {
		t.Fatal("no quedó apartada")
	}
	if _, err := os.Stat(filepath.Join(dst, "Viejo de Cobian", "x.wav")); err != nil {
		t.Fatal("se tocó algo que no había copiado Resguardo")
	}

	// Disco desconectado: no se quita nada.
	os.Rename(src, src+"-desconectado")
	run(t, e, task.ID, false, "")
	if _, err := os.Stat(filepath.Join(dst, "Tema A", "Tema A.als")); err != nil {
		t.Fatal("con el origen desconectado se borró la copia")
	}
}

func TestProjectsSimpleLayoutCopyAll(t *testing.T) {
	store, log, e, root := testEnv(t)
	src := filepath.Join(root, "PROTOOLS")
	base := time.Now().Add(-48 * time.Hour)
	pt := filepath.Join(src, "Clientes", "Banda X")
	ptx, _ := os.ReadFile("testdata/pt/TestPTX.ptx")
	writeFile(t, filepath.Join(pt, "Banda X.ptx"), ptx, base)
	writeFile(t, filepath.Join(pt, "Audio Files", "monoTone.wav"), randBytes(5000), base)
	writeFile(t, filepath.Join(pt, "letra.txt"), []byte("letra"), base)
	// fuera de cualquier proyecto
	writeFile(t, filepath.Join(src, "notas.txt"), []byte("notas"), base)
	writeFile(t, filepath.Join(src, "Referencias", "mezcla.pdf"), []byte("pdf"), base)

	dst := filepath.Join(root, "F")
	task := NewTask()
	task.Name = "Pro Tools"
	task.Type = TypeProjects
	task.Sources = []Location{{Path: src}}
	task.Destinations = []Location{{Path: dst}}
	task.Projects.Layout = "simple"
	task.Projects.CopyAll = true
	task.Projects.Thinning = false
	store.PutTask(task)
	run(t, e, task.ID, false, "")

	cur := filepath.Join(dst, "PROTOOLS", "Clientes", "Banda X")
	for _, p := range []string{"Banda X.ptx", "Audio Files/monoTone.wav", "letra.txt"} {
		if _, err := os.Stat(filepath.Join(cur, filepath.FromSlash(p))); err != nil {
			dumpLog(t, log)
			t.Fatalf("falta %s en la copia del proyecto (sin “Actual”): %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cur, "Actual")); err == nil {
		t.Fatal("no tiene que haber carpeta Actual")
	}
	for _, p := range []string{"PROTOOLS/notas.txt", "PROTOOLS/Referencias/mezcla.pdf"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(p))); err != nil {
			dumpLog(t, log)
			t.Fatalf("no se copió %s (copiar toda la carpeta)", p)
		}
	}
	hist := filepath.Join(dst, dirVersionesSimple, "PROTOOLS", "Clientes", "Banda X")
	if snaps, _ := filepath.Glob(filepath.Join(hist, dirMeta, "instantaneas", "*.json.gz")); len(snaps) != 1 {
		t.Fatalf("instantáneas: %v", snaps)
	}

	// Una segunda versión: lo reemplazado va a "_Resguardo versiones" y se
	// puede restaurar la primera.
	writeFile(t, filepath.Join(pt, "letra.txt"), []byte("letra nueva"), time.Now())
	run(t, e, task.ID, false, "")
	if b, _ := os.ReadFile(filepath.Join(cur, "letra.txt")); string(b) != "letra nueva" {
		t.Fatal("la copia no quedó al día")
	}
	old, _ := filepath.Glob(filepath.Join(hist, dirVersiones, "*", "letra.txt"))
	if len(old) != 1 {
		t.Fatalf("versión anterior: %v", old)
	}
	snaps, err := ProjectSnapshots(task, store.GetSettings(), "PROTOOLS/Clientes/Banda X", 0)
	if err != nil || len(snaps) != 2 {
		t.Fatalf("%v %v", snaps, err)
	}
	target := filepath.Join(root, "Restaurado")
	dir, _, err := RestoreSnapshot(context.Background(), task, store.GetSettings(), "PROTOOLS/Clientes/Banda X", 0, snaps[1].Name, RestoreOptions{Target: target}, &Progress{}, log)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "letra.txt")); string(b) != "letra" {
		t.Fatalf("restauró %q", b)
	}
}

func TestSimplifyOldTypes(t *testing.T) {
	for _, typ := range []string{TypeFull, TypeIncr, TypeDiff} {
		task := NewTask()
		task.Type = typ
		task.Separated = true
		task.UseArchiveAttr = true
		simplifyTask(&task)
		if task.Type != TypeFull || task.Separated || !task.Direct || task.UseArchiveAttr {
			t.Fatalf("%s: no se migró bien: %+v", typ, task)
		}
	}
	p := NewTask()
	p.Type = TypeProjects
	simplifyTask(&p)
	if p.Type != TypeProjects {
		t.Fatal("tocó una tarea de proyectos")
	}
}

func TestCopyUpdatesModifiedAndRestorePoints(t *testing.T) {
	store, log, e, root := testEnv(t)
	base := time.Now().Add(-2 * time.Hour)
	src := filepath.Join(root, "Cubase", "Tema")
	writeFile(t, filepath.Join(src, "Tema.cpr"), []byte("lunes"), base)
	writeFile(t, filepath.Join(src, "Audio", "voz.wav"), []byte("voz"), base)
	writeFile(t, filepath.Join(src, "Audio", "vieja.wav"), []byte("vieja"), base)
	dst := filepath.Join(root, "Backup")
	// puntos viejos: al hacer uno nuevo tiene que quedar sólo los últimos 5
	for i := 1; i <= 5; i++ {
		writeFile(t, filepath.Join(dst, restorePointsDir, "2020-01-0"+string(rune('0'+i))+" 10.00.00", "x.txt"), []byte("x"), base)
	}

	task := NewTask()
	task.Name = "Cubase"
	task.Type = TypeIncr // tipo viejo: se migra al guardar
	simplifyTask(&task)
	task.Mirror = true
	task.RestorePoints = true
	task.Sources = []Location{{Path: src}}
	task.Destinations = []Location{{Path: dst}}
	store.PutTask(task)

	// Vista previa antes de la primera copia: todo es nuevo.
	pv, err := PreviewTask(context.Background(), store, task, store.GetSettings(), log)
	if err != nil || !pv.Supported || len(pv.Dests) != 1 || pv.Dests[0].New != 3 || pv.Dests[0].Changed != 0 {
		t.Fatalf("vista previa inicial: %+v %v", pv, err)
	}
	run(t, e, task.ID, false, "")
	if got := treeOf(t, filepath.Join(dst, "Tema")); string(got["Tema.cpr"]) != "lunes" || len(got) != 3 {
		dumpLog(t, log)
		t.Fatalf("primera copia: %v", keys(got))
	}

	// El martes se guarda el .cpr otra vez (mismo tamaño, otra fecha) y se borra un audio.
	writeFile(t, filepath.Join(src, "Tema.cpr"), []byte("marte"), base.Add(time.Hour))
	os.Remove(filepath.Join(src, "Audio", "vieja.wav"))
	pv, _ = PreviewTask(context.Background(), store, task, store.GetSettings(), log)
	d := pv.Dests[0]
	if d.New != 0 || d.Changed != 1 || d.Removed != 1 || d.Items[0].Rel != "Tema/Tema.cpr" {
		t.Fatalf("vista previa del martes: %+v", d)
	}
	run(t, e, task.ID, false, "")
	got := treeOf(t, filepath.Join(dst, "Tema"))
	if string(got["Tema.cpr"]) != "marte" || got["Audio/vieja.wav"] != nil {
		dumpLog(t, log)
		t.Fatalf("no actualizó el destino: %v", keys(got))
	}
	// La versión anterior quedó guardada, y hay como mucho 5 puntos.
	ents, _ := os.ReadDir(filepath.Join(dst, restorePointsDir))
	if len(ents) != 5 || ents[0].Name() == "2020-01-01 10.00.00" {
		t.Fatalf("puntos de restauración: %v", ents)
	}
	last := filepath.Join(dst, restorePointsDir, ents[len(ents)-1].Name())
	old := treeOf(t, last)
	if string(old["Tema/Tema.cpr"]) != "lunes" || string(old["Tema/Audio/vieja.wav"]) != "vieja" {
		t.Fatalf("punto de restauración: %v", keys(old))
	}
	// Sin cambios: la vista previa dice que está todo al día.
	pv, _ = PreviewTask(context.Background(), store, task, store.GetSettings(), log)
	if d := pv.Dests[0]; d.New+d.Changed+d.Removed != 0 {
		t.Fatalf("debería estar al día: %+v", d)
	}
}

func TestDriveRootHelpers(t *testing.T) {
	for p, want := range map[string]bool{"/": true, "/Volumes/Disco": true, "/media/joa/DISCO": true, "/Volumes/Disco/Musica": false, "/home/x": false} {
		if isDriveRoot(p) != want {
			t.Errorf("isDriveRoot(%q) = %v", p, !want)
		}
	}
	if !isSystemRootItem("$RECYCLE.BIN") || !isSystemRootItem("System Volume Information") || isSystemRootItem("ANASTASIA") {
		t.Error("isSystemRootItem")
	}
	task := NewTask()
	task.Type = TypeFull
	task.Direct = true
	task.Separated = false
	task.Sources = []Location{{Path: "/Volumes/Disco"}}
	if !isContentsOnly(task) {
		t.Error("un disco entero tiene que copiarse sin carpeta propia")
	}
	task.Sources = []Location{{Path: "/Volumes/Disco/Musica"}}
	if isContentsOnly(task) {
		t.Error("una carpeta común conserva su carpeta")
	}
}

func TestNeverCopyPagefile(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(root, "src", "pagefile.sys"), []byte("x"), now)
	writeFile(t, filepath.Join(root, "src", "sub", "DumpStack.log.tmp"), []byte("x"), now)
	writeFile(t, filepath.Join(root, "src", "sub", "tema.cpr"), []byte("x"), now)
	writeFile(t, filepath.Join(root, "src", "$RECYCLE.BIN", "S-1-5-18", "basura"), []byte("x"), now)
	writeFile(t, filepath.Join(root, "src", "sub", "System Volume Information", "x"), []byte("x"), now)
	ents, _, err := ScanSources(context.Background(), []string{filepath.Join(root, "src")}, ScanOptions{Subdirs: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(strings.ToLower(e.Rel), "pagefile") || strings.Contains(strings.ToLower(e.Rel), "dumpstack") || strings.Contains(strings.ToLower(e.Rel), "recycle") || strings.Contains(e.Rel, "Volume") {
			t.Fatalf("no tenía que listar %s", e.Rel)
		}
	}
	if len(ents) != 3 { // src, src/sub, src/sub/tema.cpr
		t.Fatalf("entradas: %d", len(ents))
	}
}

package main

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16le(s string) []byte {
	b := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

func TestSplitCommaText(t *testing.T) {
	got := splitCommaText(`"1,""C:\Mis Documentos""","1,""D:\Sesiones"""`)
	if len(got) != 2 || got[0] != `1,"C:\Mis Documentos"` || got[1] != `1,"D:\Sesiones"` {
		t.Fatalf("%q", got)
	}
	in := splitCommaText(got[0])
	if len(in) != 2 || in[0] != "1" || in[1] != `C:\Mis Documentos` {
		t.Fatalf("%q", in)
	}
	if got := cobianItems(`1,C:\Datos`); len(got) != 1 || got[0][1] != `C:\Datos` {
		t.Fatalf("sin comillas: %q", got)
	}
}

// Una lista como la que escribe Cobian Backup 11 (claves tomadas de listas reales).
const cobian11Sample = `// Cobian Backup 11 Gravity
{11//
Id={4A51514B-4756-476C-B29A-1BA2DE5ACEC2}
Name=Proyectos Ableton
Group=Estudio
Enabled=true
Include subdirectories=true
Create separated backups=true
Use attributes=true
Use VSC=false
Backup type=1
Priority=0
Source="1,""E:\Proyectos Ableton""","1,""E:\Samples propios""","0,""C:\Users\Joa\Desktop\notas.txt"""
Destination="1,""H:\Respaldos"""
Schedule type=2
Select days of the week=false
Order of Day of the week=1
Day of the week=2,4,6
Date/Time=0100000011100100000011100010110010000000001111010000101110101010
Day of the month=1
Month=1
Timer=180
Timer from=0100000011100100000011100010000000000000000000000000000000000000
Timer to=0100000011100100000011100011111111111111111001111011101000110111
Full copies=3
Differental copies=0
One full every=7
Use fixed day=true
Fixed day=1
Compression=2
Compress individually=false
Split=0
Custom size=4300000000
Comment=Cobian Backup 11 Gravity
Encryption=1
Passphrase=fQB8AH0AdwBoAGYAeQ==
Exclusions="1,""E:\Proyectos Ableton\Backup"",0,0000000000000000000000000000000000000000000000000000000000000000","2,""*.tmp"",0,0000000000000000000000000000000000000000000000000000000000000000"
Inclusions=
Pre backup events="0,""C:\Scripts\antes.bat"",""""",""0"""
Post backup events=
Abort if pre-event fails=false
Abort if post-event fails=false
Mirror=false
Absolute paths=false
Always create top directory=true
Clear archive attribute=true
Include backup type=true
Delete empty directories=false
Impersonate=false
Abort if impersonation fails=false
Run as User name=
Run as Domain=.
Run as Password=dgBzAHMA
//11}
{11//
Id={99}
Name=Sesiones cada 3 horas
Group=
Enabled=false
Backup type=0
Create separated backups=false
Source="1,""D:\Sesiones"""
Destination="1,""\\NAS\backup\sesiones"""
Schedule type=5
Timer=180
Timer from=0100000011100100000011100010000000000000000000000000000000000000
Timer to=0100000011100100000011100011111111111111111001111011101000110111
Mirror=true
Compression=0
Encryption=0
//11}
`

func TestImportCobian11(t *testing.T) {
	p, err := parseForeignList("MainList.lst", utf16le(strings.ReplaceAll(cobian11Sample, "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if p.Program != "Cobian Backup 11" || len(p.Tasks) != 2 {
		t.Fatalf("%s %d", p.Program, len(p.Tasks))
	}
	a := p.Tasks[0].Task
	if a.Name != "Proyectos Ableton" || a.Group != "Estudio" || !a.Enabled || a.Type != TypeIncr || !a.Separated {
		t.Fatalf("básicos: %+v", a)
	}
	if a.ImportedFrom != "cobian11:{4A51514B-4756-476C-B29A-1BA2DE5ACEC2}" {
		t.Fatal(a.ImportedFrom)
	}
	if len(a.Sources) != 3 || a.Sources[0].Path != `E:\Proyectos Ableton` || a.Sources[1].Path != `E:\Samples propios` || !a.Sources[2].ForceFile {
		t.Fatalf("orígenes: %+v", a.Sources)
	}
	if len(a.Destinations) != 1 || a.Destinations[0].Path != `H:\Respaldos` {
		t.Fatalf("destinos: %+v", a.Destinations)
	}
	if a.Schedule.Kind != "semanal" || a.Schedule.Time != "09:22" || fmtInts(a.Schedule.Weekdays) != "1 3 5" {
		t.Fatalf("horario: %+v", a.Schedule)
	}
	if a.FullCopiesToKeep != 3 || a.FullEvery != 7 || a.FixedFullWeekday != 0 {
		t.Fatalf("copias: %d %d %d", a.FullCopiesToKeep, a.FullEvery, a.FixedFullWeekday)
	}
	if a.Compression != "separado" || a.Format != "7z" || a.Comment != "" || !a.Encrypt || a.Password != "" {
		t.Fatalf("compresión: %s %s %q %v", a.Compression, a.Format, a.Comment, a.Encrypt)
	}
	if a.UseArchiveAttr {
		t.Fatal("no tiene que usar el atributo de archivo")
	}
	if len(a.Exclude) != 2 || a.Exclude[0] != (FilterItem{Kind: "carpeta", Value: `E:\Proyectos Ableton\Backup`}) || a.Exclude[1] != (FilterItem{Kind: "mascara", Value: "*.tmp"}) {
		t.Fatalf("exclusiones: %+v", a.Exclude)
	}
	w := strings.Join(p.Tasks[0].Warnings, "\n")
	for _, want := range []string{"atributo de archivo", "cifrada", `C:\Scripts\antes.bat`} {
		if !strings.Contains(w, want) {
			t.Fatalf("falta el aviso %q en:\n%s", want, w)
		}
	}

	b := p.Tasks[1].Task
	if b.Enabled || b.Type != TypeFull || b.Separated || !b.Mirror || b.Compression != "ninguna" {
		t.Fatalf("segunda: %+v", b)
	}
	if b.Schedule.Kind != "temporizador" || b.Schedule.EveryMinutes != 180 || b.Schedule.WindowFrom != "" {
		t.Fatalf("temporizador: %+v", b.Schedule)
	}
	if b.Destinations[0].Path != `\\NAS\backup\sesiones` {
		t.Fatal(b.Destinations[0].Path)
	}
	if b.Group != "" {
		t.Fatal(b.Group)
	}
}

func fmtInts(a []int) string {
	var s []string
	for _, n := range a {
		s = append(s, string(rune('0'+n)))
	}
	return strings.Join(s, " ")
}

func rfSection(id string, lines ...string) string {
	return "<§- " + id + " -§>\r\n§DuplicatedKeys:True\r\n§PairSeparator:=\r\n" + strings.Join(lines, "\r\n") + "\r\n<§§- " + id + " -§§>\r\n\r\n"
}

func rfRef(id string) string { return "{ *§* " + id + " *§* }" }

func TestImportReflector(t *testing.T) {
	s := rfSection("raiz", "BackupTask="+rfRef("t1")) +
		rfSection("t1",
			"TaskId=7d7c8a4d-1704-4eed-bbc7-368049912640",
			"TaskName=Docs a K",
			"TaskGroup=",
			"TaskEnabled=True",
			"TaskBackupType=0",
			"TaskSource="+rfRef("s1"),
			"TaskSource="+rfRef("s2"),
			"TaskDestination="+rfRef("d1"),
			"TaskSchedule="+rfRef("h1"),
			"TaskFullCopiesToKeep=5",
			"TaskFixedFullBackup=False",
			"TaskExcludeFilters="+rfRef("f1"),
			"TaskIncludeSubdirectories=True",
		) +
		rfSection("s1", "SDKind=1", `SDPath=C:\Users\Joa\Documents`) +
		rfSection("s2", "SDKind=3", "SDPath=/remoto") +
		rfSection("d1", "SDKind=1", `SDPath=K:\cobian\Docs`) +
		rfSection("h1", "SchSchedule=2", "SchDateAndTime=2026-10-01 23:35:18:671", "SchDaysOfWeek=5", "SchDaysOfWeek=1") +
		rfSection("f1", "FOFilterKind=3", "FOMask=*.tmp")
	p, err := parseForeignList("MainList.lst", utf16le(s))
	if err != nil {
		t.Fatal(err)
	}
	if p.Program != "Cobian Reflector" || len(p.Tasks) != 1 {
		t.Fatalf("%s %d", p.Program, len(p.Tasks))
	}
	a := p.Tasks[0].Task
	if a.Name != "Docs a K" || a.Type != TypeFull || a.FullCopiesToKeep != 5 || a.ImportedFrom != "reflector:7d7c8a4d-1704-4eed-bbc7-368049912640" {
		t.Fatalf("%+v", a)
	}
	if len(a.Sources) != 1 || a.Sources[0].Path != `C:\Users\Joa\Documents` || a.Destinations[0].Path != `K:\cobian\Docs` {
		t.Fatalf("%+v %+v", a.Sources, a.Destinations)
	}
	if a.Schedule.Kind != "semanal" || a.Schedule.Time != "23:35" || fmtInts(a.Schedule.Weekdays) != "1 5" {
		t.Fatalf("%+v", a.Schedule)
	}
	if len(a.Exclude) != 1 || a.Exclude[0].Value != "*.tmp" {
		t.Fatalf("%+v", a.Exclude)
	}
	if !strings.Contains(strings.Join(p.Tasks[0].Warnings, "\n"), "/remoto") {
		t.Fatalf("avisos: %v", p.Tasks[0].Warnings)
	}
}

func TestImportNotAList(t *testing.T) {
	if _, err := parseForeignList("x.lst", []byte("hola\nmundo")); err == nil {
		t.Fatal("tenía que fallar")
	}
}

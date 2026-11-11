package main

import (
	"compress/gzip"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPTXNames(t *testing.T) {
	want := map[string][]string{
		"TestPTX.ptx":    {"monoTone.wav", "monoTone.1.aif", "stereoTone.L.aif", "stereoTone.R.aif"},
		"RegionTest.ptx": {"region_name_WAV.wav"},
		"Fa_16_48.pts":   {"24000.aif"},
		"TestMXF.ptx":    {"sine.mxf"},
	}
	for f, exp := range want {
		names, err := ptxAudioNames(filepath.Join("testdata", "pt", f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if strings.Join(names, "|") != strings.Join(exp, "|") {
			t.Errorf("%s: got %v want %v", f, names, exp)
		}
	}
	// el resto de las sesiones de prueba no deben romper nada
	files, _ := filepath.Glob("testdata/pt/*")
	for _, f := range files {
		names, err := ptxAudioNames(f)
		t.Logf("%s: %v %v", filepath.Base(f), names, err)
	}
}

func writeALS(t *testing.T, path, xml string) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	zw.Write([]byte(xml))
	zw.Close()
	f.Close()
}

func TestALSAnalysis(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "Tema Project")
	os.MkdirAll(filepath.Join(proj, "Samples", "Recorded"), 0o755)
	os.WriteFile(filepath.Join(proj, "Samples", "Recorded", "voz.wav"), []byte("RIFFvoz"), 0o644)
	ext := filepath.Join(dir, "Librería", "Kicks")
	os.MkdirAll(ext, 0o755)
	os.WriteFile(filepath.Join(ext, "kick 01.wav"), []byte("RIFFkick"), 0o644)
	os.WriteFile(filepath.Join(ext, "snare.wav"), []byte("RIFFsnare"), 0o644)
	extSlash := filepath.ToSlash(ext)
	parts := strings.Split(strings.Trim(extSlash, "/"), "/")
	var hint strings.Builder
	for _, p := range parts {
		hint.WriteString(`<RelativePathElement Dir="` + p + `" />`)
	}
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Ableton MajorVersion="5" MinorVersion="11.0_433">
 <LiveSet>
  <SampleRef><FileRef>
    <RelativePathType Value="3" />
    <RelativePath Value="Samples/Recorded/voz.wav" />
    <Path Value="` + filepath.ToSlash(filepath.Join(proj, "Samples", "Recorded", "voz.wav")) + `" />
    <Type Value="1" /><LivePackName Value="" /><LivePackId Value="" />
  </FileRef></SampleRef>
  <SampleRef><FileRef>
    <RelativePathType Value="1" />
    <RelativePath Value="../Librería/Kicks/kick 01.wav" />
    <Path Value="` + extSlash + `/kick 01.wav" />
    <Type Value="1" /><LivePackName Value="" /><LivePackId Value="" />
  </FileRef></SampleRef>
  <SampleRef><FileRef>
    <HasRelativePath Value="false" />
    <RelativePathType Value="0" />
    <RelativePath />
    <Name Value="snare.wav" />
    <Type Value="1" />
    <Data />
    <RefersToFolder Value="false" />
    <SearchHint><PathHint>` + hint.String() + `</PathHint></SearchHint>
    <LivePackName Value="" />
  </FileRef></SampleRef>
  <SampleRef><FileRef>
    <RelativePathType Value="5" /><RelativePath Value="" />
    <Path Value="/no/existe/perdido.wav" />
    <LivePackName Value="" />
  </FileRef></SampleRef>
  <SampleRef><FileRef>
    <Path Value="/no/existe/Core Library/Samples/x.wav" />
    <LivePackName Value="Core Library" />
  </FileRef></SampleRef>
 </LiveSet>
</Ableton>`
	writeALS(t, filepath.Join(proj, "Tema.als"), xml)
	p := Project{Key: "x/Tema Project", Name: "Tema Project", Dir: proj, DAW: "ableton", Files: []string{"Tema.als"}, Main: "Tema.als"}
	rep := AnalyzeProject(p, nil)
	if rep.Internal != 1 || len(rep.External) != 2 || len(rep.Missing) != 2 {
		t.Fatalf("reporte inesperado: %+v", rep)
	}
}

func TestCPRHeuristic(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "Cancion")
	os.MkdirAll(filepath.Join(proj, "Audio"), 0o755)
	os.WriteFile(filepath.Join(proj, "Audio", "Bajo_01.wav"), []byte("RIFF"), 0o644)
	ext := filepath.Join(dir, "Samples")
	os.MkdirAll(ext, 0o755)
	os.WriteFile(filepath.Join(ext, "Loop 90bpm.wav"), []byte("RIFF"), 0o644)
	// cpr falso: cadenas en ASCII y UTF-16 mezcladas con binario
	var b []byte
	b = append(b, []byte("RIFF\x00\x00\x10\x00NUNDROOT")...)
	b = append(b, 0, 0, 0, 12)
	b = append(b, []byte("Bajo_01.wav")...)
	b = append(b, 0, 1, 2, 3)
	u := utf16.Encode([]rune(ext))
	for _, c := range u {
		b = binary.BigEndian.AppendUint16(b, c)
	}
	b = append(b, 0, 0, 9, 9)
	u = utf16.Encode([]rune("Loop 90bpm.wav"))
	for _, c := range u {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	b = append(b, 0, 0)
	b = append(b, []byte("Faltante.wav")...)
	b = append(b, 0)
	os.WriteFile(filepath.Join(proj, "Cancion.cpr"), b, 0o644)
	p := Project{Key: "x/Cancion", Name: "Cancion", Dir: proj, DAW: "cubase", Files: []string{"Cancion.cpr"}, Main: "Cancion.cpr"}
	t.Logf("cands: %q", cprNames(filepath.Join(proj, "Cancion.cpr")))
	rep := AnalyzeProject(p, nil)
	if rep.Internal != 1 || len(rep.External) != 1 || len(rep.Missing) != 1 {
		t.Fatalf("reporte inesperado: %+v", rep)
	}
}

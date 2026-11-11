package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resguardo/internal/updatefmt"
)

func TestManifestSignature(t *testing.T) {
	seed := make([]byte, 32)
	rand.Read(seed)
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	m := updatefmt.Manifest{App: "Resguardo", Version: "1.2.0", Notes: []string{"Algo nuevo"},
		Windows: updatefmt.Asset{URL: "https://x/w.zip", Size: 10, SHA256: "ab"}}
	raw, err := updatefmt.Sign(m, seed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := updatefmt.Parse(raw, pub)
	if err != nil || got.Version != "1.2.0" || got.Windows.URL != "https://x/w.zip" {
		t.Fatalf("parse: %+v %v", got, err)
	}
	// cualquier cambio rompe la firma
	bad := strings.Replace(string(raw), "w.zip", "malo.zip", 1)
	if _, err := updatefmt.Parse([]byte(bad), pub); err == nil {
		t.Fatal("aceptó un archivo modificado")
	}
	// otra clave
	other := make([]byte, 32)
	rand.Read(other)
	raw2, _ := updatefmt.Sign(m, other)
	if _, err := updatefmt.Parse(raw2, pub); err == nil {
		t.Fatal("aceptó una firma de otra clave")
	}
	// la clave pública que va en la app es válida
	if k, err := base64.StdEncoding.DecodeString(updatePublicKey); err != nil || len(k) != ed25519.PublicKeySize {
		t.Fatal("clave pública inválida")
	}
	cases := map[[2]string]int{{"1.0.1", "1.1.0"}: -1, {"1.10.0", "1.9.9"}: 1, {"1.1", "1.1.0"}: 0, {"2.0.0", "1.99.99"}: 1}
	for c, want := range cases {
		if updatefmt.Compare(c[0], c[1]) != want {
			t.Errorf("Compare(%s, %s) != %d", c[0], c[1], want)
		}
	}
}

func makeZip(t *testing.T, path string, files map[string]string, modes map[string]os.FileMode) {
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	for name, content := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if m, ok := modes[name]; ok {
			h.SetMode(m)
		}
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(content))
	}
	zw.Close()
	f.Close()
}

func TestSwapExe(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Resguardo.exe")
	os.WriteFile(exe, []byte("viejo"), 0o755)
	z := filepath.Join(dir, "u.zip")
	makeZip(t, z, map[string]string{"Resguardo.exe": "nuevo", "LEEME.txt": "x"}, nil)
	rollback, err := swapExe(exe, z)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "nuevo" {
		t.Fatalf("no se reemplazó: %s", b)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "viejo" {
		t.Fatalf("no volvió atrás: %s", b)
	}
	// un zip sin el programa no toca nada
	makeZip(t, z, map[string]string{"otra cosa.txt": "x"}, nil)
	if _, err := swapExe(exe, z); err == nil {
		t.Fatal("aceptó un zip sin Resguardo.exe")
	}
	if b, _ := os.ReadFile(exe); string(b) != "viejo" {
		t.Fatal("tocó el programa con un zip inválido")
	}
}

func TestSwapBundle(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "Applications", "Resguardo.app")
	bin := filepath.Join(app, "Contents", "MacOS", "Resguardo")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("viejo"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("plist viejo"), 0o644)
	if macBundle(bin) != app {
		t.Fatalf("macBundle: %s", macBundle(bin))
	}
	z := filepath.Join(dir, "u.zip")
	makeZip(t, z, map[string]string{
		"Resguardo.app/Contents/MacOS/Resguardo": "nuevo",
		"Resguardo.app/Contents/Info.plist":      "plist nuevo",
		"LEEME.txt":                              "x",
	}, map[string]os.FileMode{"Resguardo.app/Contents/MacOS/Resguardo": 0o755, "Resguardo.app/Contents/Info.plist": 0o644})
	rollback, err := swapBundle(app, z)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(bin)
	if b, _ := os.ReadFile(bin); string(b) != "nuevo" || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binario nuevo: %s %v", b, st.Mode())
	}
	if _, err := os.Stat(filepath.Join(dir, "Applications", ".Resguardo-nuevo")); err == nil {
		t.Fatal("quedó la carpeta temporal")
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "viejo" {
		t.Fatalf("no volvió atrás: %s", b)
	}
	// zip sin ejecutable válido
	makeZip(t, z, map[string]string{"Resguardo.app/Contents/Info.plist": "x"}, nil)
	if _, err := swapBundle(app, z); err == nil {
		t.Fatal("aceptó una app sin ejecutable")
	}
	if b, _ := os.ReadFile(bin); string(b) != "viejo" {
		t.Fatal("tocó la app con un zip inválido")
	}
}

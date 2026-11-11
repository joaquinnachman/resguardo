// publicar copia los zips de dist/ a descargas/ y escribe version.json firmado.
//
//	go run ./tools/publicar -clave ~/resguardo-firma.key -notas notas.txt
//
// La clave es la semilla Ed25519 en base64 (32 bytes). Nunca va en el repositorio.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"resguardo/internal/updatefmt"
)

const rawBase = "https://raw.githubusercontent.com/joaquinnachman/resguardo/main/"

func main() {
	clave := flag.String("clave", os.Getenv("RESGUARDO_CLAVE"), "archivo con la clave de firma (base64)")
	notas := flag.String("notas", "", "archivo de texto con las novedades, una por línea")
	dist := flag.String("dist", "dist", "carpeta con los zips compilados")
	base := flag.String("base", rawBase, "dirección desde donde se bajan los archivos")
	salida := flag.String("salida", ".", "carpeta raíz del repositorio")
	forzar := flag.String("version", "", "versión a publicar (por defecto, la de model.go)")
	flag.Parse()

	version := readVersion()
	if *forzar != "" {
		version = *forzar
	}
	seed := readKey(*clave)
	var lines []string
	if *notas != "" {
		f, err := os.Open(*notas)
		check(err)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if l := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sc.Text()), "-")); l != "" {
				lines = append(lines, l)
			}
		}
		f.Close()
	}
	m := updatefmt.Manifest{App: "Resguardo", Version: version, Date: time.Now().Format("2006-01-02"), Notes: lines}
	desc := filepath.Join(*salida, "descargas")
	check(os.MkdirAll(desc, 0o755))
	for _, sys := range []string{"Windows", "Mac"} {
		name := fmt.Sprintf("Resguardo-%s-%s.zip", version, sys)
		src := filepath.Join(*dist, name)
		dst := filepath.Join(desc, name)
		size, sum := copyAndHash(src, dst)
		// copia con nombre fijo para el enlace de descarga del README
		copyAndHash(src, filepath.Join(desc, "Resguardo-"+sys+".zip"))
		a := updatefmt.Asset{URL: *base + "descargas/" + name, Size: size, SHA256: sum}
		if sys == "Windows" {
			m.Windows = a
		} else {
			m.Mac = a
		}
		fmt.Printf("%s  %d bytes  %s\n", name, size, sum)
	}
	out, err := updatefmt.Sign(m, seed)
	check(err)
	check(os.WriteFile(filepath.Join(*salida, "version.json"), out, 0o644))
	fmt.Println("version.json firmado para la versión", version)
}

func readVersion() string {
	b, err := os.ReadFile("model.go")
	check(err)
	m := regexp.MustCompile(`AppVersion = "([^"]+)"`).FindSubmatch(b)
	if m == nil {
		check(fmt.Errorf("no encontré AppVersion en model.go"))
	}
	return string(m[1])
}

func readKey(path string) []byte {
	if path == "" {
		check(fmt.Errorf("falta -clave"))
	}
	b, err := os.ReadFile(path)
	check(err)
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	check(err)
	return seed
}

func copyAndHash(src, dst string) (int64, string) {
	in, err := os.Open(src)
	check(err)
	defer in.Close()
	out, err := os.Create(dst)
	check(err)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	check(err)
	check(out.Close())
	return n, hex.EncodeToString(h.Sum(nil))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

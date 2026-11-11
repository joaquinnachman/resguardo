// Package updatefmt define el archivo version.json que publica Resguardo
// y su firma (Ed25519). Lo usan la app (para verificar) y tools/publicar (para firmar).
package updatefmt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Asset es un archivo descargable de una versión.
type Asset struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describe la última versión publicada.
type Manifest struct {
	App     string   `json:"app"`
	Version string   `json:"version"`
	Date    string   `json:"date"`
	Notes   []string `json:"notes"`
	Windows Asset    `json:"windows"`
	Mac     Asset    `json:"mac"`
}

// Envelope es el contenido de version.json: los datos tal cual y su firma.
// La firma se calcula sobre los bytes exactos de "datos", así una versión vieja
// puede verificar archivos con campos nuevos que todavía no conoce.
type Envelope struct {
	Datos json.RawMessage `json:"datos"`
	Firma string          `json:"firma"`
}

// Sign arma el contenido de version.json firmado con la semilla Ed25519 (32 bytes).
func Sign(m Manifest, seed []byte) ([]byte, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("la clave de firma tiene que tener 32 bytes")
	}
	datos, err := json.MarshalIndent(m, "  ", "  ")
	if err != nil {
		return nil, err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	env := Envelope{Datos: datos, Firma: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, datos))}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Parse verifica la firma con la clave pública y devuelve los datos.
func Parse(raw []byte, pub ed25519.PublicKey) (Manifest, error) {
	var m Manifest
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return m, fmt.Errorf("el archivo de versión no se entiende: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Firma)
	if err != nil || len(env.Datos) == 0 {
		return m, errors.New("el archivo de versión no tiene firma")
	}
	if !ed25519.Verify(pub, env.Datos, sig) {
		return m, errors.New("la firma del archivo de versión no es válida")
	}
	if err := json.Unmarshal(env.Datos, &m); err != nil {
		return m, fmt.Errorf("el archivo de versión no se entiende: %w", err)
	}
	if m.App != "Resguardo" || m.Version == "" {
		return m, errors.New("el archivo de versión no es de Resguardo")
	}
	return m, nil
}

// Compare compara versiones del tipo 1.10.2: -1 si a<b, 0 si son iguales, 1 si a>b.
func Compare(a, b string) int {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// Formato .rsg: archivo cifrado con AES-256-GCM en bloques de 1 MB.
//
//	"RSG1" | sal (16) | prefijo de nonce (4) | bloques...
//
// cada bloque: largo (4, BE) | datos cifrados (largo) ; el último bloque lleva AAD "fin".
const rsgMagic = "RSG1"
const rsgChunk = 1 << 20

func deriveKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 1<<15, 8, 1, 32)
}

type rsgWriter struct {
	w      io.Writer
	aead   cipher.AEAD
	prefix [4]byte
	ctr    uint64
	buf    []byte
}

func newRSGWriter(w io.Writer, password string) (*rsgWriter, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := deriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	rw := &rsgWriter{w: w, aead: aead}
	if _, err := rand.Read(rw.prefix[:]); err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(rsgMagic)); err != nil {
		return nil, err
	}
	if _, err := w.Write(salt); err != nil {
		return nil, err
	}
	if _, err := w.Write(rw.prefix[:]); err != nil {
		return nil, err
	}
	return rw, nil
}

func (r *rsgWriter) nonce() []byte {
	n := make([]byte, 12)
	copy(n, r.prefix[:])
	binary.BigEndian.PutUint64(n[4:], r.ctr)
	r.ctr++
	return n
}

func (r *rsgWriter) flush(final bool) error {
	var aad []byte
	if final {
		aad = []byte("fin")
	}
	sealed := r.aead.Seal(nil, r.nonce(), r.buf, aad)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(sealed)))
	if _, err := r.w.Write(l[:]); err != nil {
		return err
	}
	_, err := r.w.Write(sealed)
	r.buf = r.buf[:0]
	return err
}

func (r *rsgWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		room := rsgChunk - len(r.buf)
		n := len(p)
		if n > room {
			n = room
		}
		r.buf = append(r.buf, p[:n]...)
		p = p[n:]
		total += n
		if len(r.buf) == rsgChunk && len(p) > 0 {
			if err := r.flush(false); err != nil {
				return total, err
			}
		}
	}
	return total, nil
}

// Close escribe el último bloque (aunque esté vacío).
func (r *rsgWriter) Close() error { return r.flush(true) }

var errBadPassword = errors.New("contraseña incorrecta o archivo dañado")

func decryptRSG(in io.Reader, out io.Writer, password string) error {
	head := make([]byte, 4+16+4)
	if _, err := io.ReadFull(in, head); err != nil {
		return fmt.Errorf("archivo .rsg demasiado corto")
	}
	if string(head[:4]) != rsgMagic {
		return fmt.Errorf("no es un archivo cifrado por Resguardo")
	}
	key, err := deriveKey(password, head[4:20])
	if err != nil {
		return err
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	var prefix [4]byte
	copy(prefix[:], head[20:24])
	var ctr uint64
	var lb [4]byte
	var pending []byte
	havePending := false
	for {
		_, err := io.ReadFull(in, lb[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return errBadPassword
		}
		l := binary.BigEndian.Uint32(lb[:])
		if l > rsgChunk+64 {
			return errBadPassword
		}
		sealed := make([]byte, l)
		if _, err := io.ReadFull(in, sealed); err != nil {
			return errBadPassword
		}
		if havePending {
			if _, err := out.Write(pending); err != nil {
				return err
			}
		}
		nonce := make([]byte, 12)
		copy(nonce, prefix[:])
		binary.BigEndian.PutUint64(nonce[4:], ctr)
		ctr++
		plain, err := aead.Open(nil, nonce, sealed, nil)
		final := false
		if err != nil {
			plain, err = aead.Open(nil, nonce, sealed, []byte("fin"))
			if err != nil {
				return errBadPassword
			}
			final = true
		}
		if final {
			_, err := out.Write(plain)
			if err != nil {
				return err
			}
			// no debe haber nada después del bloque final
			var extra [1]byte
			if n, _ := in.Read(extra[:]); n > 0 {
				return errBadPassword
			}
			return nil
		}
		pending, havePending = plain, true
	}
	return fmt.Errorf("el archivo cifrado está incompleto")
}

// DecryptPath descifra un archivo .rsg o todos los .rsg de una carpeta.
func DecryptPath(src, dstDir, password string) (int, error) {
	st, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	one := func(p, rel string) error {
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		target := filepath.Join(dstDir, strings.TrimSuffix(rel, ".rsg"))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target + ".rsgtmp")
		if err != nil {
			return err
		}
		err = decryptRSG(in, out, password)
		out.Close()
		if err != nil {
			os.Remove(target + ".rsgtmp")
			return fmt.Errorf("%s: %w", rel, err)
		}
		if fi, err := in.Stat(); err == nil {
			_ = os.Chtimes(target+".rsgtmp", fi.ModTime(), fi.ModTime())
		}
		return os.Rename(target+".rsgtmp", target)
	}
	if !st.IsDir() {
		return 1, one(src, filepath.Base(src))
	}
	count := 0
	err = filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".rsg") {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		if err := one(p, rel); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

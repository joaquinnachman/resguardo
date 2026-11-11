package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type remoteInfo struct {
	name  string
	size  int64
	mtime time.Time
	dir   bool
}

func (r remoteInfo) Name() string       { return r.name }
func (r remoteInfo) Size() int64        { return r.size }
func (r remoteInfo) ModTime() time.Time { return r.mtime }
func (r remoteInfo) IsDir() bool        { return r.dir }
func (r remoteInfo) Sys() any           { return nil }
func (r remoteInfo) Mode() fs.FileMode {
	if r.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}

// ---------- FTP / FTPS ----------

type FTPFS struct {
	loc      Location
	settings Settings
	conn     *ftp.ServerConn
}

func dialFTP(loc Location, settings Settings) (*FTPFS, error) {
	f := &FTPFS{loc: loc, settings: settings}
	if err := f.connect(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *FTPFS) connect() error {
	port := f.loc.Port
	if port == 0 {
		port = 21
		if f.loc.TLS == "implicito" {
			port = 990
		}
	}
	addr := net.JoinHostPort(f.loc.Host, strconv.Itoa(port))
	opts := []ftp.DialOption{ftp.DialWithTimeout(30 * time.Second)}
	tcfg := &tls.Config{ServerName: f.loc.Host, InsecureSkipVerify: f.loc.IgnoreCert}
	switch f.loc.TLS {
	case "explicito":
		opts = append(opts, ftp.DialWithExplicitTLS(tcfg))
	case "implicito":
		opts = append(opts, ftp.DialWithTLS(tcfg))
	}
	c, err := ftp.Dial(addr, opts...)
	if err != nil {
		return fmt.Errorf("no se pudo conectar a %s: %w", addr, err)
	}
	user := f.loc.User
	if user == "" {
		user = "anonymous"
	}
	if err := c.Login(user, f.loc.Pass); err != nil {
		c.Quit()
		return fmt.Errorf("usuario o contraseña FTP incorrectos: %w", err)
	}
	f.conn = c
	return nil
}

func (f *FTPFS) Kind() string               { return "ftp" }
func (f *FTPFS) Join(elem ...string) string { return remoteJoin(elem...) }
func (f *FTPFS) Dir(p string) string        { return path.Dir(p) }
func (f *FTPFS) Close() error {
	if f.conn != nil {
		return f.conn.Quit()
	}
	return nil
}
func (f *FTPFS) Reconnect() error {
	if f.conn != nil {
		_ = f.conn.Quit()
	}
	return f.connect()
}

func (f *FTPFS) Stat(p string) (fs.FileInfo, error) {
	if p == "/" || p == "" || p == "." {
		return remoteInfo{name: "/", dir: true}, nil
	}
	if e, err := f.conn.GetEntry(p); err == nil && e != nil {
		return remoteInfo{name: path.Base(p), size: int64(e.Size), mtime: e.Time, dir: e.Type == ftp.EntryTypeFolder}, nil
	}
	parent := path.Dir(p)
	entries, err := f.conn.List(parent)
	if err != nil {
		return nil, err
	}
	base := path.Base(p)
	for _, e := range entries {
		if e.Name == base {
			return remoteInfo{name: base, size: int64(e.Size), mtime: e.Time, dir: e.Type == ftp.EntryTypeFolder}, nil
		}
	}
	return nil, fs.ErrNotExist
}

func (f *FTPFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := f.conn.List(p)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		out = append(out, remoteInfo{name: e.Name, size: int64(e.Size), mtime: e.Time, dir: e.Type == ftp.EntryTypeFolder})
	}
	return out, nil
}

func (f *FTPFS) MkdirAll(p string) error {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	cur := ""
	if strings.HasPrefix(p, "/") {
		cur = "/"
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur = path.Join(cur, part)
		if st, err := f.Stat(cur); err == nil && st.IsDir() {
			continue
		}
		if err := f.conn.MakeDir(cur); err != nil {
			if st, err2 := f.Stat(cur); err2 == nil && st.IsDir() {
				continue
			}
			return fmt.Errorf("no se pudo crear %s: %w", cur, err)
		}
	}
	return nil
}

type ftpWriter struct {
	pw   *io.PipeWriter
	done chan error
}

func (w *ftpWriter) Write(b []byte) (int, error) { return w.pw.Write(b) }
func (w *ftpWriter) Close() error {
	w.pw.Close()
	return <-w.done
}

func (f *FTPFS) Create(p string) (io.WriteCloser, error) {
	pr, pw := io.Pipe()
	w := &ftpWriter{pw: pw, done: make(chan error, 1)}
	go func() {
		err := f.conn.Stor(p, pr)
		pr.CloseWithError(err)
		w.done <- err
	}()
	return w, nil
}

func (f *FTPFS) Open(p string) (io.ReadCloser, error) { return f.conn.Retr(p) }
func (f *FTPFS) Remove(p string) error {
	if st, err := f.Stat(p); err == nil && st.IsDir() {
		return f.conn.RemoveDir(p)
	}
	return f.conn.Delete(p)
}
func (f *FTPFS) RemoveAll(p string) error {
	st, err := f.Stat(p)
	if err != nil {
		return nil
	}
	if st.IsDir() {
		return f.conn.RemoveDirRecur(p)
	}
	return f.conn.Delete(p)
}
func (f *FTPFS) Rename(a, b string) error { return f.conn.Rename(a, b) }
func (f *FTPFS) Chtimes(p string, t time.Time) error {
	return f.conn.SetTime(p, t)
}

// ---------- SFTP ----------

type SFTPFS struct {
	loc    Location
	ssh    *ssh.Client
	client *sftp.Client
}

var hostKeysMu sync.Mutex

func knownHostsPath() string { return filepath.Join(dataDir(), "sftp_servidores.json") }

func hostKeyCallback(hostport string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		sum := sha256.Sum256(key.Marshal())
		fp := hex.EncodeToString(sum[:])
		hostKeysMu.Lock()
		defer hostKeysMu.Unlock()
		known := map[string]string{}
		if b, err := os.ReadFile(knownHostsPath()); err == nil {
			_ = json.Unmarshal(b, &known)
		}
		if prev, ok := known[hostport]; ok {
			if prev != fp {
				return fmt.Errorf("la huella del servidor SFTP %s cambió (posible suplantación). Si el cambio es legítimo, borrá la entrada en %s", hostport, knownHostsPath())
			}
			return nil
		}
		known[hostport] = fp
		b, _ := json.MarshalIndent(known, "", "  ")
		_ = os.WriteFile(knownHostsPath(), b, 0o600)
		return nil
	}
}

func dialSFTP(loc Location) (*SFTPFS, error) {
	f := &SFTPFS{loc: loc}
	if err := f.connect(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *SFTPFS) connect() error {
	port := f.loc.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(f.loc.Host, strconv.Itoa(port))
	var auths []ssh.AuthMethod
	if f.loc.KeyFile != "" {
		key, err := os.ReadFile(f.loc.KeyFile)
		if err != nil {
			return fmt.Errorf("no se pudo leer la clave privada: %w", err)
		}
		var signer ssh.Signer
		if f.loc.Pass != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(f.loc.Pass))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return fmt.Errorf("clave privada inválida: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if f.loc.Pass != "" {
		auths = append(auths, ssh.Password(f.loc.Pass))
		auths = append(auths, ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			ans := make([]string, len(questions))
			for i := range ans {
				ans[i] = f.loc.Pass
			}
			return ans, nil
		}))
	}
	cfg := &ssh.ClientConfig{
		User:            f.loc.User,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback(addr),
		Timeout:         30 * time.Second,
	}
	c, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("no se pudo conectar por SFTP a %s: %w", addr, err)
	}
	cl, err := sftp.NewClient(c)
	if err != nil {
		c.Close()
		return fmt.Errorf("el servidor no acepta SFTP: %w", err)
	}
	f.ssh, f.client = c, cl
	return nil
}

func (f *SFTPFS) Kind() string               { return "sftp" }
func (f *SFTPFS) Join(elem ...string) string { return remoteJoin(elem...) }
func (f *SFTPFS) Dir(p string) string        { return path.Dir(p) }
func (f *SFTPFS) Close() error {
	if f.client != nil {
		f.client.Close()
	}
	if f.ssh != nil {
		return f.ssh.Close()
	}
	return nil
}
func (f *SFTPFS) Reconnect() error {
	f.Close()
	return f.connect()
}
func (f *SFTPFS) Stat(p string) (fs.FileInfo, error) { return f.client.Stat(p) }
func (f *SFTPFS) ReadDir(p string) ([]fs.FileInfo, error) {
	return f.client.ReadDir(p)
}
func (f *SFTPFS) MkdirAll(p string) error { return f.client.MkdirAll(p) }
func (f *SFTPFS) Create(p string) (io.WriteCloser, error) {
	return f.client.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
}
func (f *SFTPFS) Open(p string) (io.ReadCloser, error) { return f.client.Open(p) }
func (f *SFTPFS) Remove(p string) error                { return f.client.Remove(p) }
func (f *SFTPFS) RemoveAll(p string) error             { return removeAllGeneric(f, p) }
func (f *SFTPFS) Rename(a, b string) error {
	if err := f.client.PosixRename(a, b); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = f.client.Remove(b)
		return f.client.Rename(a, b)
	}
	return nil
}
func (f *SFTPFS) Chtimes(p string, t time.Time) error { return f.client.Chtimes(p, t, t) }

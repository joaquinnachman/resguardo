package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// splitArgs separa una línea de parámetros respetando comillas.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	has := false
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
			has = true
		case (r == ' ' || r == '\t') && !inQ:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

func eventLabel(k string) string {
	m := map[string]string{"ejecutar": "Ejecutar", "ejecutar_esperar": "Ejecutar y esperar", "cerrar_programa": "Cerrar programa",
		"iniciar_servicio": "Iniciar servicio", "detener_servicio": "Detener servicio", "pausa": "Pausa",
		"apagar": "Apagar el equipo", "reiniciar": "Reiniciar el equipo", "hibernar": "Hibernar", "suspender": "Suspender", "cerrar_sesion": "Cerrar sesión"}
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

func runEvents(ctx context.Context, events []Event, task *Task, s Settings, log *Logger) error {
	for _, ev := range events {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Info("Evento: %s %s", eventLabel(ev.Kind), ev.Target)
		if err := runEvent(ctx, ev, task, s, log); err != nil {
			return fmt.Errorf("%s %s: %w", eventLabel(ev.Kind), ev.Target, err)
		}
	}
	return nil
}

func eventArgs(ev Event, task *Task) []string {
	var src, dst []string
	for _, l := range task.Sources {
		src = append(src, describeLocation(l))
	}
	for _, l := range task.Destinations {
		dst = append(dst, describeLocation(l))
	}
	r := strings.NewReplacer("{origen}", `"`+strings.Join(src, `" "`)+`"`, "{destino}", `"`+strings.Join(dst, `" "`)+`"`, "{tarea}", `"`+task.Name+`"`)
	return splitArgs(r.Replace(ev.Args))
}

func runEvent(ctx context.Context, ev Event, task *Task, s Settings, log *Logger) error {
	switch ev.Kind {
	case "ejecutar":
		cmd := exec.Command(ev.Target, eventArgs(ev, task)...)
		cmd.Dir = filepath.Dir(ev.Target)
		return cmd.Start()
	case "ejecutar_esperar":
		cmd := exec.CommandContext(ctx, ev.Target, eventArgs(ev, task)...)
		cmd.Dir = filepath.Dir(ev.Target)
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		if len(out) > 0 {
			o := strings.TrimSpace(string(out))
			if len(o) > 800 {
				o = o[len(o)-800:]
			}
			log.Detail("Salida: %s", o)
		}
		return err
	case "cerrar_programa":
		return closeProgram(ev.Target, time.Duration(max(5, s.ProcessExitTimeout))*time.Second, log)
	case "iniciar_servicio":
		return serviceControl(ctx, ev.Target, true)
	case "detener_servicio":
		return serviceControl(ctx, ev.Target, false)
	case "pausa":
		select {
		case <-time.After(time.Duration(ev.Seconds) * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case "apagar", "reiniciar", "hibernar", "suspender", "cerrar_sesion":
		return powerAction(ev.Kind)
	}
	return fmt.Errorf("evento desconocido")
}

// ---------- correo ----------

type loginAuth struct{ user, pass string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }
func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	q := strings.ToLower(string(fromServer))
	switch {
	case strings.Contains(q, "user"):
		return []byte(a.user), nil
	case strings.Contains(q, "pass"):
		return []byte(a.pass), nil
	}
	return nil, errors.New("pregunta de autenticación inesperada")
}

type plainAuth struct{ user, pass string }

func (a plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a plainAuth) Next(fromServer []byte, more bool) ([]byte, error) { return nil, nil }

func sendMail(cfg SMTPSettings, subject, body string, attachments []string) error {
	if cfg.Host == "" || len(cfg.To) == 0 {
		return fmt.Errorf("faltan el servidor o los destinatarios")
	}
	port := cfg.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	tcfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.IgnoreCert}
	var conn net.Conn
	var err error
	if cfg.Security == "ssl" {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", addr, tcfg)
	} else {
		conn, err = net.DialTimeout("tcp", addr, timeout)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if cfg.Security == "starttls" {
		if err := c.StartTLS(tcfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if cfg.Auth {
		ok, mechs := c.Extension("AUTH")
		var a smtp.Auth = plainAuth{cfg.User, cfg.Pass}
		if ok && !strings.Contains(strings.ToUpper(mechs), "PLAIN") && strings.Contains(strings.ToUpper(mechs), "LOGIN") {
			a = loginAuth{cfg.User, cfg.Pass}
		}
		if err := c.Auth(a); err != nil {
			return fmt.Errorf("autenticación: %w", err)
		}
	}
	from := cfg.From
	if from == "" {
		from = cfg.User
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, to := range cfg.To {
		if strings.TrimSpace(to) == "" {
			continue
		}
		if err := c.Rcpt(strings.TrimSpace(to)); err != nil {
			return fmt.Errorf("destinatario %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	boundary := "resguardo-" + newID()
	fromHdr := from
	if cfg.FromName != "" {
		fromHdr = mime.QEncoding.Encode("utf-8", cfg.FromName) + " <" + from + ">"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n", fromHdr, strings.Join(cfg.To, ", "),
		mime.QEncoding.Encode("utf-8", subject), time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary)
	b.WriteString(wrap76(base64.StdEncoding.EncodeToString([]byte(body))))
	for _, f := range attachments {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name := filepath.Base(f)
		fmt.Fprintf(&b, "\r\n--%s\r\nContent-Type: text/plain; charset=utf-8; name=%q\r\nContent-Disposition: attachment; filename=%q\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary, name, name)
		b.WriteString(wrap76(base64.StdEncoding.EncodeToString(data)))
	}
	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	if _, err := w.Write([]byte(b.String())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	b.WriteString(s + "\r\n")
	return b.String()
}

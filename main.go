package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

var serviceMode bool

// preferredPort: puerto que usaba la versión anterior (después de actualizar se reusa,
// así la ventana abierta sigue conectada). afterUpdate: se arrancó tras una actualización.
var preferredPort int
var afterUpdate bool

type instanceInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func instancePath() string { return filepath.Join(dataDir(), "instancia.json") }

// runningInstance devuelve la URL de una instancia ya abierta (si la hay).
func runningInstance() string {
	b, err := os.ReadFile(instancePath())
	if err != nil {
		return ""
	}
	var inst instanceInfo
	if json.Unmarshal(b, &inst) != nil || inst.Port == 0 {
		return ""
	}
	c := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping?t=%s", inst.Port, inst.Token))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var p map[string]any
	if json.NewDecoder(resp.Body).Decode(&p) != nil || p["app"] != AppName || p["version"] == nil {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", inst.Port, inst.Token)
}

func main() {
	inicio := flag.Bool("inicio", false, "arranque automático: no abre la ventana")
	ventana := flag.String("ventana", "", "")
	inst := flag.String("instancia", "", "nombre de instancia")
	servicio := flag.Bool("servicio", false, "")
	sinVentana := flag.Bool("sin-ventana", false, "no abrir la ventana")
	daemon := flag.Bool("daemon", false, "motor en segundo plano")
	trasActualizar := flag.Bool("tras-actualizar", false, "")
	puerto := flag.Int("puerto", 0, "")
	cerrar := flag.Bool("cerrar", false, "cierra Resguardo si está abierto (lo usa el instalador)")
	flag.Parse()
	instanceName = *inst
	serviceMode = *servicio

	if *cerrar {
		if !closeRunningApp(20 * time.Second) {
			os.Exit(1)
		}
		return
	}

	if *ventana != "" {
		runWindow(*ventana)
		return
	}
	if serviceMode {
		runAsService()
		return
	}
	if url := runningInstance(); url != "" {
		if !*inicio && !*daemon {
			openUIWindow(url)
		}
		return
	}
	// En Mac la app sólo arranca el motor en segundo plano y abre la ventana:
	// así, volver a abrir Resguardo desde el Dock o el Finder siempre muestra la ventana.
	if !*inicio && !*daemon && !*sinVentana && launchDetached() {
		return
	}
	preferredPort = *puerto
	afterUpdate = *trasActualizar
	app, err := startApp()
	if err != nil {
		messageBox(AppName, "No se pudo iniciar Resguardo:\n\n"+err.Error())
		os.Exit(1)
	}
	if !*inicio && !*sinVentana && !*daemon {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openUIWindow(app.server.URL())
		}()
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		app.Quit()
	}()
	runTray(app)
	app.Quit()
}

type App struct {
	store     *Store
	log       *Logger
	engine    *Engine
	server    *Server
	once      sync.Once
	stopOnce  sync.Once
	done      chan struct{}
	trayQuit  func()
	updater   *Updater
	handedOff bool // la versión nueva ya tomó el control: no tocar instancia.json
}

func startApp() (*App, error) {
	store, err := OpenStore()
	if err != nil {
		return nil, err
	}
	s := store.GetSettings()
	log := NewLogger(filepath.Join(store.Dir, "registro"), s.LogPerDay)
	engine := NewEngine(store, log)
	server := NewServer(store, engine, log)
	ln, err := server.Listen()
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir el puerto local: %w", err)
	}
	app := &App{store: store, log: log, engine: engine, server: server, done: make(chan struct{})}
	server.quit = app.Quit
	app.updater = NewUpdater(app)
	server.upd = app.updater
	b, _ := json.Marshal(instanceInfo{Port: server.port, Token: server.token, PID: os.Getpid()})
	_ = writeFileAtomic(instancePath(), b)
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("Servidor de la interfaz: %v", err)
		}
	}()
	engine.Start()
	go app.updater.Run()
	if s.Autostart {
		_ = setAutostart(true) // por si se movió el programa
	}
	syncInstallInfo() // la versión que muestra Windows en "Aplicaciones instaladas"
	return app, nil
}

// stopForUpdate apaga el motor y la interfaz pero deja vivo el proceso,
// para poder volver atrás si la versión nueva no arranca.
func (a *App) stopForUpdate() {
	a.stopOnce.Do(func() {
		a.log.Info("Resguardo se reinicia para actualizarse")
		a.engine.Stop()
		a.server.Shutdown()
		_ = os.Remove(instancePath())
	})
}

func (a *App) Quit() {
	a.once.Do(func() {
		if !a.handedOff {
			a.log.Info("Resguardo se cierra")
		}
		a.engine.Stop()
		a.server.Shutdown()
		if !a.handedOff {
			_ = os.Remove(instancePath())
		}
		close(a.done)
		if a.trayQuit != nil {
			a.trayQuit()
		}
		go func() {
			time.Sleep(3 * time.Second)
			os.Exit(0)
		}()
	})
}

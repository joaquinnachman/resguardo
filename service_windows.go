//go:build windows

package main

import (
	"golang.org/x/sys/windows/svc"
)

type svcHandler struct{}

func (svcHandler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending}
	app, err := startApp()
	if err != nil {
		return false, 1
	}
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending}
				app.Quit()
				return false, 0
			}
		case <-app.done:
			return false, 0
		}
	}
}

// runAsService corre el motor como servicio de Windows (sin ventana ni ícono).
// Se instala con:  sc create Resguardo binPath= "\"C:\ruta\Resguardo.exe\" --servicio" start= auto
func runAsService() {
	_ = svc.Run(AppName, svcHandler{})
}

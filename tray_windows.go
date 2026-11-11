//go:build windows

package main

import (
	_ "embed"
	"runtime"

	"fyne.io/systray"
)

//go:embed icons/tray-ok.ico
var icoOK []byte

//go:embed icons/tray-work.ico
var icoWork []byte

//go:embed icons/tray-error.ico
var icoError []byte

//go:embed icons/tray-pause.ico
var icoPause []byte

func init() { runtime.LockOSThread() }

func runTray(app *App) {
	systray.Run(func() {
		systray.SetIcon(icoOK)
		systray.SetTitle("Resguardo")
		systray.SetTooltip("Resguardo")
		mOpen := systray.AddMenuItem("Abrir Resguardo", "Abre la ventana principal")
		systray.AddSeparator()
		mRunAll := systray.AddMenuItem("Ejecutar todas las tareas", "")
		mPause := systray.AddMenuItem("Pausar el respaldo", "")
		mCancel := systray.AddMenuItem("Cancelar el respaldo en curso", "")
		mSched := systray.AddMenuItemCheckbox("Programador en pausa", "No ejecutar tareas programadas", false)
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Salir de Resguardo", "")
		systray.SetOnTapped(func() { openUIWindow(app.server.URL()) })
		app.trayQuit = systray.Quit
		last := ""
		app.engine.OnState = func(state, tip string) {
			if state != last {
				switch state {
				case "trabajando":
					systray.SetIcon(icoWork)
				case "error":
					systray.SetIcon(icoError)
				case "pausado":
					systray.SetIcon(icoPause)
				default:
					systray.SetIcon(icoOK)
				}
				last = state
			}
			if app.engine.paused.Load() {
				mPause.SetTitle("Reanudar el respaldo")
			} else {
				mPause.SetTitle("Pausar el respaldo")
			}
			systray.SetTooltip(tip)
		}
		app.engine.notify()
		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					openUIWindow(app.server.URL())
				case <-mRunAll.ClickedCh:
					for _, t := range app.store.GetTasks() {
						if t.Enabled {
							app.engine.Enqueue(t.ID, false, "", "manual")
						}
					}
				case <-mPause.ClickedCh:
					app.engine.TogglePause()
				case <-mCancel.ClickedCh:
					app.engine.Cancel()
				case <-mSched.ClickedCh:
					if mSched.Checked() {
						mSched.Uncheck()
						app.engine.SetSchedulerOff(false)
					} else {
						mSched.Check()
						app.engine.SetSchedulerOff(true)
					}
				case <-mQuit.ClickedCh:
					app.Quit()
					return
				case <-app.done:
					return
				}
			}
		}()
	}, func() {})
}

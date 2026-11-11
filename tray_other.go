//go:build !windows

package main

// En Mac (y Linux) Resguardo corre en segundo plano sin ícono en la barra:
// se abre la ventana volviendo a abrir la app, y se cierra desde la propia ventana.
func runTray(app *App) {
	app.engine.OnState = func(state, tip string) {}
	<-app.done
}

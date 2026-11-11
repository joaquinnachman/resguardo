//go:build !windows

package main

func runAsService() {
	app, err := startApp()
	if err != nil {
		return
	}
	<-app.done
}

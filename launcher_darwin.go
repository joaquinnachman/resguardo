//go:build darwin

package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func launchDetached() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	args := []string{"--daemon"}
	if instanceName != "" {
		args = append(args, "--instancia", instanceName)
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return false
	}
	_ = cmd.Process.Release()
	for i := 0; i < 60; i++ {
		time.Sleep(200 * time.Millisecond)
		if url := runningInstance(); url != "" {
			openUIWindow(url)
			return true
		}
	}
	messageBox(AppName, "Resguardo no pudo arrancar el motor en segundo plano.")
	return true
}

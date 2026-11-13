package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// closeRunningApp cierra la instancia abierta de Resguardo (motor, ícono y
// ventana). La usan el instalador y el desinstalador antes de reemplazar o
// borrar el programa: Resguardo.exe --cerrar
func closeRunningApp(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	var inst instanceInfo
	if b, err := os.ReadFile(instancePath()); err == nil {
		_ = json.Unmarshal(b, &inst)
	}
	if inst.Port != 0 && runningInstance() != "" {
		req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/quit", inst.Port), nil)
		req.Header.Set("X-Resguardo-Token", inst.Token)
		req.Header.Set("X-Requested-With", "resguardo")
		c := http.Client{Timeout: 3 * time.Second}
		if resp, err := c.Do(req); err == nil {
			resp.Body.Close()
		}
		for time.Now().Before(deadline) && runningInstance() != "" {
			time.Sleep(200 * time.Millisecond)
		}
		if inst.PID > 0 && inst.PID != os.Getpid() {
			waitProcessExit(inst.PID, time.Until(deadline))
		}
	}
	closeAppWindows(time.Until(deadline))
	return runningInstance() == ""
}

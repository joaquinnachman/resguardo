//go:build windows

package main

import (
	"os"
	"path/filepath"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

// runWindow muestra la interfaz en una ventana propia (WebView2, el motor de Edge).
// Si ya hay una ventana abierta, la trae al frente en lugar de abrir otra.
func runWindow(url string) {
	name, _ := windows.UTF16PtrFromString("Local\\ResguardoVentana" + instanceName)
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h != 0 && err == windows.ERROR_ALREADY_EXISTS {
		title, _ := windows.UTF16PtrFromString("Resguardo")
		if hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title))); hwnd != 0 {
			procShowWindow.Call(hwnd, 9) // SW_RESTORE
			procSetForegroundWindow.Call(hwnd)
			return
		}
	}
	dataPath := filepath.Join(dataDir(), "ventana")
	_ = os.MkdirAll(dataPath, 0o755)
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "Resguardo",
			Width:  1280,
			Height: 840,
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		openInBrowser(url)
		return
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
}

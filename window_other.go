//go:build !windows

package main

func runWindow(url string) { openInBrowser(url) }

//go:build darwin && arm64

package main

import _ "embed"

//go:embed embed/7zz-darwin-arm64
var embedded7z []byte

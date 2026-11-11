//go:build linux && amd64

package main

import _ "embed"

//go:embed embed/7zz-linux-amd64
var embedded7z []byte

//go:build !(android && cgo)

package main

import (
	"io"
	"os"
)

// logOutput is where node logs go: the process's standard error.
var logOutput io.Writer = os.Stderr

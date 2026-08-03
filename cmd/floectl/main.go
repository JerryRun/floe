package main

import (
	"os"

	"floe/internal/app"
)

func main() {
	os.Exit(app.RunCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

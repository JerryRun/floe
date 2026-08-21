package main

import (
	"fmt"
	"os"
	"path/filepath"

	"floe/internal/app"
)

func main() {
	dataDirectory := filepath.Join(os.TempDir(), "floe-readme-demo-data")
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		panic(err)
	}

	server, err := app.New(dataDirectory)
	if err != nil {
		panic(err)
	}
	url, done, err := server.Start("127.0.0.1:7581")
	if err != nil {
		panic(err)
	}
	fmt.Println(url)
	if err := <-done; err != nil {
		panic(err)
	}
}

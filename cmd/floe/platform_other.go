//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"floe/internal/app"
)

func acquireSingleInstance() (func(), bool, error) { return func() {}, true, nil }
func platformAlreadyRunning()                      {}
func platformFatal(message string)                 { fmt.Fprintln(os.Stderr, message) }

func runPlatform(server *app.Server, url string, done <-chan error, noOpen bool, preferences *app.Preferences) {
	fmt.Printf("Floe is ready: %s\n", url)
	if !noOpen && preferences.OpenBrowserOnStartup() {
		if err := app.OpenBrowser(url); err != nil {
			fmt.Fprintf(os.Stderr, "Could not open browser automatically: %v\n", err)
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-done:
		app.LogServeError(err)
	case <-signals:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

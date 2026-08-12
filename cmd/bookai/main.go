// Package main is the bookai binary entry point.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"ebook-reader/internal/cli"
	"ebook-reader/internal/tts"
)

func main() {
	os.Exit(run())
}

func run() int {
	tts.RegisterEngines()
	root := cli.NewRoot()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	root.SetContext(ctx)
	if err := root.Execute(); err != nil {
		return cli.Fail(err)
	}
	return 0
}

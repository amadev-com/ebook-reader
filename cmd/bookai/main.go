// Package main is the bookai binary entry point.
package main

import (
	"os"

	"ebook-reader/internal/cli"
	"ebook-reader/internal/tts"
)

func main() {
	os.Exit(run())
}

func run() int {
	tts.RegisterEngines()
	root := cli.NewRoot()
	if err := root.Execute(); err != nil {
		return cli.Fail(err)
	}
	return 0
}

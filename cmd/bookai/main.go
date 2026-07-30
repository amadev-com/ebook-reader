// Package main is the bookai binary entry point.
package main

import (
	"os"

	"ebook-reader/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	root := cli.NewRoot()
	if err := root.Execute(); err != nil {
		return cli.Fail(err)
	}
	return 0
}

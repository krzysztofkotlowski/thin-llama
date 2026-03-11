package main

import (
	"os"

	"github.com/krzysztofkotlowski/thin-llama/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	}))
}

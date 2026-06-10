package main

import (
	"os"

	"github.com/webkaz-labs/kagikae/internal/cmd"
)

func main() {
	os.Exit(cmd.Root(os.Args[1:]))
}

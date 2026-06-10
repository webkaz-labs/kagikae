package main

import (
	"os"

	"example.com/dotfiles-tool/internal/cmd"
)

func main() {
	os.Exit(cmd.Root(os.Args[1:]))
}

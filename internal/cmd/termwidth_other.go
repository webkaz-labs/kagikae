//go:build !darwin && !linux

package cmd

// stdoutColumns has no terminal query on this platform; tables stay tables.
func stdoutColumns() int { return 0 }

// Command flux-vm loads and executes a compiled flux .flx file.
//
// Usage:
//
//	flux-vm <file.flx>
//
// Errors are reported on stderr and produce a non-zero exit code.
package main

import (
	"fmt"
	"os"

	"flux/vm/cpu"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: flux-vm <file.flx>")
		os.Exit(1)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}

	c := cpu.New()
	if err := c.LoadBinary(data); err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	if err := c.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(1)
	}
}

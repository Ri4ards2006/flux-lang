// Command flux-vm loads and executes a compiled flux .flx file.
// After the top-level bytecode finishes, if the program registered
// any ON_CHAT subscriptions, the CLI enters an interactive chat
// simulator that reads "<username>: <message>" lines from stdin and
// dispatches each one through DeliverChatMessage.
//
// Usage:
//
//	flux-vm <file.flx>
//
// Errors are reported on stderr and produce a non-zero exit code.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

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
	flushLogs(c)

	if len(c.ActiveSubscriptions) == 0 {
		return
	}

	fmt.Println("--- interactive chat simulator ---")
	fmt.Println("format: <username>: <message>")
	fmt.Println("Ctrl+D, blank line, or 'quit' to exit")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "quit" || line == "exit" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "expected 'username: message'")
			continue
		}
		username := strings.TrimSpace(parts[0])
		message := strings.TrimSpace(parts[1])
		c.DeliverChatMessage(username, message)
		flushLogs(c)
	}
}

// flushLogs prints every accumulated log line and resets the buffer
// so the next delivery's logs accumulate independently.
func flushLogs(c *cpu.CPU) {
	for _, line := range c.Logs {
		fmt.Println(line)
	}
	c.Logs = nil
}

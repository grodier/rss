// Command admin is the admin site and its command-line tools (see package
// internal/admin). It runs on its own origin, admin.paperround.app, as its
// own service, from the same image as www.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// version identifies the build; the container image sets it to the commit SHA
// with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// usage lists the subcommands.
const usage = `Usage:
  admin [serve] [flags]  run the admin site (admin serve -help lists its flags)
`

// run runs the command in args and returns the process's exit code: 0 on
// success or -help, 1 when the command fails, 2 for invalid configuration or
// an unknown subcommand. "admin [flags]" and "admin serve [flags]" serve the
// admin site; later issues add subcommands.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return runServe(args, getenv, stdout, stderr)
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], getenv, stdout, stderr)
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "admin: unknown command %q\n%s", args[0], usage)
	return 2
}

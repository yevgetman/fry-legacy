package main

import (
	"errors"
	"os"

	"github.com/yevgetman/fry/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// Honour subcommand-requested exit codes (e.g. fry copilot status
		// returns code 2 for stale, fry copilot attach returns code 3 for
		// busy). Falls through to the default exit-1 path on any other
		// error type.
		var ee cli.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		os.Exit(1)
	}
}

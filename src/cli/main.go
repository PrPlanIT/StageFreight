package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/cli/cmd"
)

func main() {
	// Self-reexec askpass handler. Must run before anything else — no logging,
	// no config loading, no cobra. Git calls this binary with GIT_ASKPASS when
	// it needs credentials. We inspect the prompt, print the answer, and exit.
	// This path must never fall through into normal command dispatch.
	if os.Getenv("STAGEFREIGHT_ASKPASS") == "1" {
		prompt := ""
		if len(os.Args) > 1 {
			prompt = os.Args[len(os.Args)-1]
		}
		switch {
		case strings.HasPrefix(prompt, "Username"):
			fmt.Print(os.Getenv("STAGEFREIGHT_GIT_USERNAME"))
		case strings.HasPrefix(prompt, "Password"):
			fmt.Print(os.Getenv("STAGEFREIGHT_GIT_PASSWORD"))
		}
		// Unknown prompt → print nothing. Always exit cleanly.
		os.Exit(0)
	}

	cmd.Execute()
}

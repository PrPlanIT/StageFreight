package main

import (
	"os"

	"github.com/PrPlanIT/StageFreight/src/cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

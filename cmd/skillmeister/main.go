// Command skillmeister routes an agent to skills in a local library.
package main

import (
	"os"

	"github.com/shantanugoel/skillmeister/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.StdEnv()))
}

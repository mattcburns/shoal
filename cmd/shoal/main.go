// Command shoal is the composition root for the Shoal application.
package main

import (
	"os"

	"github.com/mattcburns/shoal/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}

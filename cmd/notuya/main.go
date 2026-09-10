// Command notuya is the CLI for controlling Tuya local-protocol v3.5 bulbs.
// Scaffolding only for now — real command dispatch lands in milestone M4.
package main

import (
	"fmt"
	"os"
)

const usage = `Usage:
  notuya on
  notuya off
  notuya color RRGGBB
  notuya brightness 0-100
  notuya get-color
  notuya list
  notuya scan
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "notuya: not implemented yet")
	os.Exit(1)
}

package main

import (
	"fmt"
	"os"

	"kernelhub/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "kernelhub: %v\n", err)
		os.Exit(1)
	}
}

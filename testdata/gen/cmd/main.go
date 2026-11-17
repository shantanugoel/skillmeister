// Command cmd materializes the synthetic fixture library.
//
//	go run ./testdata/gen/cmd -n 100 -out /path/to/library
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/shantanugoel/skillmeister/testdata/gen"
)

func main() {
	n := flag.Int("n", 300, "number of skills")
	out := flag.String("out", "", "output directory (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "gen: -out is required")
		os.Exit(1)
	}
	f, err := gen.Materialize(*out, *n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d skills to %s\n", f.Skills, f.Root)
}

// ABOUTME: CLI entry point for jpg2ascii, converts JPEG images to ASCII art.
// ABOUTME: Accepts --width and --chars flags plus a positional JPEG path argument.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	width := flag.Int("width", 80, "output width in characters")
	chars := flag.String("chars", " .:-=+*#%@", "character palette from dark to light")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: jpg2ascii [--width N] [--chars STR] <image.jpg>\n")
		os.Exit(1)
	}

	result, err := LoadAndConvert(flag.Arg(0), *width, *chars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(result)
}

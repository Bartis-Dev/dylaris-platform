// Command apidocs regenerates the HTTP API reference from the router source.
//
//	cd platform/core && go run ./cmd/apidocs
//
// TestAPIDocIsCurrent fails when the checked-in document no longer matches, so
// adding a route means running this and committing the result.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"dylaris-core/apidoc"
)

func main() {
	coreDir := flag.String("core", ".", "path to the core module directory")
	check := flag.Bool("check", false, "report whether the document is current instead of writing it")
	flag.Parse()

	doc, err := apidoc.Generate(*coreDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apidocs: %v\n", err)
		os.Exit(1)
	}
	out := filepath.Join(*coreDir, apidoc.DocPath)

	if *check {
		current, err := os.ReadFile(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "apidocs: %v\n", err)
			os.Exit(1)
		}
		if apidoc.Normalize(string(current)) != doc {
			fmt.Fprintf(os.Stderr, "apidocs: %s is out of date, run: go run ./cmd/apidocs\n", out)
			os.Exit(1)
		}
		fmt.Printf("apidocs: %s is current\n", out)
		return
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "apidocs: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "apidocs: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("apidocs: wrote %s\n", out)
}

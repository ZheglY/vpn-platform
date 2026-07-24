package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ZheglY/vpn-platform/tools/credentialstage/internal/staging"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: credentialstage <stage|verify> [flags]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "path to the credential manifest")
	sourceRoot := flags.String("source", "", "source directory")
	destinationRoot := flags.String("destination", "", "destination directory")
	group := flags.String("group", "", "optional manifest group")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("parse %s flags: %w", args[0], err)
	}
	if *manifestPath == "" || *destinationRoot == "" {
		return errors.New("manifest and destination are required")
	}
	manifest, err := staging.LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "stage":
		if *sourceRoot == "" {
			return errors.New("source is required for stage")
		}
		return staging.Stage(manifest, *sourceRoot, *destinationRoot, *group)
	case "verify":
		return staging.Verify(manifest, *destinationRoot, *group)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

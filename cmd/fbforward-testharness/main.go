package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NodePath81/fbforward/test/harness"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s <run|validate> <scenario.yaml>\n", os.Args[0])
	}
	flag.Parse()

	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(1)
	}
	cmd := flag.Arg(0)
	scenarioPath := flag.Arg(1)

	switch cmd {
	case "validate":
		if _, err := harness.LoadScenario(scenarioPath); err != nil {
			fmt.Fprintf(os.Stderr, "invalid scenario: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("scenario valid")
	case "run":
		runScenario(scenarioPath)
	default:
		flag.Usage()
		os.Exit(1)
	}
}

func runScenario(path string) {
	scenario, err := harness.LoadScenario(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load scenario: %v\n", err)
		os.Exit(1)
	}
	workDir := filepath.Join(os.TempDir(), "fbforward-testharness")
	h := harness.NewHarness(workDir, scenario)
	if err := h.EnsureWorkDir(); err != nil {
		fmt.Fprintf(os.Stderr, "workdir: %v\n", err)
		os.Exit(1)
	}
	if err := h.Setup(); err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}
	defer h.Cleanup()

	if err := h.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}
	if err := h.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
	if err := h.Verify(); err != nil {
		fmt.Fprintf(os.Stderr, "verify: %v\n", err)
		os.Exit(1)
	}
	if err := h.ExportArtifacts(); err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("scenario completed")
}

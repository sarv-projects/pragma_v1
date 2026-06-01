package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sarv-projects/pragma/internal/config"
)

func runClean() int {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		return 1
	}

	outputDir := cfg.Output.Directory
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		fmt.Printf("No output directory found at %s — nothing to clean.\n", outputDir)
		return 0
	}

	var runs []string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 4 && e.Name()[:4] == "run-" {
			runs = append(runs, e.Name())
		}
	}

	if len(runs) == 0 {
		fmt.Println("No old runs to clean.")
		return 0
	}

	// Keep the 5 most recent, delete the rest
	sort.Strings(runs)
	const keep = 5
	if len(runs) <= keep {
		fmt.Printf("Only %d run(s) found — keeping all (threshold is %d).\n", len(runs), keep)
		return 0
	}

	toDelete := runs[:len(runs)-keep]
	fmt.Printf("Removing %d old run(s), keeping %d most recent...\n", len(toDelete), keep)
	failed := 0
	for _, name := range toDelete {
		path := filepath.Join(outputDir, name)
		if err := os.RemoveAll(path); err != nil {
			fmt.Fprintf(os.Stderr, "  Failed to remove %s: %v\n", name, err)
			failed++
		} else {
			fmt.Printf("  Removed %s\n", name)
		}
	}
	if failed > 0 {
		return 1
	}
	fmt.Printf("Done. %d run(s) remaining.\n", keep)
	return 0
}

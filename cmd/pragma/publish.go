package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sarv-projects/pragma/internal/config"
)

func runPublish() int {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to load config: %v\n", err)
		return 1
	}

	outputDir := cfg.Output.Directory
	if outputDir == "" {
		home, _ := os.UserHomeDir()
		outputDir = filepath.Join(home, ".pragma", "output")
	}

	// Check if git is available
	gitPath, err := exec.LookPath("git")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: git is not installed or not in PATH.\n")
		fmt.Fprintf(os.Stderr, "Please install git to use 'pragma publish'.\n")
		return 1
	}
	_ = gitPath

	// Check if output directory exists
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: output directory does not exist: %s\n", outputDir)
		fmt.Fprintf(os.Stderr, "Run a project generation first.\n")
		return 1
	}

	// Find the most recent run directory
	entries, err := os.ReadDir(outputDir)
	if err != nil || len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no project runs found in %s\n", outputDir)
		return 1
	}

	// Use the last directory entry (most recent run)
	var runDir string
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			runDir = filepath.Join(outputDir, entries[i].Name())
			break
		}
	}
	if runDir == "" {
		fmt.Fprintf(os.Stderr, "Error: no project runs found in %s\n", outputDir)
		return 1
	}

	// Check if directory has a .git repo, if not initialize one
	gitDir := filepath.Join(runDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		fmt.Println("Initializing git repository...")
		gitInit := exec.Command("git", "init")
		gitInit.Dir = runDir
		if err := gitInit.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: git init failed: %v\n", err)
			return 1
		}
		gitAdd := exec.Command("git", "add", "-A")
		gitAdd.Dir = runDir
		if err := gitAdd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: git add failed: %v\n", err)
			return 1
		}
		gitCommit := exec.Command("git", "-c", "user.name=Pragma", "-c", "user.email=pragma@local", "commit", "-m", "Initial generation")
		gitCommit.Dir = runDir
		if err := gitCommit.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: git commit failed: %v\n", err)
			return 1
		}
	}

	fmt.Printf("Project directory: %s\n\n", runDir)

	// If gh CLI is available and the directory already has a git repo, offer to publish
	ghPath, ghErr := exec.LookPath("gh")
	if ghErr == nil && ghPath != "" {
		// Check if remote already exists
		remoteCheck := exec.Command("git", "remote", "get-url", "origin")
		remoteCheck.Dir = runDir
		if remoteOut, err := remoteCheck.Output(); err == nil && len(remoteOut) > 0 {
			fmt.Printf("Remote already configured: %s\n", strings.TrimSpace(string(remoteOut)))
			fmt.Println("Pushing to remote...")
			pushCmd := exec.Command("git", "push", "-u", "origin", "main")
			pushCmd.Dir = runDir
			pushCmd.Stdout = os.Stdout
			pushCmd.Stderr = os.Stderr
			if err := pushCmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Push failed: %v\n", err)
				return 1
			}
			fmt.Println("Pushed successfully!")
			return 0
		}

		// No remote — create repo with gh
		projectName := filepath.Base(runDir)
		fmt.Printf("Creating GitHub repository '%s'...\n", projectName)
		ghCmd := exec.Command("gh", "repo", "create", projectName, "--public", "--source=.", "--push")
		ghCmd.Dir = runDir
		ghCmd.Stdout = os.Stdout
		ghCmd.Stderr = os.Stderr
		if err := ghCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "\ngh repo create failed: %v\n", err)
			fmt.Println("Falling back to manual instructions:")
			printPublishInstructions(runDir)
			return 1
		}
		fmt.Println("Repository created and pushed successfully!")
		return 0
	}

	// No gh CLI — print manual instructions
	printPublishInstructions(runDir)
	return 0
}

func printPublishInstructions(runDir string) {
	fmt.Println("To publish your project:")
	fmt.Println("  1. Create a new repo on GitHub")
	fmt.Println("  2. git remote add origin <url>")
	fmt.Println("  3. git push -u origin main")
	fmt.Printf("\nOr install the GitHub CLI and run:\n")
	fmt.Printf("  cd %s\n", runDir)
	fmt.Println("  gh repo create my-project --public --source=. --push")
}

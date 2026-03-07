package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
	"golang.org/x/term"
)

var importCmd = &cobra.Command{
	Use:     "import",
	GroupID: "sync",
	Short:   "Import issues from JSONL format",
	Long: `Import issues from JSON Lines format (one JSON object per line).

Reads from stdin by default, or use -i flag for file input.

Each line must be a valid JSON object representing an issue. The format
is compatible with 'bd export' for round-trip backup and restore.

EXAMPLES:
  bd import -i backup.jsonl              # Import from file
  bd import -i backup.jsonl --dry-run    # Preview without changes
  cat data.jsonl | bd import             # Import from stdin
  bd import -i old.jsonl --skip-prefix-validation  # Import with mismatched prefix`,
	RunE: runImport,
}

var (
	importInput                string
	importDryRun               bool
	importSkipPrefixValidation bool
)

func init() {
	importCmd.Flags().StringVarP(&importInput, "input", "i", "", "Input file path (default: stdin)")
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "Preview changes without applying them")
	importCmd.Flags().BoolVar(&importSkipPrefixValidation, "skip-prefix-validation", false, "Skip prefix validation for legacy data")
	rootCmd.AddCommand(importCmd)
}

func runImport(cmd *cobra.Command, args []string) error {
	CheckReadonly("import")

	// Check for positional arguments (common mistake: bd import file.jsonl instead of bd import -i file.jsonl)
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument(s): %v\n\nDid you mean: bd import -i %s", args, args[0])
	}

	// Check if stdin is being used interactively (not piped)
	if importInput == "" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "Error: No input specified.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  bd import -i backup.jsonl              # Import from file\n")
		fmt.Fprintf(os.Stderr, "  bd import -i backup.jsonl --dry-run    # Preview changes\n")
		fmt.Fprintf(os.Stderr, "  cat data.jsonl | bd import             # Import from stdin\n\n")
		fmt.Fprintf(os.Stderr, "For more information, run: bd import --help\n")
		os.Exit(1)
	}

	// Ensure database directory exists
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0750); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open input
	in := os.Stdin
	if importInput != "" {
		//nolint:gosec // G304: user-provided file path is intentional
		f, err := os.Open(importInput)
		if err != nil {
			return fmt.Errorf("opening input file: %w", err)
		}
		defer f.Close()
		in = f
	}

	// Read and parse all JSONL
	ctx := rootCtx
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)

	var issues []*types.Issue
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if line == "" {
			continue
		}

		var issue types.Issue
		if err := json.Unmarshal([]byte(line), &issue); err != nil {
			return fmt.Errorf("parsing line %d: %w", lineNum, err)
		}

		// Skip tombstone entries
		if issue.Status == "tombstone" {
			continue
		}

		issue.SetDefaults()

		if issue.Status == types.StatusClosed && issue.ClosedAt == nil {
			now := time.Now()
			issue.ClosedAt = &now
		}

		issues = append(issues, &issue)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading input: %w", err)
	}

	if len(issues) == 0 {
		fmt.Fprintln(os.Stderr, "No issues to import.")
		return nil
	}

	// Auto-detect and set prefix if not configured
	configuredPrefix, err := store.GetConfig(ctx, "issue_prefix")
	if err == nil && strings.TrimSpace(configuredPrefix) == "" {
		detectedPrefix := detectImportPrefix(issues)
		if detectedPrefix != "" {
			if err := store.SetConfig(ctx, "issue_prefix", detectedPrefix); err != nil {
				return fmt.Errorf("failed to set issue_prefix: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Initialized prefix '%s' from imported issues\n", detectedPrefix)
		}
	}

	// Dry-run: just report what would happen
	if importDryRun {
		fmt.Fprintf(os.Stderr, "Would import %d issues (dry-run, no changes made)\n", len(issues))
		return nil
	}

	// Determine whether to skip prefix validation
	skipPrefix := importSkipPrefixValidation

	// Import via the Dolt store's batch creation
	err = store.CreateIssuesWithFullOptions(ctx, issues, getActorWithGit(), storage.BatchCreateOptions{
		OrphanHandling:       storage.OrphanAllow,
		SkipPrefixValidation: skipPrefix,
	})
	if err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	commandDidWrite.Store(true)

	fmt.Fprintf(os.Stderr, "Import complete: %d issues imported\n", len(issues))
	return nil
}

// detectImportPrefix extracts the most common prefix from a set of issues.
func detectImportPrefix(issues []*types.Issue) string {
	if len(issues) == 0 {
		return ""
	}

	counts := make(map[string]int)
	for _, issue := range issues {
		prefix := utils.ExtractIssuePrefix(issue.ID)
		if prefix != "" {
			counts[prefix]++
		}
	}

	var best string
	bestCount := 0
	for prefix, count := range counts {
		if count > bestCount {
			best = prefix
			bestCount = count
		}
	}
	return best
}

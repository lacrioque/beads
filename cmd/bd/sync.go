package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/config"
)

var syncCmd = &cobra.Command{
	Use:     "sync",
	GroupID: "sync",
	Short:   "Sync beads data with configured backend",
	Long: `Dispatch sync to the configured backend.

The sync target is read from config key "sync.target" (default: "dolt").
Use the + separator to enable options, e.g. "gitlab+milestones+epics".

Valid targets: dolt, gitlab, federation, repo, backup
Valid options (gitlab only): milestones, epics

Flags --dry-run, --pull-only, and --push-only are forwarded to backends
that support them. For backend-specific flags, invoke the backend
command directly (e.g., bd gitlab sync --prefer-local).

Examples:
  bd config set sync.target dolt
  bd sync                          # runs dolt pull + push

  bd config set sync.target gitlab+milestones
  bd sync --dry-run                # dry-run gitlab sync with milestones

  bd config set sync.target federation
  bd sync                          # federation sync with all peers`,
	RunE: runSync,
}

var (
	syncDryRun   bool
	syncPullOnly bool
	syncPushOnly bool
	syncFlushOnly bool
)

func init() {
	rootCmd.AddCommand(syncCmd)
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Show what would be synced without making changes")
	syncCmd.Flags().BoolVar(&syncPullOnly, "pull-only", false, "Only pull from remote")
	syncCmd.Flags().BoolVar(&syncPushOnly, "push-only", false, "Only push to remote")
	syncCmd.Flags().BoolVar(&syncFlushOnly, "flush-only", false, "Legacy no-op flag (JSONL flush machinery removed)")
}

// parseSyncTarget splits a sync target string like "gitlab+milestones+epics"
// into the base target and option list.
func parseSyncTarget(target string) (string, []string) {
	parts := strings.Split(target, "+")
	if len(parts) == 1 {
		return parts[0], nil
	}
	return parts[0], parts[1:]
}

func runSync(cmd *cobra.Command, args []string) error {
	// --flush-only is a legacy no-op; JSONL flush machinery was removed.
	// Pre-commit hooks still call "bd sync --flush-only", so exit cleanly.
	if syncFlushOnly {
		return nil
	}

	target := config.GetString("sync.target")
	if target == "" {
		target = "dolt"
	}

	base, options := parseSyncTarget(target)

	switch base {
	case "dolt":
		return syncDolt(cmd)
	case "gitlab":
		return syncGitLab(cmd, args, options)
	case "federation":
		federationSyncCmd.Run(cmd, args)
		return nil
	case "repo":
		return repoSyncCmd.RunE(cmd, args)
	case "backup":
		return backupSyncCmd.RunE(cmd, args)
	default:
		return fmt.Errorf("unknown sync target %q; set sync.target to one of: dolt, gitlab, federation, repo, backup", base)
	}
}

func syncDolt(cmd *cobra.Command) error {
	ctx := context.Background()
	st := getStore()
	if st == nil {
		return fmt.Errorf("no store available")
	}

	if syncPullOnly && syncPushOnly {
		return fmt.Errorf("cannot use both --pull-only and --push-only")
	}

	if syncDryRun {
		if !syncPushOnly {
			fmt.Println("[dry-run] Would pull from Dolt remote")
		}
		if !syncPullOnly {
			fmt.Println("[dry-run] Would push to Dolt remote")
		}
		return nil
	}

	if !syncPushOnly {
		fmt.Println("Pulling from Dolt remote...")
		if err := st.Pull(ctx); err != nil {
			return fmt.Errorf("pull failed: %w", err)
		}
		fmt.Println("Pull complete.")
	}

	if !syncPullOnly {
		fmt.Println("Pushing to Dolt remote...")
		if err := st.Push(ctx); err != nil {
			return fmt.Errorf("push failed: %w", err)
		}
		fmt.Println("Push complete.")
	}

	return nil
}

func syncGitLab(cmd *cobra.Command, args []string, options []string) error {
	// Forward universal flags to gitlab sync package vars
	gitlabSyncDryRun = syncDryRun
	gitlabSyncPullOnly = syncPullOnly
	gitlabSyncPushOnly = syncPushOnly

	// Enable options from sync target
	for _, opt := range options {
		switch opt {
		case "milestones":
			gitlabSyncMilestones = true
		case "epics":
			gitlabSyncEpics = true
		}
	}

	return runGitLabSync(cmd, args)
}

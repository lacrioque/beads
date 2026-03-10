---
id: sync
title: Sync & Export
sidebar_position: 6
---

# Sync & Export Commands

Commands for synchronizing beads data with backends.

## bd sync

Configurable dispatch command that routes to the appropriate sync backend.

```bash
bd sync [flags]
```

**What it does:**

`bd sync` reads the `sync.target` config key and dispatches to the matching backend. The default target is `dolt` (pull + push).

**Flags:**
```bash
--dry-run      Preview without changes
--pull-only    Only pull from remote
--push-only    Only push to remote
--flush-only   Legacy no-op (kept for pre-commit hook compatibility)
```

**Examples:**
```bash
bd sync                # dispatch to configured target (default: dolt)
bd sync --dry-run      # preview what would happen
bd sync --pull-only    # only pull, skip push
```

### Configuring the sync target

Set `sync.target` in your project config to choose which backend `bd sync` dispatches to:

```bash
bd config set sync.target dolt                    # Dolt pull + push (default)
bd config set sync.target gitlab                  # GitLab issue sync
bd config set sync.target gitlab+milestones       # GitLab + milestone sync
bd config set sync.target gitlab+epics            # GitLab + epic sync
bd config set sync.target gitlab+milestones+epics # GitLab + both
bd config set sync.target federation              # Federation peer sync
bd config set sync.target repo                    # Multi-repo sync
bd config set sync.target backup                  # Dolt backup sync
```

The `+` separator enables additional options. Currently, options are only supported for the `gitlab` target (`milestones`, `epics`).

### Sync targets

| Target | What it does |
|--------|-------------|
| `dolt` | Pull from Dolt remote, then push (default) |
| `gitlab` | Bidirectional issue sync with GitLab |
| `gitlab+milestones` | Issue sync + sync GitLab milestones as epics |
| `gitlab+epics` | Issue sync + sync GitLab group epics as epics |
| `gitlab+milestones+epics` | Issue sync + milestones + group epics |
| `federation` | Sync with all configured federation peers |
| `repo` | Pull issues from configured additional repositories |
| `backup` | Push database to configured Dolt backup |

### Backend-specific flags

The universal flags (`--dry-run`, `--pull-only`, `--push-only`) are forwarded to all backends that support them. For backend-specific flags (e.g., `--prefer-local` for GitLab, `--peer` for federation), invoke the backend command directly:

```bash
bd gitlab sync --prefer-local
bd federation sync --peer myteam
bd backup sync
```

**When to use:**
- End of work session
- Before switching branches
- After significant changes
- Any time you want a one-command sync regardless of backend

## bd export

Export database to JSONL format (for backup and migration).

```bash
bd export [flags]
```

**Flags:**
```bash
--output, -o    Output file (default: stdout)
--dry-run       Preview without writing
--json          JSON output
```

**Examples:**
```bash
bd export
bd export -o backup.jsonl
bd export --dry-run
```

**When to use:** `bd export` is for backup and data migration, not day-to-day sync. Dolt handles sync natively via `bd dolt push`/`bd dolt pull`.

## bd import

Import from JSONL file (for migration and recovery).

```bash
bd import -i <file> [flags]
```

**Flags:**
```bash
--input, -i           Input file (required)
--dry-run             Preview without changes
--orphan-handling     How to handle missing parents
--dedupe-after        Run duplicate detection after import
--json                JSON output
```

**Orphan handling modes:**
| Mode | Behavior |
|------|----------|
| `allow` | Import orphans without validation (default) |
| `resurrect` | Restore deleted parents as tombstones |
| `skip` | Skip orphaned children with warning |
| `strict` | Fail if parent missing |

**Examples:**
```bash
bd import -i backup.jsonl
bd import -i backup.jsonl --dry-run
bd import -i issues.jsonl --orphan-handling resurrect
bd import -i issues.jsonl --dedupe-after --json
```

**When to use:** `bd import` is for loading data from external JSONL files or migrating from a legacy setup. For day-to-day sync, use `bd dolt push`/`bd dolt pull`.

## bd migrate

Migrate database schema.

```bash
bd migrate [flags]
```

**Flags:**
```bash
--inspect    Show migration plan (for agents)
--dry-run    Preview without changes
--cleanup    Remove old files after migration
--yes        Skip confirmation
--json       JSON output
```

**Examples:**
```bash
bd migrate --inspect --json
bd migrate --dry-run
bd migrate
bd migrate --cleanup --yes
```

## bd hooks

Manage git hooks.

```bash
bd hooks <subcommand> [flags]
```

**Subcommands:**
| Command | Description |
|---------|-------------|
| `install` | Install git hooks |
| `uninstall` | Remove git hooks |
| `status` | Check hook status |

**Examples:**
```bash
bd hooks install
bd hooks status
bd hooks uninstall
```

## Auto-Sync Behavior

### With Dolt Server Mode (Default)

When the Dolt server is running, sync is handled automatically:
- Dolt auto-commit tracks changes
- Dolt-native replication handles remote sync

Start the Dolt server with `bd dolt start`.

### Embedded Mode (No Server)

In CI/CD pipelines and ephemeral environments, no server is needed:
- Changes written directly to the database
- Must manually sync

```bash
bd create "CI-generated task"
bd sync  # Manual sync needed
```

## Conflict Resolution

Dolt handles conflict resolution at the database level using its built-in
merge capabilities. When conflicts arise during `dolt pull`, Dolt identifies
conflicting rows and allows resolution through SQL.

```bash
# Check for conflicts after sync
bd doctor --fix
```

## Deletion Tracking

Deletions are tracked in the Dolt database:

```bash
# Delete issue
bd delete bd-42

# View deletions
bd deleted
bd deleted --since=30d

# Deletions propagate via Dolt sync
bd sync
```

## Best Practices

1. **Always sync at session end** - `bd sync`
2. **Install git hooks** - `bd hooks install`
3. **Check sync status** - `bd info` shows sync state

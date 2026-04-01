package cli

import (
	"errors"
	"flag"
	"fmt"

	"kernelhub/internal/commands"
)

var errHelp = errors.New("help requested")

func Run(args []string) error {
	if len(args) == 0 {
		printRootUsage()
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		printRootUsage()
		return nil
	case "prepare":
		return runPrepare(args[1:])
	case "sync-git":
		return runSyncGit(args[1:])
	case "export":
		return runExport(args[1:])
	default:
		printRootUsage()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printRootUsage() {
	fmt.Println(`KernelHub (minimal Go skeleton)

Usage:
  kernelhub <command> [flags]

Commands:
  prepare    Prepare AKO4ALL task input from mimikyu kernel source
  sync-git   Parse AKO4ALL branch commits into KernelHub history (skeleton)
  export     Export static snapshot/dashboard from history data (skeleton)

Use "kernelhub <command> --help" for command-specific flags.`)
}

func runPrepare(args []string) error {
	fs := flag.NewFlagSet("prepare", flag.ContinueOnError)
	kernelSrc := fs.String("kernel-src", "", "Kernel source file or directory")
	akoRoot := fs.String("ako-root", "third_party/AKO4ALL", "AKO4ALL root path")
	referenceSrc := fs.String("reference-src", "", "Optional reference source")
	benchSrc := fs.String("bench-src", "", "Optional custom bench source")
	contextSrc := fs.String("context-src", "", "Optional context source")
	runID := fs.String("run-id", "", "Optional run id")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.Prepare(commands.PrepareOptions{
		KernelSrc:    *kernelSrc,
		AKORoot:      *akoRoot,
		ReferenceSrc: *referenceSrc,
		BenchSrc:     *benchSrc,
		ContextSrc:   *contextSrc,
		RunID:        *runID,
		DryRun:       *dryRun,
	})
}

func runSyncGit(args []string) error {
	fs := flag.NewFlagSet("sync-git", flag.ContinueOnError)
	repoPath := fs.String("repo-path", "third_party/AKO4ALL", "AKO4ALL git repo path")
	branch := fs.String("branch", "", "Branch to parse, e.g. agent/gemm/agent-a")
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	runID := fs.String("run-id", "", "Optional run id override")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.SyncGit(commands.SyncGitOptions{
		RepoPath: *repoPath,
		Branch:   *branch,
		DBPath:   *dbPath,
		RunID:    *runID,
		DryRun:   *dryRun,
	})
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	outPath := fs.String("out", "./workspace/history_snapshot.json", "Snapshot output path")
	htmlOut := fs.String("html-out", "./workspace/history_dashboard.html", "Static HTML output path")
	format := fs.String("format", "json", "Output format: json|toml")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.Export(commands.ExportOptions{
		DBPath:  *dbPath,
		OutPath: *outPath,
		HTMLOut: *htmlOut,
		Format:  *format,
		DryRun:  *dryRun,
	})
}

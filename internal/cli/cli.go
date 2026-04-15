package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"kernelhub/internal/commands"
	"kernelhub/internal/server"
)

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
	case "archive-git":
		return runArchiveGit(args[1:])
	case "restore-git":
		return runRestoreGit(args[1:])
	case "export":
		return runExport(args[1:])
	case "generate-context":
		return runGenerateContext(args[1:])
	case "patch":
		return runPatch(args[1:])
	case "recalibrate":
		return runRecalibrate(args[1:])
	case "serve":
		return runServe(args[1:])
	case "server":
		return runServer(args[1:])
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
  archive-git Archive git objects into history DB for offline restore
  restore-git Restore archived git objects from history DB
  export     Export self-contained static HTML dashboard for offline viewing
  generate-context  Generate optimization history context for a kernel from prior runs
  patch      Generate framework patches from optimized kernels via kernel-adapter
  recalibrate Re-bench existing runs with --baseline to get true baseline speedup
  serve      Start local HTTP dashboard powered by history DB
  server     Start KernelHub API server with rate limiting

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
	workloadConfig := fs.String("workload-config", "", "Workload config JSON with kernel shapes to override in reference.py")
	dbPath := fs.String("db-path", "", "History DB path for cross-run context injection")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.Prepare(commands.PrepareOptions{
		KernelSrc:      *kernelSrc,
		AKORoot:        *akoRoot,
		ReferenceSrc:   *referenceSrc,
		BenchSrc:       *benchSrc,
		ContextSrc:     *contextSrc,
		RunID:          *runID,
		WorkloadConfig: *workloadConfig,
		DBPath:         *dbPath,
		DryRun:         *dryRun,
	})
}

func runSyncGit(args []string) error {
	fs := flag.NewFlagSet("sync-git", flag.ContinueOnError)
	repoPath := fs.String("repo-path", "third_party/AKO4ALL", "AKO4ALL git repo path")
	branch := fs.String("branch", "", "Branch to parse, e.g. agent/gemm/agent-a")
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	runID := fs.String("run-id", "", "Optional run id override")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	force := fs.Bool("force", false, "Bypass history integrity check")
	upsert := fs.Bool("upsert", false, "Merge new iterations into existing run instead of failing on duplicate run_id")
	replace := fs.Bool("replace", false, "Delete existing run and re-create from scratch on duplicate run_id")
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
		Force:    *force,
		Upsert:   *upsert,
		Replace:  *replace,
	})
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	htmlOut := fs.String("html-out", "./workspace/history_dashboard.html", "Static HTML output path")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.Export(commands.ExportOptions{
		DBPath: *dbPath,
		HTMLOut: *htmlOut,
		DryRun: *dryRun,
	})
}

func runArchiveGit(args []string) error {
	fs := flag.NewFlagSet("archive-git", flag.ContinueOnError)
	repoPath := fs.String("repo-path", "third_party/AKO4ALL", "Git repo path to archive")
	branch := fs.String("branch", "", "Branch to archive, e.g. agent/run-gemm-001")
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	runID := fs.String("run-id", "", "Optional run id for archive indexing")
	note := fs.String("note", "", "Optional note for this archive")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.ArchiveGit(commands.ArchiveGitOptions{
		RepoPath: *repoPath,
		Branch:   *branch,
		DBPath:   *dbPath,
		RunID:    *runID,
		Note:     *note,
		DryRun:   *dryRun,
	})
}

func runRestoreGit(args []string) error {
	fs := flag.NewFlagSet("restore-git", flag.ContinueOnError)
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	runID := fs.String("run-id", "", "Select latest archive by run id")
	archiveID := fs.String("archive-id", "", "Select archive by exact archive id")
	outRepo := fs.String("out-repo", "./workspace/restored_repo", "Output repo path")
	checkout := fs.String("checkout", "", "Commit/branch/tag to checkout after fetch")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.RestoreGit(commands.RestoreGitOptions{
		DBPath:      *dbPath,
		RunID:       *runID,
		ArchiveID:   *archiveID,
		OutRepoPath: *outRepo,
		Checkout:    *checkout,
		DryRun:      *dryRun,
	})
}

func runGenerateContext(args []string) error {
	fs := flag.NewFlagSet("generate-context", flag.ContinueOnError)
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	kernelName := fs.String("kernel-name", "", "Target kernel name (e.g. rms_norm)")
	backend := fs.String("backend", "", "Filter by backend (triton/cuda), empty for all")
	outputPath := fs.String("output", "", "Output file path (default: stdout via --dry-run)")
	dryRun := fs.Bool("dry-run", false, "Print to stdout instead of writing file")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.GenerateContext(commands.GenerateContextOptions{
		DBPath:     *dbPath,
		KernelName: *kernelName,
		Backend:    *backend,
		OutputPath: *outputPath,
		DryRun:     *dryRun,
	})
}

func runPatch(args []string) error {
	fs := flag.NewFlagSet("patch", flag.ContinueOnError)
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	kernelAssets := fs.String("kernel-assets", "./workspace/kernel_assets", "Path to kernel_assets/ directory")
	mmqRoot := fs.String("mmq-root", "", "Path to MMQ framework root (mmq_kernels/mmq_kernels)")
	outputDir := fs.String("output-dir", "./workspace/patches", "Output directory for generated patches")
	runsDir := fs.String("runs-dir", "./workspace/runs", "Path to workspace/runs/")
	verify := fs.Bool("verify", true, "Verify patches via git apply --check")
	apply := fs.Bool("apply", false, "Apply patches to MMQ repo (requires --verify to pass)")
	dryRun := fs.Bool("dry-run", false, "Print actions only")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.Patch(commands.PatchOptions{
		DBPath:       *dbPath,
		KernelAssets: *kernelAssets,
		MMQRoot:      *mmqRoot,
		OutputDir:    *outputDir,
		RunsDir:      *runsDir,
		Verify:       *verify,
		Apply:        *apply,
		DryRun:       *dryRun,
	})
}

func runRecalibrate(args []string) error {
	fs := flag.NewFlagSet("recalibrate", flag.ContinueOnError)
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	runsDir := fs.String("runs-dir", "./workspace/runs", "Path to workspace/runs/")
	runID := fs.String("run-id", "", "Recalibrate a single run (default: all runs)")
	dryRun := fs.Bool("dry-run", false, "Print results without updating the database")
	verbose := fs.Bool("verbose", false, "Print bench command and output details")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.Recalibrate(commands.RecalibrateOptions{
		DBPath:  *dbPath,
		RunsDir: *runsDir,
		RunID:   *runID,
		DryRun:  *dryRun,
		Verbose: *verbose,
	})
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbPath := fs.String("db-path", "./workspace/history.db", "History DB path")
	listen := fs.String("listen", ":8080", "Listen address, e.g. :8080 or 127.0.0.1:8080")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return commands.Serve(commands.ServeOptions{
		DBPath:     *dbPath,
		ListenAddr: *listen,
	})
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "Listen address (host:port)")
	dbPath := fs.String("db-path", "./workspace/history.db", "Path to history SQLite DB")
	rps := fs.Float64("rate-limit-rps", 10, "Sustained requests per second per IP")
	burst := fs.Int("rate-limit-burst", 30, "Max burst size above sustained rate")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cfg := server.Config{
		ListenAddr: *listen,
		DBPath:     *dbPath,
		RateLimit: server.RateLimitConfig{
			RequestsPerSecond: *rps,
			Burst:             *burst,
			CleanupInterval:   server.DefaultRateLimitConfig().CleanupInterval,
		},
	}

	srv := server.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		fmt.Println("\n[kernelhub server] shutting down...")
		return srv.Shutdown(context.Background())
	}
}

package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ExportOptions struct {
	DBPath  string
	OutPath string
	HTMLOut string
	Format  string
	DryRun  bool
}

type Snapshot struct {
	Meta  map[string]any `json:"meta"`
	Stats map[string]any `json:"stats"`
	Runs  []RunRecord    `json:"runs"`
}

func Export(opts ExportOptions) error {
	if opts.DBPath == "" || opts.OutPath == "" {
		return fmt.Errorf("--db-path and --out are required")
	}
	history, err := loadHistory(opts.DBPath)
	if err != nil {
		return err
	}

	stats := buildStats(history.Runs)
	snapshot := Snapshot{
		Meta: map[string]any{
			"format_version": "v1",
			"generated_at":   time.Now().UTC().Format(time.RFC3339),
			"source":         opts.DBPath,
		},
		Stats: stats,
		Runs:  history.Runs,
	}

	if opts.DryRun {
		fmt.Printf("[kernelhub export] dry-run runs=%d format=%s out=%s\n", len(history.Runs), opts.Format, opts.OutPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0o755); err != nil {
		return err
	}
	switch strings.ToLower(opts.Format) {
	case "", "json":
		content, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(opts.OutPath, content, 0o644); err != nil {
			return err
		}
	case "toml":
		content, err := renderSimpleTOML(snapshot)
		if err != nil {
			return err
		}
		if err := os.WriteFile(opts.OutPath, []byte(content), 0o644); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported format: %s", opts.Format)
	}

	fmt.Printf("[kernelhub export] snapshot written: %s\n", opts.OutPath)
	if opts.HTMLOut != "" {
		if err := os.MkdirAll(filepath.Dir(opts.HTMLOut), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(opts.HTMLOut, []byte(renderStaticHTML(snapshot)), 0o644); err != nil {
			return err
		}
		fmt.Printf("[kernelhub export] static html written: %s\n", opts.HTMLOut)
	}
	return nil
}

func buildStats(runs []RunRecord) map[string]any {
	best := 0.0
	iterations := 0
	kernels := map[string]struct{}{}
	agents := map[string]struct{}{}
	for _, run := range runs {
		for _, it := range run.Iterations {
			iterations++
			if it.SpeedupVsBaseline > best {
				best = it.SpeedupVsBaseline
			}
			if it.Kernel != "" {
				kernels[it.Kernel] = struct{}{}
			}
			if it.Agent != "" {
				agents[it.Agent] = struct{}{}
			}
		}
	}
	return map[string]any{
		"run_count":        len(runs),
		"iteration_count":  iterations,
		"unique_kernels":   len(kernels),
		"unique_agents":    len(agents),
		"best_speedup":     best,
		"history_generated": time.Now().UTC().Format(time.RFC3339),
	}
}

func renderSimpleTOML(snapshot Snapshot) (string, error) {
	runsJSON, err := json.Marshal(snapshot.Runs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`format_version = "v1"
generated_at = "%s"
source = "%s"
run_count = %v
iteration_count = %v
unique_kernels = %v
unique_agents = %v
best_speedup = %v
runs_json = '''%s'''
`,
		snapshot.Meta["generated_at"],
		snapshot.Meta["source"],
		snapshot.Stats["run_count"],
		snapshot.Stats["iteration_count"],
		snapshot.Stats["unique_kernels"],
		snapshot.Stats["unique_agents"],
		snapshot.Stats["best_speedup"],
		string(runsJSON),
	), nil
}

func renderStaticHTML(snapshot Snapshot) string {
	payload, _ := json.Marshal(snapshot)
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>KernelHub Static Dashboard</title>
  <style>
    body { font-family: Arial, sans-serif; margin: 24px; color: #111; }
    table { border-collapse: collapse; width: 100%%; margin-top: 12px; }
    th, td { border: 1px solid #ddd; padding: 8px; text-align: left; font-size: 13px; }
    th { background: #f4f4f4; }
    code { background: #f2f2f2; padding: 1px 4px; border-radius: 4px; }
  </style>
</head>
<body>
  <h1>KernelHub Static Dashboard</h1>
  <div id="stats"></div>
  <table>
    <thead><tr><th>run_id</th><th>branch</th><th>commits</th><th>latest_commit</th></tr></thead>
    <tbody id="rows"></tbody>
  </table>
  <script>
    const SNAPSHOT = %s;
    const stats = SNAPSHOT.stats || {};
    document.getElementById("stats").innerText =
      "runs=" + (stats.run_count || 0) +
      " iterations=" + (stats.iteration_count || 0) +
      " best_speedup=" + (stats.best_speedup || 0);

    const rows = document.getElementById("rows");
    for (const run of (SNAPSHOT.runs || [])) {
      const tr = document.createElement("tr");
      const its = run.iterations || [];
      const latest = its.length ? its[its.length - 1].commit_hash : "";
      tr.innerHTML = "<td><code>" + run.run_id + "</code></td>" +
        "<td>" + (run.branch || "") + "</td>" +
        "<td>" + (run.commit_count || 0) + "</td>" +
        "<td><code>" + latest + "</code></td>";
      rows.appendChild(tr);
    }
  </script>
</body>
</html>`, string(payload))
}

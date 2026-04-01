package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

const maxEmbeddedPatchChars = 200000

func Export(opts ExportOptions) error {
	if opts.DBPath == "" || opts.OutPath == "" {
		return fmt.Errorf("--db-path and --out are required")
	}
	history, err := loadHistory(opts.DBPath)
	if err != nil {
		return err
	}

	runsWithPatches := enrichRunsWithPatches(history.Runs)
	stats := buildStats(runsWithPatches)
	snapshot := Snapshot{
		Meta: map[string]any{
			"format_version": "v1",
			"generated_at":   time.Now().UTC().Format(time.RFC3339),
			"source":         opts.DBPath,
		},
		Stats: stats,
		Runs:  runsWithPatches,
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

type patchResult struct {
	Patch string
	Err   string
}

func enrichRunsWithPatches(runs []RunRecord) []RunRecord {
	if len(runs) == 0 {
		return nil
	}
	enriched := cloneRuns(runs)
	cache := map[string]patchResult{}
	for runIdx := range enriched {
		run := &enriched[runIdx]
		repoPath := strings.TrimSpace(run.RepoPath)
		for itIdx := range run.Iterations {
			it := &run.Iterations[itIdx]
			commit := strings.TrimSpace(it.CommitHash)
			parent := strings.TrimSpace(it.ParentCommitHash)
			key := repoPath + "\x00" + commit + "\x00" + parent
			if cached, ok := cache[key]; ok {
				it.Patch = cached.Patch
				it.PatchError = cached.Err
				continue
			}
			patch, patchErr := buildCommitPatch(repoPath, commit, parent)
			cache[key] = patchResult{Patch: patch, Err: patchErr}
			it.Patch = patch
			it.PatchError = patchErr
		}
	}
	return enriched
}

func cloneRuns(src []RunRecord) []RunRecord {
	dst := make([]RunRecord, len(src))
	for i, run := range src {
		dst[i] = run
		dst[i].Iterations = append([]IterationRecord(nil), run.Iterations...)
	}
	return dst
}

func buildCommitPatch(repoPath, commitHash, parentCommitHash string) (string, string) {
	if commitHash == "" {
		return "", "commit hash missing"
	}
	if repoPath == "" {
		return "", "repo path missing in run record"
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return "", fmt.Sprintf("repo unavailable: %v", err)
	}
	if parentCommitHash != "" {
		if patch, err := runGitText(repoPath, "diff", "--no-color", parentCommitHash, commitHash); err == nil {
			return clampPatch(patch), ""
		}
	}
	if patch, err := runGitText(repoPath, "show", "--no-color", "--format=", "--patch", commitHash); err == nil {
		return clampPatch(patch), ""
	}
	return "", fmt.Sprintf("cannot generate patch for commit %s", shortHash(commitHash, 12))
}

func runGitText(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}

func clampPatch(patch string) string {
	trimmed := strings.TrimSpace(patch)
	if len(trimmed) <= maxEmbeddedPatchChars {
		return trimmed
	}
	return trimmed[:maxEmbeddedPatchChars] + "\n\n... [patch truncated in dashboard export]"
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
    .stats { margin-bottom: 10px; font-size: 14px; }
    table { border-collapse: collapse; width: 100%%; margin-top: 10px; }
    th, td { border: 1px solid #ddd; padding: 8px; text-align: left; font-size: 13px; }
    th { background: #f4f4f4; }
    code { background: #f2f2f2; padding: 1px 4px; border-radius: 4px; }
    button.toggle {
      border: 1px solid #ccc;
      background: #fff;
      border-radius: 4px;
      padding: 3px 8px;
      cursor: pointer;
      font-size: 12px;
    }
    .details-row { background: #fafafa; }
    .details-cell { padding: 0; }
    .inner-wrap { padding: 10px; }
    .inner-table { margin-top: 0; }
    .muted { color: #666; }
    .hidden { display: none; }
    .patch-row td { background: #fff; }
    .patch-wrap { max-width: 100%%; overflow: auto; }
    .patch-wrap pre {
      margin: 8px 0 0;
      background: #111;
      color: #f5f5f5;
      padding: 12px;
      border-radius: 6px;
      font-family: Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
      font-size: 12px;
      line-height: 1.45;
      white-space: pre;
    }
  </style>
</head>
<body>
  <h1>KernelHub Static Dashboard</h1>
  <div id="stats" class="stats"></div>
  <table>
    <thead><tr><th>run_id</th><th>branch</th><th>commits</th><th>latest_commit</th><th>details</th></tr></thead>
    <tbody id="rows"></tbody>
  </table>
  <script>
    const SNAPSHOT = %s;
    const stats = SNAPSHOT.stats || {};
    const runs = SNAPSHOT.runs || [];

    function shortHash(hash) {
      return hash ? hash.slice(0, 12) : "";
    }

    function formatMetric(value, suffix) {
      if (typeof value !== "number" || Number.isNaN(value)) {
        return "-";
      }
      const text = Number.isInteger(value) ? value.toString() : value.toFixed(2).replace(/\.?0+$/, "");
      return suffix ? text + suffix : text;
    }

    function escapeHtml(value) {
      if (value === null || value === undefined) {
        return "";
      }
      return String(value)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#39;");
    }

    document.getElementById("stats").innerText =
      "runs=" + (stats.run_count || 0) +
      " iterations=" + (stats.iteration_count || 0) +
      " best_speedup=" + formatMetric(stats.best_speedup || 0, "x");

    const rows = document.getElementById("rows");
    runs.forEach((run, idx) => {
      const tr = document.createElement("tr");
      const its = run.iterations || [];
      const latest = its.length ? its[its.length - 1].commit_hash : "";
      const detailID = "run-details-" + idx;
      tr.innerHTML = "<td><code>" + run.run_id + "</code></td>" +
        "<td>" + (run.branch || "") + "</td>" +
        "<td>" + (run.commit_count || 0) + "</td>" +
        "<td><code>" + shortHash(latest) + "</code></td>" +
        "<td><button class='toggle' data-target='" + detailID + "'>Show</button></td>";
      rows.appendChild(tr);

      const detailRow = document.createElement("tr");
      detailRow.id = detailID;
      detailRow.className = "details-row hidden";

      const commitRows = its.length ? its.map((it, commitIdx) => {
        const patchID = detailID + "-patch-" + commitIdx;
        const patchText = it.patch || "";
        const patchError = it.patch_error || "";
        const hasPatch = patchText.length > 0;
        const hasPatchInfo = hasPatch || patchError.length > 0;
        const patchAction = hasPatchInfo
          ? "<button class='toggle patch-toggle' data-target='" + patchID + "'>View</button>"
          : "<span class='muted'>-</span>";
        const commitRow = "<tr>" +
          "<td>" + (it.iteration ?? "-") + "</td>" +
          "<td><code>" + shortHash(it.commit_hash || "") + "</code></td>" +
          "<td>" + (it.commit_time || "-") + "</td>" +
          "<td>" + escapeHtml(it.subject || "-") + "</td>" +
          "<td>" + (it.correctness || "-") + "</td>" +
          "<td>" + formatMetric(it.speedup_vs_baseline, "x") + "</td>" +
          "<td>" + formatMetric(it.latency_us, " us") + "</td>" +
          "<td>" + patchAction + "</td>" +
          "</tr>";
        if (!hasPatchInfo) {
          return commitRow;
        }
        const patchBody = hasPatch
          ? "<pre>" + escapeHtml(patchText) + "</pre>"
          : "<div class='muted'>Patch unavailable: " + escapeHtml(patchError) + "</div>";
        const patchRow =
          "<tr id='" + patchID + "' class='patch-row hidden'>" +
            "<td colspan='8'>" +
              "<div class='patch-wrap'>" + patchBody + "</div>" +
            "</td>" +
          "</tr>";
        return commitRow + patchRow;
      }).join("") : "<tr><td colspan='8' class='muted'>No commit details found.</td></tr>";

      detailRow.innerHTML =
        "<td class='details-cell' colspan='5'>" +
          "<div class='inner-wrap'>" +
            "<table class='inner-table'>" +
              "<thead><tr><th>iter</th><th>commit</th><th>time</th><th>subject</th><th>correctness</th><th>speedup</th><th>latency</th><th>patch</th></tr></thead>" +
              "<tbody>" + commitRows + "</tbody>" +
            "</table>" +
          "</div>" +
        "</td>";
      rows.appendChild(detailRow);
    });

    rows.addEventListener("click", (event) => {
      const target = event.target;
      if (!(target instanceof HTMLButtonElement)) {
        return;
      }
      if (!target.classList.contains("toggle")) {
        return;
      }
      const detailID = target.getAttribute("data-target");
      if (!detailID) {
        return;
      }
      const detailRow = document.getElementById(detailID);
      if (!detailRow) {
        return;
      }
      const isHidden = detailRow.classList.toggle("hidden");
      const closedText = target.classList.contains("patch-toggle") ? "View" : "Show";
      target.textContent = isHidden ? closedText : "Hide";
    });
  </script>
</body>
</html>`, string(payload))
}

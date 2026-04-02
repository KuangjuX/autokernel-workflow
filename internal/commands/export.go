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
	snapshot, err := buildSnapshot(opts.DBPath, true)
	if err != nil {
		return err
	}

	if opts.DryRun {
		fmt.Printf("[kernelhub export] dry-run runs=%d format=%s out=%s\n", len(snapshot.Runs), opts.Format, opts.OutPath)
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

func buildSnapshot(dbPath string, includePatches bool) (Snapshot, error) {
	history, err := loadHistory(dbPath)
	if err != nil {
		return Snapshot{}, err
	}

	runs := history.Runs
	if includePatches {
		runs = enrichRunsWithPatches(runs)
	}

	return Snapshot{
		Meta: map[string]any{
			"format_version": "v1",
			"generated_at":   time.Now().UTC().Format(time.RFC3339),
			"source":         dbPath,
		},
		Stats: buildStats(runs),
		Runs:  runs,
	}, nil
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
		"run_count":         len(runs),
		"iteration_count":   iterations,
		"unique_kernels":    len(kernels),
		"unique_agents":     len(agents),
		"best_speedup":      best,
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

func renderStaticHTMLLegacy(snapshot Snapshot) string {
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

func renderStaticHTML(snapshot Snapshot) string {
	payload, _ := json.Marshal(snapshot)
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>KernelHub Static Dashboard</title>
  <style>
    :root {
      --bg: #0d1117;
      --panel: #161b22;
      --panel-soft: #1f2630;
      --line: #30363d;
      --text: #e6edf3;
      --muted: #9da7b3;
      --good: #3fb950;
      --warn: #d29922;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    .container {
      max-width: 1400px;
      margin: 0 auto;
      padding: 24px;
    }
    .topbar {
      display: flex;
      justify-content: space-between;
      align-items: flex-end;
      gap: 16px;
      margin-bottom: 16px;
    }
    .title {
      font-size: 28px;
      margin: 0;
      letter-spacing: 0.2px;
    }
    .subtitle {
      color: var(--muted);
      margin-top: 6px;
      font-size: 13px;
    }
    .status {
      color: var(--muted);
      font-size: 13px;
      min-height: 20px;
      margin-bottom: 2px;
    }
    .stats-grid {
      margin: 14px 0 16px;
      display: grid;
      grid-template-columns: repeat(4, minmax(140px, 1fr));
      gap: 10px;
    }
    .stat-card {
      border: 1px solid var(--line);
      background: var(--panel);
      border-radius: 12px;
      padding: 12px;
    }
    .stat-label {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.5px;
    }
    .stat-value {
      margin-top: 6px;
      font-size: 24px;
      font-weight: 700;
      line-height: 1.1;
    }
    .panel {
      border: 1px solid var(--line);
      background: var(--panel);
      border-radius: 12px;
      overflow: hidden;
    }
    table {
      width: 100%%;
      border-collapse: collapse;
      font-size: 13px;
    }
    th, td {
      border-bottom: 1px solid var(--line);
      padding: 10px;
      text-align: left;
      vertical-align: top;
    }
    th {
      font-size: 12px;
      color: var(--muted);
      letter-spacing: 0.3px;
      text-transform: uppercase;
      background: #121820;
    }
    tr:last-child td { border-bottom: 0; }
    code {
      background: #111923;
      border: 1px solid #28303b;
      border-radius: 6px;
      padding: 2px 6px;
      color: #d2e3ff;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
      font-size: 12px;
    }
    .btn {
      border: 1px solid var(--line);
      background: linear-gradient(180deg, #2d333b, #252b33);
      color: var(--text);
      border-radius: 8px;
      padding: 5px 10px;
      cursor: pointer;
      font-size: 12px;
      font-weight: 600;
    }
    .btn:hover { border-color: #3f4750; }
    .details-cell {
      padding: 0;
      background: #111821;
    }
    .inner-wrap { padding: 10px 12px 12px; }
    .muted { color: var(--muted); }
    .hidden { display: none; }
    .badge {
      display: inline-block;
      border-radius: 999px;
      border: 1px solid #32593f;
      background: rgba(63, 185, 80, 0.15);
      color: #9af0a6;
      font-size: 11px;
      padding: 2px 7px;
      font-weight: 600;
    }
    .badge.warn {
      border-color: #5f4b1f;
      background: rgba(210, 153, 34, 0.16);
      color: #f5d58a;
    }
    .modal {
      position: fixed;
      inset: 0;
      z-index: 60;
      background: rgba(1, 4, 9, 0.75);
      backdrop-filter: blur(2px);
      display: flex;
      justify-content: center;
      align-items: stretch;
      padding: 24px;
    }
    .modal.hidden { display: none; }
    .modal-card {
      width: min(1250px, 100%%);
      background: #0f1722;
      border: 1px solid #2b3440;
      border-radius: 14px;
      display: flex;
      flex-direction: column;
      overflow: hidden;
      box-shadow: 0 16px 50px rgba(0, 0, 0, 0.35);
    }
    .modal-header {
      border-bottom: 1px solid var(--line);
      padding: 14px 16px 10px;
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
    }
    .modal-title {
      margin: 0;
      font-size: 16px;
      line-height: 1.35;
      max-width: 90%%;
    }
    .modal-meta {
      padding: 8px 16px 0;
      color: var(--muted);
      font-size: 12px;
    }
    .modal-body {
      padding: 14px 16px 16px;
      overflow: auto;
      display: grid;
      gap: 12px;
      grid-template-columns: repeat(3, minmax(220px, 1fr));
      grid-auto-rows: min-content;
    }
    .analysis-card {
      border: 1px solid var(--line);
      background: var(--panel-soft);
      border-radius: 10px;
      overflow: hidden;
    }
    .analysis-card h3 {
      margin: 0;
      padding: 10px 12px;
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.5px;
      color: var(--muted);
      border-bottom: 1px solid var(--line);
      background: #1a212b;
    }
    .analysis-card pre {
      margin: 0;
      padding: 12px;
      white-space: pre-wrap;
      word-break: break-word;
      font-size: 12px;
      line-height: 1.45;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
      min-height: 84px;
    }
    .patch-card {
      grid-column: 1 / -1;
    }
    .patch-card pre {
      max-height: 56vh;
      overflow: auto;
      background: #0d1117;
      color: #e6edf3;
    }
    @media (max-width: 1100px) {
      .stats-grid { grid-template-columns: repeat(2, minmax(140px, 1fr)); }
      .modal-body { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="topbar">
      <div>
        <h1 class="title">KernelHub</h1>
        <div class="subtitle">Static export with commit-level optimization analysis</div>
      </div>
    </div>
    <div id="status" class="status">Static snapshot loaded</div>
    <div id="stats" class="stats-grid"></div>
    <div class="panel">
      <table>
        <thead><tr><th>run_id</th><th>branch</th><th>commits</th><th>latest_commit</th><th>details</th></tr></thead>
        <tbody id="rows"></tbody>
      </table>
    </div>
  </div>
  <div id="commitModal" class="modal hidden" role="dialog" aria-modal="true">
    <div class="modal-card">
      <div class="modal-header">
        <h2 id="modalTitle" class="modal-title">Commit Insight</h2>
        <button id="closeModal" class="btn">Close</button>
      </div>
      <div id="modalMeta" class="modal-meta"></div>
      <div class="modal-body">
        <section class="analysis-card">
          <h3>Hypothesis</h3>
          <pre id="modalHypothesis">-</pre>
        </section>
        <section class="analysis-card">
          <h3>Changes</h3>
          <pre id="modalChanges">-</pre>
        </section>
        <section class="analysis-card">
          <h3>Analysis</h3>
          <pre id="modalAnalysis">-</pre>
        </section>
        <section class="analysis-card patch-card">
          <h3>Patch</h3>
          <pre id="modalPatch">-</pre>
        </section>
      </div>
    </div>
  </div>
  <script>
    const SNAPSHOT = %s;
    const state = { currentIteration: null };

    function shortHash(hash) {
      return hash ? hash.slice(0, 12) : "";
    }

    function formatMetric(value, suffix) {
      if (typeof value !== "number" || Number.isNaN(value)) return "-";
      const text = Number.isInteger(value) ? value.toString() : value.toFixed(2).replace(/\.?0+$/, "");
      return suffix ? text + suffix : text;
    }

    function safeText(value, fallback) {
      if (value === null || value === undefined) return fallback || "-";
      const text = String(value).trim();
      return text.length ? text : (fallback || "-");
    }

    function escapeHtml(value) {
      return String(value)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#39;");
    }

    function summarizePatch(patch) {
      if (!patch || !patch.trim()) return "";
      const lines = patch.split("\n");
      let files = 0;
      let adds = 0;
      let dels = 0;
      for (const line of lines) {
        if (line.startsWith("diff --git ")) {
          files += 1;
          continue;
        }
        if (line.startsWith("+") && !line.startsWith("+++")) adds += 1;
        if (line.startsWith("-") && !line.startsWith("---")) dels += 1;
      }
      if (files === 0) files = 1;
      return files + " file(s) changed, +" + adds + " / -" + dels;
    }

    function buildAnalysisFallback(it) {
      const parts = [];
      const correctness = safeText(it.correctness || "-", "-");
      parts.push("correctness: " + correctness);
      if (typeof it.speedup_vs_baseline === "number" && !Number.isNaN(it.speedup_vs_baseline)) {
        parts.push("speedup_vs_baseline: " + formatMetric(it.speedup_vs_baseline, "x"));
      }
      if (typeof it.latency_us === "number" && !Number.isNaN(it.latency_us)) {
        parts.push("latency_us: " + formatMetric(it.latency_us, " us"));
      }
      return parts.join("\n");
    }

    function renderStats(stats) {
      const items = [
        { label: "Runs", value: String(stats.run_count || 0) },
        { label: "Iterations", value: String(stats.iteration_count || 0) },
        { label: "Best Speedup", value: formatMetric(stats.best_speedup || 0, "x") },
        { label: "Unique Kernels", value: String(stats.unique_kernels || 0) },
      ];
      const html = items.map((it) =>
        "<div class='stat-card'><div class='stat-label'>" + it.label +
        "</div><div class='stat-value'>" + it.value + "</div></div>"
      ).join("");
      document.getElementById("stats").innerHTML = html;
    }

    function correctnessBadge(correctness) {
      const text = safeText(correctness, "-");
      const cls = text.toUpperCase() === "PASS" ? "badge" : "badge warn";
      return "<span class='" + cls + "'>" + escapeHtml(text) + "</span>";
    }

    function renderRunDetails(run) {
      const wrap = document.createElement("div");
      wrap.className = "inner-wrap";
      const table = document.createElement("table");
      table.innerHTML = "<thead><tr><th>iter</th><th>commit</th><th>time</th><th>subject</th><th>correctness</th><th>speedup</th><th>latency</th><th>inspect</th></tr></thead>";
      const body = document.createElement("tbody");
      const iterations = run.iterations || [];
      if (!iterations.length) {
        const tr = document.createElement("tr");
        tr.innerHTML = "<td class='muted' colspan='8'>No commit details found.</td>";
        body.appendChild(tr);
      } else {
        iterations.forEach((it) => {
          const tr = document.createElement("tr");
          tr.innerHTML =
            "<td>" + escapeHtml(safeText(it.iteration, "-")) + "</td>" +
            "<td><code>" + shortHash(it.commit_hash || "") + "</code></td>" +
            "<td>" + escapeHtml(safeText(it.commit_time, "-")) + "</td>" +
            "<td>" + escapeHtml(safeText(it.subject, "-")) + "</td>" +
            "<td>" + correctnessBadge(it.correctness) + "</td>" +
            "<td>" + formatMetric(it.speedup_vs_baseline, "x") + "</td>" +
            "<td>" + formatMetric(it.latency_us, " us") + "</td>" +
            "<td><button class='btn inspect-btn'>Inspect</button></td>";
          tr.querySelector(".inspect-btn").addEventListener("click", () => openCommitModal(run, it));
          body.appendChild(tr);
        });
      }
      table.appendChild(body);
      wrap.appendChild(table);
      return wrap;
    }

    function openCommitModal(run, it) {
      state.currentIteration = it;
      const modal = document.getElementById("commitModal");
      modal.classList.remove("hidden");
      document.getElementById("modalTitle").textContent =
        shortHash(it.commit_hash || "") + "  " + safeText(it.subject, "-");
      document.getElementById("modalMeta").textContent =
        "run: " + safeText(run.run_id, "-") +
        " | branch: " + safeText(run.branch, "-") +
        " | time: " + safeText(it.commit_time, "-");
      document.getElementById("modalHypothesis").textContent = safeText(it.hypothesis, "-");
      document.getElementById("modalChanges").textContent =
        safeText(it.changes, summarizePatch(it.patch || "") || safeText(it.subject, "-"));
      document.getElementById("modalAnalysis").textContent =
        safeText(it.analysis, buildAnalysisFallback(it));
      if (it.patch && it.patch.length) {
        document.getElementById("modalPatch").textContent = it.patch;
      } else {
        document.getElementById("modalPatch").textContent =
          "Patch unavailable: " + safeText(it.patch_error, "missing embedded patch");
      }
    }

    function closeModal() {
      state.currentIteration = null;
      document.getElementById("commitModal").classList.add("hidden");
    }

    function renderSnapshot() {
      const stats = SNAPSHOT.stats || {};
      const runs = SNAPSHOT.runs || [];
      renderStats(stats);

      const rows = document.getElementById("rows");
      rows.innerHTML = "";
      runs.forEach((run, runIdx) => {
        const iterations = run.iterations || [];
        const latest = iterations.length ? iterations[iterations.length - 1].commit_hash : "";
        const detailID = "run-details-" + runIdx;

        const tr = document.createElement("tr");
        tr.innerHTML =
          "<td><code>" + escapeHtml(safeText(run.run_id, "-")) + "</code></td>" +
          "<td>" + escapeHtml(safeText(run.branch, "-")) + "</td>" +
          "<td>" + escapeHtml(safeText(run.commit_count, "0")) + "</td>" +
          "<td><code>" + shortHash(latest) + "</code></td>" +
          "<td><button class='btn run-toggle' data-target='" + detailID + "'>Show</button></td>";
        rows.appendChild(tr);

        const detailRow = document.createElement("tr");
        detailRow.id = detailID;
        detailRow.className = "hidden";
        const detailCell = document.createElement("td");
        detailCell.colSpan = 5;
        detailCell.className = "details-cell";
        detailCell.appendChild(renderRunDetails(run));
        detailRow.appendChild(detailCell);
        rows.appendChild(detailRow);
      });
    }

    document.getElementById("rows").addEventListener("click", (event) => {
      const target = event.target;
      if (!(target instanceof HTMLButtonElement)) return;
      if (!target.classList.contains("run-toggle")) return;
      const targetID = target.getAttribute("data-target");
      if (!targetID) return;
      const row = document.getElementById(targetID);
      if (!row) return;
      const hidden = row.classList.toggle("hidden");
      target.textContent = hidden ? "Show" : "Hide";
    });

    document.getElementById("closeModal").addEventListener("click", closeModal);
    document.getElementById("commitModal").addEventListener("click", (event) => {
      if (event.target && event.target.id === "commitModal") closeModal();
    });
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") closeModal();
    });

    renderSnapshot();
  </script>
</body>
</html>`, string(payload))
}

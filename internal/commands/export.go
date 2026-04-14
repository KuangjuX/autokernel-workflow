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
	HTMLOut string
	DryRun  bool
}

type Snapshot struct {
	Meta  map[string]any `json:"meta"`
	Stats map[string]any `json:"stats"`
	Runs  []RunRecord    `json:"runs"`
}

const maxEmbeddedPatchChars = 200000

func Export(opts ExportOptions) error {
	if opts.DBPath == "" {
		return fmt.Errorf("--db-path is required")
	}
	if opts.HTMLOut == "" {
		return fmt.Errorf("--html-out is required")
	}
	snapshot, err := buildSnapshot(opts.DBPath, true)
	if err != nil {
		return err
	}

	if opts.DryRun {
		fmt.Printf("[kernelhub export] dry-run runs=%d html-out=%s\n", len(snapshot.Runs), opts.HTMLOut)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(opts.HTMLOut), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(opts.HTMLOut, []byte(renderStaticHTML(snapshot)), 0o644); err != nil {
		return err
	}
	fmt.Printf("[kernelhub export] static html written: %s\n", opts.HTMLOut)
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

// BuildSnapshot is an exported wrapper for server package reuse.
func BuildSnapshot(dbPath string, includePatches bool) (Snapshot, error) {
	return buildSnapshot(dbPath, includePatches)
}

func buildStats(runs []RunRecord) map[string]any {
	best := 0.0
	iterations := 0
	kernels := map[string]struct{}{}
	agents := map[string]struct{}{}
	gpus := map[string]struct{}{}
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
			if it.GPU != "" {
				gpus[it.GPU] = struct{}{}
			}
		}
	}
	return map[string]any{
		"run_count":         len(runs),
		"iteration_count":   iterations,
		"unique_kernels":    len(kernels),
		"unique_agents":     len(agents),
		"unique_gpus":       len(gpus),
		"best_speedup":      best,
		"history_generated": time.Now().UTC().Format(time.RFC3339),
	}
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

// BuildCommitPatch is an exported wrapper for server package reuse.
func BuildCommitPatch(repoPath, commitHash, parentCommitHash string) (string, string) {
	return buildCommitPatch(repoPath, commitHash, parentCommitHash)
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
    .chart-container {
      display: flex;
      gap: 12px;
      margin-bottom: 12px;
    }
    .chart-panel {
      flex: 1;
      border: 1px solid var(--line);
      background: var(--panel-soft);
      border-radius: 10px;
      padding: 12px 14px;
      min-width: 0;
    }
    .chart-title {
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.5px;
      color: var(--muted);
      margin: 0 0 8px;
    }
    .chart-svg { display: block; width: 100%%; }
    .chart-svg .grid-line { stroke: var(--line); stroke-width: 0.5; }
    .chart-svg .baseline { stroke: var(--warn); stroke-width: 1; stroke-dasharray: 5 3; }
    .chart-svg .data-line { fill: none; stroke-width: 2; stroke-linejoin: round; stroke-linecap: round; }
    .chart-svg .data-dot { cursor: pointer; }
    .chart-svg .axis-label { fill: var(--muted); font-size: 10px; font-family: ui-monospace, monospace; }
    .chart-svg .tooltip-bg { fill: var(--panel); stroke: var(--line); rx: 4; }
    .chart-svg .tooltip-text { fill: var(--text); font-size: 11px; font-family: ui-monospace, monospace; }
    @media (max-width: 900px) { .chart-container { flex-direction: column; } }
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
    .badge-triton {
      border-color: #2d4a7a;
      background: rgba(56, 139, 253, 0.15);
      color: #79c0ff;
    }
    .badge-cuda {
      border-color: #5a3e2b;
      background: rgba(210, 120, 50, 0.16);
      color: #f0b070;
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
        <thead><tr><th>run_id</th><th>branch</th><th>backend</th><th>commits</th><th>latest_commit</th><th>details</th></tr></thead>
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
      if (safeText(it.gpu || "", "") !== "") {
        parts.push("gpu: " + safeText(it.gpu, "-"));
      }
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
        { label: "Unique GPUs", value: String(stats.unique_gpus || 0) },
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

    function backendBadge(backend) {
      const text = safeText(backend, "").toLowerCase();
      if (!text) return "<span class='muted'>-</span>";
      const cls = text === "triton" ? "badge badge-triton" : "badge badge-cuda";
      return "<span class='" + cls + "'>" + escapeHtml(text) + "</span>";
    }

    function inferRunBackend(run) {
      const its = run.iterations || [];
      for (let i = its.length - 1; i >= 0; i--) {
        const b = safeText(its[i].backend, "").toLowerCase();
        if (b) return b;
      }
      return "";
    }

    function renderIterCharts(iterations) {
      const pts = [];
      iterations.forEach((it, i) => {
        const lat = parseFloat(it.latency_us);
        const spd = parseFloat(it.speedup_vs_baseline);
        if (!isNaN(lat) && !isNaN(spd)) {
          pts.push({ idx: i, iter: safeText(it.iteration, String(i)), lat: lat, spd: spd, hash: shortHash(it.commit_hash || "") });
        }
      });
      if (pts.length < 2) return null;

      const baseLat = pts[0].lat;
      const baseSpd = pts[0].spd;
      pts.forEach(p => { p.relSpd = baseSpd > 0 ? p.spd / baseSpd : 1; });

      function buildSVG(data, yKey, yLabel, color, baselineVal, baselineLabel, fmtY) {
        const W = 460, H = 200, padL = 52, padR = 16, padT = 18, padB = 28;
        const plotW = W - padL - padR, plotH = H - padT - padB;

        const xs = data.map(d => d.idx);
        const ys = data.map(d => d[yKey]);
        const allY = baselineVal !== null ? ys.concat(baselineVal) : ys;
        let yMin = Math.min.apply(null, allY);
        let yMax = Math.max.apply(null, allY);
        const yPad = (yMax - yMin) * 0.12 || 1;
        yMin -= yPad; yMax += yPad;

        const xMin = Math.min.apply(null, xs);
        const xMax = Math.max.apply(null, xs);
        const xRange = xMax - xMin || 1;

        function sx(v) { return padL + (v - xMin) / xRange * plotW; }
        function sy(v) { return padT + (1 - (v - yMin) / (yMax - yMin)) * plotH; }

        let svg = '<svg class="chart-svg" viewBox="0 0 ' + W + ' ' + H + '" xmlns="http://www.w3.org/2000/svg">';

        const nTicks = 4;
        for (let i = 0; i <= nTicks; i++) {
          const yv = yMin + (yMax - yMin) * i / nTicks;
          const y = sy(yv);
          svg += '<line class="grid-line" x1="' + padL + '" x2="' + (W - padR) + '" y1="' + y + '" y2="' + y + '"/>';
          svg += '<text class="axis-label" x="' + (padL - 4) + '" y="' + (y + 3) + '" text-anchor="end">' + fmtY(yv) + '</text>';
        }

        if (baselineVal !== null) {
          const by = sy(baselineVal);
          svg += '<line class="baseline" x1="' + padL + '" x2="' + (W - padR) + '" y1="' + by + '" y2="' + by + '"/>';
          svg += '<text class="axis-label" x="' + (W - padR + 2) + '" y="' + (by - 4) + '" fill="var(--warn)" font-size="9">' + escapeHtml(baselineLabel) + '</text>';
        }

        let pathD = '';
        data.forEach((d, i) => {
          const x = sx(d.idx), y = sy(d[yKey]);
          pathD += (i === 0 ? 'M' : 'L') + x.toFixed(1) + ',' + y.toFixed(1);
        });
        svg += '<path class="data-line" d="' + pathD + '" stroke="' + color + '"/>';

        data.forEach(d => {
          const x = sx(d.idx), y = sy(d[yKey]);
          svg += '<circle class="data-dot" cx="' + x.toFixed(1) + '" cy="' + y.toFixed(1) + '" r="4" fill="' + color + '" stroke="var(--panel)" stroke-width="1.5">';
          svg += '<title>iter ' + escapeHtml(d.iter) + ' (' + escapeHtml(d.hash) + ')\n' + yLabel + ': ' + fmtY(d[yKey]) + '</title></circle>';
        });

        data.forEach(d => {
          const x = sx(d.idx);
          svg += '<text class="axis-label" x="' + x.toFixed(1) + '" y="' + (H - 4) + '" text-anchor="middle">' + escapeHtml(d.iter) + '</text>';
        });

        svg += '</svg>';
        return svg;
      }

      const container = document.createElement("div");
      container.className = "chart-container";

      const panel1 = document.createElement("div");
      panel1.className = "chart-panel";
      panel1.innerHTML = '<p class="chart-title">Latency (us) per Iteration</p>' +
        buildSVG(pts, "lat", "latency", "#58a6ff", baseLat, "iter 0 baseline", function(v) { return v.toFixed(0); });
      container.appendChild(panel1);

      const panel2 = document.createElement("div");
      panel2.className = "chart-panel";
      panel2.innerHTML = '<p class="chart-title">Relative Speedup (vs iter 0)</p>' +
        buildSVG(pts, "relSpd", "rel. speedup", "#9af0a6", 1.0, "1.0x (no change)", function(v) { return v.toFixed(2) + "x"; });
      container.appendChild(panel2);

      return container;
    }

    function renderRunDetails(run) {
      const wrap = document.createElement("div");
      wrap.className = "inner-wrap";
      const table = document.createElement("table");
      table.innerHTML = "<thead><tr><th>iter</th><th>commit</th><th>time</th><th>subject</th><th>gpu</th><th>correctness</th><th>speedup</th><th>latency</th><th>inspect</th></tr></thead>";
      const body = document.createElement("tbody");
      const iterations = run.iterations || [];
      if (!iterations.length) {
        const tr = document.createElement("tr");
        tr.innerHTML = "<td class='muted' colspan='9'>No commit details found.</td>";
        body.appendChild(tr);
      } else {
        iterations.forEach((it) => {
          const tr = document.createElement("tr");
          tr.innerHTML =
            "<td>" + escapeHtml(safeText(it.iteration, "-")) + "</td>" +
            "<td><code>" + shortHash(it.commit_hash || "") + "</code></td>" +
            "<td>" + escapeHtml(safeText(it.commit_time, "-")) + "</td>" +
            "<td>" + escapeHtml(safeText(it.subject, "-")) + "</td>" +
            "<td>" + escapeHtml(safeText(it.gpu, "-")) + "</td>" +
            "<td>" + correctnessBadge(it.correctness) + "</td>" +
            "<td>" + formatMetric(it.speedup_vs_baseline, "x") + "</td>" +
            "<td>" + formatMetric(it.latency_us, " us") + "</td>" +
            "<td><button class='btn inspect-btn'>Inspect</button></td>";
          tr.querySelector(".inspect-btn").addEventListener("click", () => openCommitModal(run, it));
          body.appendChild(tr);
        });
      }
      table.appendChild(body);

      const chart = renderIterCharts(iterations);
      if (chart) wrap.appendChild(chart);

      wrap.appendChild(table);
      return wrap;
    }

    function openCommitModal(run, it) {
      state.currentIteration = it;
      const modal = document.getElementById("commitModal");
      modal.classList.remove("hidden");
      document.getElementById("modalTitle").textContent =
        shortHash(it.commit_hash || "") + "  " + safeText(it.subject, "-");
      const backendText = safeText(it.backend, "");
      document.getElementById("modalMeta").textContent =
        "run: " + safeText(run.run_id, "-") +
        " | branch: " + safeText(run.branch, "-") +
        " | gpu: " + safeText(it.gpu, "-") +
        (backendText ? " | backend: " + backendText : "") +
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
          "<td>" + backendBadge(inferRunBackend(run)) + "</td>" +
          "<td>" + escapeHtml(safeText(run.commit_count, "0")) + "</td>" +
          "<td><code>" + shortHash(latest) + "</code></td>" +
          "<td><button class='btn run-toggle' data-target='" + detailID + "'>Show</button></td>";
        rows.appendChild(tr);

        const detailRow = document.createElement("tr");
        detailRow.id = detailID;
        detailRow.className = "hidden";
        const detailCell = document.createElement("td");
        detailCell.colSpan = 6;
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

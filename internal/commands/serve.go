package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type ServeOptions struct {
	DBPath     string
	ListenAddr string
}

type patchResponse struct {
	Patch string `json:"patch,omitempty"`
	Error string `json:"error,omitempty"`
}

func Serve(opts ServeOptions) error {
	if strings.TrimSpace(opts.DBPath) == "" {
		return errors.New("--db-path cannot be empty")
	}
	addr := strings.TrimSpace(opts.ListenAddr)
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(renderServeHTML()))
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("/api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		includePatches := parseBoolQuery(r.URL.Query().Get("include_patches"))
		snapshot, err := buildSnapshot(opts.DBPath, includePatches)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	})

	mux.HandleFunc("/api/patch", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		repoPath := strings.TrimSpace(query.Get("repo_path"))
		commitHash := strings.TrimSpace(query.Get("commit"))
		parentHash := strings.TrimSpace(query.Get("parent"))
		if repoPath == "" || commitHash == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "repo_path and commit are required",
			})
			return
		}
		patch, patchErr := buildCommitPatch(repoPath, commitHash, parentHash)
		if patchErr != "" {
			writeJSON(w, http.StatusOK, patchResponse{Error: patchErr})
			return
		}
		writeJSON(w, http.StatusOK, patchResponse{Patch: patch})
	})

	fmt.Printf("[kernelhub serve] listening on http://%s\n", normalizeListenAddress(addr))
	return http.ListenAndServe(addr, mux)
}

func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func normalizeListenAddress(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func renderServeHTML() string {
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>KernelHub Live Dashboard</title>
  <style>
    :root {
      --bg: #0d1117;
      --panel: #161b22;
      --panel-soft: #1f2630;
      --line: #30363d;
      --text: #e6edf3;
      --muted: #9da7b3;
      --accent: #2f81f7;
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
    .toolbar {
      display: flex;
      align-items: center;
      gap: 10px;
      flex-wrap: wrap;
    }
    .btn {
      border: 1px solid var(--line);
      background: linear-gradient(180deg, #2d333b, #252b33);
      color: var(--text);
      border-radius: 8px;
      padding: 7px 12px;
      cursor: pointer;
      font-size: 13px;
      font-weight: 600;
    }
    .btn:hover { border-color: #3f4750; }
    .btn.small { padding: 5px 10px; font-size: 12px; }
    .toolbar label {
      color: var(--muted);
      font-size: 13px;
      display: inline-flex;
      gap: 7px;
      align-items: center;
    }
    .status {
      color: var(--muted);
      font-size: 13px;
      min-height: 20px;
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
      width: 100%;
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
      width: min(1250px, 100%);
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
      max-width: 90%;
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
        <div class="subtitle">Agent-first optimization timeline with commit-level evidence</div>
      </div>
      <div class="toolbar">
        <button id="refresh" class="btn">Refresh</button>
        <label><input id="embedPatches" type="checkbox"> Embed patches in snapshot (slower)</label>
      </div>
    </div>

    <div id="status" class="status"></div>
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
        <button id="closeModal" class="btn small">Close</button>
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
    const state = { snapshot: null, currentRun: null, currentIteration: null };

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

    function setStatus(text) {
      document.getElementById("status").textContent = text || "";
    }

    async function fetchJSON(url) {
      const res = await fetch(url, { cache: "no-store" });
      if (!res.ok) {
        const body = await res.text();
        throw new Error(body || ("HTTP " + res.status));
      }
      return await res.json();
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

    async function ensurePatchLoaded(run, it) {
      if (it.patch && it.patch.length) return;
      if (!run || !run.repo_path || !it.commit_hash) {
        it.patch_error = "missing repo_path or commit hash";
        return;
      }
      const params = new URLSearchParams({
        repo_path: run.repo_path || "",
        commit: it.commit_hash || "",
        parent: it.parent_commit_hash || "",
      });
      try {
        const payload = await fetchJSON("/api/patch?" + params.toString());
        if (payload.error) {
          it.patch_error = payload.error;
          return;
        }
        it.patch = payload.patch || "";
      } catch (err) {
        it.patch_error = String(err);
      }
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

    async function loadSnapshot() {
      const includePatches = document.getElementById("embedPatches").checked;
      setStatus("Loading snapshot...");
      const query = includePatches ? "?include_patches=1" : "";
      const snapshot = await fetchJSON("/api/snapshot" + query);
      state.snapshot = snapshot;
      renderSnapshot();
      setStatus("Updated at " + new Date().toLocaleTimeString());
    }

    function renderSnapshot() {
      const snapshot = state.snapshot || {};
      const stats = snapshot.stats || {};
      const runs = snapshot.runs || [];
      renderStats(stats);

      const rows = document.getElementById("rows");
      rows.innerHTML = "";
      runs.forEach((run, runIdx) => {
        const its = run.iterations || [];
        const latest = its.length ? its[its.length - 1].commit_hash : "";
        const detailID = "run-details-" + runIdx;

        const tr = document.createElement("tr");
        tr.innerHTML =
          "<td><code>" + escapeHtml(safeText(run.run_id, "-")) + "</code></td>" +
          "<td>" + escapeHtml(safeText(run.branch, "-")) + "</td>" +
          "<td>" + escapeHtml(safeText(run.commit_count, "0")) + "</td>" +
          "<td><code>" + shortHash(latest) + "</code></td>" +
          "<td><button class='btn small run-toggle' data-target='" + detailID + "'>Show</button></td>";
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

    function correctnessBadge(correctness) {
      const text = safeText(correctness, "-");
      const cls = text.toUpperCase() === "PASS" ? "badge" : "badge warn";
      return "<span class='" + cls + "'>" + escapeHtml(text) + "</span>";
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
            "<td><button class='btn small inspect-btn'>Inspect</button></td>";
          tr.querySelector(".inspect-btn").addEventListener("click", () => {
            openCommitModal(run, it).catch((err) => setStatus("Open commit failed: " + err));
          });
          body.appendChild(tr);
        });
      }

      table.appendChild(body);
      wrap.appendChild(table);
      return wrap;
    }

    async function openCommitModal(run, it) {
      state.currentRun = run;
      state.currentIteration = it;
      const modal = document.getElementById("commitModal");
      modal.classList.remove("hidden");

      document.getElementById("modalTitle").textContent =
        shortHash(it.commit_hash || "") + "  " + safeText(it.subject, "-");
      document.getElementById("modalMeta").textContent =
        "run: " + safeText(run.run_id, "-") +
        " | branch: " + safeText(run.branch, "-") +
        " | gpu: " + safeText(it.gpu, "-") +
        " | time: " + safeText(it.commit_time, "-");
      document.getElementById("modalHypothesis").textContent = safeText(it.hypothesis, "-");

      const defaultChanges = summarizePatch(it.patch || "") || safeText(it.subject, "-");
      document.getElementById("modalChanges").textContent =
        safeText(it.changes, defaultChanges);

      document.getElementById("modalAnalysis").textContent =
        safeText(it.analysis, buildAnalysisFallback(it));

      const patchEl = document.getElementById("modalPatch");
      patchEl.textContent = "Loading patch...";
      await ensurePatchLoaded(run, it);

      const latest = state.currentIteration;
      if (latest !== it) return;

      if (it.patch && it.patch.length) {
        patchEl.textContent = it.patch;
      } else {
        patchEl.textContent = "Patch unavailable: " + safeText(it.patch_error, "unknown error");
      }
      if (!it.changes || !it.changes.trim()) {
        const refreshChanges = summarizePatch(it.patch || "") || safeText(it.subject, "-");
        document.getElementById("modalChanges").textContent = refreshChanges;
      }
    }

    function closeModal() {
      state.currentRun = null;
      state.currentIteration = null;
      document.getElementById("commitModal").classList.add("hidden");
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

    document.getElementById("refresh").addEventListener("click", () => {
      loadSnapshot().catch((err) => setStatus("Load failed: " + err));
    });

    loadSnapshot().catch((err) => setStatus("Load failed: " + err));
  </script>
</body>
</html>`
}

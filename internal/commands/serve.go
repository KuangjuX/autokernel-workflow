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
    body { font-family: Arial, sans-serif; margin: 24px; color: #111; }
    .toolbar { display: flex; gap: 12px; align-items: center; margin-bottom: 12px; }
    .toolbar button { padding: 6px 10px; cursor: pointer; }
    .status { color: #555; font-size: 13px; }
    .stats { margin: 12px 0; font-size: 14px; }
    table { border-collapse: collapse; width: 100%; margin-top: 10px; }
    th, td { border: 1px solid #ddd; padding: 8px; text-align: left; font-size: 13px; vertical-align: top; }
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
    .hidden { display: none; }
    .details-cell { padding: 0; }
    .inner-wrap { padding: 10px; background: #fafafa; }
    .muted { color: #666; }
    .patch-wrap pre {
      margin: 8px 0 0;
      background: #111;
      color: #f5f5f5;
      padding: 12px;
      border-radius: 6px;
      font-size: 12px;
      line-height: 1.45;
      white-space: pre;
      overflow: auto;
      max-width: 100%;
    }
  </style>
</head>
<body>
  <h1>KernelHub Live Dashboard</h1>
  <div class="toolbar">
    <button id="refresh">Refresh</button>
    <label><input id="embedPatches" type="checkbox"> Embed patches in snapshot (slow)</label>
    <span id="status" class="status"></span>
  </div>
  <div id="stats" class="stats"></div>
  <table>
    <thead><tr><th>run_id</th><th>branch</th><th>commits</th><th>latest_commit</th><th>details</th></tr></thead>
    <tbody id="rows"></tbody>
  </table>
  <script>
    const state = { snapshot: null };

    function shortHash(hash) {
      return hash ? hash.slice(0, 12) : "";
    }

    function formatMetric(value, suffix) {
      if (typeof value !== "number" || Number.isNaN(value)) return "-";
      const text = Number.isInteger(value) ? value.toString() : value.toFixed(2).replace(/\.?0+$/, "");
      return suffix ? text + suffix : text;
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

    async function loadSnapshot() {
      const includePatches = document.getElementById("embedPatches").checked;
      setStatus("Loading...");
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
      document.getElementById("stats").textContent =
        "runs=" + (stats.run_count || 0) +
        " iterations=" + (stats.iteration_count || 0) +
        " best_speedup=" + formatMetric(stats.best_speedup || 0, "x");

      const rows = document.getElementById("rows");
      rows.innerHTML = "";

      runs.forEach((run, runIdx) => {
        const tr = document.createElement("tr");
        const its = run.iterations || [];
        const latest = its.length ? its[its.length - 1].commit_hash : "";
        const detailID = "run-details-" + runIdx;
        tr.innerHTML =
          "<td><code>" + (run.run_id || "") + "</code></td>" +
          "<td>" + (run.branch || "") + "</td>" +
          "<td>" + (run.commit_count || 0) + "</td>" +
          "<td><code>" + shortHash(latest) + "</code></td>" +
          "<td><button class='toggle' data-target='" + detailID + "'>Show</button></td>";
        rows.appendChild(tr);

        const detailRow = document.createElement("tr");
        detailRow.id = detailID;
        detailRow.className = "hidden";

        const detailCell = document.createElement("td");
        detailCell.className = "details-cell";
        detailCell.colSpan = 5;
        detailCell.appendChild(renderRunDetails(run, runIdx));
        detailRow.appendChild(detailCell);
        rows.appendChild(detailRow);
      });
    }

    function renderRunDetails(run, runIdx) {
      const wrap = document.createElement("div");
      wrap.className = "inner-wrap";
      const table = document.createElement("table");
      table.innerHTML = "<thead><tr><th>iter</th><th>commit</th><th>time</th><th>subject</th><th>correctness</th><th>speedup</th><th>latency</th><th>patch</th></tr></thead>";
      const body = document.createElement("tbody");
      const iterations = run.iterations || [];
      if (!iterations.length) {
        const tr = document.createElement("tr");
        tr.innerHTML = "<td class='muted' colspan='8'>No commit details found.</td>";
        body.appendChild(tr);
      } else {
        iterations.forEach((it, itIdx) => {
          const tr = document.createElement("tr");
          tr.innerHTML =
            "<td>" + (it.iteration ?? "-") + "</td>" +
            "<td><code>" + shortHash(it.commit_hash || "") + "</code></td>" +
            "<td>" + (it.commit_time || "-") + "</td>" +
            "<td></td>" +
            "<td>" + (it.correctness || "-") + "</td>" +
            "<td>" + formatMetric(it.speedup_vs_baseline, "x") + "</td>" +
            "<td>" + formatMetric(it.latency_us, " us") + "</td>" +
            "<td></td>";
          tr.children[3].textContent = it.subject || "-";

          const patchCell = tr.children[7];
          const patchButton = document.createElement("button");
          patchButton.className = "toggle";
          patchButton.textContent = (it.patch && it.patch.length) ? "View" : "Load";
          patchCell.appendChild(patchButton);

          const patchWrap = document.createElement("div");
          patchWrap.className = "patch-wrap hidden";
          patchCell.appendChild(patchWrap);

          patchButton.addEventListener("click", async () => {
            if (!patchWrap.classList.contains("hidden")) {
              patchWrap.classList.add("hidden");
              patchButton.textContent = (it.patch && it.patch.length) ? "View" : "Load";
              return;
            }

            if (!it.patch || !it.patch.length) {
              patchButton.disabled = true;
              patchButton.textContent = "Loading...";
              try {
                const params = new URLSearchParams({
                  repo_path: run.repo_path || "",
                  commit: it.commit_hash || "",
                  parent: it.parent_commit_hash || "",
                });
                const payload = await fetchJSON("/api/patch?" + params.toString());
                if (payload.error) {
                  it.patch_error = payload.error;
                } else {
                  it.patch = payload.patch || "";
                }
              } catch (err) {
                it.patch_error = String(err);
              } finally {
                patchButton.disabled = false;
              }
            }

            patchWrap.innerHTML = "";
            if (it.patch && it.patch.length) {
              const pre = document.createElement("pre");
              pre.textContent = it.patch;
              patchWrap.appendChild(pre);
              patchButton.textContent = "Hide";
            } else {
              const div = document.createElement("div");
              div.className = "muted";
              div.textContent = "Patch unavailable: " + (it.patch_error || "unknown error");
              patchWrap.appendChild(div);
              patchButton.textContent = "Hide";
            }
            patchWrap.classList.remove("hidden");
          });

          body.appendChild(tr);
        });
      }

      table.appendChild(body);
      wrap.appendChild(table);
      return wrap;
    }

    document.getElementById("rows").addEventListener("click", (event) => {
      const target = event.target;
      if (!(target instanceof HTMLButtonElement)) return;
      const targetID = target.getAttribute("data-target");
      if (!targetID) return;
      const detailRow = document.getElementById(targetID);
      if (!detailRow) return;
      const hidden = detailRow.classList.toggle("hidden");
      target.textContent = hidden ? "Show" : "Hide";
    });

    document.getElementById("refresh").addEventListener("click", () => {
      loadSnapshot().catch((err) => setStatus("Load failed: " + err));
    });

    loadSnapshot().catch((err) => setStatus("Load failed: " + err));
  </script>
</body>
</html>`
}

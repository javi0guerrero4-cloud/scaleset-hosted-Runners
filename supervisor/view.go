package supervisor

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
)

// Handler serves the live view of the supervisor: an HTML page at / and the
// state it renders at /api/state.
func (s *Supervisor) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.servePage)
	mux.HandleFunc("GET /api/state", s.serveState)
	return mux
}

func (s *Supervisor) serveState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	if err := json.NewEncoder(w).Encode(s.Snapshot()); err != nil {
		log.Printf("supervisor: writing state: %v", err)
	}
}

func (s *Supervisor) servePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if err := pageTemplate.Execute(w, s.Snapshot()); err != nil {
		log.Printf("supervisor: rendering page: %v", err)
	}
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Supervisor · {{.ScaleSetName}}</title>
<style>
  :root {
    color-scheme: dark;
    --bg: #0f1216;
    --card: #171c22;
    --line: #262d36;
    --ink: #e7edf3;
    --muted: #98a5b3;
    --good: #4ade80;
    --busy: #60a5fa;
    --warn: #fbbf24;
    --bad: #f87171;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    background: var(--bg);
    color: var(--ink);
    font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  }
  main { max-width: 1000px; margin: 0 auto; padding: 44px 24px 64px; }
  .eyebrow {
    margin: 0 0 10px;
    font: 500 12.5px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    letter-spacing: .08em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .title { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }
  h1 { margin: 0; font-size: 32px; line-height: 1.15; letter-spacing: -0.02em; }
  .status {
    font: 500 11px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    letter-spacing: .08em;
    text-transform: uppercase;
    padding: 6px 10px;
    border-radius: 999px;
    border: 1px solid var(--line);
    color: var(--muted);
    white-space: nowrap;
  }
  .status.live { color: var(--good); border-color: rgba(74, 222, 128, .35); }
  .status.stalled { color: var(--warn); border-color: rgba(251, 191, 36, .35); }
  .status.bad { color: var(--bad); border-color: rgba(248, 113, 113, .35); }
  .lede { margin: 12px 0 0; color: var(--muted); }
  .lede code { color: var(--ink); }
  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: 1px;
    margin: 28px 0 32px;
    background: var(--line);
    border: 1px solid var(--line);
    border-radius: 12px;
    overflow: hidden;
  }
  .stats div { background: var(--card); padding: 14px 16px; }
  .stats span { display: block; font-size: 11.5px; letter-spacing: .06em; text-transform: uppercase; color: var(--muted); }
  .stats strong { display: block; margin-top: 6px; font: 500 24px/1.1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
  .stats strong.busy { color: var(--busy); }
  .stats strong.good { color: var(--good); }
  .panel { margin: 0 0 30px; }
  .panel h2 {
    margin: 0 0 12px;
    font-size: 13px;
    font-weight: 600;
    letter-spacing: .07em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .card { background: var(--card); border: 1px solid var(--line); border-radius: 12px; overflow: hidden; }
  table { width: 100%; border-collapse: collapse; }
  th, td { text-align: left; padding: 11px 16px; border-bottom: 1px solid var(--line); font-size: 14px; }
  th { font: 500 11.5px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; letter-spacing: .06em; text-transform: uppercase; color: var(--muted); }
  tbody tr:last-child td { border-bottom: none; }
  td.mono, th.mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 13px; }
  td.job { color: var(--muted); }
  .pill {
    display: inline-block;
    font: 500 10.5px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    letter-spacing: .08em;
    text-transform: uppercase;
    padding: 5px 8px;
    border-radius: 999px;
    border: 1px solid var(--line);
    color: var(--muted);
  }
  .pill.busy { color: var(--busy); border-color: rgba(96, 165, 250, .35); }
  .pill.pending { color: var(--warn); border-color: rgba(251, 191, 36, .35); }
  .pill.done { color: var(--good); border-color: rgba(74, 222, 128, .35); }
  .empty { margin: 0; padding: 18px 16px; color: var(--muted); font-size: 14px; }
  ul.events { list-style: none; margin: 0; padding: 0; }
  ul.events li { display: flex; gap: 12px; padding: 10px 16px; border-bottom: 1px solid var(--line); font-size: 14px; align-items: baseline; }
  ul.events li:last-child { border-bottom: none; }
  ul.events time { font: 400 12px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; color: var(--muted); white-space: nowrap; }
  ul.events .kind { font: 500 10.5px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; letter-spacing: .06em; text-transform: uppercase; color: var(--muted); min-width: 58px; }
  ul.events .message { overflow-wrap: anywhere; }
  footer { margin-top: 34px; color: var(--muted); font-size: 13.5px; line-height: 1.6; }
  footer code { color: var(--ink); }
  @media (max-width: 600px) {
    main { padding: 30px 16px 48px; }
    h1 { font-size: 26px; }
    th, td { padding: 10px 12px; }
    /* The age is the least useful column when the table is this narrow. */
    th:nth-child(5), td:nth-child(5) { display: none; }
  }
</style>
</head>
<body>
<main>
  <p class="eyebrow">github.com/actions/scaleset · supervisor</p>
  <div class="title">
    <h1 id="scale-set-name">{{if .ScaleSetName}}{{.ScaleSetName}}{{else}}supervisor{{end}}</h1>
    <span class="status" id="status">starting</span>
  </div>
  <p class="lede" id="subtitle">Scale set <code id="scale-set-id">#{{.ScaleSetID}}</code> · keeping <span id="min-max">{{.MinRunners}}–{{.MaxRunners}}</span> runners · <span id="polls">{{.Polls}}</span> polls</p>

  <section class="stats">
    <div><span>Desired</span><strong id="desired">{{.DesiredRunners}}</strong></div>
    <div><span>Active</span><strong id="active">{{.ActiveRunners}}</strong></div>
    <div><span>Busy</span><strong class="busy" id="busy">{{.BusyRunners}}</strong></div>
    <div><span>Pending</span><strong id="pending">{{.PendingRunners}}</strong></div>
    <div><span>Assigned jobs</span><strong id="assigned">{{if .Statistics}}{{.Statistics.TotalAssignedJobs}}{{else}}0{{end}}</strong></div>
    <div><span>Running jobs</span><strong id="running">{{if .Statistics}}{{.Statistics.TotalRunningJobs}}{{else}}0{{end}}</strong></div>
  </section>

  <section class="panel">
    <h2>Runners</h2>
    <div class="card">
      <table>
        <thead>
          <tr><th>Runner</th><th class="mono">ID</th><th>State</th><th>Job</th><th>Age</th></tr>
        </thead>
        <tbody id="runners"></tbody>
      </table>
      <p class="empty" id="runners-empty">No runners provisioned yet — waiting for jobs.</p>
    </div>
  </section>

  <section class="panel">
    <h2>Recent activity</h2>
    <div class="card">
      <ul class="events" id="events"></ul>
      <p class="empty" id="events-empty">Nothing yet. The supervisor logs every scale decision here.</p>
    </div>
  </section>

  <footer>
    This command supervises the scale set: it polls for messages, acquires jobs, issues a JIT config per
    runner and retires runners it no longer needs. Starting the runner itself is up to your launcher, which
    receives the config; with no launcher configured the runners are tracked only.
  </footer>
</main>
<script>
(function () {
  function text(id, value) {
    var node = document.getElementById(id);
    if (node) { node.textContent = String(value); }
  }

  function setStatus(label, kind) {
    var node = document.getElementById("status");
    node.textContent = label;
    node.className = "status" + (kind ? " " + kind : "");
  }

  function elapsed(since) {
    var seconds = Math.max(0, Math.round((Date.now() - new Date(since).getTime()) / 1000));
    if (seconds < 60) { return seconds + "s"; }
    var minutes = Math.floor(seconds / 60);
    if (minutes < 60) { return minutes + "m " + (seconds % 60) + "s"; }
    return Math.floor(minutes / 60) + "h " + (minutes % 60) + "m";
  }

  function renderRunners(runners) {
    var body = document.getElementById("runners");
    var empty = document.getElementById("runners-empty");
    body.textContent = "";

    empty.style.display = runners.length === 0 ? "" : "none";

    runners.forEach(function (runner) {
      var row = document.createElement("tr");

      var name = document.createElement("td");
      name.className = "mono";
      name.textContent = runner.name;
      row.appendChild(name);

      var id = document.createElement("td");
      id.className = "mono";
      id.textContent = runner.id ? "#" + runner.id : "—";
      row.appendChild(id);

      var state = document.createElement("td");
      var pill = document.createElement("span");
      pill.className = "pill " + runner.state;
      pill.textContent = runner.state;
      state.appendChild(pill);
      row.appendChild(state);

      var job = document.createElement("td");
      job.className = "job";
      job.textContent = runner.jobDisplayName || runner.jobId || "—";
      row.appendChild(job);

      var age = document.createElement("td");
      age.className = "mono";
      age.textContent = runner.state === "done" && runner.finishedAt
        ? "ran " + elapsed(runner.createdAt)
        : elapsed(runner.createdAt);
      row.appendChild(age);

      body.appendChild(row);
    });
  }

  function renderEvents(events) {
    var list = document.getElementById("events");
    var empty = document.getElementById("events-empty");
    list.textContent = "";

    empty.style.display = events.length === 0 ? "" : "none";

    events.forEach(function (event) {
      var item = document.createElement("li");

      var at = document.createElement("time");
      at.textContent = new Date(event.at).toLocaleTimeString();
      item.appendChild(at);

      var kind = document.createElement("span");
      kind.className = "kind";
      kind.textContent = event.kind;
      item.appendChild(kind);

      var message = document.createElement("span");
      message.className = "message";
      message.textContent = event.message;
      item.appendChild(message);

      list.appendChild(item);
    });
  }

  function render(state) {
    text("scale-set-name", state.scaleSetName || "supervisor");
    text("scale-set-id", "#" + state.scaleSetId);
    text("min-max", state.minRunners + "–" + state.maxRunners);
    text("polls", state.polls);
    text("desired", state.desiredRunners);
    text("active", state.activeRunners);
    text("busy", state.busyRunners);
    text("pending", state.pendingRunners);
    text("assigned", state.statistics ? state.statistics.totalAssignedJobs : 0);
    text("running", state.statistics ? state.statistics.totalRunningJobs : 0);

    renderRunners(state.runners || []);
    renderEvents(state.events || []);

    if (!state.lastPollAt) {
      setStatus("starting", "");
    } else if (Date.now() - new Date(state.lastPollAt).getTime() < 15000) {
      setStatus("polling", "live");
    } else {
      setStatus("no messages", "stalled");
    }
  }

  function refresh() {
    fetch("/api/state", { cache: "no-store" })
      .then(function (response) {
        if (!response.ok) { throw new Error("state " + response.status); }
        return response.json();
      })
      .then(render)
      .catch(function () { setStatus("unreachable", "bad"); });
  }

  refresh();
  setInterval(refresh, 1000);
})();
</script>
</body>
</html>
`))

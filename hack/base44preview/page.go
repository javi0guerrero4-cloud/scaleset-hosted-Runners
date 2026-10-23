package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"time"
)

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>scaleset client — live preview</title>
<style>
  :root {
    color-scheme: dark;
    --bg: #0f1216;
    --card: #171c22;
    --line: #262d36;
    --ink: #e7edf3;
    --muted: #98a5b3;
    --good: #4ade80;
    --bad: #f87171;
    --skip: #7c8797;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    background: var(--bg);
    color: var(--ink);
    font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  }
  main { max-width: 940px; margin: 0 auto; padding: 48px 24px 72px; }
  .eyebrow {
    margin: 0 0 12px;
    font: 500 13px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    letter-spacing: .08em;
    text-transform: uppercase;
    color: var(--muted);
  }
  h1 { margin: 0 0 16px; font-size: 34px; line-height: 1.15; letter-spacing: -0.02em; }
  .lede { margin: 0; max-width: 46em; color: var(--muted); }
  .meta {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
    gap: 1px;
    margin: 32px 0;
    background: var(--line);
    border: 1px solid var(--line);
    border-radius: 12px;
    overflow: hidden;
  }
  .meta div { background: var(--card); padding: 14px 16px; }
  .meta span { display: block; font-size: 12px; letter-spacing: .06em; text-transform: uppercase; color: var(--muted); }
  .meta strong { display: block; margin-top: 6px; font-size: 14px; font-weight: 500; word-break: break-all; }
  .meta strong.good { color: var(--good); }
  .meta strong.bad { color: var(--bad); }
  ol.steps { list-style: none; margin: 0; padding: 0; display: grid; gap: 14px; }
  .step {
    background: var(--card);
    border: 1px solid var(--line);
    border-left: 3px solid var(--skip);
    border-radius: 10px;
    padding: 16px 18px;
    min-width: 0;
  }
  .step.ok { border-left-color: var(--good); }
  .step.failed { border-left-color: var(--bad); }
  .step.skipped { opacity: .55; }
  .step .row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
  .step h2 { margin: 0; font-size: 16px; font-weight: 600; flex: 1 1 auto; }
  .pill {
    font: 500 11px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    letter-spacing: .08em;
    text-transform: uppercase;
    padding: 5px 9px;
    border-radius: 999px;
    border: 1px solid var(--line);
    color: var(--muted);
  }
  .step.ok .pill { color: var(--good); border-color: rgba(74, 222, 128, .35); }
  .step.failed .pill { color: var(--bad); border-color: rgba(248, 113, 113, .35); }
  .duration { font: 400 12px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; color: var(--muted); }
  .summary { margin: 10px 0 0; color: var(--ink); overflow-wrap: anywhere; }
  .error {
    margin: 10px 0 0;
    color: var(--bad);
    font: 400 14px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    word-break: break-word;
  }
  pre {
    margin: 14px 0 0;
    padding: 14px;
    background: #0b0e12;
    border: 1px solid var(--line);
    border-radius: 8px;
    max-width: 100%;
    overflow-x: auto;
    font: 400 12.5px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    color: #cbd5e1;
  }
  footer { margin-top: 32px; color: var(--muted); font-size: 14px; }
  code { font: 400 13px/1 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; color: var(--ink); }
</style>
</head>
<body>
<main>
  <p class="eyebrow">github.com/actions/scaleset</p>
  <h1>The runner scale set client, running for real</h1>
  <p class="lede">
    This repository is a Go client library, so it ships no user interface of its own. This page drives the
    library's own code through a complete runner scale set flow &mdash; registration, scale set creation, a
    message session, queued job messages and a JIT runner config. GitHub's API is replaced by a stand-in
    served inside this container: no credentials, no outbound calls, and nothing created on GitHub.
  </p>

  <section class="meta">
    <div><span>Result</span><strong class="{{if .Failed}}bad{{else}}good{{end}}">{{if .Failed}}flow failed{{else}}every step passed{{end}}</strong></div>
    <div><span>Config URL</span><strong>{{.ConfigURL}}</strong></div>
    <div><span>Started (UTC)</span><strong>{{.StartedAt}}</strong></div>
    <div><span>Total</span><strong>{{.Total}}</strong></div>
  </section>

  <ol class="steps">
  {{range .Steps}}
    <li class="step {{.Status}}">
      <div class="row">
        <h2>{{.Title}}</h2>
        <span class="pill">{{.Status}}</span>
        {{if .Duration}}<span class="duration">{{.Duration}}</span>{{end}}
      </div>
      {{if .Summary}}<p class="summary">{{.Summary}}</p>{{end}}
      {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
      {{if .Payload}}<pre>{{.Payload}}</pre>{{end}}
    </li>
  {{end}}
  </ol>

  <footer>
    Reload this page to run the whole flow again. Go has no hot reload, so restart the
    <code>preview</code> service after editing code.
  </footer>
</main>
</body>
</html>
`))

// pageHandler runs the demo scenario on every request, so reloading the page
// re-runs the library against a fresh stand-in API.
type pageHandler struct {
	stub      *stub
	configURL string
}

func newPageHandler(stub *stub, configURL string) *pageHandler {
	return &pageHandler{stub: stub, configURL: configURL}
}

func (h *pageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	h.stub.reset()

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	result := runScenario(ctx, h.configURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if err := pageTemplate.Execute(w, result); err != nil {
		log.Printf("preview: rendering page: %v", err)
	}
}

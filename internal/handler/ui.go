package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/yourusername/priorityserve/internal/dashboard"
	"github.com/yourusername/priorityserve/internal/scheduler"
)

// WorkerStater is satisfied by worker.Pool — avoids an import cycle.
type WorkerStater interface {
	ActiveWorkers() int
	TotalWorkers() int
}

type UIHandler struct {
	dash  *dashboard.Dashboard
	queue *scheduler.MultiQueue
	pool  WorkerStater
}

func NewUIHandler(d *dashboard.Dashboard, q *scheduler.MultiQueue, p WorkerStater) *UIHandler {
	return &UIHandler{dash: d, queue: q, pool: p}
}

func (h *UIHandler) Page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboardHTML)
}

type stateJSON struct {
	High          int             `json:"high"`
	Medium        int             `json:"medium"`
	Low           int             `json:"low"`
	WorkersActive int             `json:"workers_active"`
	WorkersTotal  int             `json:"workers_total"`
	QueueDepth    int             `json:"queue_depth"`
	Requests      []requestJSON   `json:"requests"`
}

type requestJSON struct {
	ID        string  `json:"id"`
	Priority  string  `json:"priority"`
	Kind      string  `json:"kind"`
	LatencyMs float64 `json:"latency_ms"`
	Status    int     `json:"status"`
	Time      string  `json:"time"`
}

func (h *UIHandler) Events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			high, med, low := h.queue.Depths()
			events := h.dash.Snapshot()

			reqs := make([]requestJSON, 0, len(events))
			for _, e := range events {
				reqs = append(reqs, requestJSON{
					ID:        e.ID,
					Priority:  e.Priority,
					Kind:      string(e.Kind),
					LatencyMs: e.LatencyMs,
					Status:    e.Status,
					Time:      e.Timestamp.Format("15:04:05"),
				})
			}

			state := stateJSON{
				High:          high,
				Medium:        med,
				Low:           low,
				WorkersActive: h.pool.ActiveWorkers(),
				WorkersTotal:  h.pool.TotalWorkers(),
				QueueDepth:    100,
				Requests:      reqs,
			}
			data, _ := json.Marshal(state)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>PriorityServe</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: 'SF Mono', 'Fira Code', monospace; background: #0d1117; color: #e6edf3; padding: 28px; }
  h1 { color: #58a6ff; font-size: 20px; margin-bottom: 6px; }
  .subtitle { color: #8b949e; font-size: 12px; margin-bottom: 24px; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin-bottom: 16px; }
  .card { background: #161b22; border: 1px solid #30363d; border-radius: 8px; padding: 16px; }
  .card-full { background: #161b22; border: 1px solid #30363d; border-radius: 8px; padding: 16px; }
  .card-title { font-size: 11px; color: #8b949e; text-transform: uppercase; letter-spacing: 1px; margin-bottom: 14px; }
  .bar-row { display: flex; align-items: center; gap: 10px; margin-bottom: 10px; }
  .bar-label { width: 36px; font-size: 11px; font-weight: bold; }
  .label-high { color: #f85149; }
  .label-med  { color: #d29922; }
  .label-low  { color: #3fb950; }
  .bar-track { flex: 1; background: #21262d; border-radius: 3px; height: 18px; overflow: hidden; }
  .bar-fill { height: 100%; border-radius: 3px; transition: width 0.35s ease; min-width: 0; }
  .fill-high { background: #f85149; }
  .fill-med  { background: #d29922; }
  .fill-low  { background: #3fb950; }
  .bar-count { width: 28px; text-align: right; font-size: 12px; color: #8b949e; }
  .workers-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .dot { width: 14px; height: 14px; border-radius: 50%; background: #21262d; transition: background 0.2s; }
  .dot.active { background: #58a6ff; box-shadow: 0 0 6px #58a6ff88; }
  .worker-label { font-size: 12px; color: #8b949e; margin-left: 4px; }
  table { width: 100%; border-collapse: collapse; font-size: 12px; }
  thead th { color: #8b949e; text-align: left; padding: 4px 10px 8px; border-bottom: 1px solid #21262d; font-weight: normal; font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px; }
  tbody td { padding: 7px 10px; border-bottom: 1px solid #21262d; }
  tbody tr:last-child td { border-bottom: none; }
  .tag { display: inline-block; padding: 1px 7px; border-radius: 10px; font-size: 10px; font-weight: bold; }
  .tag-high    { background: #3d1c1c; color: #f85149; }
  .tag-medium  { background: #2d2111; color: #d29922; }
  .tag-low     { background: #1a2d1d; color: #3fb950; }
  .kind-enqueued  { color: #6e7681; }
  .kind-active    { color: #58a6ff; }
  .kind-done      { color: #3fb950; }
  .latency        { color: #8b949e; }
  .latency.fast   { color: #3fb950; }
  .latency.slow   { color: #f85149; }
  .id { color: #6e7681; font-size: 11px; }
  .dot-status { display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-right: 4px; }
  #conn-status { font-size: 11px; color: #f85149; float: right; }
  #conn-status.connected { color: #3fb950; }
  .fire-grid { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 10px; margin-bottom: 12px; }
  .fire-group label { display: block; font-size: 11px; color: #8b949e; text-transform: uppercase; letter-spacing: 0.5px; margin-bottom: 6px; }
  .fire-group select, .fire-group input[type=number], .fire-group input[type=text] {
    width: 100%; background: #0d1117; border: 1px solid #30363d; border-radius: 5px;
    color: #e6edf3; padding: 7px 10px; font-family: inherit; font-size: 13px; outline: none;
  }
  .fire-group select:focus, .fire-group input:focus { border-color: #58a6ff; }
  .prompt-group { grid-column: 1 / -1; }
  .btn-row { display: flex; gap: 10px; }
  .btn { padding: 8px 18px; border: none; border-radius: 5px; font-family: inherit; font-size: 12px; font-weight: bold; cursor: pointer; transition: opacity 0.15s; }
  .btn:hover { opacity: 0.85; }
  .btn-high   { background: #3d1c1c; color: #f85149; border: 1px solid #f85149; }
  .btn-medium { background: #2d2111; color: #d29922; border: 1px solid #d29922; }
  .btn-low    { background: #1a2d1d; color: #3fb950; border: 1px solid #3fb950; }
  .btn-flood  { background: #1c2333; color: #58a6ff; border: 1px solid #58a6ff; }
  .fire-status { font-size: 11px; color: #8b949e; margin-top: 8px; min-height: 16px; }
</style>
</head>
<body>
<h1>PriorityServe <span id="conn-status">● disconnected</span></h1>
<p class="subtitle">Priority-Aware Request Scheduling — Live Dashboard</p>

<div class="grid">
  <div class="card">
    <div class="card-title">Queue Depth</div>
    <div class="bar-row">
      <span class="bar-label label-high">HIGH</span>
      <div class="bar-track"><div class="bar-fill fill-high" id="bar-high" style="width:0%"></div></div>
      <span class="bar-count" id="count-high">0</span>
    </div>
    <div class="bar-row">
      <span class="bar-label label-med">MED</span>
      <div class="bar-track"><div class="bar-fill fill-med" id="bar-med" style="width:0%"></div></div>
      <span class="bar-count" id="count-med">0</span>
    </div>
    <div class="bar-row">
      <span class="bar-label label-low">LOW</span>
      <div class="bar-track"><div class="bar-fill fill-low" id="bar-low" style="width:0%"></div></div>
      <span class="bar-count" id="count-low">0</span>
    </div>
  </div>

  <div class="card">
    <div class="card-title">Workers</div>
    <div class="workers-row" id="workers-dots"></div>
    <p style="font-size:12px;color:#8b949e;margin-top:12px" id="workers-label">—</p>
  </div>
</div>

<div class="card-full" style="margin-bottom:16px">
  <div class="card-title">Fire Requests</div>
  <div class="fire-grid">
    <div class="fire-group">
      <label>Priority</label>
      <select id="f-priority">
        <option value="high">HIGH</option>
        <option value="medium" selected>MEDIUM</option>
        <option value="low">LOW</option>
      </select>
    </div>
    <div class="fire-group">
      <label>Count</label>
      <input type="number" id="f-count" value="1" min="1" max="50">
    </div>
    <div class="fire-group">
      <label>&nbsp;</label>
      <div class="btn-row" style="margin-top:0">
        <button class="btn btn-high"   onclick="fireRequests('high',   parseInt(document.getElementById('f-count').value))">Fire HIGH</button>
        <button class="btn btn-medium" onclick="fireRequests('medium', parseInt(document.getElementById('f-count').value))">Fire MED</button>
        <button class="btn btn-low"    onclick="fireRequests('low',    parseInt(document.getElementById('f-count').value))">Fire LOW</button>
      </div>
    </div>
    <div class="fire-group prompt-group">
      <label>Prompt</label>
      <input type="text" id="f-prompt" value="Count from 1 to 20 slowly." placeholder="Enter a prompt...">
    </div>
  </div>
  <div class="btn-row">
    <button class="btn btn-flood" onclick="floodTest()">⚡ Flood Test — 10 LOW + 1 HIGH simultaneously</button>
  </div>
  <div class="fire-status" id="fire-status"></div>
</div>

<div class="card-full">
  <div class="card-title">Recent Requests</div>
  <table>
    <thead>
      <tr><th>Time</th><th>ID</th><th>Priority</th><th>Status</th><th>Latency</th></tr>
    </thead>
    <tbody id="req-body">
      <tr><td colspan="5" style="color:#6e7681;text-align:center;padding:20px">Waiting for requests...</td></tr>
    </tbody>
  </table>
</div>

<script>
const MAX_DEPTH = 100;

const es = new EventSource('/ui/events');

es.onopen = () => {
  document.getElementById('conn-status').textContent = '● live';
  document.getElementById('conn-status').className = 'connected';
};
es.onerror = () => {
  document.getElementById('conn-status').textContent = '● disconnected';
  document.getElementById('conn-status').className = '';
};

es.onmessage = (e) => {
  const s = JSON.parse(e.data);
  const max = s.queue_depth || 100;

  setBar('high', s.high, max);
  setBar('med',  s.medium, max);
  setBar('low',  s.low, max);

  const dotsEl = document.getElementById('workers-dots');
  dotsEl.innerHTML = '';
  for (let i = 0; i < s.workers_total; i++) {
    const d = document.createElement('div');
    d.className = 'dot' + (i < s.workers_active ? ' active' : '');
    dotsEl.appendChild(d);
  }
  document.getElementById('workers-label').textContent =
    s.workers_active + ' / ' + s.workers_total + ' active';

  const tbody = document.getElementById('req-body');
  if (!s.requests || s.requests.length === 0) return;
  tbody.innerHTML = '';

  for (const r of s.requests) {
    const tr = document.createElement('tr');
    const latencyEl = r.latency_ms > 0
      ? '<span class="latency' + (r.latency_ms < 3000 ? ' fast' : ' slow') + '">' + r.latency_ms.toFixed(0) + 'ms</span>'
      : '<span class="latency">—</span>';
    const kindEl = '<span class="kind-' + r.kind + '">' + r.kind + '</span>';
    tr.innerHTML =
      '<td style="color:#6e7681">' + r.time + '</td>' +
      '<td class="id">' + r.id.slice(0, 8) + '</td>' +
      '<td><span class="tag tag-' + r.priority + '">' + r.priority.toUpperCase() + '</span></td>' +
      '<td>' + kindEl + '</td>' +
      '<td>' + latencyEl + '</td>';
    tbody.appendChild(tr);
  }
};

function setBar(id, val, max) {
  document.getElementById('bar-' + id).style.width = Math.min(val / max * 100, 100) + '%';
  document.getElementById('count-' + id).textContent = val;
}

function fireRequests(priority, count) {
  const prompt = document.getElementById('f-prompt').value || 'Count from 1 to 20 slowly.';
  const statusEl = document.getElementById('fire-status');
  statusEl.textContent = 'Firing ' + count + ' ' + priority.toUpperCase() + ' request(s)...';
  let done = 0;
  for (let i = 0; i < count; i++) {
    fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Priority': priority },
      body: JSON.stringify({
        model: 'llama3.2',
        messages: [{ role: 'user', content: prompt }],
        stream: false
      })
    }).then(() => {
      done++;
      if (done === count) statusEl.textContent = count + ' ' + priority.toUpperCase() + ' request(s) completed.';
    }).catch(() => {
      statusEl.textContent = 'Error — is the server running?';
    });
  }
}

function floodTest() {
  const prompt = document.getElementById('f-prompt').value || 'Count from 1 to 20 slowly.';
  document.getElementById('fire-status').textContent = 'Flood test started — 10 LOW + 1 HIGH fired simultaneously.';
  const body = (priority) => JSON.stringify({
    model: 'llama3.2',
    messages: [{ role: 'user', content: prompt }],
    stream: false
  });
  // Fire 10 low first, then 1 high with a tiny delay so all lows hit the queue first
  for (let i = 0; i < 10; i++) {
    fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Priority': 'low' },
      body: body('low')
    });
  }
  setTimeout(() => {
    fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Priority': 'high' },
      body: body('high')
    }).then(() => {
      document.getElementById('fire-status').textContent = 'HIGH request completed. Check latency vs LOW in the table above.';
    });
  }, 50);
}
</script>
</body>
</html>`

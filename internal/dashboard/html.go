package dashboard

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>kar98k Dashboard</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { background: #0a0a0a; color: #e0e0e0; font-family: 'SF Mono', 'Fira Code', monospace; }

.header {
  background: #111; border-bottom: 2px solid #87CEEB;
  padding: 16px 24px; display: flex; align-items: center; justify-content: space-between;
}
.header h1 { color: #87CEEB; font-size: 20px; letter-spacing: 2px; }
.header .meta { color: #666; font-size: 13px; }
.header .meta span { color: #87CEEB; }

.grid {
  display: grid; grid-template-columns: repeat(4, 1fr);
  gap: 12px; padding: 16px 24px;
}

.card {
  background: #141414; border: 1px solid #222; border-radius: 8px;
  padding: 16px; text-align: center;
}
.card .label { color: #666; font-size: 11px; text-transform: uppercase; letter-spacing: 1px; }
.card .value { color: #87CEEB; font-size: 28px; font-weight: bold; margin: 6px 0; }
.card .sub { color: #555; font-size: 12px; }
.card.error .value { color: #ff6b6b; }
.card.success .value { color: #51cf66; }

.charts {
  display: grid; grid-template-columns: 1fr 1fr;
  gap: 12px; padding: 0 24px 16px;
}

.chart-box {
  background: #141414; border: 1px solid #222; border-radius: 8px;
  padding: 16px;
}
.chart-box h3 { color: #87CEEB; font-size: 13px; margin-bottom: 8px; letter-spacing: 1px; }
canvas { width: 100% !important; height: 200px !important; }

.bottom {
  display: grid; grid-template-columns: 1fr 1fr;
  gap: 12px; padding: 0 24px 24px;
}

.checks-box, .status-box {
  background: #141414; border: 1px solid #222; border-radius: 8px;
  padding: 16px;
}
.checks-box h3, .status-box h3 { color: #87CEEB; font-size: 13px; margin-bottom: 10px; letter-spacing: 1px; }

.check-row {
  display: flex; justify-content: space-between; padding: 4px 0;
  border-bottom: 1px solid #1a1a1a; font-size: 13px;
}
.check-row .name { color: #aaa; }
.check-row .pass { color: #51cf66; }
.check-row .fail { color: #ff6b6b; }

.status-row {
  display: flex; justify-content: space-between; padding: 4px 0;
  border-bottom: 1px solid #1a1a1a; font-size: 13px;
}
.status-row .code { color: #87CEEB; font-weight: bold; }
.status-row .count { color: #aaa; }

.elapsed { color: #87CEEB; font-size: 14px; font-weight: bold; }
</style>
</head>
<body>

<div class="header">
  <h1>⌖ KAR98K</h1>
  <div class="meta">
    Scenario: <span id="scenario">-</span> &nbsp;|&nbsp;
    Preset: <span id="preset">-</span> &nbsp;|&nbsp;
    <span class="elapsed" id="elapsed">00:00</span>
  </div>
</div>

<div class="grid">
  <div class="card"><div class="label">RPS</div><div class="value" id="rps">0</div><div class="sub">requests/sec</div></div>
  <div class="card"><div class="label">Total Requests</div><div class="value" id="total">0</div><div class="sub" id="iters">0 iterations</div></div>
  <div class="card"><div class="label">Avg Latency</div><div class="value" id="latency">0ms</div><div class="sub" id="p95">P95: 0ms</div></div>
  <div class="card" id="errorCard"><div class="label">Error Rate</div><div class="value" id="errorRate">0%</div><div class="sub" id="errorCount">0 errors</div></div>
</div>

<div class="charts">
  <div class="chart-box"><h3>RPS</h3><canvas id="rpsChart"></canvas></div>
  <div class="chart-box"><h3>LATENCY (ms)</h3><canvas id="latChart"></canvas></div>
</div>

<div class="bottom">
  <div class="checks-box"><h3>CHECKS</h3><div id="checks">-</div></div>
  <div class="status-box"><h3>STATUS CODES</h3><div id="statusCodes">-</div></div>
</div>

<script>
const MAX_POINTS = 120;
const rpsData = [];
const latData = [];
const p95Data = [];

function drawChart(canvas, datasets, maxVal) {
  const ctx = canvas.getContext('2d');
  const W = canvas.width = canvas.offsetWidth * 2;
  const H = canvas.height = canvas.offsetHeight * 2;
  ctx.scale(1, 1);
  ctx.clearRect(0, 0, W, H);

  if (datasets[0].length < 2) return;

  const pad = { t: 10, r: 10, b: 20, l: 50 };
  const cw = W - pad.l - pad.r;
  const ch = H - pad.t - pad.b;

  // Grid
  ctx.strokeStyle = '#222';
  ctx.lineWidth = 1;
  for (let i = 0; i <= 4; i++) {
    const y = pad.t + ch * i / 4;
    ctx.beginPath(); ctx.moveTo(pad.l, y); ctx.lineTo(W - pad.r, y); ctx.stroke();
    ctx.fillStyle = '#555'; ctx.font = '20px monospace';
    ctx.fillText((maxVal * (4 - i) / 4).toFixed(0), 4, y + 6);
  }

  const colors = ['#87CEEB', '#00BFFF', '#ff6b6b'];
  datasets.forEach((data, di) => {
    ctx.strokeStyle = colors[di] || '#87CEEB';
    ctx.lineWidth = di === 0 ? 3 : 2;
    ctx.globalAlpha = di === 0 ? 1 : 0.5;
    ctx.beginPath();
    data.forEach((v, i) => {
      const x = pad.l + (i / (MAX_POINTS - 1)) * cw;
      const y = pad.t + ch - (v / maxVal) * ch;
      i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y);
    });
    ctx.stroke();
    ctx.globalAlpha = 1;
  });
}

function fmt(n) {
  if (n >= 1000000) return (n/1000000).toFixed(1) + 'M';
  if (n >= 1000) return (n/1000).toFixed(1) + 'K';
  return n.toString();
}

function fmtMs(ms) {
  if (ms >= 1000) return (ms/1000).toFixed(2) + 's';
  if (ms >= 1) return ms.toFixed(1) + 'ms';
  return (ms * 1000).toFixed(0) + 'µs';
}

function fmtElapsed(s) {
  const m = Math.floor(s / 60);
  const sec = Math.floor(s % 60);
  return (m < 10 ? '0' : '') + m + ':' + (sec < 10 ? '0' : '') + sec;
}

function update(stats) {
  document.getElementById('rps').textContent = stats.rps.toFixed(0);
  document.getElementById('total').textContent = fmt(stats.total_reqs);
  document.getElementById('iters').textContent = fmt(stats.iterations) + ' iterations';
  document.getElementById('latency').textContent = fmtMs(stats.avg_latency * 1000);
  document.getElementById('p95').textContent = 'P95: ' + fmtMs(stats.p95_latency * 1000) + ' / P99: ' + fmtMs(stats.p99_latency * 1000);
  document.getElementById('elapsed').textContent = fmtElapsed(stats.elapsed);

  const errRate = stats.total_reqs > 0 ? (stats.total_errors / stats.total_reqs * 100) : 0;
  document.getElementById('errorRate').textContent = errRate.toFixed(1) + '%';
  document.getElementById('errorCount').textContent = stats.total_errors + ' errors';

  const card = document.getElementById('errorCard');
  card.className = errRate > 5 ? 'card error' : errRate > 0 ? 'card' : 'card success';

  // Charts
  rpsData.push(stats.rps);
  latData.push(stats.avg_latency * 1000);
  p95Data.push(stats.p95_latency * 1000);
  if (rpsData.length > MAX_POINTS) { rpsData.shift(); latData.shift(); p95Data.shift(); }

  const rpsMax = Math.max(10, ...rpsData) * 1.2;
  const latMax = Math.max(1, ...p95Data) * 1.2;
  drawChart(document.getElementById('rpsChart'), [rpsData], rpsMax);
  drawChart(document.getElementById('latChart'), [latData, p95Data], latMax);

  // Checks
  if (stats.checks && stats.checks.length > 0) {
    document.getElementById('checks').innerHTML = stats.checks.map(c => {
      const cls = c.failed > 0 ? 'fail' : 'pass';
      const icon = c.failed > 0 ? '✗' : '✓';
      return '<div class="check-row"><span class="name">' + icon + ' ' + c.name +
        '</span><span class="' + cls + '">' + c.rate.toFixed(0) + '% (' + c.passed + '/' + (c.passed + c.failed) + ')</span></div>';
    }).join('');
  }

  // Status codes
  if (stats.status_codes) {
    const entries = Object.entries(stats.status_codes).sort((a, b) => a[0] - b[0]);
    document.getElementById('statusCodes').innerHTML = entries.map(([code, count]) =>
      '<div class="status-row"><span class="code">' + code + '</span><span class="count">' + fmt(count) + '</span></div>'
    ).join('');
  }
}

// SSE connection
const es = new EventSource('/events');

es.addEventListener('init', e => {
  const data = JSON.parse(e.data);
  document.getElementById('scenario').textContent = data.scenario || '-';
  document.getElementById('preset').textContent = data.preset || '-';
});

es.onmessage = e => {
  update(JSON.parse(e.data));
};

es.onerror = () => {
  document.getElementById('scenario').textContent += ' (disconnected)';
};
</script>
</body>
</html>`

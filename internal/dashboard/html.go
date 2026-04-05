package dashboard

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>kar98k</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { background: #111; color: #ddd; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; font-size: 14px; }

.topbar {
  background: #1a1a1a; padding: 12px 24px;
  display: flex; align-items: center; justify-content: space-between;
  border-bottom: 1px solid #333;
}
.topbar-left { display: flex; align-items: center; gap: 20px; }
.topbar h1 { color: #87CEEB; font-size: 16px; font-weight: 600; }
.topbar .info { color: #888; font-size: 13px; }
.topbar .info b { color: #87CEEB; font-weight: 500; }
.topbar .timer { color: #87CEEB; font-size: 15px; font-family: monospace; font-weight: 600; }

.stats-bar {
  display: flex; gap: 0; border-bottom: 1px solid #333; background: #161616;
}
.stat-item {
  flex: 1; padding: 14px 24px; border-right: 1px solid #333; text-align: center;
}
.stat-item:last-child { border-right: none; }
.stat-item .num { font-size: 24px; font-weight: 700; color: #87CEEB; font-family: monospace; }
.stat-item .lbl { font-size: 11px; color: #777; text-transform: uppercase; margin-top: 2px; }
.stat-item .peak { font-size: 10px; color: #555; font-family: monospace; margin-top: 2px; }
.stat-item.green .num { color: #4ade80; }
.stat-item.red .num { color: #f87171; }

.main { display: flex; height: calc(100vh - 120px); }

.left { flex: 1; border-right: 1px solid #333; display: flex; flex-direction: column; }
.right { width: 340px; overflow-y: auto; }

.chart-section { flex: 1; padding: 16px 20px; border-bottom: 1px solid #282828; min-height: 0; }
.chart-header { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: 4px; }
.chart-header h3 { font-size: 12px; color: #777; text-transform: uppercase; font-weight: 500; }
.chart-header .legend { font-size: 10px; color: #555; }
.chart-header .legend span { margin-left: 12px; }
.chart-header .legend .dot { display: inline-block; width: 8px; height: 3px; border-radius: 1px; margin-right: 4px; vertical-align: middle; }
.chart-desc { font-size: 11px; color: #555; margin-bottom: 8px; }
canvas { width: 100% !important; height: 100% !important; display: block; }
.chart-wrap { height: calc(100% - 46px); }

.panel { padding: 16px 20px; border-bottom: 1px solid #282828; }
.panel h3 { font-size: 12px; color: #777; text-transform: uppercase; margin-bottom: 4px; font-weight: 500; }
.panel .desc { font-size: 11px; color: #555; margin-bottom: 10px; }

table { width: 100%; border-collapse: collapse; }
th { text-align: left; font-size: 11px; color: #666; text-transform: uppercase; padding: 6px 8px; border-bottom: 1px solid #282828; font-weight: 500; }
td { padding: 7px 8px; border-bottom: 1px solid #1e1e1e; font-family: monospace; font-size: 13px; }
tr:hover { background: #1a1a1a; }

.tag {
  display: inline-block; padding: 1px 8px; border-radius: 3px;
  font-size: 12px; font-family: monospace; font-weight: 600;
}
.tag-2 { background: #143326; color: #4ade80; }
.tag-4 { background: #332814; color: #fbbf24; }
.tag-5 { background: #331414; color: #f87171; }

.check-row { display: flex; justify-content: space-between; padding: 6px 0; border-bottom: 1px solid #1e1e1e; font-size: 13px; }
.check-row:last-child { border-bottom: none; }
.pass { color: #4ade80; }
.fail { color: #f87171; }
.mono { font-family: monospace; }
.dim { color: #555; font-size: 11px; }

.history-row { display: flex; justify-content: space-between; padding: 5px 0; border-bottom: 1px solid #1e1e1e; font-size: 12px; font-family: monospace; }
.history-row:last-child { border-bottom: none; }
.history-row .ts { color: #555; }
.history-row .val { color: #87CEEB; }
.history-row .spike { color: #fbbf24; }
</style>
</head>
<body>

<div class="topbar">
  <div class="topbar-left">
    <h1>kar98k</h1>
    <span class="info">Scenario: <b id="scenario">-</b></span>
    <span class="info">Preset: <b id="preset">-</b></span>
  </div>
  <div style="display:flex;align-items:center;gap:12px">
    <button id="startBtn" onclick="triggerStart()" style="background:#143326;color:#4ade80;border:1px solid #1e5e3e;padding:6px 18px;border-radius:4px;cursor:pointer;font-size:13px;font-weight:600;display:none">Start</button>
    <button id="stopBtn" onclick="triggerStop()" style="background:#331414;color:#f87171;border:1px solid #5e1e1e;padding:6px 18px;border-radius:4px;cursor:pointer;font-size:13px;font-weight:600;display:none">Stop</button>
    <span class="timer" id="elapsed">00:00</span>
  </div>
</div>

<div class="stats-bar">
  <div class="stat-item">
    <div class="num" id="rps">0</div><div class="lbl">RPS</div>
    <div class="peak" id="rpsPeak">peak: 0</div>
  </div>
  <div class="stat-item">
    <div class="num" id="total">0</div><div class="lbl">Requests</div>
    <div class="peak" id="reqRate">0 req/s avg</div>
  </div>
  <div class="stat-item" id="errBox">
    <div class="num" id="errRate">0%</div><div class="lbl">Failures</div>
    <div class="peak" id="errTotal">0 total</div>
  </div>
  <div class="stat-item">
    <div class="num" id="avg">0ms</div><div class="lbl">Avg Latency</div>
    <div class="peak" id="latPeak">peak: 0ms</div>
  </div>
  <div class="stat-item">
    <div class="num" id="p95">0ms</div><div class="lbl">P95</div>
    <div class="peak" id="p95Peak">peak: 0ms</div>
  </div>
  <div class="stat-item">
    <div class="num" id="vus">0</div><div class="lbl">Users</div>
    <div class="peak" id="vuPeak">peak: 0</div>
  </div>
</div>

<div class="main">
  <div class="left">
    <div class="chart-section" style="flex:1">
      <div class="chart-header">
        <h3>Requests per Second</h3>
        <div class="legend"><span><span class="dot" style="background:#87CEEB"></span>RPS</span></div>
      </div>
      <div class="chart-desc">Current throughput. Spikes appear as sharp peaks above the baseline.</div>
      <div class="chart-wrap"><canvas id="c1"></canvas></div>
    </div>
    <div class="chart-section" style="flex:1">
      <div class="chart-header">
        <h3>Response Times (ms)</h3>
        <div class="legend">
          <span><span class="dot" style="background:#87CEEB"></span>Avg</span>
          <span><span class="dot" style="background:#00BFFF"></span>P95</span>
          <span><span class="dot" style="background:#f87171"></span>P99</span>
        </div>
      </div>
      <div class="chart-desc">Server response time. P95 = 95% of requests are faster than this value.</div>
      <div class="chart-wrap"><canvas id="c2"></canvas></div>
    </div>
  </div>
  <div class="right">
    <div class="panel">
      <h3>RPS History</h3>
      <div class="desc">Last 10 seconds. Highlights when RPS jumps above 1.5x of average (spike detected).</div>
      <div id="history"><span class="dim">Collecting...</span></div>
    </div>
    <div class="panel">
      <h3>Checks</h3>
      <div class="desc">Assertions defined in your test script. Green = all passing.</div>
      <div id="checks"><span class="dim">Waiting...</span></div>
    </div>
    <div class="panel">
      <h3>Status Codes</h3>
      <div class="desc">HTTP response code distribution. 2xx = success, 4xx/5xx = errors.</div>
      <table>
        <thead><tr><th>Code</th><th style="text-align:right">Count</th></tr></thead>
        <tbody id="codes"><tr><td colspan="2" class="dim">Waiting...</td></tr></tbody>
      </table>
    </div>
    <div class="panel">
      <h3>Latency Summary</h3>
      <div class="desc">Avg = mean response time. P95/P99 = tail latency experienced by slowest requests.</div>
      <table>
        <thead><tr><th>Metric</th><th style="text-align:right">Value</th></tr></thead>
        <tbody>
          <tr><td>Avg</td><td style="text-align:right" class="mono" id="la">-</td></tr>
          <tr><td>P95</td><td style="text-align:right" class="mono" id="l95">-</td></tr>
          <tr><td>P99</td><td style="text-align:right" class="mono" id="l99">-</td></tr>
          <tr><td>Peak Avg</td><td style="text-align:right" class="mono" id="lPeak">-</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</div>

<script>
const N=180, rD=[], aD=[], pD=[], p9D=[];
let peakRPS=0, peakLat=0, peakP95=0, peakVU=0;
const hist=[];

function draw(cv, sets, cols, mx) {
  const r=cv.getBoundingClientRect(), d=2;
  cv.width=r.width*d; cv.height=r.height*d;
  const c=cv.getContext('2d'); c.scale(d,d);
  const W=r.width, H=r.height;
  c.clearRect(0,0,W,H);
  if(sets[0].length<2) return;

  const L=44, cw=W-L-8, ch=H-20-4;

  c.strokeStyle='#222'; c.lineWidth=1;
  c.font='10px -apple-system,sans-serif'; c.fillStyle='#555';
  for(let i=0;i<=4;i++){
    const y=4+ch*i/4;
    c.beginPath(); c.moveTo(L,y); c.lineTo(W-8,y); c.stroke();
    const v=mx*(4-i)/4;
    c.fillText(v>=1000?(v/1000).toFixed(1)+'k':v.toFixed(v<10?1:0), 2, y+3);
  }

  sets.forEach((data,di)=>{
    c.strokeStyle=cols[di]; c.lineWidth=di===0?2:1.5;
    c.beginPath();
    data.forEach((v,i)=>{
      const x=L+(i/(N-1))*cw, y=4+ch-Math.min(v/mx,1)*ch;
      i===0?c.moveTo(x,y):c.lineTo(x,y);
    });
    c.stroke();
    if(di===0){
      c.lineTo(L+((data.length-1)/(N-1))*cw, 4+ch);
      c.lineTo(L, 4+ch); c.closePath();
      c.fillStyle=cols[0]+'22'; c.fill();
    }
  });
}

function f(n){return n>=1e6?(n/1e6).toFixed(1)+'M':n>=1e3?(n/1e3).toFixed(1)+'K':Math.round(n).toString();}
function ms(s){const m=s*1000; return m>=1000?(m/1000).toFixed(2)+'s':m>=1?m.toFixed(1)+'ms':(m*1000).toFixed(0)+'us';}
function tm(s){const m=Math.floor(s/60),sec=Math.floor(s%60); return String(m).padStart(2,'0')+':'+String(sec).padStart(2,'0');}

function upd(s){
  // Update peaks
  if(s.rps>peakRPS) peakRPS=s.rps;
  if(s.avg_latency>peakLat) peakLat=s.avg_latency;
  if(s.p95_latency>peakP95) peakP95=s.p95_latency;
  if(s.active_vus>peakVU) peakVU=s.active_vus;

  document.getElementById('rps').textContent=Math.round(s.rps);
  document.getElementById('rpsPeak').textContent='peak: '+f(Math.round(peakRPS));
  document.getElementById('total').textContent=f(s.total_reqs);
  document.getElementById('reqRate').textContent=s.elapsed>0?Math.round(s.total_reqs/s.elapsed)+' req/s avg':'0 req/s avg';
  document.getElementById('avg').textContent=ms(s.avg_latency);
  document.getElementById('latPeak').textContent='peak: '+ms(peakLat);
  document.getElementById('p95').textContent=ms(s.p95_latency);
  document.getElementById('p95Peak').textContent='peak: '+ms(peakP95);
  document.getElementById('vus').textContent=s.active_vus;
  document.getElementById('vuPeak').textContent='peak: '+peakVU;
  document.getElementById('elapsed').textContent=tm(s.elapsed);
  document.getElementById('la').textContent=ms(s.avg_latency);
  document.getElementById('l95').textContent=ms(s.p95_latency);
  document.getElementById('l99').textContent=ms(s.p99_latency);
  document.getElementById('lPeak').textContent=ms(peakLat);

  const er=s.total_reqs>0?(s.total_errors/s.total_reqs*100):0;
  document.getElementById('errRate').textContent=er.toFixed(1)+'%';
  document.getElementById('errTotal').textContent=f(s.total_errors)+' total';
  const eb=document.getElementById('errBox');
  eb.className=er>5?'stat-item red':er>0?'stat-item':'stat-item green';

  // Chart data
  rD.push(s.rps); aD.push(s.avg_latency*1000); pD.push(s.p95_latency*1000); p9D.push(s.p99_latency*1000);
  if(rD.length>N){rD.shift();aD.shift();pD.shift();p9D.shift();}

  draw(document.getElementById('c1'),[rD],['#87CEEB'],Math.max(10,...rD)*1.1);
  draw(document.getElementById('c2'),[aD,pD,p9D],['#87CEEB','#00BFFF','#f87171'],Math.max(1,...p9D)*1.1);

  // RPS History (last 10)
  const avgRPS=rD.length>0?rD.reduce((a,b)=>a+b,0)/rD.length:0;
  const isSpike=s.rps>avgRPS*1.5&&avgRPS>0;
  hist.push({t:s.elapsed, rps:s.rps, lat:s.avg_latency, spike:isSpike});
  if(hist.length>10) hist.shift();

  document.getElementById('history').innerHTML=hist.slice().reverse().map(h=>{
    const cls=h.spike?'spike':'val';
    const tag=h.spike?' spike':'';
    return '<div class="history-row"><span class="ts">'+tm(h.t)+'</span><span class="'+cls+'">'+Math.round(h.rps)+' rps / '+ms(h.lat)+tag+'</span></div>';
  }).join('');

  // Checks
  if(s.checks&&s.checks.length){
    document.getElementById('checks').innerHTML=s.checks.map(c=>{
      const t=c.passed+c.failed, cls=c.failed>0?'fail':'pass', ic=c.failed>0?'✗':'✓';
      return '<div class="check-row"><span>'+ic+' '+c.name+'</span><span class="mono '+cls+'">'+c.rate.toFixed(0)+'% ('+f(c.passed)+'/'+f(t)+')</span></div>';
    }).join('');
  }

  // Status codes
  if(s.status_codes){
    const e=Object.entries(s.status_codes).sort((a,b)=>a[0]-b[0]);
    if(e.length){
      document.getElementById('codes').innerHTML=e.map(([code,cnt])=>{
        const c=parseInt(code), cls=c<300?'tag-2':c<500?'tag-4':'tag-5';
        return '<tr><td><span class="tag '+cls+'">'+code+'</span></td><td style="text-align:right" class="mono">'+f(cnt)+'</td></tr>';
      }).join('');
    }
  }
}

function setButtons(running){
  document.getElementById('startBtn').style.display=running?'none':'inline-block';
  document.getElementById('stopBtn').style.display=running?'inline-block':'none';
}
function triggerStart(){
  fetch('/api/start',{method:'POST'}).then(()=>setButtons(true));
}
function triggerStop(){
  fetch('/api/stop',{method:'POST'}).then(()=>setButtons(false));
}
fetch('/api/state').then(r=>r.json()).then(d=>setButtons(d.running));

const es=new EventSource('/events');
es.addEventListener('init',e=>{const d=JSON.parse(e.data);document.getElementById('scenario').textContent=d.scenario||'-';document.getElementById('preset').textContent=d.preset||'-';});
es.onmessage=e=>upd(JSON.parse(e.data));
</script>
</body>
</html>`

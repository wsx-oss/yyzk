const $ = (q)=>document.querySelector(q);
const api = (p, opt={})=>fetch('/api'+p, opt);

let cpuChart, memChart;
function initCharts(){
  const cpuCtx = document.getElementById('cpuChart');
  const memCtx = document.getElementById('memChart');
  const conf = (label)=>({type:'line',data:{labels:[],datasets:[{label,borderColor:'#0ea5e9',data:[],fill:false}]},options:{responsive:true,animation:false,scales:{x:{display:false},y:{beginAtZero:true,max:100}}}});
  try { cpuChart = new Chart(cpuCtx, conf('CPU %')); memChart = new Chart(memCtx, conf('Mem %')); } catch(e) { console.warn('Chart init failed', e); }
}

function connectMetrics(){
  const wsProto = location.protocol==='https:'?'wss':'ws';
  const ws = new WebSocket(wsProto+'://'+location.host+'/api/metrics/stream');
  ws.onmessage = (ev)=>{
    const m = JSON.parse(ev.data);
    $('#cpuVal').textContent = m.cpu_percent.toFixed(1)+'%';
    $('#memVal').textContent = m.mem_percent.toFixed(1)+'%';
    $('#loadVal').textContent = m.load1.toFixed(2);
    $('#diskVal').textContent = m.disk_used_percent.toFixed(1)+'%';
    $('#txVal').textContent = (m.net_bytes_sent/1024/1024).toFixed(2)+' MB';
    $('#rxVal').textContent = (m.net_bytes_recv/1024/1024).toFixed(2)+' MB';
    if(cpuChart && memChart){
      const t = new Date(m.ts*1000).toLocaleTimeString();
      cpuChart.data.labels.push(t); cpuChart.data.datasets[0].data.push(m.cpu_percent);
      memChart.data.labels.push(t); memChart.data.datasets[0].data.push(m.mem_percent);
      if(cpuChart.data.labels.length>60){cpuChart.data.labels.shift();cpuChart.data.datasets[0].data.shift();}
      if(memChart.data.labels.length>60){memChart.data.labels.shift();memChart.data.datasets[0].data.shift();}
      cpuChart.update(); memChart.update();
    }
  };
}

let mediaStream, mediaRecorder, chunks=[], recStart=0;
async function startRec(){
  mediaStream = await navigator.mediaDevices.getUserMedia({audio:true});
  mediaRecorder = new MediaRecorder(mediaStream, {mimeType:'audio/webm'});
  chunks=[]; recStart = Date.now();
  mediaRecorder.ondataavailable = e=>{ if(e.data.size>0) chunks.push(e.data); };
  mediaRecorder.onstop = async ()=>{
    const blob = new Blob(chunks, {type:'audio/webm'});
    const file = new File([blob], 'recording.webm', {type:'audio/webm'});
    const form = new FormData();
    form.append('file', file);
    const dur = Math.max(0, (Date.now()-recStart)/1000);
    form.append('duration', dur.toFixed(2));
    await api('/audio/upload',{method:'POST', body:form});
    stopTracks();
    loadRecordings();
  };
  mediaRecorder.start(250);
  $('#btnRec').disabled=true; $('#btnStop').disabled=false;
}
function stopTracks(){ mediaStream && mediaStream.getTracks().forEach(t=>t.stop()); }
function stopRec(){ mediaRecorder && mediaRecorder.stop(); $('#btnRec').disabled=false; $('#btnStop').disabled=true; }

async function loadRecordings(){
  const r = await api('/audio/list'); const j = await r.json();
  const ul = $('#recList'); ul.innerHTML='';
  (j.items||[]).forEach(it=>{
    const li = document.createElement('li');
    const a = document.createElement('a'); a.textContent = `${it.id} - ${it.filename} (${(it.size/1024).toFixed(1)} KB)`; a.href = `/api/audio/download/${it.id}`; a.target='_blank';
    li.appendChild(a); ul.appendChild(li);
  });
}

function openVNC(){
  const target = $('#vncTarget').value || '127.0.0.1:5900';
  window.open(`/app/vnc.html?target=${encodeURIComponent(target)}`, '_blank');
}

async function initCamera(){
  try{ const s = await navigator.mediaDevices.getUserMedia({video:true}); $('#cam').srcObject = s; }catch(e){ console.warn(e); }
}

async function refreshAlerts(){
  const r = await api('/alerts/list'); const j = await r.json();
  const ul = $('#alertList'); ul.innerHTML='';
  (j.items||[]).forEach(it=>{ const li=document.createElement('li'); li.textContent = `[${it.severity}] ${it.message} @${it.created_at}`; ul.appendChild(li); });
}
async function refreshLogs(){
  const r = await api('/logs/list'); const j = await r.json();
  const ul = $('#logList'); ul.innerHTML='';
  (j.items||[]).forEach(it=>{ const li=document.createElement('li'); li.textContent = `[${it.level}] ${it.message} @${it.created_at}`; ul.appendChild(li); });
}
async function checkUpdate(){ const r=await api('/updates/check'); $('#updateInfo').textContent = JSON.stringify(await r.json(),null,2); }
async function doSync(){ await api('/sync/status',{method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({status:'syncing', message:'manual'})}); const r=await api('/sync/status'); $('#syncInfo').textContent = JSON.stringify(await r.json(),null,2); }

window.addEventListener('DOMContentLoaded', ()=>{
  initCharts(); connectMetrics(); initCamera(); loadRecordings(); refreshAlerts(); refreshLogs();
  $('#btnRec').addEventListener('click', startRec);
  $('#btnStop').addEventListener('click', stopRec);
  $('#btnVNC').addEventListener('click', openVNC);
  $('#btnRefreshAlerts').addEventListener('click', refreshAlerts);
  $('#btnRefreshLogs').addEventListener('click', refreshLogs);
  $('#btnCheckUpdate').addEventListener('click', checkUpdate);
  $('#btnSync').addEventListener('click', doSync);
});

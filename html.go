package main

// indexHTML is the mobile-first Tailwind UI, served at GET /.
// No login screen: the page silently opens a guest session on load.
// NOTE: this is a Go raw string (backtick-delimited) — do NOT use backticks
// anywhere inside (JS uses string concatenation, not template literals).
const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<meta name="theme-color" content="#0b1120"/>
<title>TaskBoard · quikdb-frame</title>
<script src="https://cdn.tailwindcss.com"></script>
<style>
  body{background:radial-gradient(1200px 600px at 10% -10%,#1e293b 0%,#0b1120 55%)}
  ::-webkit-scrollbar{width:8px;height:8px}
  ::-webkit-scrollbar-thumb{background:#334155;border-radius:8px}
  .card{transition:transform .12s ease, box-shadow .12s ease}
  .card:hover{transform:translateY(-2px);box-shadow:0 8px 24px rgba(0,0,0,.35)}
  .fade{animation:f .25s ease}
  @keyframes f{from{opacity:0;transform:translateY(4px)}to{opacity:1;transform:none}}
</style>
</head>
<body class="text-slate-100 min-h-screen">
<div class="max-w-6xl mx-auto px-4 pb-10">

  <!-- nav -->
  <header class="flex items-center justify-between py-5">
    <div class="flex items-center gap-3">
      <div class="h-10 w-10 rounded-xl bg-gradient-to-br from-emerald-400 to-cyan-500 grid place-items-center text-xl shadow-lg">📋</div>
      <div>
        <h1 class="text-lg font-bold leading-tight">TaskBoard</h1>
        <p class="text-[11px] text-slate-400 -mt-0.5">powered by <span class="text-emerald-400 font-medium">quikdb-frame</span></p>
      </div>
    </div>
    <div class="flex items-center gap-2 text-xs">
      <span id="who" class="hidden sm:inline px-2.5 py-1 rounded-full bg-slate-800/70 text-slate-300"></span>
      <span id="ws" class="px-2.5 py-1 rounded-full bg-slate-800/70 flex items-center gap-1.5">
        <span id="dot" class="h-2 w-2 rounded-full bg-amber-400"></span><span id="wsText">connecting</span>
      </span>
    </div>
  </header>

  <!-- stats -->
  <section id="stats" class="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-6"></section>

  <!-- add task -->
  <section class="bg-slate-800/60 backdrop-blur rounded-2xl p-3 sm:p-4 mb-6 ring-1 ring-white/5">
    <div class="flex flex-col sm:flex-row gap-2">
      <input id="title" placeholder="What needs doing?" class="flex-1 bg-slate-900/70 rounded-xl px-4 py-3 text-sm outline-none focus:ring-2 focus:ring-emerald-500/60" onkeydown="if(event.key==='Enter')addTask()"/>
      <select id="prio" class="bg-slate-900/70 rounded-xl px-3 py-3 text-sm outline-none focus:ring-2 focus:ring-emerald-500/60">
        <option value="low">Low</option>
        <option value="medium" selected>Medium</option>
        <option value="high">High</option>
      </select>
      <button onclick="addTask()" class="bg-gradient-to-r from-emerald-500 to-cyan-500 hover:opacity-90 rounded-xl px-6 py-3 text-sm font-semibold shadow-lg">+ Add task</button>
    </div>
  </section>

  <!-- kanban -->
  <section class="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
    <div data-col="todo"></div>
    <div data-col="doing"></div>
    <div data-col="done"></div>
  </section>

  <!-- chat -->
  <section class="bg-slate-800/60 backdrop-blur rounded-2xl p-4 ring-1 ring-white/5">
    <div class="flex items-center gap-2 mb-3">
      <span class="text-sm font-semibold text-slate-200">💬 Live chat</span>
      <span class="text-[10px] text-slate-500">native WebSocket</span>
    </div>
    <ul id="chat" class="space-y-1.5 text-sm mb-3 max-h-48 overflow-y-auto pr-1"></ul>
    <div class="flex gap-2">
      <input id="msg" placeholder="Message the team…" class="flex-1 bg-slate-900/70 rounded-xl px-4 py-2.5 text-sm outline-none focus:ring-2 focus:ring-cyan-500/60" onkeydown="if(event.key==='Enter')sendChat()"/>
      <button onclick="sendChat()" class="bg-cyan-600 hover:bg-cyan-500 rounded-xl px-5 text-sm font-medium">Send</button>
    </div>
  </section>
</div>

<script>
var token = "";
var COLS = {todo:{name:"To Do",accent:"slate"}, doing:{name:"In Progress",accent:"amber"}, done:{name:"Done",accent:"emerald"}};
var PRIO = {high:"bg-rose-500/20 text-rose-300", medium:"bg-amber-500/20 text-amber-300", low:"bg-emerald-500/20 text-emerald-300"};

function api(p, opt){ opt = opt || {}; opt.headers = Object.assign({"Content-Type":"application/json","Authorization":"Bearer "+token}, opt.headers||{}); return fetch(p, opt); }
function esc(s){ return (s||"").replace(/[&<>"]/g, function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c];}); }

async function boot(){
  var r = await fetch("/api/auth/guest");
  var d = await r.json();
  token = d.token;
  document.getElementById("who").textContent = "👤 " + d.user.name;
  connect();
  refresh();
  setInterval(refresh, 8000);
}

async function refresh(){
  if(!token) return;
  var tasks = await (await api("/api/tasks")).json();
  var groups = {todo:[],doing:[],done:[]};
  tasks.forEach(function(t){ (groups[t.status]||groups.todo).push(t); });
  Object.keys(COLS).forEach(function(k){
    var col = document.querySelector('[data-col="'+k+'"]');
    var items = groups[k];
    var cards = items.map(cardHTML).join("") || '<p class="text-xs text-slate-500 py-6 text-center">Nothing here</p>';
    col.innerHTML =
      '<div class="flex items-center justify-between mb-3 px-1">'+
        '<h2 class="text-sm font-semibold text-slate-200">'+COLS[k].name+'</h2>'+
        '<span class="text-xs px-2 py-0.5 rounded-full bg-slate-800 text-slate-400">'+items.length+'</span>'+
      '</div>'+
      '<div class="space-y-3 min-h-[40px]">'+cards+'</div>';
  });

  var a = await (await api("/api/analytics")).json();
  document.getElementById("stats").innerHTML =
    stat("Total", a.totalTasks, "📦") + stat("To Do", a.tasksByStatus.todo, "🗒️") +
    stat("In Progress", a.tasksByStatus.doing, "⚡") + stat("Done", a.tasksByStatus.done, "✅");

  var chat = await (await api("/api/chat")).json();
  var cl = document.getElementById("chat");
  cl.innerHTML = chat.slice(-30).map(function(m){
    return '<li class="fade"><b class="text-cyan-400">'+esc(m.user)+':</b> <span class="text-slate-200">'+esc(m.text)+'</span></li>';
  }).join("");
  cl.scrollTop = cl.scrollHeight;
}

function stat(label, val, icon){
  return '<div class="card bg-slate-800/60 ring-1 ring-white/5 rounded-2xl p-4">'+
    '<div class="text-2xl">'+icon+'</div>'+
    '<div class="text-3xl font-bold mt-1">'+(val||0)+'</div>'+
    '<div class="text-xs text-slate-400">'+label+'</div></div>';
}

function cardHTML(t){
  var canBack = t.status!=="todo", canFwd = t.status!=="done";
  return '<div class="card fade bg-slate-800 rounded-xl p-3 ring-1 ring-white/5">'+
    '<div class="flex items-start justify-between gap-2">'+
      '<p class="text-sm font-medium leading-snug">'+esc(t.title)+'</p>'+
      '<button onclick="del(\''+t.id+'\')" class="text-slate-500 hover:text-rose-400 text-sm">✕</button>'+
    '</div>'+
    (t.description?'<p class="text-xs text-slate-400 mt-1">'+esc(t.description)+'</p>':'')+
    '<div class="flex items-center justify-between mt-3">'+
      '<span class="text-[10px] uppercase tracking-wide px-2 py-0.5 rounded-full '+(PRIO[t.priority]||PRIO.medium)+'">'+esc(t.priority)+'</span>'+
      '<div class="flex gap-1">'+
        (canBack?'<button onclick="move(\''+t.id+'\',-1)" class="h-7 w-7 rounded-lg bg-slate-700 hover:bg-slate-600 text-xs">◀</button>':'')+
        (canFwd?'<button onclick="move(\''+t.id+'\',1)" class="h-7 w-7 rounded-lg bg-slate-700 hover:bg-slate-600 text-xs">▶</button>':'')+
      '</div>'+
    '</div></div>';
}

var ORDER=["todo","doing","done"];
async function addTask(){
  var t=document.getElementById("title"), p=document.getElementById("prio");
  if(!t.value.trim()) return;
  await api("/api/tasks",{method:"POST",body:JSON.stringify({title:t.value.trim(),priority:p.value})});
  t.value=""; refresh();
}
async function move(id,dir){
  var cards = await (await api("/api/tasks")).json();
  var t = cards.find(function(x){return x.id===id;}); if(!t) return;
  var i = ORDER.indexOf(t.status)+dir; if(i<0||i>2) return;
  await api("/api/tasks/"+id,{method:"PUT",body:JSON.stringify({status:ORDER[i]})}); refresh();
}
async function del(id){ await api("/api/tasks/"+id,{method:"DELETE"}); refresh(); }

function sendChat(){
  var m=document.getElementById("msg"); if(!m.value.trim()) return;
  if(ws && ws.readyState===1){ ws.send(JSON.stringify({text:m.value.trim(),user:"Guest"})); }
  else { api("/api/chat",{method:"POST",body:JSON.stringify({text:m.value.trim()})}).then(refresh); }
  m.value="";
}

var ws;
function connect(){
  var proto = location.protocol==="https:"?"wss":"ws";
  ws = new WebSocket(proto+"://"+location.host+"/ws");
  ws.onopen = function(){ setWs(true); };
  ws.onclose = function(){ setWs(false); setTimeout(connect,1500); };
  ws.onmessage = function(e){ try{ var m=JSON.parse(e.data); if(m.event==="chat.message"||(m.event&&m.event.indexOf("task.")===0)) refresh(); }catch(_){} };
}
function setWs(on){
  document.getElementById("dot").className = "h-2 w-2 rounded-full "+(on?"bg-emerald-400 animate-pulse":"bg-rose-400");
  document.getElementById("wsText").textContent = on?"live":"offline";
}

boot();
</script>
</body>
</html>`

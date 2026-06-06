package main

// indexHTML is the mobile-first Tailwind UI, served at GET /.
// Tailwind via CDN keeps the binary tiny while staying responsive.
const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<meta name="theme-color" content="#0f172a"/>
<title>TaskBoard · quikdb-frame</title>
<script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-slate-900 text-slate-100 min-h-screen">
<div class="max-w-md mx-auto p-4 sm:max-w-3xl">
  <header class="flex items-center justify-between py-4">
    <h1 class="text-xl font-bold">📋 TaskBoard <span class="text-xs text-emerald-400 align-top">quikdb-frame</span></h1>
    <span id="ws" class="text-xs px-2 py-1 rounded bg-slate-700">ws: …</span>
  </header>

  <section id="auth" class="bg-slate-800 rounded-xl p-4 mb-4">
    <div class="flex gap-2 mb-3 text-sm">
      <input id="email" placeholder="email" value="admin@taskboard.dev" class="flex-1 bg-slate-700 rounded px-3 py-2"/>
      <input id="pw" type="password" placeholder="password" value="admin123" class="flex-1 bg-slate-700 rounded px-3 py-2"/>
    </div>
    <div class="flex gap-2">
      <button onclick="login()" class="flex-1 bg-emerald-600 hover:bg-emerald-500 rounded py-2 text-sm font-medium">Login</button>
      <button onclick="register()" class="flex-1 bg-slate-600 hover:bg-slate-500 rounded py-2 text-sm">Register</button>
    </div>
    <p id="who" class="text-xs text-slate-400 mt-2"></p>
  </section>

  <section class="grid grid-cols-3 gap-2 mb-4" id="stats"></section>

  <section class="bg-slate-800 rounded-xl p-4 mb-4">
    <div class="flex gap-2 mb-3">
      <input id="title" placeholder="New task…" class="flex-1 bg-slate-700 rounded px-3 py-2 text-sm"/>
      <button onclick="addTask()" class="bg-emerald-600 hover:bg-emerald-500 rounded px-4 text-sm">Add</button>
    </div>
    <ul id="tasks" class="space-y-2"></ul>
  </section>

  <section class="bg-slate-800 rounded-xl p-4">
    <h2 class="text-sm font-semibold mb-2">💬 Live Chat (native WebSocket)</h2>
    <ul id="chat" class="space-y-1 text-sm mb-2 max-h-40 overflow-y-auto"></ul>
    <div class="flex gap-2">
      <input id="msg" placeholder="Message…" class="flex-1 bg-slate-700 rounded px-3 py-2 text-sm" onkeydown="if(event.key==='Enter')sendChat()"/>
      <button onclick="sendChat()" class="bg-emerald-600 hover:bg-emerald-500 rounded px-4 text-sm">Send</button>
    </div>
  </section>
</div>

<script>
let token = localStorage.getItem("tb_token") || "";
const api = (p, opt={}) => fetch(p, {...opt, headers:{"Content-Type":"application/json","Authorization":"Bearer "+token, ...(opt.headers||{})}});

async function login(){ const r = await fetch("/api/auth/login",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({email:email.value,password:pw.value})}); auth(await r.json()); }
async function register(){ const r = await fetch("/api/auth/register",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({name:email.value.split("@")[0],email:email.value,password:pw.value})}); auth(await r.json()); }
function auth(d){ if(!d.token){who.textContent=d.error||"failed";return;} token=d.token; localStorage.setItem("tb_token",token); who.textContent="Signed in as "+d.user.name+" ("+d.user.role+")"; refresh(); }

async function refresh(){
  if(!token) return;
  const tasks = await (await api("/api/tasks")).json();
  document.getElementById("tasks").innerHTML = tasks.map(t=>
    '<li class="flex items-center justify-between bg-slate-700 rounded px-3 py-2 text-sm">'+
    '<span><b>'+t.title+'</b> <em class="text-xs text-slate-400">'+t.status+'</em></span>'+
    '<span class="flex gap-2"><button onclick="cycle(\''+t.id+'\',\''+t.status+'\')" class="text-emerald-400">↻</button>'+
    '<button onclick="del(\''+t.id+'\')" class="text-rose-400">✕</button></span></li>').join("");
  const a = await (await api("/api/analytics")).json();
  document.getElementById("stats").innerHTML =
    card("Users",a.totalUsers)+card("Tasks",a.totalTasks)+card("Done",a.tasksByStatus.done);
  const chat = await (await api("/api/chat")).json();
  renderChat(chat);
}
const card=(k,v)=>'<div class="bg-slate-800 rounded-xl p-3 text-center"><div class="text-2xl font-bold">'+v+'</div><div class="text-xs text-slate-400">'+k+'</div></div>';
async function addTask(){ if(!title.value)return; await api("/api/tasks",{method:"POST",body:JSON.stringify({title:title.value})}); title.value=""; }
async function cycle(id,s){ const n={todo:"doing",doing:"done",done:"todo"}[s]; await api("/api/tasks/"+id,{method:"PUT",body:JSON.stringify({status:n})}); }
async function del(id){ await api("/api/tasks/"+id,{method:"DELETE"}); refresh(); }

function renderChat(c){ document.getElementById("chat").innerHTML=c.map(m=>'<li><b class="text-emerald-400">'+m.user+':</b> '+m.text+'</li>').join(""); }
function sendChat(){ if(!msg.value)return; if(ws&&ws.readyState===1){ws.send(JSON.stringify({text:msg.value,user:"me"}));} else {api("/api/chat",{method:"POST",body:JSON.stringify({text:msg.value})}).then(refresh);} msg.value=""; }

let ws;
function connect(){
  const proto = location.protocol==="https:"?"wss":"ws";
  ws = new WebSocket(proto+"://"+location.host+"/ws");
  ws.onopen=()=>document.getElementById("ws").textContent="ws: live";
  ws.onclose=()=>{document.getElementById("ws").textContent="ws: off"; setTimeout(connect,1500);};
  ws.onmessage=(e)=>{ const m=JSON.parse(e.data); if(m.event==="chat.message"||m.event&&m.event.startsWith("task.")) refresh(); };
}
connect();
if(token) refresh();
</script>
</body>
</html>`

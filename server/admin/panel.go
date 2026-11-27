package admin

// panelHTML is the admin panel. It keeps the admin key in sessionStorage for the
// browser session only, and sends it in the Authorization header, the same way
// the API expects it. All values from the server are written as text, never as
// HTML, so a domain name cannot inject markup.
const panelHTML = `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Lipstick · panel</title>
<style>
  body { font: 14px/1.4 system-ui, sans-serif; margin: 24px; color: #1d1d1f; background: #fafafa; }
  h1 { font-size: 20px; margin: 0 0 4px; }
  .sub { color: #6e6e73; margin-bottom: 16px; }
  table { border-collapse: collapse; width: 100%; background: #fff; }
  th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid #e5e5ea; }
  th { font-weight: 600; color: #6e6e73; font-size: 12px; text-transform: uppercase; letter-spacing: .03em; }
  td.num { text-align: right; font-variant-numeric: tabular-nums; }
  .dot { display: inline-block; width: 9px; height: 9px; border-radius: 50%; margin-right: 6px; vertical-align: middle; }
  .active { background: #34c759; } .idle { background: #c7c7cc; } .unresponsive { background: #ff3b30; }
  .muted { color: #6e6e73; }
  #err { color: #ff3b30; margin: 12px 0; min-height: 1em; }
  button { font: inherit; padding: 4px 10px; }
</style>
</head>
<body>
<h1>Lipstick</h1>
<div class="sub">Dominios, conexiones y tráfico · actualiza cada 5 s · <button id="logout">Cambiar clave</button></div>
<div id="err"></div>
<table>
  <thead><tr>
    <th>Dominio</th><th>Estado</th><th class="num">Clientes</th><th class="num">Sesiones activas</th>
    <th class="num">Visitantes en espera</th><th class="num">Tráfico desde arranque</th>
    <th class="num">Tráfico hoy</th><th>Puertos de intranet</th>
  </tr></thead>
  <tbody id="rows"><tr><td colspan="8" class="muted">Cargando…</td></tr></tbody>
</table>
<h2 style="font-size:16px;margin:24px 0 8px">Peticiones recientes</h2>
<table>
  <thead><tr>
    <th>Hora</th><th>Dominio</th><th>Método</th><th>Ruta</th><th class="num">Estado</th>
    <th class="num">Duración</th><th class="num">Respuesta</th>
  </tr></thead>
  <tbody id="reqs"><tr><td colspan="7" class="muted">Sin peticiones todavía.</td></tr></tbody>
</table>
<script>
(function () {
  var key = sessionStorage.getItem("lipstickAdminKey") || "";
  var rows = document.getElementById("rows");
  var err = document.getElementById("err");

  function ask() {
    key = prompt("Clave de administración") || "";
    sessionStorage.setItem("lipstickAdminKey", key);
  }
  function fmt(n) {
    if (n === undefined || n === null) return "";
    var units = ["B", "KiB", "MiB", "GiB", "TiB"], i = 0, v = Number(n);
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return (i === 0 ? v.toFixed(0) : v.toFixed(1)) + " " + units[i];
  }
  function cell(text, cls) {
    var td = document.createElement("td");
    if (cls) td.className = cls;
    td.textContent = text;
    return td;
  }
  function todayBytes(name) {
    var day = new Date().toISOString().slice(0, 10);
    return fetch("/domains/" + encodeURIComponent(name) + "/traffic?from=" + day + "&to=" + day,
      { headers: { Authorization: key } })
      .then(function (r) { return r.ok ? r.json() : { consumptions: [] }; })
      .then(function (j) {
        return (j.consumptions || []).reduce(function (s, c) { return s + (c.BytesUsed || 0); }, 0);
      })
      .catch(function () { return null; });
  }
  function render(list) {
    rows.textContent = "";
    if (!list.length) {
      var tr = document.createElement("tr");
      tr.appendChild(cell("Ningún dominio tiene un hub activo.", "muted"));
      tr.firstChild.colSpan = 8;
      rows.appendChild(tr);
      return;
    }
    list.forEach(function (d) {
      var tr = document.createElement("tr");
      var dom = cell(d.domain);
      tr.appendChild(dom);
      var st = document.createElement("td");
      var dot = document.createElement("span");
      dot.className = "dot " + d.status;
      st.appendChild(dot);
      st.appendChild(document.createTextNode(d.status));
      tr.appendChild(st);
      tr.appendChild(cell(String(d.clients), "num"));
      tr.appendChild(cell(String(d.activeSessions), "num"));
      tr.appendChild(cell(String(d.pendingVisitors), "num"));
      tr.appendChild(cell(fmt(d.bytesSinceStart), "num"));
      var today = cell("…", "num");
      tr.appendChild(today);
      tr.appendChild(cell((d.intranetPorts || []).join(", ") || "—"));
      rows.appendChild(tr);
      todayBytes(d.domain).then(function (b) { today.textContent = b === null ? "—" : fmt(b); });
    });
  }
  function loadRequests() {
    var body = document.getElementById("reqs");
    fetch("/requests?limit=50", { headers: { Authorization: key } })
      .then(function (r) { if (!r.ok) throw new Error(); return r.json(); })
      .then(function (list) {
        body.textContent = "";
        if (!list.length) {
          var empty = document.createElement("tr");
          empty.appendChild(cell("Sin peticiones todavía.", "muted"));
          empty.firstChild.colSpan = 7;
          body.appendChild(empty);
          return;
        }
        list.forEach(function (q) {
          var tr = document.createElement("tr");
          tr.appendChild(cell(new Date(q.time).toLocaleTimeString()));
          tr.appendChild(cell(q.domain));
          tr.appendChild(cell(q.method));
          tr.appendChild(cell(q.path));
          tr.appendChild(cell(String(q.status), "num"));
          tr.appendChild(cell(q.durationMs + " ms", "num"));
          tr.appendChild(cell(q.responseBytes < 0 ? "—" : fmt(q.responseBytes), "num"));
          body.appendChild(tr);
        });
      })
      .catch(function () {});
  }
  function load() {
    if (!key) { ask(); if (!key) { err.textContent = "Se necesita la clave para ver el panel."; return; } }
    fetch("/stats", { headers: { Authorization: key } })
      .then(function (r) {
        if (r.status === 401) { sessionStorage.removeItem("lipstickAdminKey"); key = ""; throw new Error("La clave no es válida."); }
        if (!r.ok) throw new Error("Error " + r.status + " al leer el estado.");
        return r.json();
      })
      .then(function (list) { err.textContent = ""; render(list); loadRequests(); })
      .catch(function (e) { err.textContent = e.message; });
  }
  document.getElementById("logout").addEventListener("click", function () {
    sessionStorage.removeItem("lipstickAdminKey"); key = ""; ask(); load();
  });
  load();
  setInterval(load, 5000);
})();
</script>
</body>
</html>
`

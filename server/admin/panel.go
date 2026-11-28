package admin

// panelHTML is the admin panel. It reads the read-only admin endpoints, which
// need no login: the admin port listens on 127.0.0.1 only. Changing anything
// requires a session token from /login, and the panel never asks for one. All values from the
// server are written as text, never as HTML, so a domain name cannot inject markup.
const panelHTML = `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Lipstick · panel</title>
<link rel="stylesheet" href="/admin/panel.css">
</head>
<body>
<h1>Lipstick</h1>
<div class="sub">Dominios, conexiones y tráfico · actualiza cada 5 s</div>
<div class="session"><button id="logout" type="button">Cerrar sesión</button></div>
<div id="err"></div>
<table>
  <thead><tr>
    <th>Dominio</th><th>Estado</th><th class="num">Clientes</th><th class="num">Sesiones activas</th>
    <th class="num">Visitantes en espera</th><th class="num">Tráfico desde arranque</th>
    <th class="num">Tráfico hoy</th><th>Puertos de intranet</th>
  </tr></thead>
  <tbody id="rows"><tr><td colspan="8" class="muted">Cargando…</td></tr></tbody>
</table>
<h2 class="section-title">Peticiones recientes</h2>
<div class="filters">
  <input id="fdom" list="domains" placeholder="Dominio (todos)" aria-label="Filtrar por dominio">
  <datalist id="domains"></datalist>
  <input id="fport" type="number" min="1" max="65535" placeholder="Puerto (todos)" aria-label="Filtrar por puerto">
  <span class="muted">5050 = proxy · otros puertos = intranet</span>
</div>
<table>
  <thead><tr>
    <th>Hora</th><th>Dominio</th><th class="num">Puerto</th><th>Método</th><th>Ruta</th><th class="num">Estado</th>
    <th class="num">Duración</th><th class="num">Respuesta</th>
  </tr></thead>
  <tbody id="reqs"><tr><td colspan="8" class="muted">Sin peticiones todavía.</td></tr></tbody>
</table>
<script src="/admin/panel.js"></script>
</body>
</html>
`

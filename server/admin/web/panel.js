// Every request is same-origin and carries the session cookie. When the session
// ends, the server answers 401 and the page returns to the login form.
(function () {
  var nativeFetch = window.fetch;
  window.fetch = function () {
    return nativeFetch.apply(this, arguments).then(function (response) {
      if (response.status === 401) { window.location.reload(); }
      return response;
    });
  };
  document.getElementById("logout").addEventListener("click", function () {
    nativeFetch("/logout", { method: "POST", headers: { "X-Lipstick-CSRF": "1" } })
      .finally(function () { window.location.reload(); });
  });
})();
(function () {
  var rows = document.getElementById("rows");
  var err = document.getElementById("err");

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
    return fetch("/domains/" + encodeURIComponent(name) + "/traffic?from=" + day + "&to=" + day)
      .then(function (r) { return r.ok ? r.json() : { consumptions: [] }; })
      .then(function (j) {
        return (j.consumptions || []).reduce(function (s, c) { return s + (c.BytesUsed || 0); }, 0);
      })
      .catch(function () { return null; });
  }
  function render(list) {
    list = list || [];
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
  // Offers the known domains as suggestions for the domain filter.
  function fillDomains(list) {
    var dl = document.getElementById("domains");
    dl.textContent = "";
    list.forEach(function (d) {
      var o = document.createElement("option");
      o.value = d.domain;
      dl.appendChild(o);
    });
  }
  document.getElementById("fdom").addEventListener("change", loadRequests);
  document.getElementById("fport").addEventListener("change", loadRequests);
  function loadRequests() {
    var body = document.getElementById("reqs");
    var dom = document.getElementById("fdom").value.trim();
    var port = document.getElementById("fport").value.trim();
    var url = "/requests?limit=50";
    if (dom) url += "&domain=" + encodeURIComponent(dom);
    if (port) url += "&port=" + encodeURIComponent(port);
    fetch(url)
      .then(function (r) { if (!r.ok) throw new Error(); return r.json(); })
      .then(function (list) {
        body.textContent = "";
        list = list || [];
        if (!list.length) {
          var empty = document.createElement("tr");
          empty.appendChild(cell("Sin peticiones todavía.", "muted"));
          empty.firstChild.colSpan = 8;
          body.appendChild(empty);
          return;
        }
        list.forEach(function (q) {
          var tr = document.createElement("tr");
          tr.appendChild(cell(new Date(q.time).toLocaleTimeString()));
          tr.appendChild(cell(q.domain));
          tr.appendChild(cell(q.port ? String(q.port) : "—", "num"));
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
    fetch("/stats")
      .then(function (r) {
        if (!r.ok) throw new Error("Error " + r.status + " al leer el estado.");
        return r.json();
      })
      .then(function (list) { err.textContent = ""; render(list); fillDomains(list); loadRequests(); })
      .catch(function (e) { err.textContent = e.message; });
  }
  load();
  setInterval(load, 5000);
})();

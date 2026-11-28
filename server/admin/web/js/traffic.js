// Traffic and requests: the request history with filters, and what it says about health.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;

  var METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
  var CLASSES = [["", "Todos los estados"], ["2", "2xx correctas"], ["3", "3xx redirecciones"], ["4", "4xx errores del cliente"], ["5", "5xx errores del servidor"]];

  function select(options, name, label) {
    var s = h("select", { class: "input", name: name, "aria-label": label });
    options.forEach(function (o) { s.appendChild(h("option", { value: o[0], text: o[1] })); });
    return s;
  }

  // percentile of a sorted array of numbers.
  function percentile(sorted, p) {
    if (sorted.length === 0) { return 0; }
    var idx = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
    return sorted[Math.max(0, idx)];
  }

  function summary(rows) {
    var durations = rows.map(function (r) { return r.durationMs; }).sort(function (a, b) { return a - b; });
    var errors = rows.filter(function (r) { return r.status >= 400; }).length;
    var bytes = rows.reduce(function (s, r) { return s + (r.responseBytes || 0); }, 0);
    var avg = durations.length ? Math.round(durations.reduce(function (s, d) { return s + d; }, 0) / durations.length) : 0;
    return [
      LP.metricCard("Peticiones", LP.fmt.num(rows.length), "según los filtros"),
      LP.metricCard("Errores", rows.length ? Math.round((errors / rows.length) * 100) + " %" : "—", errors + " con estado 4xx o 5xx"),
      LP.metricCard("Duración", LP.fmt.duration(avg), "media · p95 " + LP.fmt.duration(percentile(durations, 95))),
      LP.metricCard("Respuestas", LP.fmt.bytes(bytes), "tamaño total devuelto")
    ];
  }

  function render(root) {
    LP.clear(root);
    root.appendChild(h("div", { class: "page-head" }, [
      h("div", null, [h("h1", { text: "Tráfico y peticiones" }), h("p", { text: "Peticiones HTTP terminadas. Sin cabeceras ni cuerpos." })])
    ]));

    LP.api("GET", "/domains").then(function (res) {
      var domains = res.ok ? res.data : [];
      var domainSelect = select([["", "Todos los dominios"]].concat(domains.map(function (d) { return [d.name, d.name]; })), "domain", "Dominio");
      var port = h("input", { class: "input", name: "port", type: "number", min: "1", max: "65535", placeholder: "Puerto", "aria-label": "Puerto" });
      var method = select([["", "Todos los métodos"]].concat(METHODS.map(function (m) { return [m, m]; })), "method", "Método");
      var klass = select(CLASSES, "class", "Estado HTTP");
      var limit = select([["100", "Últimas 100"], ["300", "Últimas 300"], ["500", "Últimas 500"]], "limit", "Cantidad");
      var from = h("input", { class: "input", type: "date", name: "from", "aria-label": "Desde" });
      var to = h("input", { class: "input", type: "date", name: "to", "aria-label": "Hasta" });
      var apply = h("button", { class: "btn btn-primary", type: "submit", text: "Aplicar" });
      var filters = h("form", { class: "card mb-4 grid gap-3 grid-cols-[repeat(auto-fit,minmax(170px,1fr))] items-end" }, [
        LP.field("Dominio", domainSelect).node,
        LP.field("Puerto", port, "5050 es el proxy").node,
        LP.field("Método", method).node,
        LP.field("Estado", klass).node,
        LP.field("Cantidad", limit).node,
        LP.field("Desde", from, "Registro de 7 días").node,
        LP.field("Hasta", to).node,
        h("div", { class: "flex gap-2" }, [apply])
      ]);
      var results = h("div");
      root.appendChild(filters);
      root.appendChild(results);

      function load() {
        LP.clear(results);
        results.appendChild(LP.loading());
        var params = [];
        if (domainSelect.value) { params.push("domain=" + encodeURIComponent(domainSelect.value)); }
        if (port.value) { params.push("port=" + encodeURIComponent(port.value)); }
        if (limit.value) { params.push("limit=" + encodeURIComponent(limit.value)); }
        if (from.value) { params.push("from=" + encodeURIComponent(from.value)); }
        if (to.value) { params.push("to=" + encodeURIComponent(to.value)); }
        var url = "/requests" + (params.length ? "?" + params.join("&") : "");
        LP.api("GET", url).then(function (r) {
          LP.clear(results);
          if (!r.ok) {
            results.appendChild(LP.state("No se pudo cargar el registro.", LP.errorText(r, ""), LP.retryButton(load)));
            return;
          }
          // Method and status class are filtered here: the server does not offer them yet.
          var rows = r.data.filter(function (row) {
            if (method.value && row.method !== method.value) { return false; }
            if (klass.value && String(row.status).charAt(0) !== klass.value) { return false; }
            return true;
          });
          LP.clear(results);
          results.appendChild(h("div", { class: "grid-metrics" }, summary(rows)));
          results.appendChild(LP.table({
            text: function (row) { return row.path + " " + row.domain + " " + row.method; },
            searchPlaceholder: "Buscar en la ruta o el dominio…",
            emptyText: "Ninguna petición coincide con estos filtros.",
            rows: rows,
            columns: [
              { label: "Hora", render: function (row) { return LP.fmt.date(row.time); } },
              { label: "Dominio", render: function (row) { return h("a", { href: "#/domains/" + encodeURIComponent(row.domain), text: row.domain }); } },
              { label: "Puerto", num: true, key: "port" },
              { label: "Método", render: function (row) { return h("span", { class: "badge badge-plain", text: row.method }); } },
              { label: "Ruta", render: function (row) { return h("code", { class: "block max-w-[320px] truncate", title: row.path, text: row.path }); } },
              { label: "Estado", render: function (row) { return LP.httpBadge(row.status); } },
              { label: "Duración", num: true, render: function (row) { return LP.fmt.duration(row.durationMs); } },
              { label: "Respuesta", num: true, render: function (row) { return LP.fmt.bytes(row.responseBytes); } }
            ]
          }));
        });
      }

      filters.addEventListener("submit", function (event) { event.preventDefault(); load(); });
      load();
    });
  }

  LP.screens.traffic = { title: "Tráfico y peticiones", render: render };
})();

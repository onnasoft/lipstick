// Dashboard: the state of the server at a glance. Every number says where it comes from.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;

  // Shared badges, used by several screens.
  LP.domainStatus = function (stats) {
    if (!stats) { return LP.badge("sin cliente", ""); }
    if (stats.status === "active") { return LP.badge("activo", "ok"); }
    if (stats.status === "idle") { return LP.badge("inactivo", "warn"); }
    if (stats.status === "unresponsive") { return LP.badge("sin respuesta", "danger"); }
    return LP.badge(stats.status || "desconocido", "");
  };

  LP.httpBadge = function (code) {
    var kind = code >= 500 ? "danger" : code >= 400 ? "warn" : code >= 300 ? "accent" : "ok";
    return LP.badge(String(code), kind);
  };

  // bars draws one horizontal bar per row, scaled to the largest value. The width is
  // set as a DOM property, which the page policy allows; style attributes are not used.
  LP.bars = function (items) {
    var max = items.reduce(function (m, it) { return Math.max(m, it.value); }, 0) || 1;
    var wrap = h("div", { class: "grid gap-2.5" });
    items.forEach(function (it) {
      var fill = h("i");
      fill.style.width = Math.max(2, Math.round((it.value / max) * 100)) + "%";
      wrap.appendChild(h("div", { class: "grid grid-cols-[120px_1fr_96px] items-center gap-2.5 text-[12.5px]" }, [
        h("span", { class: "truncate", text: it.label, title: it.label }),
        h("div", { class: "bar", role: "img", "aria-label": it.label + ": " + LP.fmt.bytes(it.value) }, [fill]),
        h("span", { class: "text-right tabular-nums text-ink-2", text: LP.fmt.bytes(it.value) })
      ]));
    });
    return wrap;
  };

  function metric(label, value, sub) {
    return h("div", { class: "card" }, [
      h("div", { class: "metric-label", text: label }),
      h("div", { class: "metric-value", text: value }),
      h("div", { class: "metric-sub", text: sub })
    ]);
  }

  function loadDashboard() {
    var started = Date.now();
    return Promise.all([
      LP.api("GET", "/domains"),
      LP.api("GET", "/stats"),
      LP.api("GET", "/health"),
      LP.api("GET", "/requests?limit=8")
    ]).then(function (all) {
      var domains = all[0], stats = all[1], health = all[2], requests = all[3];
      if (!domains.ok || !stats.ok) { throw new Error("dashboard"); }
      var latency = Date.now() - started;
      var today = LP.day(0);
      // Traffic of today comes from the daily records, one request per domain.
      return Promise.all(domains.data.map(function (d) {
        return LP.api("GET", "/domains/" + encodeURIComponent(d.name) + "/traffic?from=" + today + "&to=" + today);
      })).then(function (traffic) {
        var perDomain = {};
        traffic.forEach(function (t, i) {
          var name = domains.data[i].name;
          perDomain[name] = t.ok ? (t.data.consumptions || []).reduce(function (s, c) { return s + (c.BytesUsed || 0); }, 0) : 0;
        });
        return {
          domains: domains.data,
          stats: stats.data,
          healthOk: health.ok,
          latency: latency,
          requests: requests.ok ? requests.data : [],
          perDomain: perDomain
        };
      });
    });
  }

  function render(root) {
    LP.load(root, loadDashboard, function (d) {
      var clients = 0, sessions = 0, pending = 0, total = 0;
      d.stats.forEach(function (s) {
        clients += s.clients; sessions += s.activeSessions; pending += s.pendingVisitors; total += s.bytesSinceStart;
      });
      var todayTotal = Object.keys(d.perDomain).reduce(function (s, k) { return s + d.perDomain[k]; }, 0);
      var statsByName = {};
      d.stats.forEach(function (s) { statsByName[s.domain] = s; });
      var published = d.domains.length;
      var online = d.stats.filter(function (s) { return s.status === "active"; }).length;

      root.appendChild(h("div", { class: "page-head" }, [
        h("div", null, [
          h("h1", { text: "Panel" }),
          h("p", { text: "Estado de Lipstick en este momento." })
        ]),
        d.healthOk
          ? LP.badge("Servidor operativo · " + d.latency + " ms", "ok")
          : LP.badge("Servidor con problemas", "danger")
      ]));

      root.appendChild(h("div", { class: "grid-metrics" }, [
        metric("Dominios publicados", LP.fmt.num(published), online + " con cliente activo"),
        metric("Clientes conectados", LP.fmt.num(clients), "en vivo"),
        metric("Sesiones activas", LP.fmt.num(sessions), "conexiones de túnel abiertas"),
        metric("Visitantes pendientes", LP.fmt.num(pending), "esperando a un cliente"),
        metric("Tráfico de hoy", LP.fmt.bytes(todayTotal), "según el registro diario"),
        metric("Tráfico desde el arranque", LP.fmt.bytes(total), "suma de los servidores del cluster")
      ]));

      var perDomainBars = d.domains.map(function (dom) {
        return { label: dom.name, value: d.perDomain[dom.name] || 0 };
      }).sort(function (a, b) { return b.value - a.value; }).slice(0, 8);

      var recent = LP.table({
        pageSize: 8,
        emptyText: "Todavía no hay peticiones registradas.",
        columns: [
          { label: "Hora", render: function (r) { return LP.fmt.time(r.time); } },
          { label: "Dominio", key: "domain" },
          { label: "Método", key: "method" },
          { label: "Ruta", render: function (r) { return h("code", { class: "truncate block max-w-[260px]", text: r.path }); } },
          { label: "Estado", render: function (r) { return LP.httpBadge(r.status); } },
          { label: "Duración", num: true, render: function (r) { return LP.fmt.duration(r.durationMs); } }
        ],
        rows: d.requests
      });

      root.appendChild(h("div", { class: "grid-2" }, [
        h("div", null, [
          h("div", { class: "card" }, [
            h("div", { class: "card-head" }, [h("h2", { text: "Tráfico de hoy por dominio" })]),
            perDomainBars.length
              ? LP.bars(perDomainBars)
              : h("p", { class: "muted", text: "Ningún dominio ha transferido datos hoy." })
          ])
        ]),
        h("div", { class: "card" }, [
          h("div", { class: "card-head" }, [h("h2", { text: "Estado de los dominios" })]),
          d.domains.length === 0
            ? h("p", { class: "muted", text: "Todavía no hay dominios. Créalo desde la sección Dominios." })
            : h("div", { class: "grid gap-2" }, d.domains.slice(0, 8).map(function (dom) {
                return h("a", { class: "flex items-center justify-between gap-3 rounded-lg px-2 py-2 hover:bg-surface-2 hover:no-underline", href: "#/domains/" + encodeURIComponent(dom.name) }, [
                  h("span", { class: "truncate font-medium text-ink", text: dom.name }),
                  LP.domainStatus(statsByName[dom.name])
                ]);
              }))
        ])
      ]));

      root.appendChild(h("div", { class: "section" }, [
        h("h2", { text: "Actividad reciente" }),
        h("p", { class: "muted mb-3 text-[13px]", text: "Últimas peticiones HTTP terminadas. Sin cabeceras ni cuerpos." }),
        recent
      ]));
    });
  }

  LP.screens.dashboard = { title: "Panel", render: render };
})();

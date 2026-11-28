// Dashboard: the state of the server at a glance. Every number says where it comes from.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  // Shared badges, used by several screens.
  LP.domainStatus = function (stats) {
    if (!stats) { return LP.badge(t("no client"), ""); }
    if (stats.status === "active") { return LP.badge(t("active"), "ok"); }
    if (stats.status === "idle") { return LP.badge(t("idle"), "warn"); }
    if (stats.status === "unresponsive") { return LP.badge(t("unresponsive"), "danger"); }
    return LP.badge(stats.status || t("unknown"), "");
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
        traffic.forEach(function (tr, i) {
          var name = domains.data[i].name;
          perDomain[name] = tr.ok ? (tr.data.consumptions || []).reduce(function (s, c) { return s + (c.BytesUsed || 0); }, 0) : 0;
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
          h("h1", { text: t("Dashboard") }),
          h("p", { text: t("The state of Lipstick right now.") })
        ]),
        d.healthOk
          ? LP.badge(t("Server healthy · {ms} ms", { ms: d.latency }), "ok")
          : LP.badge(t("Server has problems"), "danger")
      ]));

      root.appendChild(h("div", { class: "grid-metrics" }, [
        metric(t("Published domains"), LP.fmt.num(published), t("{count} with an active client", { count: online })),
        metric(t("Connected clients"), LP.fmt.num(clients), t("live")),
        metric(t("Active sessions"), LP.fmt.num(sessions), t("open tunnel connections")),
        metric(t("Waiting visitors"), LP.fmt.num(pending), t("waiting for a client")),
        metric(t("Traffic today"), LP.fmt.bytes(todayTotal), t("from the daily record")),
        metric(t("Traffic since start"), LP.fmt.bytes(total), t("sum of the cluster's servers"))
      ]));

      var perDomainBars = d.domains.map(function (dom) {
        return { label: dom.name, value: d.perDomain[dom.name] || 0 };
      }).sort(function (a, b) { return b.value - a.value; }).slice(0, 8);

      var recent = LP.table({
        pageSize: 8,
        emptyText: t("No requests recorded yet."),
        columns: [
          { label: t("Time"), render: function (r) { return LP.fmt.time(r.time); } },
          { label: t("Domain"), key: "domain" },
          { label: t("Method"), key: "method" },
          { label: t("Path"), render: function (r) { return h("code", { class: "truncate block max-w-[260px]", text: r.path }); } },
          { label: t("Status"), render: function (r) { return LP.httpBadge(r.status); } },
          { label: t("Duration"), num: true, render: function (r) { return LP.fmt.duration(r.durationMs); } }
        ],
        rows: d.requests
      });

      root.appendChild(h("div", { class: "grid-2" }, [
        h("div", null, [
          h("div", { class: "card" }, [
            h("div", { class: "card-head" }, [h("h2", { text: t("Traffic today by domain") })]),
            perDomainBars.length
              ? LP.bars(perDomainBars)
              : h("p", { class: "muted", text: t("No domain has transferred data today.") })
          ])
        ]),
        h("div", { class: "card" }, [
          h("div", { class: "card-head" }, [h("h2", { text: t("Domain status") })]),
          d.domains.length === 0
            ? h("p", { class: "muted", text: t("No domains yet. Create one from the Domains section.") })
            : h("div", { class: "grid gap-2" }, d.domains.slice(0, 8).map(function (dom) {
                return h("a", { class: "flex items-center justify-between gap-3 rounded-lg px-2 py-2 hover:bg-surface-2 hover:no-underline", href: "#/domains/" + encodeURIComponent(dom.name) }, [
                  h("span", { class: "truncate font-medium text-ink", text: dom.name }),
                  LP.domainStatus(statsByName[dom.name])
                ]);
              }))
        ])
      ]));

      root.appendChild(h("div", { class: "section" }, [
        h("h2", { text: t("Recent activity") }),
        h("p", { class: "muted mb-3 text-[13px]", text: t("Latest finished HTTP requests. No headers or bodies.") }),
        recent
      ]));
    });
  }

  LP.screens.dashboard = { title: "Dashboard", render: render };
})();

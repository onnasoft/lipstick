// Traffic and requests: the request history with filters, and what it says about health.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  var METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

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
      LP.metricCard(t("Requests"), LP.fmt.num(rows.length), t("with these filters")),
      LP.metricCard(t("Errors"), rows.length ? Math.round((errors / rows.length) * 100) + " %" : "—", t("{count} with status 4xx or 5xx", { count: errors })),
      LP.metricCard(t("Duration"), LP.fmt.duration(avg), t("average · p95 {p95}", { p95: LP.fmt.duration(percentile(durations, 95)) })),
      LP.metricCard(t("Responses"), LP.fmt.bytes(bytes), t("total size returned"))
    ];
  }

  function render(root) {
    LP.clear(root);
    root.appendChild(h("div", { class: "page-head" }, [
      h("div", null, [h("h1", { text: t("Traffic and requests") }), h("p", { text: t("Finished HTTP requests. No headers or bodies.") })])
    ]));

    LP.api("GET", "/domains").then(function (res) {
      var domains = res.ok ? res.data : [];
      var domainSelect = select([["", t("All domains")]].concat(domains.map(function (d) { return [d.name, d.name]; })), "domain", t("Domain"));
      var port = h("input", { class: "input", name: "port", type: "number", min: "1", max: "65535", placeholder: t("Port"), "aria-label": t("Port") });
      var method = select([["", t("All methods")]].concat(METHODS.map(function (m) { return [m, m]; })), "method", t("Method"));
      var klass = select([
        ["", t("All statuses")], ["2", t("2xx successful")], ["3", t("3xx redirects")],
        ["4", t("4xx client errors")], ["5", t("5xx server errors")]
      ], "class", t("HTTP status"));
      var limit = select([["100", t("Last 100")], ["300", t("Last 300")], ["500", t("Last 500")]], "limit", t("Amount"));
      var from = h("input", { class: "input", type: "date", name: "from", "aria-label": t("From") });
      var to = h("input", { class: "input", type: "date", name: "to", "aria-label": t("To") });
      var apply = h("button", { class: "btn btn-primary", type: "submit", text: t("Apply") });
      var filters = h("form", { class: "card mb-4 grid gap-3 grid-cols-[repeat(auto-fit,minmax(170px,1fr))] items-end" }, [
        LP.field(t("Domain"), domainSelect).node,
        LP.field(t("Port"), port, t("5050 is the proxy")).node,
        LP.field(t("Method"), method).node,
        LP.field(t("Status"), klass).node,
        LP.field(t("Amount"), limit).node,
        LP.field(t("From"), from, t("7-day record")).node,
        LP.field(t("To"), to).node,
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
            results.appendChild(LP.state(t("The log could not be loaded."), LP.errorText(r, ""), LP.retryButton(load)));
            return;
          }
          // Method and status class are filtered here: the server does not offer them yet.
          var rows = r.data.filter(function (row) {
            if (method.value && row.method !== method.value) { return false; }
            if (klass.value && String(row.status).charAt(0) !== klass.value) { return false; }
            return true;
          });
          results.appendChild(h("div", { class: "grid-metrics" }, summary(rows)));
          results.appendChild(LP.table({
            text: function (row) { return row.path + " " + row.domain + " " + row.method; },
            searchPlaceholder: t("Search path or domain…"),
            emptyText: t("No request matches these filters."),
            rows: rows,
            columns: [
              { label: t("Time"), render: function (row) { return LP.fmt.date(row.time); } },
              { label: t("Domain"), render: function (row) { return h("a", { href: "#/domains/" + encodeURIComponent(row.domain), text: row.domain }); } },
              { label: t("Port"), num: true, key: "port" },
              { label: t("Method"), render: function (row) { return h("span", { class: "badge badge-plain", text: row.method }); } },
              { label: t("Path"), render: function (row) { return h("code", { class: "block max-w-[320px] truncate", title: row.path, text: row.path }); } },
              { label: t("Status"), render: function (row) { return LP.httpBadge(row.status); } },
              { label: t("Duration"), num: true, render: function (row) { return LP.fmt.duration(row.durationMs); } },
              { label: t("Response"), num: true, render: function (row) { return LP.fmt.bytes(row.responseBytes); } }
            ]
          }));
        });
      }

      filters.addEventListener("submit", function (event) { event.preventDefault(); load(); });
      load();
    });
  }

  LP.screens.traffic = { title: "Traffic and requests", render: render };
})();

// Audit log: who changed what, and when. Superusers only; the server enforces it too.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  function outcomeBadge(outcome) {
    if (outcome === "succeeded") { return LP.badge(t("succeeded"), "ok"); }
    if (outcome === "failed") { return LP.badge(t("failed"), "danger"); }
    return LP.badge(t("pending"), "warn");
  }

  // fieldsOf lists the names of the fields a change touched. Values are never stored.
  function fieldsOf(details) {
    var f = details && details.fields;
    return Array.isArray(f) ? f.join(", ") : "";
  }

  function render(root) {
    LP.load(root, function () {
      return LP.api("GET", "/audit?limit=500").then(function (res) {
        if (!res.ok) { throw new Error("audit"); }
        return res.data;
      });
    }, function (events) {
      root.appendChild(h("div", { class: "page-head" }, [
        h("div", null, [
          h("h1", { text: t("Audit log") }),
          h("p", { text: t("Who changed what, and when. Changes and login attempts. Values of keys, secrets and passwords are never recorded.") })
        ])
      ]));
      root.appendChild(LP.table({
        text: function (e) { return [e.actor, e.action, e.target, e.outcome, e.clientAddr].join(" "); },
        searchPlaceholder: t("Search by user, action or object…"),
        emptyText: t("Nothing has been recorded yet."),
        rows: events,
        columns: [
          { label: t("Time"), render: function (e) { return LP.fmt.date(e.occurredAt); } },
          { label: t("Who"), key: "actor" },
          { label: t("Action"), render: function (e) { return h("code", { text: e.action }); } },
          { label: t("Object"), render: function (e) { return h("span", { class: "truncate block max-w-[220px]", title: e.target, text: e.target || "—" }); } },
          { label: t("Outcome"), render: function (e) { return outcomeBadge(e.outcome); } },
          { label: t("Status"), render: function (e) { return e.statusCode ? LP.httpBadge(e.statusCode) : "—"; } },
          { label: t("Fields"), render: function (e) { return h("span", { class: "muted", text: fieldsOf(e.details) || "—" }); } },
          { label: t("From"), render: function (e) { return h("code", { text: e.clientAddr }); } }
        ]
      }));
    });
  }

  LP.screens.audit = { title: "Audit log", role: "superuser", render: render };
})();

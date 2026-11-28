// Domains: the list, the creation form, and the detail with its settings.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  // randomKey returns a 256-bit key, base64url, from the browser's secure generator.
  LP.randomKey = function () {
    var raw = new Uint8Array(32);
    window.crypto.getRandomValues(raw);
    var bin = "";
    raw.forEach(function (b) { bin += String.fromCharCode(b); });
    return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  };

  // listValue turns "a, b" or one per line into a clean list.
  LP.listValue = function (text) {
    return String(text || "").split(/[,\n]/).map(function (s) { return s.trim(); }).filter(Boolean);
  };

  // keyBox shows a key once, with a copy button. Nothing keeps it afterwards.
  LP.keyBox = function (label, key) {
    var code = h("code", { text: key });
    var copy = h("button", { class: "btn btn-sm", type: "button", text: t("Copy") });
    copy.addEventListener("click", function () {
      if (window.navigator.clipboard) {
        window.navigator.clipboard.writeText(key).then(function () { copy.textContent = t("Copied"); });
      }
    });
    return h("div", { class: "secret-box", role: "status" }, [
      h("strong", { text: label }),
      h("span", { class: "hint", text: t("Copy it now. It will not be shown again.") }),
      h("div", { class: "secret-value" }, [code, copy])
    ]);
  };

  function closeDialog() {
    var dlg = document.querySelector("dialog.modal");
    if (dlg) { dlg.close(); }
  }

  function domainStatsIndex(stats) {
    var index = {};
    stats.forEach(function (s) { index[s.domain] = s; });
    return index;
  }

  function openCreate(onDone) {
    var name = h("input", { class: "input", name: "name", required: true, autocomplete: "off", placeholder: "robot.example.com" });
    var key = h("input", { class: "input font-mono", name: "apiKey", required: true, autocomplete: "off", placeholder: t("client key") });
    var generated = false;
    var generate = h("button", { class: "btn", type: "button", text: t("Generate") });
    generate.addEventListener("click", function () { key.value = LP.randomKey(); generated = true; });
    var multi = h("input", { type: "checkbox", checked: true, name: "multi" });
    var pass = h("input", { type: "checkbox", name: "passthrough" });
    var cidrs = h("textarea", { class: "input", rows: 2, name: "cidrs", placeholder: "10.0.0.0/8, 192.168.1.7" });
    var status = h("div", { class: "msg", role: "status" });
    var fName = LP.field(t("Domain"), name, t("Full name, without http:// or a trailing slash."));

    var form = h("form", { class: "form" }, [
      fName.node,
      h("div", { class: "grid gap-1.5" }, [
        h("span", { class: "text-[12.5px] font-medium text-ink-2", text: t("Client key") }),
        h("div", { class: "flex gap-2" }, [key, generate]),
        h("span", { class: "hint", text: t("The client uses it with -k. Generate one or paste your own.") })
      ]),
      h("label", { class: "checkbox" }, [multi, h("span", { text: t("Allow several connections from the client") })]),
      h("label", { class: "checkbox" }, [pass, h("span", { text: t("TLS passthrough (the service terminates its own TLS)") })]),
      LP.field(t("Allowed networks"), cidrs, t("Empty = anyone. A list of CIDR networks or addresses.")).node,
      status
    ]);

    var submit = h("button", { class: "btn btn-primary", type: "submit", text: t("Create domain") });
    var cancel = h("button", { class: "btn", type: "button", text: t("Cancel") });
    form.appendChild(h("div", { class: "modal-foot -mx-5 -mb-4 mt-2" }, [cancel, submit]));
    var dialog = LP.dialog({ title: t("New domain"), body: [form], actions: [] });
    cancel.addEventListener("click", function () { dialog.close(); });

    form.addEventListener("submit", function (event) {
      event.preventDefault();
      submit.disabled = true;
      LP.api("POST", "/domains", {
        name: name.value.trim(),
        apiKey: key.value,
        allowMultipleConnections: multi.checked,
        passthrough: pass.checked,
        allowedCidrs: LP.listValue(cidrs.value)
      }).then(function (res) {
        submit.disabled = false;
        if (!res.ok) { status.textContent = LP.errorText(res, t("The domain could not be created.")); status.className = "msg msg-error"; return; }
        dialog.close();
        LP.notify("ok", t("Domain created"), name.value.trim());
        if (generated) {
          LP.dialog({
            title: t("Client key"),
            body: [LP.keyBox(t("Key of {domain}", { domain: name.value.trim() }), key.value),
              h("p", { class: "muted", text: t("Set it in the client with -k, in its configuration file.") })],
            actions: [h("button", { class: "btn btn-primary", type: "button", text: t("Done"), on: { click: closeDialog } })]
          });
        }
        onDone();
      });
    });
  }

  function renderList(root) {
    LP.load(root, function () {
      return Promise.all([LP.api("GET", "/domains"), LP.api("GET", "/stats")]).then(function (all) {
        if (!all[0].ok) { throw new Error("domains"); }
        return { domains: all[0].data, stats: all[1].ok ? all[1].data : [] };
      });
    }, function (d) {
      var index = domainStatsIndex(d.stats);
      var create = h("button", { class: "btn btn-primary", type: "button", text: t("New domain") });
      create.addEventListener("click", function () {
        openCreate(function () { window.location.reload(); });
      });
      root.appendChild(h("div", { class: "page-head" }, [
        h("div", null, [h("h1", { text: t("Domains") }), h("p", { text: t("Published domains, their clients and their traffic.") })]),
        create
      ]));
      root.appendChild(LP.table({
        text: function (r) { return r.name + " " + (index[r.name] ? index[r.name].status : ""); },
        searchPlaceholder: t("Search domain…"),
        emptyText: t("No domains yet. Create the first one with “New domain”."),
        rows: d.domains,
        columns: [
          { label: t("Domain"), render: function (r) { return h("a", { class: "cell-strong", href: "#/domains/" + encodeURIComponent(r.name), text: r.name }); } },
          { label: t("Status"), render: function (r) { return r.disabled ? LP.badge(t("disabled"), "danger") : LP.domainStatus(index[r.name]); } },
          { label: t("Clients"), num: true, render: function (r) { return String(index[r.name] ? index[r.name].clients : 0); } },
          { label: t("Sessions"), num: true, render: function (r) { return String(index[r.name] ? index[r.name].activeSessions : 0); } },
          { label: t("Intranet ports"), render: function (r) { return (index[r.name] && index[r.name].intranetPorts || []).join(", ") || "—"; } },
          { label: t("Traffic since start"), num: true, render: function (r) { return LP.fmt.bytes(index[r.name] ? index[r.name].bytesSinceStart : 0); } },
          { label: "", render: function (r) { return h("div", { class: "actions" }, [h("a", { class: "btn btn-sm", href: "#/domains/" + encodeURIComponent(r.name), text: t("Open") })]); } }
        ]
      }));
    });
  }

  // toggleButton turns a domain off or on. Turning it off asks first: its client is
  // disconnected and its visitors get 503 until it is enabled again.
  function toggleButton(dom, reload) {
    var off = !dom.disabled;
    var button = h("button", { class: "btn" + (off ? "" : " btn-primary"), type: "button", text: off ? t("Disable domain") : t("Enable domain") });
    button.addEventListener("click", function () {
      var go = function () {
        LP.api("POST", "/domains/" + encodeURIComponent(dom.name) + (off ? "/disable" : "/enable")).then(function (res) {
          if (res.ok) { LP.notify("ok", off ? t("Domain disabled") : t("Domain enabled"), dom.name); reload(); }
          else { LP.notify("error", t("Could not change the domain"), LP.errorText(res)); }
        });
      };
      if (!off) { go(); return; }
      LP.confirm({
        title: t("Disable the domain?"),
        text: t("Its client is disconnected and its visitors get 503 until it is enabled again. Settings and history are kept."),
        confirmLabel: t("Disable"),
        danger: true
      }).then(function (ok) { if (ok) { go(); } });
    });
    return button;
  }



  function renderDetail(root, name) {
    LP.load(root, function () {
      var from = LP.day(-29), to = LP.day(0);
      return Promise.all([
        LP.api("GET", "/domains/" + encodeURIComponent(name)),
        LP.api("GET", "/stats"),
        LP.api("GET", "/domains/" + encodeURIComponent(name) + "/traffic?from=" + from + "&to=" + to)
      ]).then(function (all) {
        if (!all[0].ok) { throw new Error("domain"); }
        var stats = all[1].ok ? domainStatsIndex(all[1].data)[name] : null;
        return { domain: all[0].data, stats: stats, traffic: all[2].ok ? all[2].data.consumptions || [] : [] };
      });
    }, function (d) {
      var dom = d.domain;
      var reload = function () { renderDetail(root, name); };
      var st = d.stats;

      root.appendChild(h("div", { class: "page-head" }, [
        h("div", null, [
          h("p", { class: "mb-1" }, [h("a", { href: "#/domains", text: t("← Domains") })]),
          h("h1", { class: "font-mono", text: dom.name })
        ]),
        h("div", { class: "toolbar-group" }, [
          dom.disabled ? LP.badge(t("disabled"), "danger") : LP.domainStatus(st),
          h("a", { class: "btn", href: "#/visitors/" + encodeURIComponent(dom.name), text: t("Visitor users") }),
          toggleButton(dom, reload)
        ])
      ]));

      root.appendChild(h("div", { class: "grid-metrics" }, [
        LP.metricCard(t("Clients"), String(st ? st.clients : 0), t("client connections")),
        LP.metricCard(t("Active sessions"), String(st ? st.activeSessions : 0), t("open tunnels")),
        LP.metricCard(t("Waiting visitors"), String(st ? st.pendingVisitors : 0), t("pending")),
        LP.metricCard(t("Traffic since start"), LP.fmt.bytes(st ? st.bytesSinceStart : 0), t("sum of the cluster's servers"))
      ]));

      var days = d.traffic.slice().sort(function (a, b) { return String(a.Date).localeCompare(String(b.Date)); });
      var dayItems = days.map(function (c) {
        return { label: String(c.Date).slice(0, 10), value: c.BytesUsed || 0 };
      });
      root.appendChild(h("div", { class: "grid-2" }, [
        h("div", { class: "card" }, [
          h("div", { class: "card-head" }, [h("h2", { text: t("Traffic over the last 30 days") })]),
          dayItems.length ? LP.bars(dayItems) : h("p", { class: "muted", text: t("No traffic recorded in this period.") })
        ]),
        h("div", { class: "card" }, [
          h("div", { class: "card-head" }, [h("h2", { text: t("Information") })]),
          h("dl", { class: "kv" }, [
            h("dt", { text: t("Several connections") }), h("dd", { text: dom.allowMultipleConnections ? t("Yes") : t("No") }),
            h("dt", { text: t("TLS passthrough") }), h("dd", { text: dom.passthrough ? t("Yes") : t("No") }),
            h("dt", { text: t("Visitors") }), h("dd", null, [dom.authLatched ? LP.badge(t("restricted"), "warn") : LP.badge(t("open"), "ok")]),
            h("dt", { text: t("Allowed networks") }), h("dd", { text: (dom.allowedCidrs || []).join(", ") || t("anyone") }),
            h("dt", { text: t("Intranet ports") }), h("dd", { class: "mono", text: (st && st.intranetPorts || []).join(", ") || "—" })
          ])
        ])
      ]));

      root.appendChild(settingsCard(dom, reload));
      root.appendChild(dangerCard(dom));
    });
  }

  function settingsCard(dom, reload) {
    var multi = h("input", { type: "checkbox", checked: dom.allowMultipleConnections });
    var pass = h("input", { type: "checkbox", checked: dom.passthrough, disabled: dom.authLatched });
    var cidrs = h("textarea", { class: "input", rows: 2, text: (dom.allowedCidrs || []).join(", ") });
    var status = h("div", { class: "msg", role: "status" });
    var save = h("button", { class: "btn btn-primary", type: "submit", text: t("Save changes") });
    var form = h("form", { class: "form" }, [
      h("label", { class: "checkbox" }, [multi, h("span", { text: t("Allow several connections from the client") })]),
      h("label", { class: "checkbox" }, [pass, h("span", { text: dom.authLatched ? t("TLS passthrough (cannot be turned on while visitors are restricted)") : t("TLS passthrough") })]),
      LP.field(t("Allowed networks"), cidrs, t("Empty = anyone.")).node,
      h("div", { class: "flex items-center justify-between gap-3" }, [status, save])
    ]);
    form.addEventListener("submit", function (event) {
      event.preventDefault();
      // Only the fields that changed are sent.
      var body = {};
      if (multi.checked !== dom.allowMultipleConnections) { body.allowMultipleConnections = multi.checked; }
      if (!dom.authLatched && pass.checked !== dom.passthrough) { body.passthrough = pass.checked; }
      var list = LP.listValue(cidrs.value);
      if (JSON.stringify(list) !== JSON.stringify(dom.allowedCidrs || [])) { body.allowedCidrs = list; }
      if (Object.keys(body).length === 0) { status.textContent = t("Nothing to save."); status.className = "msg"; return; }
      save.disabled = true;
      LP.api("PATCH", "/domains/" + encodeURIComponent(dom.name), body).then(function (res) {
        save.disabled = false;
        if (!res.ok) { status.textContent = LP.errorText(res); status.className = "msg msg-error"; return; }
        LP.notify("ok", t("Settings saved"), dom.name);
        reload();
      });
    });

    var restrict = null;
    if (dom.authLatched) {
      restrict = h("button", { class: "btn", type: "button", text: t("Remove visitor restriction") });
      restrict.addEventListener("click", function () {
        LP.confirm({
          title: t("Remove the restriction?"),
          text: t("Visitors will be able to enter without credentials. Only do this if you no longer need the protection."),
          confirmLabel: t("Remove restriction"),
          danger: true
        }).then(function (ok) {
          if (!ok) { return; }
          LP.api("PATCH", "/domains/" + encodeURIComponent(dom.name), { authLatched: false }).then(function (res) {
            if (res.ok) { LP.notify("ok", t("Restriction removed"), dom.name); reload(); }
            else { LP.notify("error", t("Could not remove it"), LP.errorText(res)); }
          });
        });
      });
    }

    return h("div", { class: "card mt-4" }, [
      h("div", { class: "card-head" }, [h("h2", { text: t("Domain settings") }), restrict]),
      form,
      keyCard(dom, reload)
    ]);
  }

  function keyCard(dom, reload) {
    var key = h("input", { class: "input font-mono", autocomplete: "off", placeholder: t("new client key") });
    var gen = h("button", { class: "btn", type: "button", text: t("Generate") });
    gen.addEventListener("click", function () { key.value = LP.randomKey(); });
    var change = h("button", { class: "btn", type: "button", text: t("Change key") });
    change.addEventListener("click", function () {
      if (key.value.length < 16) { LP.notify("error", t("Key too short"), t("Use at least 16 characters.")); return; }
      LP.confirm({
        title: t("Change the client key?"),
        text: t("The client will disconnect and need the new key. Update its configuration before you confirm."),
        confirmLabel: t("Change key"),
        danger: true
      }).then(function (ok) {
        if (!ok) { return; }
        var value = key.value;
        LP.api("PATCH", "/domains/" + encodeURIComponent(dom.name), { apiKey: value }).then(function (res) {
          if (!res.ok) { LP.notify("error", t("Could not change the key"), LP.errorText(res)); return; }
          key.value = "";
          LP.dialog({
            title: t("Key changed"),
            body: [LP.keyBox(t("New key of {domain}", { domain: dom.name }), value)],
            actions: [h("button", { class: "btn btn-primary", type: "button", text: t("Done"), on: { click: function () { closeDialog(); reload(); } } })]
          });
        });
      });
    });
    return h("div", { class: "section mt-2 border-t border-line pt-4" }, [
      h("h2", { class: "mb-1", text: t("Client key") }),
      h("p", { class: "muted mb-3 text-[13px]", text: t("Changing it disconnects the client until it uses the new key.") }),
      h("div", { class: "flex flex-wrap gap-2" }, [key, gen, change])
    ]);
  }

  function dangerCard(dom) {
    var remove = h("button", { class: "btn btn-danger", type: "button", text: t("Delete domain") });
    remove.addEventListener("click", function () {
      LP.confirm({
        title: t("Delete {domain}?", { domain: dom.name }),
        text: t("Its clients are disconnected and its visitor users are removed. This cannot be undone."),
        confirmLabel: t("Delete domain"),
        typeToConfirm: dom.name
      }).then(function (ok) {
        if (!ok) { return; }
        LP.api("DELETE", "/domains/" + encodeURIComponent(dom.name)).then(function (res) {
          if (res.ok) { LP.notify("ok", t("Domain deleted"), dom.name); window.location.hash = "#/domains"; }
          else { LP.notify("error", t("Could not delete it"), LP.errorText(res)); }
        });
      });
    });
    return h("div", { class: "card mt-4 border-danger/40" }, [
      h("div", { class: "card-head" }, [
        h("div", null, [h("h2", { text: t("Danger zone") }), h("p", { class: "muted text-[13px]", text: t("Deleting a domain disconnects its clients.") })]),
        remove
      ])
    ]);
  }

  LP.metricCard = function (label, value, sub) {
    return h("div", { class: "card" }, [
      h("div", { class: "metric-label", text: label }),
      h("div", { class: "metric-value", text: value }),
      h("div", { class: "metric-sub", text: sub })
    ]);
  };

  LP.screens.domains = {
    title: "Domains",
    render: function (root, params) {
      if (params[0]) { renderDetail(root, params[0]); } else { renderList(root); }
    }
  };
})();

// Domains: the list, the creation form, and the detail with its settings.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;

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
    var copy = h("button", { class: "btn btn-sm", type: "button", text: "Copiar" });
    copy.addEventListener("click", function () {
      if (window.navigator.clipboard) {
        window.navigator.clipboard.writeText(key).then(function () { copy.textContent = "Copiado"; });
      }
    });
    return h("div", { class: "secret-box", role: "status" }, [
      h("strong", { text: label }),
      h("span", { class: "hint", text: "Cópiala ahora. No se volverá a mostrar." }),
      h("div", { class: "secret-value" }, [code, copy])
    ]);
  };

  function domainStatsIndex(stats) {
    var index = {};
    stats.forEach(function (s) { index[s.domain] = s; });
    return index;
  }

  function openCreate(onDone) {
    var name = h("input", { class: "input", name: "name", required: true, autocomplete: "off", placeholder: "robot.example.com" });
    var key = h("input", { class: "input font-mono", name: "apiKey", required: true, autocomplete: "off", placeholder: "clave del cliente" });
    var generated = false;
    var generate = h("button", { class: "btn", type: "button", text: "Generar" });
    generate.addEventListener("click", function () { key.value = LP.randomKey(); generated = true; });
    var multi = h("input", { type: "checkbox", checked: true, name: "multi" });
    var pass = h("input", { type: "checkbox", name: "passthrough" });
    var cidrs = h("textarea", { class: "input", rows: 2, name: "cidrs", placeholder: "10.0.0.0/8, 192.168.1.7" });
    var status = h("div", { class: "msg", role: "status" });
    var fName = LP.field("Dominio", name, "Nombre completo, sin http:// ni barra final.");
    var fKey = LP.field("Clave del cliente", key, "La usa el cliente con -k. Genera una o pega la tuya.");

    var form = h("form", { class: "form" }, [
      fName.node,
      h("div", { class: "grid gap-1.5" }, [
        h("span", { class: "text-[12.5px] font-medium text-ink-2", text: "Clave del cliente" }),
        h("div", { class: "flex gap-2" }, [key, generate]),
        h("span", { class: "hint", text: "La usa el cliente con -k. Genera una o pega la tuya." })
      ]),
      h("label", { class: "checkbox" }, [multi, h("span", { text: "Permitir varias conexiones del cliente" })]),
      h("label", { class: "checkbox" }, [pass, h("span", { text: "Passthrough TLS (el servicio termina su propio TLS)" })]),
      LP.field("Redes permitidas", cidrs, "Vacío = cualquiera. Lista de redes CIDR o direcciones.").node,
      status
    ]);

    var submit = h("button", { class: "btn btn-primary", type: "submit", text: "Crear dominio" });
    var cancel = h("button", { class: "btn", type: "button", text: "Cancelar" });
    form.appendChild(h("div", { class: "modal-foot -mx-5 -mb-4 mt-2" }, [cancel, submit]));
    var dialog = LP.dialog({ title: "Nuevo dominio", body: [form], actions: [] });
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
        if (!res.ok) { status.textContent = LP.errorText(res, "No se pudo crear el dominio."); status.className = "msg msg-error"; return; }
        dialog.close();
        LP.notify("ok", "Dominio creado", name.value.trim());
        if (generated) {
          LP.dialog({
            title: "Clave del cliente",
            body: [LP.keyBox("Clave de " + name.value.trim(), key.value), h("p", { class: "muted", text: "Configúrala en el cliente con -k, en su archivo de configuración." })],
            actions: [h("button", { class: "btn btn-primary", type: "button", text: "Listo", on: { click: function () { document.querySelector("dialog.modal").close(); } } })]
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
      var create = h("button", { class: "btn btn-primary", type: "button", text: "Nuevo dominio" });
      create.addEventListener("click", function () {
        openCreate(function () { window.location.reload(); });
      });
      root.appendChild(h("div", { class: "page-head" }, [
        h("div", null, [h("h1", { text: "Dominios" }), h("p", { text: "Dominios publicados, sus clientes y su tráfico." })]),
        create
      ]));
      root.appendChild(LP.table({
        text: function (r) { return r.name + " " + (index[r.name] ? index[r.name].status : ""); },
        searchPlaceholder: "Buscar dominio…",
        emptyText: "Todavía no hay dominios. Crea el primero con «Nuevo dominio».",
        rows: d.domains,
        columns: [
          { label: "Dominio", render: function (r) { return h("a", { class: "cell-strong", href: "#/domains/" + encodeURIComponent(r.name), text: r.name }); } },
          { label: "Estado", render: function (r) { return LP.domainStatus(index[r.name]); } },
          { label: "Clientes", num: true, render: function (r) { return String(index[r.name] ? index[r.name].clients : 0); } },
          { label: "Sesiones", num: true, render: function (r) { return String(index[r.name] ? index[r.name].activeSessions : 0); } },
          { label: "Puertos de intranet", render: function (r) { return (index[r.name] && index[r.name].intranetPorts || []).join(", ") || "—"; } },
          { label: "Tráfico desde el arranque", num: true, render: function (r) { return LP.fmt.bytes(index[r.name] ? index[r.name].bytesSinceStart : 0); } },
          { label: "", render: function (r) { return h("div", { class: "actions" }, [h("a", { class: "btn btn-sm", href: "#/domains/" + encodeURIComponent(r.name), text: "Abrir" })]); } }
        ]
      }));
    });
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
          h("p", { class: "mb-1", text: "" }, [h("a", { href: "#/domains", text: "← Dominios" })]),
          h("h1", { class: "font-mono", text: dom.name })
        ]),
        h("div", { class: "toolbar-group" }, [
          LP.domainStatus(st),
          h("a", { class: "btn", href: "#/visitors/" + encodeURIComponent(dom.name), text: "Usuarios visitantes" })
        ])
      ]));

      root.appendChild(h("div", { class: "grid-metrics" }, [
        LP.metricCard("Clientes", String(st ? st.clients : 0), "conexiones del cliente"),
        LP.metricCard("Sesiones activas", String(st ? st.activeSessions : 0), "túneles abiertos"),
        LP.metricCard("Visitantes en espera", String(st ? st.pendingVisitors : 0), "pendientes"),
        LP.metricCard("Tráfico desde el arranque", LP.fmt.bytes(st ? st.bytesSinceStart : 0), "se reinicia con el servidor")
      ]));

      var days = d.traffic.slice().sort(function (a, b) { return String(a.Date).localeCompare(String(b.Date)); });
      var dayItems = days.map(function (c) {
        return { label: String(c.Date).slice(0, 10), value: c.BytesUsed || 0 };
      });
      root.appendChild(h("div", { class: "grid-2" }, [
        h("div", { class: "card" }, [
          h("div", { class: "card-head" }, [h("h2", { text: "Tráfico de los últimos 30 días" })]),
          dayItems.length ? LP.bars(dayItems) : h("p", { class: "muted", text: "Sin registros de tráfico en este periodo." })
        ]),
        h("div", { class: "card" }, [
          h("div", { class: "card-head" }, [h("h2", { text: "Información" })]),
          h("dl", { class: "kv" }, [
            h("dt", { text: "Varias conexiones" }), h("dd", { text: dom.allowMultipleConnections ? "Sí" : "No" }),
            h("dt", { text: "Passthrough TLS" }), h("dd", { text: dom.passthrough ? "Sí" : "No" }),
            h("dt", { text: "Visitantes" }), h("dd", null, [dom.authLatched ? LP.badge("restringido", "warn") : LP.badge("abierto", "ok")]),
            h("dt", { text: "Redes permitidas" }), h("dd", { text: (dom.allowedCidrs || []).join(", ") || "cualquiera" }),
            h("dt", { text: "Puertos de intranet" }), h("dd", { class: "mono", text: (st && st.intranetPorts || []).join(", ") || "—" })
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
    var save = h("button", { class: "btn btn-primary", type: "submit", text: "Guardar cambios" });
    var form = h("form", { class: "form" }, [
      h("label", { class: "checkbox" }, [multi, h("span", { text: "Permitir varias conexiones del cliente" })]),
      h("label", { class: "checkbox" }, [pass, h("span", { text: dom.authLatched ? "Passthrough TLS (no se puede activar con visitantes restringidos)" : "Passthrough TLS" })]),
      LP.field("Redes permitidas", cidrs, "Vacío = cualquiera.").node,
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
      if (Object.keys(body).length === 0) { status.textContent = "No hay cambios que guardar."; status.className = "msg"; return; }
      save.disabled = true;
      LP.api("PATCH", "/domains/" + encodeURIComponent(dom.name), body).then(function (res) {
        save.disabled = false;
        if (!res.ok) { status.textContent = LP.errorText(res); status.className = "msg msg-error"; return; }
        LP.notify("ok", "Configuración guardada", dom.name);
        reload();
      });
    });

    var restrict = null;
    if (dom.authLatched) {
      restrict = h("button", { class: "btn", type: "button", text: "Quitar restricción de visitantes" });
      restrict.addEventListener("click", function () {
        LP.confirm({
          title: "¿Quitar la restricción?",
          text: "Los visitantes podrán entrar sin credenciales. Solo hazlo si ya no necesitas la protección.",
          confirmLabel: "Quitar restricción",
          danger: true
        }).then(function (ok) {
          if (!ok) { return; }
          LP.api("PATCH", "/domains/" + encodeURIComponent(dom.name), { authLatched: false }).then(function (res) {
            if (res.ok) { LP.notify("ok", "Restricción quitada", dom.name); reload(); }
            else { LP.notify("error", "No se pudo quitar", LP.errorText(res)); }
          });
        });
      });
    }

    return h("div", { class: "card mt-4" }, [
      h("div", { class: "card-head" }, [h("h2", { text: "Configuración del dominio" }), restrict]),
      form,
      keyCard(dom, reload)
    ]);
  }

  function keyCard(dom, reload) {
    var key = h("input", { class: "input font-mono", autocomplete: "off", placeholder: "nueva clave del cliente" });
    var gen = h("button", { class: "btn", type: "button", text: "Generar" });
    gen.addEventListener("click", function () { key.value = LP.randomKey(); });
    var change = h("button", { class: "btn", type: "button", text: "Cambiar clave" });
    change.addEventListener("click", function () {
      if (key.value.length < 16) { LP.notify("error", "Clave demasiado corta", "Usa al menos 16 caracteres."); return; }
      LP.confirm({
        title: "¿Cambiar la clave del cliente?",
        text: "El cliente se desconectará y necesitará la nueva clave. Actualízala en su configuración antes de confirmar.",
        confirmLabel: "Cambiar clave",
        danger: true
      }).then(function (ok) {
        if (!ok) { return; }
        var value = key.value;
        LP.api("PATCH", "/domains/" + encodeURIComponent(dom.name), { apiKey: value }).then(function (res) {
          if (!res.ok) { LP.notify("error", "No se pudo cambiar la clave", LP.errorText(res)); return; }
          key.value = "";
          LP.dialog({
            title: "Clave cambiada",
            body: [LP.keyBox("Nueva clave de " + dom.name, value)],
            actions: [h("button", { class: "btn btn-primary", type: "button", text: "Listo", on: { click: function () { document.querySelector("dialog.modal").close(); reload(); } } })]
          });
        });
      });
    });
    return h("div", { class: "section mt-2 border-t border-line pt-4" }, [
      h("h2", { class: "mb-1", text: "Clave del cliente" }),
      h("p", { class: "muted mb-3 text-[13px]", text: "Cambiarla desconecta al cliente hasta que use la nueva." }),
      h("div", { class: "flex flex-wrap gap-2" }, [key, gen, change])
    ]);
  }

  function dangerCard(dom) {
    var remove = h("button", { class: "btn btn-danger", type: "button", text: "Borrar dominio" });
    remove.addEventListener("click", function () {
      LP.confirm({
        title: "¿Borrar " + dom.name + "?",
        text: "Se desconectan sus clientes y se borran sus usuarios de visitante. No se puede deshacer.",
        confirmLabel: "Borrar dominio",
        typeToConfirm: dom.name
      }).then(function (ok) {
        if (!ok) { return; }
        LP.api("DELETE", "/domains/" + encodeURIComponent(dom.name)).then(function (res) {
          if (res.ok) { LP.notify("ok", "Dominio borrado", dom.name); window.location.hash = "#/domains"; }
          else { LP.notify("error", "No se pudo borrar", LP.errorText(res)); }
        });
      });
    });
    return h("div", { class: "card mt-4 border-danger/40" }, [
      h("div", { class: "card-head" }, [
        h("div", null, [h("h2", { text: "Zona de peligro" }), h("p", { class: "muted text-[13px]", text: "Borrar el dominio desconecta a sus clientes." })]),
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
    title: "Dominios",
    render: function (root, params) {
      if (params[0]) { renderDetail(root, params[0]); } else { renderList(root); }
    }
  };
})();

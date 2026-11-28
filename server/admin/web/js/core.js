// Core of the admin panel: the API client, DOM helpers, tables, dialogs, notices and
// formatting. Screens are registered on LP.screens by the other files.
(function () {
  "use strict";
  var LP = (window.LP = {});
  LP.screens = {};
  LP.me = null;

  // h builds an element. Text always goes through textContent, so data from the server
  // is never parsed as HTML. Attributes are set one by one.
  LP.h = function (tag, props, children) {
    var node = document.createElement(tag);
    Object.keys(props || {}).forEach(function (key) {
      var value = props[key];
      if (value === undefined || value === null || value === false) { return; }
      if (key === "text") { node.textContent = String(value); }
      else if (key === "class") { node.className = value; }
      else if (key === "on") {
        Object.keys(value).forEach(function (event) { node.addEventListener(event, value[event]); });
      } else if (key === "disabled" || key === "checked" || key === "selected" || key === "required" || key === "hidden") {
        node[key] = !!value;
      } else if (key === "value") { node.value = String(value); }
      else { node.setAttribute(key, value === true ? "" : String(value)); }
    });
    (children || []).forEach(function (child) {
      if (child === null || child === undefined || child === false) { return; }
      node.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
    });
    return node;
  };
  var h = LP.h;

  LP.clear = function (node) { while (node.firstChild) { node.removeChild(node.firstChild); } };

  // api sends one request. Writes carry the CSRF header the server requires for
  // cookie sessions. A 401 means the session ended: the page reloads to the login form.
  LP.api = function (method, path, body) {
    var init = { method: method, headers: { "X-Lipstick-CSRF": "1" }, credentials: "same-origin" };
    if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    return fetch(path, init).then(function (response) {
      if (response.status === 401 && path !== "/account") {
        window.location.reload();
        return { ok: false, status: 401, data: { error: "La sesión terminó." } };
      }
      return response.json().catch(function () { return {}; }).then(function (data) {
        return { ok: response.ok, status: response.status, data: data };
      });
    }, function () {
      return { ok: false, status: 0, data: { error: "No hay conexión con el servidor." } };
    });
  };

  // errorText turns a failed answer into a sentence for the user.
  LP.errorText = function (res, fallback) {
    if (res && res.status === 403) { return "Tu rol no permite esta acción."; }
    if (res && res.status === 429) { return "Demasiados intentos. Espera un momento."; }
    return (res && res.data && res.data.error) || fallback || "La operación no se completó.";
  };

  LP.fmt = {
    bytes: function (n) {
      n = Number(n) || 0;
      var units = ["B", "KB", "MB", "GB", "TB"];
      var i = 0;
      while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
      return (i === 0 ? n : n.toFixed(n < 10 ? 2 : 1)) + " " + units[i];
    },
    num: function (n) { return (Number(n) || 0).toLocaleString("es"); },
    date: function (iso) {
      var d = new Date(iso);
      return isNaN(d) ? "—" : d.toLocaleString("es", { dateStyle: "medium", timeStyle: "short" });
    },
    time: function (iso) {
      var d = new Date(iso);
      return isNaN(d) ? "—" : d.toLocaleTimeString("es", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
    },
    duration: function (ms) {
      ms = Number(ms) || 0;
      return ms < 1000 ? ms + " ms" : (ms / 1000).toFixed(2) + " s";
    }
  };

  // day returns the local calendar day as YYYY-MM-DD, offset by a number of days.
  LP.day = function (offset) {
    var d = new Date();
    d.setDate(d.getDate() + (offset || 0));
    var m = String(d.getMonth() + 1), day = String(d.getDate());
    return d.getFullYear() + "-" + (m.length < 2 ? "0" + m : m) + "-" + (day.length < 2 ? "0" + day : day);
  };

  // notify shows a short message in the corner. It never replaces a form error.
  LP.notify = function (kind, title, text) {
    var box = document.getElementById("toasts");
    var node = h("div", { class: "toast toast-" + (kind === "error" ? "error" : "ok"), role: kind === "error" ? "alert" : "status" }, [
      h("div", { class: "toast-title", text: title }),
      text ? h("div", { class: "muted", text: text }) : null
    ]);
    box.appendChild(node);
    setTimeout(function () { node.remove(); }, kind === "error" ? 7000 : 4000);
  };

  // dialog opens a native modal dialog. Escape closes it, and focus returns to the page.
  LP.dialog = function (opts) {
    var dlg = h("dialog", { class: "modal", "aria-labelledby": "dlg-title" });
    dlg.appendChild(h("div", { class: "modal-head" }, [h("h2", { id: "dlg-title", text: opts.title })]));
    dlg.appendChild(h("div", { class: "modal-body" }, opts.body || []));
    dlg.appendChild(h("div", { class: "modal-foot" }, opts.actions || []));
    var opener = document.activeElement;
    dlg.addEventListener("close", function () {
      var value = dlg.returnValue;
      dlg.remove();
      if (opener && opener.focus) { opener.focus(); }
      if (opts.onClose) { opts.onClose(value); }
    });
    document.body.appendChild(dlg);
    dlg.showModal();
    var first = dlg.querySelector("input, select, textarea");
    if (first) { first.focus(); }
    return { close: function (value) { dlg.close(value); } };
  };

  // confirm asks before an action. Destructive actions can require the user to type a
  // word, so one click cannot delete by accident.
  LP.confirm = function (opts) {
    return new Promise(function (resolve) {
      var typed = null, ok = null;
      var body = [h("p", { text: opts.text })];
      if (opts.typeToConfirm) {
        typed = h("input", { class: "input", autocomplete: "off", "aria-label": "Confirmación" });
        ok = h("button", { class: "btn btn-danger", type: "button", disabled: true, text: opts.confirmLabel || "Confirmar" });
        typed.addEventListener("input", function () { ok.disabled = typed.value !== opts.typeToConfirm; });
        body.push(h("label", { class: "label" }, [h("span", { text: "Escribe " + opts.typeToConfirm + " para confirmar" }), typed]));
      } else {
        ok = h("button", { class: "btn " + (opts.danger ? "btn-danger" : "btn-primary"), type: "button", text: opts.confirmLabel || "Confirmar" });
      }
      var cancel = h("button", { class: "btn", type: "button", text: "Cancelar" });
      var dialog = LP.dialog({
        title: opts.title,
        body: body,
        actions: [cancel, ok],
        onClose: function (value) { resolve(value === "ok"); }
      });
      ok.addEventListener("click", function () { dialog.close("ok"); });
      cancel.addEventListener("click", function () { dialog.close("cancel"); });
    });
  };

  // state builds the empty, error and loading views.
  LP.state = function (title, text, action) {
    return h("div", { class: "state" }, [
      h("strong", { text: title }),
      text ? h("p", { text: text }) : null,
      action || null
    ]);
  };
  LP.loading = function () {
    return h("div", { class: "card grid gap-3", role: "status", "aria-label": "Cargando" }, [
      h("div", { class: "skeleton w-1/3" }), h("div", { class: "skeleton" }), h("div", { class: "skeleton w-5/6" })
    ]);
  };
  LP.retryButton = function (onRetry) {
    return h("button", { class: "btn", type: "button", text: "Reintentar", on: { click: onRetry } });
  };

  // table builds a sortable-by-order table with search and pagination on the client.
  // columns: [{label, render(row) -> node|string, num}]. text(row) feeds the search box.
  LP.table = function (opts) {
    var pageSize = opts.pageSize || 15;
    var rows = opts.rows || [];
    var page = 0, query = "";
    var tbody = h("tbody");
    var info = h("span");
    var prev = h("button", { class: "btn btn-sm", type: "button", text: "Anterior" });
    var next = h("button", { class: "btn btn-sm", type: "button", text: "Siguiente" });
    var filtered = [];

    function draw() {
      filtered = rows.filter(function (row) {
        return !query || (opts.text ? opts.text(row) : "").toLowerCase().indexOf(query) !== -1;
      });
      var pages = Math.max(1, Math.ceil(filtered.length / pageSize));
      page = Math.min(page, pages - 1);
      LP.clear(tbody);
      if (filtered.length === 0) {
        var emptyText = query ? "Ningún resultado para “" + query + "”." : (opts.emptyText || "No hay nada que mostrar todavía.");
        tbody.appendChild(h("tr", null, [h("td", { class: "td", colspan: opts.columns.length, text: emptyText })]));
      }
      filtered.slice(page * pageSize, page * pageSize + pageSize).forEach(function (row) {
        tbody.appendChild(h("tr", { class: "tr" }, opts.columns.map(function (col) {
          var value = col.render ? col.render(row) : row[col.key];
          return h("td", { class: "td" + (col.num ? " text-right tabular-nums" : "") }, [
            typeof value === "string" || typeof value === "number" ? String(value) : value
          ]);
        })));
      });
      info.textContent = filtered.length + (filtered.length === 1 ? " resultado" : " resultados") + " · página " + (page + 1) + " de " + pages;
      prev.disabled = page === 0;
      next.disabled = page >= pages - 1;
    }

    prev.addEventListener("click", function () { page--; draw(); });
    next.addEventListener("click", function () { page++; draw(); });

    var toolbar = null;
    if (opts.text) {
      var search = h("input", {
        class: "input search", type: "search", placeholder: opts.searchPlaceholder || "Buscar…",
        "aria-label": "Buscar", autocomplete: "off"
      });
      search.addEventListener("input", function () { query = search.value.trim().toLowerCase(); page = 0; draw(); });
      toolbar = h("div", { class: "toolbar" }, [search, opts.toolbar || null]);
    } else if (opts.toolbar) {
      toolbar = h("div", { class: "toolbar" }, [opts.toolbar]);
    }

    var table = h("table", { class: "w-full border-collapse text-[13.5px]" }, [
      h("thead", null, [h("tr", null, opts.columns.map(function (col) {
        return h("th", { class: "th" + (col.num ? " text-right" : ""), text: col.label });
      }))]),
      tbody
    ]);
    draw();
    return h("div", null, [
      toolbar,
      h("div", { class: "table-wrap" }, [
        table,
        h("div", { class: "pager" }, [info, h("div", { class: "pager-buttons" }, [prev, next])])
      ])
    ]);
  };

  // field wraps an input with its label and an error line.
  LP.field = function (label, input, hint) {
    var error = h("div", { class: "field-error", role: "alert" });
    var node = h("label", { class: "label" }, [
      h("span", { text: label }),
      input,
      hint ? h("span", { class: "hint", text: hint }) : null,
      error
    ]);
    return { node: node, input: input, error: error, setError: function (text) {
      error.textContent = text || "";
      input.setAttribute("aria-invalid", text ? "true" : "false");
    } };
  };

  LP.badge = function (text, kind) {
    return h("span", { class: "badge" + (kind ? " badge-" + kind : ""), text: text });
  };

  // route sets the page title and the breadcrumb.
  LP.setCrumb = function (section, title) {
    var crumb = document.getElementById("crumb");
    LP.clear(crumb);
    crumb.appendChild(h("span", { text: section + " / " }));
    crumb.appendChild(h("strong", { text: title }));
    document.title = title + " · Lipstick";
  };

  // screenBody runs a screen's load function and shows loading, error or the view.
  LP.load = function (root, loader, render) {
    LP.clear(root);
    root.appendChild(LP.loading());
    return loader().then(function (data) {
      LP.clear(root);
      render(data);
    }, function () {
      LP.clear(root);
      root.appendChild(LP.state("No se pudo cargar esta pantalla.", "Revisa tu conexión e inténtalo de nuevo.",
        LP.retryButton(function () { LP.load(root, loader, render); })));
    });
  };

  window.LP = LP;
})();

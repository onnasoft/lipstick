// Admin accounts. Superusers only: the panel hides the screen for operators, and the
// server refuses every call from them as well. The server also keeps at least one
// active superuser and does not let anyone disable their own account.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;

  var ROLE_LABEL = { superuser: "Superusuario", operator: "Operador" };

  function roleSelect(current) {
    var s = h("select", { class: "input", name: "role", "aria-label": "Rol" });
    [["operator", "Operador"], ["superuser", "Superusuario"]].forEach(function (r) {
      s.appendChild(h("option", { value: r[0], text: r[1], selected: r[0] === current }));
    });
    return s;
  }

  function passwordFields() {
    var pass = h("input", { class: "input", type: "password", autocomplete: "new-password", minlength: "12", required: true });
    var again = h("input", { class: "input", type: "password", autocomplete: "new-password", required: true });
    var fPass = LP.field("Contraseña", pass, "12 caracteres o más.");
    var fAgain = LP.field("Repítela", again);
    return {
      nodes: [fPass.node, fAgain.node],
      value: function () {
        var ok = true;
        fPass.setError(pass.value.length >= 12 ? "" : "Debe tener al menos 12 caracteres.");
        if (pass.value.length < 12) { ok = false; }
        fAgain.setError(again.value === pass.value ? "" : "Las contraseñas no coinciden.");
        if (again.value !== pass.value) { ok = false; }
        return ok ? pass.value : null;
      }
    };
  }

  // formDialog runs a small form and calls send(values) -> promise of an answer.
  function formDialog(title, fields, submitLabel, send, onDone) {
    var status = h("div", { class: "msg msg-error", role: "alert" });
    var submit = h("button", { class: "btn btn-primary", type: "submit", text: submitLabel });
    var cancel = h("button", { class: "btn", type: "button", text: "Cancelar" });
    var form = h("form", { class: "form" }, fields.nodes.concat([status]));
    var dialog = LP.dialog({ title: title, body: [form], actions: [] });
    form.appendChild(h("div", { class: "modal-foot -mx-5 -mb-4 mt-2" }, [cancel, submit]));
    cancel.addEventListener("click", function () { dialog.close(); });
    form.addEventListener("submit", function (event) {
      event.preventDefault();
      var values = fields.value();
      if (!values) { return; }
      submit.disabled = true;
      send(values).then(function (res) {
        submit.disabled = false;
        if (!res.ok) { status.textContent = LP.errorText(res); return; }
        dialog.close();
        if (onDone) { onDone(); }
      });
    });
    return dialog;
  }

  function render(root) {
    LP.load(root, function () {
      return LP.api("GET", "/admin-users").then(function (res) {
        if (!res.ok) { throw new Error("accounts"); }
        return res.data;
      });
    }, function (accounts) {
      var me = LP.me || {};
      function reload() { LP.clear(root); render(root); }

      var create = h("button", { class: "btn btn-primary", type: "button", text: "Nueva cuenta" });
      create.addEventListener("click", function () {
        var username = h("input", { class: "input", name: "username", required: true, autocomplete: "off", pattern: "[A-Za-z0-9._@\\-]{1,64}" });
        var fUser = LP.field("Usuario", username, "Letras, números y . _ @ -");
        var pw = passwordFields();
        var role = roleSelect("operator");
        formDialog("Nueva cuenta de administrador", {
          nodes: [fUser.node].concat(pw.nodes, [LP.field("Rol", role, "Un operador no gestiona cuentas.").node]),
          value: function () {
            var password = pw.value();
            if (!password || !username.value) { return null; }
            return { username: username.value.trim(), password: password, role: role.value };
          }
        }, "Crear cuenta", function (v) {
          return LP.api("POST", "/admin-users", v).then(function (res) {
            if (res.ok) { LP.notify("ok", "Cuenta creada", v.username); }
            return res;
          });
        }, reload);
      });

      root.appendChild(h("div", { class: "page-head" }, [
        h("div", null, [h("h1", { text: "Cuentas de administrador" }), h("p", { text: "Quién puede entrar al panel y con qué rol." })]),
        create
      ]));

      root.appendChild(LP.table({
        text: function (a) { return a.username + " " + a.role; },
        searchPlaceholder: "Buscar cuenta…",
        emptyText: "No hay cuentas. Algo raro pasa si esto se ve vacío.",
        rows: accounts,
        columns: [
          { label: "Usuario", render: function (a) {
            return h("span", { class: "cell-strong", text: a.username }, [a.username === me.username ? h("span", { class: "muted", text: " (tú)" }) : null]);
          } },
          { label: "Rol", render: function (a) { return LP.badge(ROLE_LABEL[a.role] || a.role, a.role === "superuser" ? "accent" : ""); } },
          { label: "Estado", render: function (a) { return a.disabled ? LP.badge("bloqueada", "danger") : LP.badge("activa", "ok"); } },
          { label: "Creada", render: function (a) { return LP.fmt.date(a.createdAt); } },
          { label: "", render: function (a) { return rowActions(a, me, reload); } }
        ]
      }));

      root.appendChild(h("p", { class: "hint mt-3", text: "Al menos un superusuario activo debe quedar siempre. No puedes bloquear tu propia cuenta." }));
    });
  }

  function rowActions(a, me, reload) {
    var base = "/admin-users/" + encodeURIComponent(a.username);
    var roleBtn = h("button", { class: "btn btn-sm", type: "button", text: "Cambiar rol" });
    roleBtn.addEventListener("click", function () {
      var role = roleSelect(a.role);
      formDialog("Rol de " + a.username, {
        nodes: [LP.field("Rol", role, "Los superusuarios gestionan cuentas; los operadores no.").node],
        value: function () { return { role: role.value }; }
      }, "Guardar rol", function (v) {
        return LP.api("POST", base + "/role", v).then(function (res) {
          if (res.ok) { LP.notify("ok", "Rol cambiado", a.username + " ahora es " + (ROLE_LABEL[v.role] || v.role)); }
          return res;
        });
      }, reload);
    });

    var passBtn = h("button", { class: "btn btn-sm", type: "button", text: "Nueva contraseña" });
    passBtn.addEventListener("click", function () {
      var pw = passwordFields();
      formDialog("Nueva contraseña de " + a.username, {
        nodes: [h("p", { class: "text-[13px] text-ink-2", text: "Sus sesiones abiertas se cerrarán y tendrá que entrar de nuevo." })].concat(pw.nodes),
        value: function () { var p = pw.value(); return p ? { password: p } : null; }
      }, "Cambiar contraseña", function (v) {
        return LP.api("POST", base + "/password", v).then(function (res) {
          if (res.ok) { LP.notify("ok", "Contraseña cambiada", a.username); }
          return res;
        });
      }, reload);
    });

    var nodes = [roleBtn, passBtn];
    if (a.username !== me.username) {
      var toggle = h("button", {
        class: "btn btn-sm " + (a.disabled ? "" : "btn-danger"), type: "button",
        text: a.disabled ? "Activar" : "Bloquear"
      });
      toggle.addEventListener("click", function () {
        LP.confirm({
          title: (a.disabled ? "¿Activar " : "¿Bloquear ") + a.username + "?",
          text: a.disabled
            ? "Podrá volver a entrar con su contraseña."
            : "No podrá entrar y sus sesiones abiertas se cerrarán.",
          confirmLabel: a.disabled ? "Activar" : "Bloquear",
          danger: !a.disabled
        }).then(function (ok) {
          if (!ok) { return; }
          LP.api("POST", base + (a.disabled ? "/enable" : "/disable")).then(function (res) {
            if (res.ok) { LP.notify("ok", a.disabled ? "Cuenta activada" : "Cuenta bloqueada", a.username); reload(); }
            else { LP.notify("error", "No se pudo cambiar la cuenta", LP.errorText(res)); }
          });
        });
      });
      nodes.push(toggle);
    }
    return h("div", { class: "actions" }, nodes);
  }

  LP.screens.accounts = { title: "Cuentas de administrador", role: "superuser", render: render };
})();

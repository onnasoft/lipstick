// Visitor users of a domain. Each user has its own secret that signs its requests; the
// secret is shown once, when it is created or rotated. The API lists names only, so this
// screen shows names and actions and does not invent a status it cannot read.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;

  function showSecretOnce(username, secret) {
    LP.dialog({
      title: "Secreto de " + username,
      body: [
        LP.keyBox("Secreto de " + username, secret),
        h("p", { class: "muted text-[13px]", text: "El usuario lo usa para firmar cada petición. Guárdalo en un lugar seguro: no podrás verlo otra vez." })
      ],
      actions: [h("button", { class: "btn btn-primary", type: "button", text: "Ya lo guardé", on: { click: function () { document.querySelector("dialog.modal").close(); } } })]
    });
  }

  function openCreate(domain, onDone) {
    var username = h("input", { class: "input", name: "username", required: true, autocomplete: "off" });
    var secret = h("input", { class: "input font-mono", name: "secret", autocomplete: "off", placeholder: "vacío = el servidor genera uno" });
    var fUser = LP.field("Usuario", username, "Letras, números y . _ @ -");
    var fSecret = LP.field("Secreto (opcional)", secret, "Si lo dejas vacío, se genera uno y se muestra una vez. Mínimo 32 caracteres si lo escribes.");
    var status = h("div", { class: "msg msg-error", role: "alert" });
    var submit = h("button", { class: "btn btn-primary", type: "submit", text: "Crear usuario" });
    var cancel = h("button", { class: "btn", type: "button", text: "Cancelar" });
    var form = h("form", { class: "form" }, [fUser.node, fSecret.node, status]);
    form.appendChild(h("div", { class: "modal-foot -mx-5 -mb-4 mt-2" }, [cancel, submit]));
    var dialog = LP.dialog({ title: "Nuevo usuario de visitante en " + domain, body: [form], actions: [] });
    cancel.addEventListener("click", function () { dialog.close(); });
    form.addEventListener("submit", function (event) {
      event.preventDefault();
      var body = { username: username.value.trim() };
      if (secret.value) { body.secret = secret.value; }
      submit.disabled = true;
      LP.api("POST", "/domains/" + encodeURIComponent(domain) + "/visitor-users", body).then(function (res) {
        submit.disabled = false;
        if (!res.ok) { status.textContent = LP.errorText(res); return; }
        dialog.close();
        LP.notify("ok", "Usuario creado", body.username);
        if (res.data.secret) { showSecretOnce(body.username, res.data.secret); }
        onDone();
      });
    });
  }

  function render(root, params) {
    var current = params[0] || "";
    LP.load(root, function () {
      return LP.api("GET", "/domains").then(function (res) {
        if (!res.ok) { throw new Error("domains"); }
        var domain = current || (res.data[0] && res.data[0].name) || "";
        if (!domain) { return { domains: [], domain: "", users: [] }; }
        return LP.api("GET", "/domains/" + encodeURIComponent(domain) + "/visitor-users").then(function (u) {
          if (!u.ok) { throw new Error("users"); }
          return { domains: res.data, domain: domain, users: u.data };
        });
      });
    }, function (d) {
      function go(name) { window.location.hash = "#/visitors/" + encodeURIComponent(name); }
      root.appendChild(h("div", { class: "page-head" }, [
        h("div", null, [h("h1", { text: "Usuarios visitantes" }), h("p", { text: "Quién puede entrar a un dominio restringido, con su propio secreto." })])
      ]));

      if (d.domains.length === 0) {
        root.appendChild(LP.state("No hay dominios todavía.", "Crea un dominio para poder añadir usuarios de visitante.",
          h("a", { class: "btn", href: "#/domains", text: "Ir a dominios" })));
        return;
      }

      var pick = h("select", { class: "input", "aria-label": "Dominio" });
      d.domains.forEach(function (dom) { pick.appendChild(h("option", { value: dom.name, text: dom.name, selected: dom.name === d.domain })); });
      pick.addEventListener("change", function () { go(pick.value); });

      var add = h("button", { class: "btn btn-primary", type: "button", text: "Nuevo usuario" });
      add.addEventListener("click", function () {
        openCreate(d.domain, function () { window.location.reload(); });
      });
      root.appendChild(h("div", { class: "toolbar" }, [
        LP.field("Dominio", pick).node,
        add
      ]));

      root.appendChild(LP.table({
        text: function (u) { return u; },
        searchPlaceholder: "Buscar usuario…",
        emptyText: "Este dominio no tiene usuarios de visitante.",
        rows: d.users,
        columns: [
          { label: "Usuario", render: function (u) { return h("span", { class: "cell-strong font-mono", text: u }); } },
          { label: "Acciones", render: function (u) {
            var base = "/domains/" + encodeURIComponent(d.domain) + "/visitor-users/" + encodeURIComponent(u);
            var rotate = h("button", { class: "btn btn-sm", type: "button", text: "Rotar secreto" });
            rotate.addEventListener("click", function () {
              LP.confirm({
                title: "¿Rotar el secreto de " + u + "?",
                text: "El secreto actual dejará de funcionar al instante. Tendrás que darle el nuevo.",
                confirmLabel: "Rotar secreto",
                danger: true
              }).then(function (ok) {
                if (!ok) { return; }
                LP.api("POST", base + "/rotate").then(function (res) {
                  if (!res.ok) { LP.notify("error", "No se pudo rotar", LP.errorText(res)); return; }
                  showSecretOnce(u, res.data.secret);
                });
              });
            });
            var remove = h("button", { class: "btn btn-sm btn-danger", type: "button", text: "Borrar" });
            remove.addEventListener("click", function () {
              LP.confirm({
                title: "¿Borrar a " + u + "?",
                text: "Perderá el acceso al dominio de inmediato.",
                confirmLabel: "Borrar usuario",
                typeToConfirm: u
              }).then(function (ok) {
                if (!ok) { return; }
                LP.api("DELETE", base).then(function (res) {
                  if (res.ok) { LP.notify("ok", "Usuario borrado", u); window.location.reload(); }
                  else { LP.notify("error", "No se pudo borrar", LP.errorText(res)); }
                });
              });
            });
            return h("div", { class: "actions" }, [rotate, remove]);
          } }
        ]
      }));
    });
  }

  LP.screens.visitors = { title: "Usuarios visitantes", render: render };
})();

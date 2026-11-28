// Visitor users of a domain. Each user has its own secret that signs its requests; the
// secret is shown once, when it is created or rotated. The API lists names only, so this
// screen shows names and actions and does not invent a status it cannot read.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  function closeDialog() {
    var dlg = document.querySelector("dialog.modal");
    if (dlg) { dlg.close(); }
  }

  function showSecretOnce(username, secret) {
    LP.dialog({
      title: t("Secret of {user}", { user: username }),
      body: [
        LP.keyBox(t("Secret of {user}", { user: username }), secret),
        h("p", { class: "muted text-[13px]", text: t("The user signs every request with it. Store it somewhere safe: you will not be able to see it again.") })
      ],
      actions: [h("button", { class: "btn btn-primary", type: "button", text: t("I have saved it"), on: { click: closeDialog } })]
    });
  }

  function openCreate(domain, onDone) {
    var username = h("input", { class: "input", name: "username", required: true, autocomplete: "off" });
    var secret = h("input", { class: "input font-mono", name: "secret", autocomplete: "off", placeholder: t("empty = the server generates one") });
    var fUser = LP.field(t("Username"), username, t("Letters, digits and . _ @ -"));
    var fSecret = LP.field(t("Secret (optional)"), secret, t("If left empty, one is generated and shown once. At least 32 characters if you type one."));
    var status = h("div", { class: "msg msg-error", role: "alert" });
    var submit = h("button", { class: "btn btn-primary", type: "submit", text: t("Create user") });
    var cancel = h("button", { class: "btn", type: "button", text: t("Cancel") });
    var form = h("form", { class: "form" }, [fUser.node, fSecret.node, status]);
    form.appendChild(h("div", { class: "modal-foot -mx-5 -mb-4 mt-2" }, [cancel, submit]));
    var dialog = LP.dialog({ title: t("New visitor user in {domain}", { domain: domain }), body: [form], actions: [] });
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
        LP.notify("ok", t("User created"), body.username);
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
        h("div", null, [h("h1", { text: t("Visitor users") }), h("p", { text: t("Who can enter a restricted domain, each with its own secret.") })])
      ]));

      if (d.domains.length === 0) {
        root.appendChild(LP.state(t("No domains yet."), t("Create a domain first to add visitor users."),
          h("a", { class: "btn", href: "#/domains", text: t("Go to domains") })));
        return;
      }

      var pick = h("select", { class: "input", "aria-label": t("Domain") });
      d.domains.forEach(function (dom) { pick.appendChild(h("option", { value: dom.name, text: dom.name, selected: dom.name === d.domain })); });
      pick.addEventListener("change", function () { go(pick.value); });

      var add = h("button", { class: "btn btn-primary", type: "button", text: t("New user") });
      add.addEventListener("click", function () {
        openCreate(d.domain, function () { window.location.reload(); });
      });
      root.appendChild(h("div", { class: "toolbar" }, [
        LP.field(t("Domain"), pick).node,
        add
      ]));

      root.appendChild(LP.table({
        text: function (u) { return u; },
        searchPlaceholder: t("Search user…"),
        emptyText: t("This domain has no visitor users."),
        rows: d.users,
        columns: [
          { label: t("Username"), render: function (u) { return h("span", { class: "cell-strong font-mono", text: u }); } },
          { label: t("Actions"), render: function (u) {
            var base = "/domains/" + encodeURIComponent(d.domain) + "/visitor-users/" + encodeURIComponent(u);
            var rotate = h("button", { class: "btn btn-sm", type: "button", text: t("Rotate secret") });
            rotate.addEventListener("click", function () {
              LP.confirm({
                title: t("Rotate the secret of {user}?", { user: u }),
                text: t("The current secret stops working at once. You will have to give the user the new one."),
                confirmLabel: t("Rotate secret"),
                danger: true
              }).then(function (ok) {
                if (!ok) { return; }
                LP.api("POST", base + "/rotate").then(function (res) {
                  if (!res.ok) { LP.notify("error", t("Could not rotate it"), LP.errorText(res)); return; }
                  showSecretOnce(u, res.data.secret);
                });
              });
            });
            var remove = h("button", { class: "btn btn-sm btn-danger", type: "button", text: t("Delete") });
            remove.addEventListener("click", function () {
              LP.confirm({
                title: t("Delete {user}?", { user: u }),
                text: t("They lose access to the domain at once."),
                confirmLabel: t("Delete user"),
                typeToConfirm: u
              }).then(function (ok) {
                if (!ok) { return; }
                LP.api("DELETE", base).then(function (res) {
                  if (res.ok) { LP.notify("ok", t("User deleted"), u); window.location.reload(); }
                  else { LP.notify("error", t("Could not delete it"), LP.errorText(res)); }
                });
              });
            });
            return h("div", { class: "actions" }, [rotate, remove]);
          } }
        ]
      }));
    });
  }

  LP.screens.visitors = { title: "Visitor users", render: render };
})();

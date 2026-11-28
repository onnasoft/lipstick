// Admin accounts. Superusers only: the panel hides the screen for operators, and the
// server refuses every call from them as well. The server also keeps at least one
// active superuser and does not let anyone disable their own account.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  function roleLabel(role) {
    return role === "superuser" ? t("Superuser") : t("Operator");
  }

  function roleSelect(current) {
    var s = h("select", { class: "input", name: "role", "aria-label": t("Role") });
    [["operator", t("Operator")], ["superuser", t("Superuser")]].forEach(function (r) {
      s.appendChild(h("option", { value: r[0], text: r[1], selected: r[0] === current }));
    });
    return s;
  }

  function passwordFields() {
    var pass = h("input", { class: "input", type: "password", autocomplete: "new-password", minlength: "12", required: true });
    var again = h("input", { class: "input", type: "password", autocomplete: "new-password", required: true });
    var fPass = LP.field(t("Password"), pass, t("12 characters or more."));
    var fAgain = LP.field(t("Repeat it"), again);
    return {
      nodes: [fPass.node, fAgain.node],
      value: function () {
        var ok = true;
        fPass.setError(pass.value.length >= 12 ? "" : t("It must be at least 12 characters."));
        if (pass.value.length < 12) { ok = false; }
        fAgain.setError(again.value === pass.value ? "" : t("The passwords do not match."));
        if (again.value !== pass.value) { ok = false; }
        return ok ? pass.value : null;
      }
    };
  }

  // formDialog runs a small form and calls send(values) -> promise of an answer.
  function formDialog(title, fields, submitLabel, send, onDone) {
    var status = h("div", { class: "msg msg-error", role: "alert" });
    var submit = h("button", { class: "btn btn-primary", type: "submit", text: submitLabel });
    var cancel = h("button", { class: "btn", type: "button", text: t("Cancel") });
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

      var create = h("button", { class: "btn btn-primary", type: "button", text: t("New account") });
      create.addEventListener("click", function () {
        var username = h("input", { class: "input", name: "username", required: true, autocomplete: "off", pattern: "[A-Za-z0-9._@\\-]{1,64}" });
        var fUser = LP.field(t("Username"), username, t("Letters, digits and . _ @ -"));
        var pw = passwordFields();
        var role = roleSelect("operator");
        formDialog(t("New admin account"), {
          nodes: [fUser.node].concat(pw.nodes, [LP.field(t("Role"), role, t("An operator does not manage accounts.")).node]),
          value: function () {
            var password = pw.value();
            if (!password || !username.value) { return null; }
            return { username: username.value.trim(), password: password, role: role.value };
          }
        }, t("Create account"), function (v) {
          return LP.api("POST", "/admin-users", v).then(function (res) {
            if (res.ok) { LP.notify("ok", t("Account created"), v.username); }
            return res;
          });
        }, reload);
      });

      root.appendChild(h("div", { class: "page-head" }, [
        h("div", null, [h("h1", { text: t("Admin accounts") }), h("p", { text: t("Who can sign in to the panel, and with which role.") })]),
        create
      ]));

      root.appendChild(LP.table({
        text: function (a) { return a.username + " " + a.role; },
        searchPlaceholder: t("Search account…"),
        emptyText: t("No accounts yet."),
        rows: accounts,
        columns: [
          { label: t("Username"), render: function (a) {
            return h("span", { class: "cell-strong", text: a.username }, [a.username === me.username ? h("span", { class: "muted", text: " " + t("(you)") }) : null]);
          } },
          { label: t("Role"), render: function (a) { return LP.badge(roleLabel(a.role), a.role === "superuser" ? "accent" : ""); } },
          { label: t("Status"), render: function (a) { return a.disabled ? LP.badge(t("blocked"), "danger") : LP.badge(t("active"), "ok"); } },
          { label: t("Created"), render: function (a) { return LP.fmt.date(a.createdAt); } },
          { label: "", render: function (a) { return rowActions(a, me, reload); } }
        ]
      }));

      root.appendChild(h("p", { class: "hint mt-3", text: t("At least one active superuser must always remain. You cannot block your own account.") }));
    });
  }

  function rowActions(a, me, reload) {
    var base = "/admin-users/" + encodeURIComponent(a.username);
    var roleBtn = h("button", { class: "btn btn-sm", type: "button", text: t("Change role") });
    roleBtn.addEventListener("click", function () {
      var role = roleSelect(a.role);
      formDialog(t("Role of {user}", { user: a.username }), {
        nodes: [LP.field(t("Role"), role, t("Superusers manage accounts; operators do not.")).node],
        value: function () { return { role: role.value }; }
      }, t("Save role"), function (v) {
        return LP.api("POST", base + "/role", v).then(function (res) {
          if (res.ok) { LP.notify("ok", t("Role changed"), t("{user} is now {role}", { user: a.username, role: roleLabel(v.role) })); }
          return res;
        });
      }, reload);
    });

    var passBtn = h("button", { class: "btn btn-sm", type: "button", text: t("New password") });
    passBtn.addEventListener("click", function () {
      var pw = passwordFields();
      formDialog(t("New password for {user}", { user: a.username }), {
        nodes: [h("p", { class: "text-[13px] text-ink-2", text: t("Their open sessions will end and they will have to sign in again.") })].concat(pw.nodes),
        value: function () { var p = pw.value(); return p ? { password: p } : null; }
      }, t("Change password"), function (v) {
        return LP.api("POST", base + "/password", v).then(function (res) {
          if (res.ok) { LP.notify("ok", t("Password changed"), a.username); }
          return res;
        });
      }, reload);
    });

    var nodes = [roleBtn, passBtn];
    if (a.username !== me.username) {
      var toggle = h("button", {
        class: "btn btn-sm " + (a.disabled ? "" : "btn-danger"), type: "button",
        text: a.disabled ? t("Activate") : t("Block")
      });
      toggle.addEventListener("click", function () {
        LP.confirm({
          title: a.disabled ? t("Activate {user}?", { user: a.username }) : t("Block {user}?", { user: a.username }),
          text: a.disabled
            ? t("They will be able to sign in with their password again.")
            : t("They will not be able to sign in, and their open sessions will end."),
          confirmLabel: a.disabled ? t("Activate") : t("Block"),
          danger: !a.disabled
        }).then(function (ok) {
          if (!ok) { return; }
          LP.api("POST", base + (a.disabled ? "/enable" : "/disable")).then(function (res) {
            if (res.ok) { LP.notify("ok", a.disabled ? t("Account activated") : t("Account blocked"), a.username); reload(); }
            else { LP.notify("error", t("The account could not be changed"), LP.errorText(res)); }
          });
        });
      });
      nodes.push(toggle);
    }
    return h("div", { class: "actions" }, nodes);
  }

  LP.screens.accounts = { title: "Admin accounts", role: "superuser", render: render };
})();

// My account: who is signed in, and changing the password. The server ends every
// session of the account after a change, so the page returns to the login form.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  function render(root) {
    LP.clear(root);
    var me = LP.me || {};
    root.appendChild(h("div", { class: "page-head" }, [
      h("div", null, [h("h1", { text: t("My account") }), h("p", { text: t("Your details and your password. Only you can change it.") })])
    ]));

    var current = h("input", { class: "input", type: "password", autocomplete: "current-password", required: true });
    var next = h("input", { class: "input", type: "password", autocomplete: "new-password", minlength: "12", required: true });
    var confirmInput = h("input", { class: "input", type: "password", autocomplete: "new-password", required: true });
    var fCurrent = LP.field(t("Current password"), current);
    var fNext = LP.field(t("New password"), next, t("12 characters or more."));
    var fConfirm = LP.field(t("Repeat the new password"), confirmInput);
    var status = h("div", { class: "msg", role: "status" });
    var submit = h("button", { class: "btn btn-primary", type: "submit", text: t("Change password") });

    var form = h("form", { class: "form max-w-md" }, [
      fCurrent.node, fNext.node, fConfirm.node,
      h("div", { class: "flex items-center justify-between gap-3" }, [status, submit])
    ]);

    form.addEventListener("submit", function (event) {
      event.preventDefault();
      var ok = true;
      fCurrent.setError(current.value ? "" : t("Enter your current password."));
      if (!current.value) { ok = false; }
      fNext.setError(next.value.length >= 12 ? "" : t("It must be at least 12 characters."));
      if (next.value.length < 12) { ok = false; }
      fConfirm.setError(confirmInput.value === next.value ? "" : t("The passwords do not match."));
      if (confirmInput.value !== next.value) { ok = false; }
      if (next.value && next.value === current.value) {
        fNext.setError(t("Choose a password different from the current one."));
        ok = false;
      }
      if (!ok) { status.textContent = ""; return; }

      submit.disabled = true;
      status.textContent = t("Changing…");
      status.className = "msg";
      LP.api("POST", "/account/password", { currentPassword: current.value, newPassword: next.value }).then(function (res) {
        current.value = next.value = confirmInput.value = "";
        submit.disabled = false;
        if (!res.ok) {
          status.textContent = LP.errorText(res, t("The password could not be changed."));
          status.className = "msg msg-error";
          return;
        }
        status.textContent = t("Password changed. Sign in again with the new one.");
        status.className = "msg msg-ok";
        setTimeout(function () { window.location.reload(); }, 1500);
      });
    });

    root.appendChild(h("div", { class: "grid-2" }, [
      h("div", { class: "card" }, [
        h("div", { class: "card-head" }, [h("h2", { text: t("Password") })]),
        form
      ]),
      h("div", { class: "card" }, [
        h("div", { class: "card-head" }, [h("h2", { text: t("Session") })]),
        h("dl", { class: "kv" }, [
          h("dt", { text: t("User") }), h("dd", { text: me.username || "—" }),
          h("dt", { text: t("Role") }), h("dd", null, [LP.badge(me.role === "superuser" ? t("Superuser") : t("Operator"), "accent")]),
          h("dt", { text: t("Duration") }), h("dd", { text: t("12 hours from sign-in") })
        ]),
        h("p", { class: "muted mt-4 text-[13px]", text: t("To sign out, use your name in the top bar.") })
      ])
    ]));
  }

  LP.screens.account = { title: "My account", render: render };
})();

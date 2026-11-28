// My account: who is signed in, and changing the password. The server ends every
// session of the account after a change, so the page returns to the login form.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;

  function render(root) {
    LP.clear(root);
    var me = LP.me || {};
    root.appendChild(h("div", { class: "page-head" }, [
      h("div", null, [h("h1", { text: "Mi cuenta" }), h("p", { text: "Tus datos y tu contraseña. Solo tú puedes cambiarla." })])
    ]));

    var current = h("input", { class: "input", type: "password", autocomplete: "current-password", required: true });
    var next = h("input", { class: "input", type: "password", autocomplete: "new-password", minlength: "12", required: true });
    var confirmInput = h("input", { class: "input", type: "password", autocomplete: "new-password", required: true });
    var fCurrent = LP.field("Contraseña actual", current);
    var fNext = LP.field("Contraseña nueva", next, "12 caracteres o más.");
    var fConfirm = LP.field("Repite la contraseña nueva", confirmInput);
    var status = h("div", { class: "msg", role: "status" });
    var submit = h("button", { class: "btn btn-primary", type: "submit", text: "Cambiar contraseña" });

    var form = h("form", { class: "form max-w-md" }, [
      fCurrent.node, fNext.node, fConfirm.node,
      h("div", { class: "flex items-center justify-between gap-3" }, [status, submit])
    ]);

    form.addEventListener("submit", function (event) {
      event.preventDefault();
      var ok = true;
      fCurrent.setError(current.value ? "" : "Escribe tu contraseña actual.");
      if (!current.value) { ok = false; }
      fNext.setError(next.value.length >= 12 ? "" : "Debe tener al menos 12 caracteres.");
      if (next.value.length < 12) { ok = false; }
      fConfirm.setError(confirmInput.value === next.value ? "" : "Las contraseñas no coinciden.");
      if (confirmInput.value !== next.value) { ok = false; }
      if (next.value && next.value === current.value) {
        fNext.setError("Elige una contraseña distinta de la actual.");
        ok = false;
      }
      if (!ok) { status.textContent = ""; return; }

      submit.disabled = true;
      status.textContent = "Cambiando…";
      status.className = "msg";
      LP.api("POST", "/account/password", { currentPassword: current.value, newPassword: next.value }).then(function (res) {
        current.value = next.value = confirmInput.value = "";
        submit.disabled = false;
        if (!res.ok) {
          status.textContent = LP.errorText(res, "No se pudo cambiar la contraseña.");
          status.className = "msg msg-error";
          return;
        }
        status.textContent = "Contraseña cambiada. Vuelve a entrar con la nueva.";
        status.className = "msg msg-ok";
        setTimeout(function () { window.location.reload(); }, 1500);
      });
    });

    root.appendChild(h("div", { class: "grid-2" }, [
      h("div", { class: "card" }, [
        h("div", { class: "card-head" }, [h("h2", { text: "Contraseña" })]),
        form
      ]),
      h("div", { class: "card" }, [
        h("div", { class: "card-head" }, [h("h2", { text: "Sesión" })]),
        h("dl", { class: "kv" }, [
          h("dt", { text: "Usuario" }), h("dd", { text: me.username || "—" }),
          h("dt", { text: "Rol" }), h("dd", null, [LP.badge(me.role === "superuser" ? "Superusuario" : "Operador", "accent")]),
          h("dt", { text: "Duración" }), h("dd", { text: "12 horas desde el inicio de sesión" })
        ]),
        h("p", { class: "muted mt-4 text-[13px]", text: "Para salir usa «Cerrar sesión» en la parte superior." })
      ])
    ]));
  }

  LP.screens.account = { title: "Mi cuenta", render: render };
})();

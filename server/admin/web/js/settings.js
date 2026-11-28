// Settings. Only what the panel really changes is offered here. Server settings live in
// config.yml and are read when the server starts; the API does not expose them yet.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;

  function themeChoice() {
    var current = document.documentElement.getAttribute("data-theme") || "light";
    var options = [["light", "Claro"], ["dark", "Oscuro"]];
    var group = h("div", { class: "flex gap-2", role: "radiogroup", "aria-label": "Tema" });
    options.forEach(function (o) {
      var active = o[0] === current;
      var btn = h("button", { class: "btn " + (active ? "btn-primary" : ""), type: "button", "aria-pressed": active ? "true" : "false", text: o[1] });
      btn.addEventListener("click", function () {
        document.documentElement.setAttribute("data-theme", o[0]);
        try { window.localStorage.setItem("lipstick-theme", o[0]); } catch (e) { /* not kept */ }
        LP.screens.settings.render(document.getElementById("view"));
      });
      group.appendChild(btn);
    });
    return group;
  }

  function render(root) {
    LP.clear(root);
    root.appendChild(h("div", { class: "page-head" }, [
      h("div", null, [h("h1", { text: "Configuración" }), h("p", { text: "Lo que puedes cambiar desde el panel, y lo que vive en el servidor." })])
    ]));

    root.appendChild(h("div", { class: "grid-2" }, [
      h("div", { class: "card" }, [
        h("div", { class: "card-head" }, [h("h2", { text: "Apariencia" })]),
        h("p", { class: "muted mb-3 text-[13px]", text: "Tema de la interfaz. Se guarda solo en este navegador." }),
        themeChoice()
      ]),
      h("div", { class: "card" }, [
        h("div", { class: "card-head" }, [h("h2", { text: "Se administra desde el panel" })]),
        h("ul", { class: "grid gap-2 text-[13.5px]" }, [
          h("li", null, [h("a", { href: "#/domains", text: "Dominios" }), h("span", { class: "muted", text: " · clave, passthrough, redes permitidas y restricción de visitantes" })]),
          h("li", null, [h("a", { href: "#/visitors", text: "Usuarios visitantes" }), h("span", { class: "muted", text: " · crear, rotar el secreto y borrar" })]),
          h("li", null, [h("a", { href: "#/account", text: "Mi cuenta" }), h("span", { class: "muted", text: " · tu contraseña" })]),
          LP.me && LP.me.role === "superuser"
            ? h("li", null, [h("a", { href: "#/accounts", text: "Cuentas de administrador" }), h("span", { class: "muted", text: " · roles, bloqueos y contraseñas" })])
            : null
        ])
      ])
    ]));

    root.appendChild(h("div", { class: "card mt-4" }, [
      h("div", { class: "card-head" }, [h("h2", { text: "Requiere el archivo de configuración" })]),
      h("p", { class: "muted mb-3 text-[13px]", text: "Estos valores se leen al arrancar el servidor. Cámbialos en config.yml y reinicia." }),
      h("dl", { class: "kv" }, [
        h("dt", { text: "Puertos y direcciones" }), h("dd", { text: "proxy, manager, admin y los puertos de intranet" }),
        h("dt", { text: "TLS" }), h("dd", { text: "certificado y clave del servidor" }),
        h("dt", { text: "Base de datos" }), h("dd", { text: "conexión a PostgreSQL" }),
        h("dt", { text: "NATS" }), h("dd", { text: "bus del cluster, si lo usas" })
      ])
    ]));

    root.appendChild(h("div", { class: "card mt-4" }, [
      h("div", { class: "card-head" }, [h("h2", { text: "Pendiente en el servidor" })]),
      h("p", { class: "muted text-[13px]", text: "El panel todavía no puede: cerrar todas tus sesiones a la vez, mostrar la versión y el tiempo activo del servidor, ni consultar un registro de cambios de cuentas. Están listados en revision.md, punto 61." })
    ]));
  }

  LP.screens.settings = { title: "Configuración", render: render };
})();

// Settings. Only what the panel really changes is offered here. Server settings live in
// config.yml and are read when the server starts; the API does not expose them yet.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  function languageChoice() {
    var group = h("div", { class: "flex gap-2", role: "radiogroup", "aria-label": t("Language") });
    LP.languages.forEach(function (l) {
      var active = l[0] === LP.lang;
      group.appendChild(h("button", {
        class: "btn " + (active ? "btn-primary" : ""), type: "button",
        "aria-pressed": active ? "true" : "false", text: l[1],
        on: { click: function () { LP.setLang(l[0]); } }
      }));
    });
    return group;
  }

  function render(root) {
    LP.clear(root);
    root.appendChild(h("div", { class: "page-head" }, [
      h("div", null, [h("h1", { text: t("Settings") }), h("p", { text: t("What you can change from the panel, and what lives on the server.") })])
    ]));

    root.appendChild(h("div", { class: "grid-2" }, [
      h("div", { class: "card" }, [
        h("div", { class: "card-head" }, [h("h2", { text: t("Language") })]),
        h("p", { class: "muted mb-3 text-[13px]", text: t("The language of the panel. It is kept in this browser only.") }),
        languageChoice()
      ]),
      h("div", { class: "card" }, [
        h("div", { class: "card-head" }, [h("h2", { text: t("Managed from the panel") })]),
        h("ul", { class: "grid gap-2 text-[13.5px]" }, [
          h("li", null, [h("a", { href: "#/domains", text: t("Domains") }), h("span", { class: "muted", text: " · " + t("key, passthrough, allowed networks and visitor restriction") })]),
          h("li", null, [h("a", { href: "#/visitors", text: t("Visitor users") }), h("span", { class: "muted", text: " · " + t("create, rotate the secret and delete") })]),
          h("li", null, [h("a", { href: "#/account", text: t("My account") }), h("span", { class: "muted", text: " · " + t("your password") })]),
          LP.me && LP.me.role === "superuser"
            ? h("li", null, [h("a", { href: "#/accounts", text: t("Admin accounts") }), h("span", { class: "muted", text: " · " + t("roles, blocks and passwords") })])
            : null
        ])
      ])
    ]));

    root.appendChild(h("div", { class: "card mt-4" }, [
      h("div", { class: "card-head" }, [h("h2", { text: t("Set in the configuration file") })]),
      h("p", { class: "muted mb-3 text-[13px]", text: t("These values are read when the server starts. Change them in config.yml and restart.") }),
      h("dl", { class: "kv" }, [
        h("dt", { text: t("Ports and addresses") }), h("dd", { text: t("proxy, manager, admin and the intranet ports") }),
        h("dt", { text: "TLS" }), h("dd", { text: t("the server's certificate and key") }),
        h("dt", { text: t("Database") }), h("dd", { text: t("the PostgreSQL connection") }),
        h("dt", { text: "NATS" }), h("dd", { text: t("the cluster bus, if you use one") })
      ])
    ]));

    root.appendChild(h("div", { class: "card mt-4" }, [
      h("div", { class: "card-head" }, [h("h2", { text: t("Pending on the server") })]),
      h("p", { class: "muted text-[13px]", text: t("The panel cannot yet: close all your sessions at once, show the server's version and uptime, or read a log of account changes. They are listed in revision.md, item 61.") })
    ]));
  }

  LP.screens.settings = { title: "Settings", render: render };
})();

// Starts the panel: reads who is signed in, builds the navigation for that role,
// and routes the hash (#/domains/example.com) to a screen.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;

  var NAV = [
    { group: "Operación", items: [
      { id: "dashboard", label: "Panel", icon: "◎" },
      { id: "domains", label: "Dominios", icon: "▤" },
      { id: "traffic", label: "Tráfico y peticiones", icon: "⇅" },
      { id: "visitors", label: "Usuarios visitantes", icon: "◉" }
    ] },
    { group: "Cuenta", items: [
      { id: "account", label: "Mi cuenta", icon: "○" },
      { id: "accounts", label: "Cuentas de administrador", icon: "◆", role: "superuser" },
      { id: "settings", label: "Configuración", icon: "⚙" }
    ] }
  ];

  function groupOf(id) {
    for (var g = 0; g < NAV.length; g++) {
      for (var i = 0; i < NAV[g].items.length; i++) {
        if (NAV[g].items[i].id === id) { return { group: NAV[g].group, item: NAV[g].items[i] }; }
      }
    }
    return null;
  }

  function buildNav(role) {
    var nav = document.getElementById("nav");
    LP.clear(nav);
    NAV.forEach(function (group) {
      var links = group.items.filter(function (item) { return !item.role || item.role === role; });
      if (links.length === 0) { return; }
      nav.appendChild(h("div", { class: "nav-group", text: group.group }));
      links.forEach(function (item) {
        nav.appendChild(h("a", { class: "nav-link", href: "#/" + item.id, "data-screen": item.id }, [
          h("span", { class: "w-4 text-center opacity-80", "aria-hidden": "true", text: item.icon }),
          h("span", { text: item.label })
        ]));
      });
    });
  }

  function route() {
    var raw = (window.location.hash || "#/dashboard").replace(/^#\/?/, "");
    var parts = raw.split("/").map(function (p) {
      try { return decodeURIComponent(p); } catch (e) { return p; }
    });
    var name = parts[0] || "dashboard";
    var screen = LP.screens[name];
    if (!screen) { name = "dashboard"; screen = LP.screens.dashboard; parts = [name]; }

    var view = document.getElementById("view");
    LP.clear(view);
    document.querySelectorAll("#nav a").forEach(function (a) {
      if (a.getAttribute("data-screen") === name) { a.setAttribute("aria-current", "page"); }
      else { a.removeAttribute("aria-current"); }
    });
    var where = groupOf(name);
    LP.setCrumb(where ? where.group : "Lipstick", screen.title);
    window.scrollTo(0, 0);

    if (screen.role && LP.me && LP.me.role !== screen.role) {
      view.appendChild(LP.state("Esta sección es solo para superusuarios.",
        "Tu rol es " + LP.me.role + ". Si necesitas acceso, pídelo a un superusuario."));
      return;
    }
    try {
      screen.render(view, parts.slice(1));
    } catch (err) {
      view.appendChild(LP.state("Algo salió mal al mostrar esta pantalla.", "Recarga la página para intentarlo de nuevo."));
    }
    view.focus({ preventScroll: true });
  }

  function paintMe(me) {
    LP.me = me;
    document.getElementById("me-name").textContent = me.username;
    document.getElementById("me-role").textContent = me.role === "superuser" ? "Superusuario" : "Operador";
    document.getElementById("avatar").textContent = (me.username || "?").charAt(0);
  }

  document.getElementById("logout").addEventListener("click", function () {
    LP.api("POST", "/logout").then(function () { window.location.reload(); });
  });

  document.getElementById("theme-toggle").addEventListener("click", function () {
    var root = document.documentElement;
    var next = root.getAttribute("data-theme") === "dark" ? "light" : "dark";
    root.setAttribute("data-theme", next);
    try { window.localStorage.setItem("lipstick-theme", next); } catch (e) { /* the choice simply is not kept */ }
  });

  LP.api("GET", "/account").then(function (res) {
    if (!res.ok) { window.location.reload(); return; }
    paintMe(res.data);
    buildNav(res.data.role);
    window.addEventListener("hashchange", route);
    route();
  });
})();

// Starts the panel: reads who is signed in, builds the navigation for that role, routes
// the hash (#/domains/example.com) to a screen, and runs the account menu.
(function () {
  "use strict";
  var LP = window.LP;
  var h = LP.h;
  var t = LP.t;

  // The navigation is built from keys so that it can be rebuilt in the other language.
  var NAV = [
    { group: "Operations", items: [
      { id: "dashboard", label: "Dashboard", icon: "◎" },
      { id: "domains", label: "Domains", icon: "▤" },
      { id: "traffic", label: "Traffic and requests", icon: "⇅" },
      { id: "visitors", label: "Visitor users", icon: "◉" }
    ] },
    { group: "Account", items: [
      { id: "account", label: "My account", icon: "○" },
      { id: "accounts", label: "Admin accounts", icon: "◆", role: "superuser" },
      { id: "settings", label: "Settings", icon: "⚙" }
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
      nav.appendChild(h("div", { class: "nav-group", text: t(group.group) }));
      links.forEach(function (item) {
        nav.appendChild(h("a", { class: "nav-link", href: "#/" + item.id, "data-screen": item.id }, [
          h("span", { class: "w-4 text-center opacity-80", "aria-hidden": "true", text: item.icon }),
          h("span", { text: t(item.label) })
        ]));
      });
    });
  }

  function roleLabel(role) {
    return role === "superuser" ? t("Superuser") : t("Operator");
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
    LP.setCrumb(where ? t(where.group) : "Lipstick", t(screen.title));
    window.scrollTo(0, 0);

    if (screen.role && LP.me && LP.me.role !== screen.role) {
      view.appendChild(LP.state(t("This section is for superusers only."),
        t("Your role is {role}. Ask a superuser if you need access.", { role: roleLabel(LP.me.role) })));
      return;
    }
    try {
      screen.render(view, parts.slice(1));
    } catch (err) {
      view.appendChild(LP.state(t("Something went wrong showing this screen."), t("Reload the page to try again.")));
    }
    view.focus({ preventScroll: true });
  }

  function paintMe(me) {
    LP.me = me;
    document.getElementById("me-name").textContent = me.username;
    document.getElementById("me-role").textContent = roleLabel(me.role);
    document.getElementById("avatar").textContent = (me.username || "?").charAt(0);
    document.getElementById("menu-name").textContent = me.username;
    document.getElementById("menu-role").textContent = roleLabel(me.role);
  }

  // ---- account menu -----------------------------------------------------------
  var menu = document.getElementById("user-menu");
  var menuButton = document.getElementById("user-button");

  function setMenu(open) {
    menu.hidden = !open;
    menuButton.setAttribute("aria-expanded", open ? "true" : "false");
    if (open) { menu.querySelector("a, button").focus(); }
  }

  function buildLanguageItems() {
    var box = document.getElementById("menu-languages");
    LP.clear(box);
    LP.languages.forEach(function (l) {
      var active = l[0] === LP.lang;
      box.appendChild(h("button", {
        class: "menu-item" + (active ? " menu-item-active" : ""), type: "button", role: "menuitemradio",
        "aria-checked": active ? "true" : "false", text: l[1],
        on: { click: function () { LP.setLang(l[0]); window.location.reload(); } }
      }));
    });
  }

  menuButton.addEventListener("click", function () { setMenu(menu.hidden); });
  document.addEventListener("click", function (event) {
    // The button's children (name, icon) count as the button, or the click would close the menu it opened.
    if (!menu.hidden && !menu.contains(event.target) && !menuButton.contains(event.target)) { setMenu(false); }
  });
  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape" && !menu.hidden) { setMenu(false); menuButton.focus(); }
  });
  menu.addEventListener("click", function (event) {
    if (event.target.getAttribute("data-action") === "account") { window.location.hash = "#/account"; setMenu(false); }
  });

  document.getElementById("logout").addEventListener("click", function () {
    LP.api("POST", "/logout").then(function () { window.location.reload(); });
  });
  document.getElementById("brand").addEventListener("click", function (event) {
    event.preventDefault();
    window.location.hash = "#/dashboard";
  });

  // The language can change without a reload of the data: the screen is drawn again.
  document.addEventListener("lp:lang", function () {
    buildLanguageItems();
    if (LP.me) { buildNav(LP.me.role); paintMe(LP.me); }
    route();
  });

  LP.api("GET", "/account").then(function (res) {
    if (!res.ok) { window.location.reload(); return; }
    paintMe(res.data);
    buildNav(res.data.role);
    buildLanguageItems();
    window.addEventListener("hashchange", route);
    route();
  });
})();

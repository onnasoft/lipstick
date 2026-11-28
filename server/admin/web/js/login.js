// The login form. The server sets the session cookie on a successful answer, so the
// script never sees the token. X-Lipstick-CSRF is required by the server for cookie
// logins: a page on another site cannot add it.
(function () {
  "use strict";
  var form = document.getElementById("login-form");
  var message = document.getElementById("login-error");
  var submit = document.getElementById("login-submit");

  form.addEventListener("submit", function (event) {
    event.preventDefault();
    message.textContent = "";
    submit.disabled = true;
    submit.textContent = "Entrando…";
    var body = JSON.stringify({ username: form.username.value, password: form.password.value });
    form.password.value = "";
    fetch("/web/login", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-Lipstick-CSRF": "1" },
      body: body
    }).then(function (response) {
      if (response.ok) { window.location.reload(); return; }
      return response.json().catch(function () { return {}; }).then(function (data) {
        message.textContent = response.status === 429
          ? "Demasiados intentos. Espera unos minutos."
          : (data.error || "No se pudo entrar.");
        reset();
      });
    }, function () {
      message.textContent = "No hay conexión con el servidor.";
      reset();
    });
  });

  function reset() {
    submit.disabled = false;
    submit.textContent = "Entrar";
  }
})();

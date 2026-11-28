// The login form. The server sets the session cookie on a successful answer, so
// the script never sees the token. The header X-Lipstick-CSRF is required by the
// server for cookie logins: a page on another site cannot add it.
(function () {
  var form = document.getElementById("login-form");
  var message = document.getElementById("login-error");
  form.addEventListener("submit", function (event) {
    event.preventDefault();
    message.textContent = "";
    var body = JSON.stringify({
      username: form.username.value,
      password: form.password.value
    });
    form.password.value = "";
    fetch("/web/login", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Lipstick-CSRF": "1" },
      body: body
    }).then(function (response) {
      if (response.ok) { window.location.reload(); return; }
      return response.json().then(function (data) {
        message.textContent = data.error || "No se pudo entrar.";
      }, function () {
        message.textContent = "No se pudo entrar.";
      });
    }, function () {
      message.textContent = "No hay conexión con el servidor.";
    });
  });
})();

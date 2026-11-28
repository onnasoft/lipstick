// Runs in the page head, before the first paint, so a dark page never flashes light.
// The theme is a display preference; nothing sensitive is stored in the browser.
(function () {
  var saved = null;
  try { saved = window.localStorage.getItem("lipstick-theme"); } catch (e) { saved = null; }
  var dark = saved ? saved === "dark" : window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
})();

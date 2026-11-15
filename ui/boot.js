// Si algo falla, se muestra en la ventana en lugar de quedar en blanco.
(function () {
  function show(msg) {
    var box = document.getElementById("fatal");
    if (!box) {
      box = document.createElement("div");
      box.id = "fatal";
      document.body.appendChild(box);
    }
    box.textContent = "Algo falló en la ventana de Resguardo: " + msg + ". Cerrala y volvé a abrirla; si sigue pasando, mandá una captura de este mensaje.";
  }
  window.addEventListener("error", function (e) { show(e.message || "error desconocido"); });
  window.addEventListener("unhandledrejection", function (e) { show((e.reason && e.reason.message) || String(e.reason)); });
})();

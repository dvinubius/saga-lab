const refreshLive = (next) => {
  localTimes(next);
  [...next.querySelectorAll("[data-live]")].reverse().forEach((fresh) => {
    const current = document.querySelector(`[data-live="${fresh.dataset.live}"]`);
    if (current && current.outerHTML !== fresh.outerHTML) current.replaceWith(document.importNode(fresh, true));
  });
  checkDiagramView(document);
  next.querySelectorAll("body script[src]").forEach((script) => {
    const src = script.getAttribute("src");
    if (document.querySelector(`script[src="${src}"]`)) return;
    document.body.append(Object.assign(document.createElement("script"), { src }));
  });
};

addEventListener("submit", async (event) => {
  const form = event.target;
  if (!form.matches("form[data-in-place]")) return;
  event.preventDefault();
  form.querySelectorAll("button").forEach((button) => (button.disabled = true));
  let response, text;
  try {
    response = await fetch(form.action, { method: "POST", body: new URLSearchParams(new FormData(form)) });
    text = await response.text();
  } catch {
    location.reload();
    return;
  }
  if (response.ok || response.status === 409) {
    refreshLive(new DOMParser().parseFromString(text, "text/html"));
  } else {
    document.open();
    document.write(text);
    document.close();
  }
});

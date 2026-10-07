(() => {
  const poll = async () => {
    let next;
    try {
      const response = await fetch(location.href, { cache: "no-store" });
      if (!response.ok) throw new Error(response.statusText);
      next = new DOMParser().parseFromString(await response.text(), "text/html");
    } catch {
      setTimeout(poll, 1000);
      return;
    }
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
    if (next.querySelector('script[src="/static/poll.js"]')) setTimeout(poll, 1000);
  };
  setTimeout(poll, 1000);
})();

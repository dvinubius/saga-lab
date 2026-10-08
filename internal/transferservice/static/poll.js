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
    refreshLive(next);
    if (next.querySelector('script[src="/static/poll.js"]')) setTimeout(poll, 1000);
  };
  setTimeout(poll, 1000);
})();

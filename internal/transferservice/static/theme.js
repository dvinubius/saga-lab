(() => {
  const key = "saga-lab.theme";
  const root = document.documentElement;
  try {
    if (localStorage.getItem(key) === "light") root.dataset.theme = "light";
  } catch {}

  document.addEventListener("DOMContentLoaded", () => {
    const toggle = document.querySelector(".theme-toggle");
    if (!toggle) return;
    const describe = () => {
      const label = root.dataset.theme === "dark" ? "Switch to light theme" : "Switch to dark theme";
      toggle.setAttribute("aria-label", label);
      toggle.title = label;
    };
    describe();
    toggle.addEventListener("click", () => {
      root.dataset.theme = root.dataset.theme === "dark" ? "light" : "dark";
      try {
        localStorage.setItem(key, root.dataset.theme);
      } catch {}
      describe();
    });
  });
})();

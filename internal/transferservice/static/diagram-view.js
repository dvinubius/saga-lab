(() => {
  const views = ["overview", "detailed", "sequence"];
  let view = "overview";
  try {
    const stored = localStorage.getItem("saga-lab.diagram-view");
    if (views.includes(stored)) view = stored;
  } catch {}
  document.documentElement.dataset.view = view;
  addEventListener("DOMContentLoaded", () =>
    document.querySelectorAll('[name="diagram-view"]').forEach((radio) => {
      radio.checked = radio.value === view;
      radio.addEventListener("change", () => {
        document.documentElement.dataset.view = radio.value;
        try {
          localStorage.setItem("saga-lab.diagram-view", radio.value);
        } catch {}
      });
    })
  );
})();

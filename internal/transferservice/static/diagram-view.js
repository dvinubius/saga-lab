(() => {
  const views = ["overview", "detailed", "sequence"];
  let view = "overview";
  try {
    const stored = localStorage.getItem("saga-lab.diagram-view");
    if (views.includes(stored)) view = stored;
  } catch {}
  document.documentElement.dataset.view = view;
  addEventListener("change", ({ target }) => {
    if (target.name !== "diagram-view") return;
    document.documentElement.dataset.view = target.value;
    try {
      localStorage.setItem("saga-lab.diagram-view", target.value);
    } catch {}
  });
})();

const checkDiagramView = (root) =>
  root.querySelectorAll('[name="diagram-view"]').forEach((radio) => (radio.checked = radio.value === document.documentElement.dataset.view));

addEventListener("DOMContentLoaded", () => checkDiagramView(document));

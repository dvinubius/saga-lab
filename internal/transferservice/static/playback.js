(() => {
  const panel = document.getElementById("playback");
  const rows = [...document.querySelectorAll(".history tbody tr")];
  const entry = panel.querySelector(".playback-entry");
  const note = panel.querySelector(".playback-note");
  const time = panel.querySelector(".playback-time");
  const gap = panel.querySelector(".playback-gap");
  const back = panel.querySelector('[data-control="back"]');
  const play = panel.querySelector('[data-control="play"]');
  const forward = panel.querySelector('[data-control="forward"]');
  const diagram = panel.querySelector(".diagram");
  const track = panel.querySelector(".timeline-track");
  const progress = panel.querySelector(".timeline-progress");
  const starts = [];
  rows.reduce((at, row) => (starts.push(at), at + Number(row.dataset.dwell)), 0);
  const span = starts[starts.length - 1] || 1;
  const ticks = starts.map((start) => {
    const tick = document.createElement("span");
    tick.className = "timeline-tick";
    tick.style.left = `${(start / span) * 100}%`;
    track.append(tick);
    return tick;
  });
  let position = 0;
  let timer = null;

  const showRow = (row) => {
    const content = row.querySelector("td:not(:first-child):not(:empty)").cloneNode(true);
    const about = content.querySelector("[popover]");
    content.querySelectorAll(".about, [popover]").forEach((element) => element.remove());
    entry.replaceChildren(...content.childNodes);
    entry.dataset.observation = row.dataset.observation || "";
    note.textContent = about ? about.textContent : "";
    time.textContent = row.dataset.observedAt;
    gap.textContent = row.dataset.gap ? `${row.dataset.gap} to the next entry` : "last entry";
    const path = row.dataset.path ? row.dataset.path.split(" ") : [];
    diagram.querySelectorAll("[data-node]").forEach((node) => node.toggleAttribute("data-lit", path.includes(node.dataset.node)));
    diagram.querySelectorAll("[data-edge]").forEach((edge) => delete edge.dataset.lit);
    path.slice(1).forEach((to, index) => {
      const from = path[index];
      const forward = diagram.querySelector(`[data-edge="${from} ${to}"]`);
      (forward || diagram.querySelector(`[data-edge="${to} ${from}"]`)).dataset.lit = forward ? "forward" : "backward";
    });
    diagram.toggleAttribute("data-no-consumer", "noConsumer" in row.dataset);
  };

  const render = () => {
    rows.forEach((row, index) => {
      row.dataset.playback = ticks[index].dataset.playback = index < position ? "played" : index === position ? "current" : "unplayed";
    });
    const current = Math.min(position, rows.length - 1);
    progress.style.width = `${(starts[current] / span) * 100}%`;
    showRow(rows[current]);
    back.disabled = position === 0;
    forward.disabled = position === rows.length;
    const [state, label] = timer ? ["pause", "Pause"] : position === rows.length ? ["replay", "Replay"] : ["play", "Play"];
    play.dataset.state = state;
    play.setAttribute("aria-label", label);
    play.title = label;
  };

  const schedule = () => {
    timer = setTimeout(() => {
      position += 1;
      timer = null;
      if (position < rows.length) schedule();
      render();
    }, Number(rows[position].dataset.dwell));
  };

  const pause = () => {
    clearTimeout(timer);
    timer = null;
  };

  const step = (to) => {
    pause();
    position = to;
    render();
  };

  play.addEventListener("click", () => {
    if (timer) {
      pause();
    } else {
      if (position === rows.length) position = 0;
      schedule();
    }
    render();
  });
  back.addEventListener("click", () => step(Math.max(position - 1, 0)));
  forward.addEventListener("click", () => step(Math.min(position + 1, rows.length)));

  if ("autoplay" in panel.dataset) {
    history.replaceState(null, "", location.pathname);
    schedule();
  }
  render();
})();

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
  };

  const render = () => {
    rows.forEach((row, index) => {
      row.dataset.playback = index < position ? "played" : index === position ? "current" : "unplayed";
    });
    showRow(rows[Math.min(position, rows.length - 1)]);
    back.disabled = position === 0;
    forward.disabled = position === rows.length;
    play.textContent = timer ? "Pause" : position === rows.length ? "Replay" : "Play";
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

  schedule();
  render();
})();

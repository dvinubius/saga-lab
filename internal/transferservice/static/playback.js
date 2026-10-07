(() => {
  const panel = document.getElementById("playback");
  const rows = [...document.querySelectorAll(".history tbody tr")];
  const n = rows.length;
  const entry = panel.querySelector(".playback-entry");
  const note = panel.querySelector(".playback-note");
  const time = panel.querySelector(".playback-time");
  const gap = panel.querySelector(".playback-gap");
  const reset = panel.querySelector('[data-control="reset"]');
  const back = panel.querySelector('[data-control="back"]');
  const play = panel.querySelector('[data-control="play"]');
  const forward = panel.querySelector('[data-control="forward"]');
  const end = panel.querySelector('[data-control="end"]');
  const track = panel.querySelector(".timeline-track");
  const progress = panel.querySelector(".timeline-progress");

  const slowed = (row) => row.dataset.observation !== "DeliveryWaiting";
  const dwell = rows.map((row) => Number(row.dataset.dwell) * (slowed(row) ? 1.75 : 1));
  const starts = [];
  dwell.reduce((at, d) => (starts.push(at), at + d), 0);
  const span = starts[n - 1] || 1;
  const ticks = starts.map((start) => {
    const tick = document.createElement("span");
    tick.className = "timeline-tick";
    tick.style.left = `${(start / span) * 100}%`;
    track.append(tick);
    return tick;
  });
  const paths = rows.map((row) => (row.dataset.path ? row.dataset.path.split(" ") : []));
  const laneNodes = ["transfer-service", "bank-a", "bank-b"];
  const lanes = rows.map((row) => laneNodes[[...row.cells].slice(1).findIndex((td) => !td.matches(":empty"))]);
  const observation = (i) => rows[i]?.dataset.observation;
  const motionFor = (i) => Math.min(Math.max(Number(rows[i].dataset.dwell) * 0.75, 450), 1400) * (slowed(rows[i]) ? 1.75 : 1);
  const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);
  const clamp01 = (t) => Math.min(Math.max(t, 0), 1);

  const tones = { DuplicateSuppressed: "teal", DeliveryResumed: "teal" };
  const destOf = (nodes, k, obs) => {
    if (nodes[k + 1] !== "broker") return "";
    if (nodes[k + 2]) return nodes[k + 2];
    if (nodes[k] === "transfer-service") return obs === "CreditConfirmed" ? "bank-b" : "bank-a";
    return "transfer-service";
  };
  const own = (nodes, obs) => nodes.slice(1).map((to, k) => ({ from: nodes[k], to, dest: destOf(nodes, k, obs), tone: tones[obs], animate: true }));
  const still = (hops) => hops.map((hop) => ({ ...hop, animate: false }));
  const continuesResumed = (i) => observation(i - 1) === "DeliveryResumed" && paths[i].join(" ") === "transfer-service broker bank-b";
  const scenes = [];
  rows.forEach((row, i) => {
    const obs = observation(i);
    const p = paths[i];
    const prev = scenes[i - 1];
    if (row.dataset.step === "requested") scenes.push({ hops: own(["client", "transfer-service"]), lit: ["transfer-service"] });
    else if (obs === "NackRequested") scenes.push({ hops: [], fail: lanes[i], lit: [lanes[i]] });
    else if (obs === "DeliveryWaiting") scenes.push({ hops: still(prev.hops), park: "broker", lit: ["broker"] });
    else if (obs === "DeliveryResumed") scenes.push({ hops: [...still(prev.hops), ...own(p, obs)], lit: p });
    else if (continuesResumed(i)) scenes.push({ hops: still(prev.hops), lit: p });
    else scenes.push({ hops: own(p, obs), lit: p, park: p.length === 1 ? p[0] : null });
  });
  const animated = (i) => scenes[i].hops.filter((hop) => hop.animate);
  const hopProgress = (i, m) => clamp01(m / motionFor(i)) * animated(i).length;
  const litNodes = (i, m) => {
    const t = hopProgress(i, m);
    const pending = animated(i).filter((_, k) => t < k + 1 - 1e-6).map((hop) => hop.to);
    return scenes[i].lit.filter((node) => !pending.includes(node));
  };

  const smOf = { requested: "debit", credit_requested: "credit", finished: "done", transfer_rejected: "rejected", credit_rejected: "credit-rejected", refund_requested: "refund", transfer_refunded: "refunded" };
  const sagaStates = [];
  rows.reduce((acc, row) => {
    const state = smOf[row.dataset.step];
    const next = state ? { current: state, visited: [...acc.visited, state] } : acc;
    sagaStates.push(next);
    return next;
  }, { current: null, visited: [] });

  const balanceOf = rows.map((row, i) => {
    const numbers = (row.querySelector(".balance")?.textContent.match(/-?\d+/g) || []).map(Number);
    return numbers.length ? { bank: lanes[i], from: numbers[0], to: numbers.at(-1) } : null;
  });
  const balanceAt = (bank, i, m) => {
    for (let j = i; j >= 0; j--) {
      const b = balanceOf[j];
      if (b && b.bank === bank) {
        if (j < i) return b.to;
        return Math.round(b.from + (b.to - b.from) * ease(clamp01((m - motionFor(i)) / 500)));
      }
    }
    const first = balanceOf.find((b) => b && b.bank === bank);
    return first ? first.from : null;
  };

  const graphs = [...panel.querySelectorAll("svg[data-graph]")].map((svg) => {
    const traces = new Map();
    svg.querySelectorAll("[data-edge],[data-route]").forEach((el) => {
      const trace = el.cloneNode();
      trace.removeAttribute("data-edge");
      trace.removeAttribute("data-route");
      trace.setAttribute("class", "trace");
      trace.setAttribute("pathLength", "1");
      el.after(trace);
      traces.set(el, trace);
    });
    const kind = svg.dataset.graph;
    const edgeOf = (hop) => {
      if (kind === "detailed") return { el: svg.querySelector(`[data-route="${hop.from}>${hop.to}${hop.dest ? "@" + hop.dest : ""}"]`), reverse: false };
      const fwd = svg.querySelector(`[data-edge="${hop.from} ${hop.to}"]`);
      return fwd ? { el: fwd, reverse: false } : { el: svg.querySelector(`[data-edge="${hop.to} ${hop.from}"]`), reverse: true };
    };
    if (kind === "detailed" && rows.some((row) => row.dataset.step === "credit_rejected")) svg.querySelector('[data-sm="credit-rejected"]').removeAttribute("hidden");
    return { svg, kind, traces, edgeOf, head: svg.querySelector(".head"), fails: [...svg.querySelectorAll("[data-fail]")] };
  });

  const arrivalOf = (g, i) => {
    const hop = i >= 0 && animated(i).at(-1);
    if (!hop) return null;
    const { el, reverse } = g.edgeOf(hop);
    return el && el.getPointAtLength(reverse ? 0 : el.getTotalLength());
  };

  const renderGraph = (g, i, m) => {
    const scene = scenes[i];
    const anim = animated(i);
    const t = hopProgress(i, m);
    g.traces.forEach((trace) => {
      trace.style.strokeDasharray = "0 1";
      delete trace.dataset.tone;
    });
    let head = null;
    scene.hops.forEach((hop) => {
      const { el, reverse } = g.edgeOf(hop);
      if (!el) return;
      const k = anim.indexOf(hop);
      const f = k < 0 ? 1 : ease(clamp01(t - k));
      if (k >= 0 && k === Math.min(Math.floor(t), anim.length - 1)) head = { el, reverse, f, tone: hop.tone };
      if (f <= 0) return;
      const trace = g.traces.get(el);
      trace.style.strokeDasharray = `${f} 1`;
      trace.style.strokeDashoffset = reverse ? `${-(1 - f)}` : "0";
      if (hop.tone) trace.dataset.tone = hop.tone;
    });
    if (head) {
      const length = head.el.getTotalLength();
      const tip = head.el.getPointAtLength((head.reverse ? 1 - head.f : head.f) * length);
      g.head.setAttribute("transform", `translate(${tip.x} ${tip.y})`);
      g.head.style.opacity = 1;
    } else {
      const park = scene.park && g.svg.querySelector(`[data-park="${scene.park}"]`);
      if (park) {
        const to = { x: Number(park.getAttribute("cx")), y: Number(park.getAttribute("cy")) };
        const from = arrivalOf(g, i - 1) || to;
        const f = ease(clamp01(m / motionFor(i)));
        g.head.setAttribute("transform", `translate(${from.x + (to.x - from.x) * f} ${from.y + (to.y - from.y) * f})`);
      }
      g.head.style.opacity = park ? 1 : 0;
    }
    g.fails.forEach((fail) => {
      const f = fail.dataset.fail === scene.fail ? ease(clamp01(m / motionFor(i))) : 0;
      fail.querySelector(".fail-line").style.strokeDasharray = `${f} 1`;
      fail.querySelector(".fail-bar").style.visibility = f >= 1 ? "visible" : "hidden";
    });
    const lit = litNodes(i, m);
    g.svg.querySelectorAll("[data-node]").forEach((node) => node.toggleAttribute("data-lit", lit.includes(node.dataset.node)));
    g.svg.toggleAttribute("data-no-consumer", "noConsumer" in rows[i].dataset);
    if (g.kind === "detailed") {
      const saga = sagaStates[i];
      g.svg.querySelectorAll("[data-sm]").forEach((state) => {
        state.toggleAttribute("data-visited", saga.visited.includes(state.dataset.sm));
        state.toggleAttribute("data-current", saga.current === state.dataset.sm);
      });
      g.svg.querySelectorAll("[data-balance]").forEach((label) => {
        const value = balanceAt(label.dataset.balance, i, m);
        label.textContent = value === null ? "—" : `${value} credits`;
      });
    }
  };

  const seq = panel.querySelector('[data-diagram="sequence"]');
  const seqBody = seq.querySelector(".seq-body");
  const seqWindow = seq.querySelector(".seq-window");
  const lifelineX = { client: 10, "transfer-service": 75, broker: 243, "bank-a": 416, "bank-b": 559 };
  const svgNS = "http://www.w3.org/2000/svg";
  const make = (parent, tag, attrs, text) => {
    const el = document.createElementNS(svgNS, tag);
    Object.entries(attrs).forEach(([k, v]) => v !== undefined && el.setAttribute(k, v));
    if (text) el.textContent = text;
    parent.append(el);
    return el;
  };
  const labelOf = (row) => {
    const cell = row.querySelector("td:not(:first-child):not(:empty)").cloneNode(true);
    cell.querySelectorAll(".about, [popover]").forEach((element) => element.remove());
    return (cell.querySelector(".meta") || cell.querySelector(".label"))?.textContent.trim() || "";
  };
  const textTone = { NackRequested: "danger", DeliveryWaiting: "danger", DuplicateSuppressed: "teal", DeliveryResumed: "teal" };
  const ys = [];
  rows.reduce((y, _, i) => {
    const at = continuesResumed(i) ? ys[i - 1] : y;
    ys.push(at);
    return continuesResumed(i) ? y + 18 : at + 44;
  }, 26);
  const seqHeight = Math.max(...ys) + 44;
  seqBody.setAttribute("height", seqHeight);
  seqBody.setAttribute("viewBox", `0 0 624 ${seqHeight}`);
  ["transfer-service", "broker", "bank-a", "bank-b"].forEach((node) => make(seqBody, "line", { class: "lifeline", x1: lifelineX[node], y1: 0, x2: lifelineX[node], y2: seqHeight }));
  const messages = rows.map((row, i) => {
    const g = make(seqBody, "g", { class: "msg" });
    const obs = observation(i);
    const y = ys[i];
    const ly = y + 8;
    const label = labelOf(row);
    const tone = textTone[obs];
    const segments = [];
    const arrow = (from, to, lineTone) => {
      const x1 = lifelineX[from];
      const x2 = lifelineX[to];
      const dir = Math.sign(x2 - x1);
      segments.push({ x1, x2: x2 - dir * 2, dir, line: make(g, "line", { x1, y1: ly, x2: x1, y2: ly, "data-tone": lineTone }), chev: make(g, "path", { class: "chev", d: "M-6 -4.5 0 0-6 4.5", "data-tone": lineTone }) });
    };
    const centred = (nodes) => (Math.min(...nodes.map((node) => lifelineX[node])) + Math.max(...nodes.map((node) => lifelineX[node]))) / 2;
    if (row.dataset.step === "requested") {
      make(g, "circle", { class: "dot", cx: lifelineX.client, cy: ly, r: 3 });
      arrow("client", "transfer-service");
      make(g, "text", { x: lifelineX.client, y: y - 4, style: "text-anchor:start" }, "User request");
    } else if (obs === "NackRequested") {
      const x = lifelineX[lanes[i]];
      segments.push({ x1: x, x2: x - 28, dir: -1, line: make(g, "line", { class: "fail", x1: x, y1: ly, x2: x, y2: ly, "data-tone": "danger" }), bar: make(g, "path", { class: "fail", d: `M${x - 28} ${ly - 7}V${ly + 7}`, "data-tone": "danger" }) });
      make(g, "text", { x: x - 36, y: ly, style: "text-anchor:end", "data-tone": tone }, label);
    } else if (obs === "DeliveryWaiting") {
      make(g, "rect", { class: "wait", x: lifelineX.broker - 7, y: y - 2, width: 14, height: 22, rx: 1, "data-tone": "danger" });
      make(g, "text", { x: centred(["broker", "bank-b"]), y: ly, "data-tone": tone }, label);
    } else if (continuesResumed(i)) {
      make(g, "text", { x: centred(paths[i - 1]), y: ly + 14 }, label);
    } else if (paths[i].length > 1) {
      paths[i].slice(1).forEach((to, k) => arrow(paths[i][k], to, tones[obs]));
      const vias = obs === "DeliveryResumed" ? ["broker"] : paths[i].slice(1, -1).filter((node) => node === "broker");
      vias.forEach(() => make(g, "rect", { class: "via", x: lifelineX.broker - 7, y: ly - 4, width: 14, height: 8, rx: 1, "data-tone": tones[obs] }));
      make(g, "text", { x: centred(paths[i]), y: y - 4, "data-tone": tone }, label);
    } else {
      const x = lifelineX[paths[i][0] || lanes[i]] ?? lifelineX["transfer-service"];
      make(g, "circle", { class: "dot", cx: x, cy: ly, r: 3.5 });
      make(g, "text", { x: x < 312 ? x + 12 : x - 12, y: ly, style: `text-anchor:${x < 312 ? "start" : "end"}`, "data-tone": tone }, label);
    }
    return { g, segments };
  });
  let seqScroll = null;
  const renderSeq = (i, m) => {
    const t = clamp01(m / motionFor(i)) * messages[i].segments.length;
    messages.forEach((msg, j) => {
      const current = j === i || (j === i - 1 && continuesResumed(i));
      msg.g.dataset.state = current ? "current" : j < i ? "played" : "unplayed";
      msg.segments.forEach((s, k) => {
        const f = j < i ? 1 : j > i ? 0 : ease(clamp01(t - k));
        const x = s.x1 + (s.x2 - s.x1) * f;
        s.line.setAttribute("x2", x);
        if (s.chev) {
          s.chev.setAttribute("transform", `translate(${x} ${s.line.getAttribute("y1")}) scale(${s.dir} 1)`);
          s.chev.style.visibility = f > 0 ? "visible" : "hidden";
        }
        if (s.bar) s.bar.style.visibility = f >= 1 ? "visible" : "hidden";
      });
    });
    const lit = litNodes(i, m);
    seq.querySelectorAll("[data-node]").forEach((node) => node.toggleAttribute("data-lit", lit.includes(node.dataset.node)));
    const view = seqWindow.clientHeight || 264;
    const scroll = Math.min(0, Math.max(view - seqHeight, view * 0.6 - ys[i]));
    if (scroll !== seqScroll) seqBody.style.transform = `translateY(${(seqScroll = scroll)}px)`;
  };

  let position = 0;
  let elapsed = 0;
  let motion = Infinity;
  let playing = false;
  let stepping = false;
  let shown = -1;
  let shownPosition = -1;
  let last = performance.now();

  const showRow = (row) => {
    const content = row.querySelector("td:not(:first-child):not(:empty)").cloneNode(true);
    content.querySelectorAll("[data-full]").forEach((label) => (label.textContent = label.dataset.full));
    const about = content.querySelector("[popover]");
    content.querySelectorAll(".about, [popover]").forEach((element) => element.remove());
    entry.replaceChildren(...content.childNodes);
    entry.dataset.observation = row.dataset.observation || "";
    note.textContent = about ? about.textContent : "";
    time.textContent = localTime(row.dataset.observedAt);
    gap.textContent = row.dataset.gap ? `${row.dataset.gap} to the next entry` : "last entry";
  };

  const draw = () => {
    const current = Math.min(position, n - 1);
    if (current !== shown) showRow(rows[(shown = current)]);
    if (position !== shownPosition) {
      shownPosition = position;
      rows.forEach((row, index) => {
        row.dataset.playback = ticks[index].dataset.playback = index < position ? "played" : index === position ? "current" : "unplayed";
      });
    }
    const at = position >= n ? span : starts[position] + Math.min(elapsed, dwell[position]);
    progress.style.width = `${Math.min(at / span, 1) * 100}%`;
    const view = document.documentElement.dataset.view || "overview";
    graphs.forEach((g) => g.kind === view && renderGraph(g, current, motion));
    if (view === "sequence") renderSeq(current, motion);
    reset.disabled = back.disabled = position === 0;
    forward.disabled = end.disabled = position === n;
    const [state, label] = playing ? ["pause", "Pause"] : position === n ? ["replay", "Replay"] : ["play", "Play"];
    if (play.dataset.state !== state) {
      play.dataset.state = state;
      play.setAttribute("aria-label", label);
      play.title = label;
    }
  };

  const frame = (now) => {
    const dt = Math.min(now - last, 100);
    last = now;
    if (playing) {
      elapsed += dt;
      while (playing && elapsed >= dwell[position]) {
        elapsed -= dwell[position];
        position += 1;
        motion = 0;
        if (position === n) {
          playing = false;
          elapsed = 0;
          motion = Infinity;
        }
      }
    }
    if (playing || stepping) motion += dt;
    if (stepping && motion > motionFor(Math.min(position, n - 1)) + 600) stepping = false;
    draw();
    requestAnimationFrame(frame);
  };

  play.addEventListener("click", () => {
    if (playing) {
      playing = stepping = false;
      return;
    }
    if (position === n) {
      position = 0;
      elapsed = 0;
      motion = 0;
    }
    playing = true;
  });
  reset.addEventListener("click", () => {
    playing = stepping = false;
    position = 0;
    elapsed = 0;
    motion = Infinity;
  });
  back.addEventListener("click", () => {
    playing = stepping = false;
    position = Math.max(position - 1, 0);
    elapsed = 0;
    motion = Infinity;
  });
  forward.addEventListener("click", () => {
    playing = false;
    position = Math.min(position + 1, n);
    elapsed = 0;
    motion = position === n ? Infinity : 0;
    stepping = position < n;
  });
  end.addEventListener("click", () => {
    playing = stepping = false;
    position = n;
    elapsed = 0;
    motion = Infinity;
  });

  requestAnimationFrame(frame);
})();

// Browser glue of the all-skills page (LoopMap.astro, HandoverTable.astro,
// SkillTiles.astro). The build draws the map one time; this file adds the
// selection and the signal that travels the loop. `related` decides what a
// selection marks and `loopMotion` decides the motion; this file only applies
// what they return.
import { related } from '../lib/selection.mjs';
import { loopMotion } from '../lib/loop-motion.mjs';

type MapData = {
  shipSteps: string[];
  handovers: Array<{ From: string; To: string }>;
  skills: Array<{ name: string; role: string }>;
  defaultId: string;
};

/** Signal speed in user units per second, as in the reference script. */
const SPEED = 185;
/** The load sequence of the map takes this long before the signal starts. */
const INTRO_MS = 2500;

function initMap(root: HTMLElement): void {
  const svg = root.querySelector<SVGSVGElement>('[data-map-svg]');
  const route = root.querySelector<SVGPathElement>('[data-map-route]');
  const pulse = root.querySelector<SVGGElement>('[data-map-pulse]');
  const detail = root.querySelector<HTMLElement>('[data-sk-detail]');
  const table = root.querySelector<HTMLTableElement>('[data-hand]');
  if (!svg || !route || !pulse || !detail) return;
  const data = JSON.parse(root.dataset.map ?? '{}') as MapData;

  const nodes = [...root.querySelectorAll<HTMLElement | SVGGElement>('.node[data-id]')];
  const wsegs = [...svg.querySelectorAll<SVGPathElement>('.wseg')];
  const loop = svg.querySelector<SVGPathElement>('[data-map-loop]');
  const rows = table ? [...table.querySelectorAll<HTMLTableRowElement>('tbody tr[data-row]')] : [];
  const panels = [...detail.querySelectorAll<HTMLElement>('[data-panel]')];
  const media = window.matchMedia('(prefers-reduced-motion: reduce)');
  let selected = '';

  // ---- selection ----
  function pick(id: string, scroll: boolean): void {
    const panel = panels.find((p) => p.dataset.panel === id);
    const { nodes: linked, rows: marked } = related(id, data);
    if (!panel || linked.length === 0) return;
    selected = id;
    const set = new Set<string>(linked);
    for (const n of nodes) {
      const nid = n.dataset.id ?? '';
      n.classList.toggle('is-sel', nid === id);
      n.classList.toggle('is-rel', nid !== id && set.has(nid));
      if (n instanceof HTMLButtonElement) n.setAttribute('aria-pressed', String(nid === id));
    }
    svg!.classList.add('has-sel');
    svg!.classList.toggle('sel-dashboard', id === 'skill:dashboard');
    svg!.classList.toggle('sel-ship', id === 'ship');
    for (const s of wsegs) s.classList.toggle('on', set.has(s.dataset.a ?? '') && set.has(s.dataset.b ?? ''));
    loop?.classList.toggle('on', set.has('lane:harden'));
    rows.forEach((r) => r.classList.toggle('is-on', marked.includes(Number(r.dataset.row))));
    for (const p of panels) p.hidden = p !== panel;
    if (!media.matches) {
      panel.classList.remove('swap');
      void panel.offsetWidth; // restart the swap animation
      panel.classList.add('swap');
    }
    if (scroll) detail!.scrollIntoView({ block: 'nearest', behavior: media.matches ? 'auto' : 'smooth' });
  }

  for (const n of nodes) {
    const go = () => pick(n.dataset.id ?? '', n.dataset.sup === '1');
    n.addEventListener('click', go);
    if (n instanceof SVGGElement) {
      n.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          go();
        }
      });
    }
  }
  // A skill name in the panel or in the table selects that skill.
  root.addEventListener('click', (e) => {
    const b = (e.target as Element | null)?.closest<HTMLElement>('[data-go]');
    if (!b) return;
    const inTable = !!table && table.contains(b);
    pick(b.dataset.go ?? '', inTable);
    if (!inTable) {
      panels.find((p) => !p.hidden)?.querySelector<HTMLElement>('.sd-name')?.focus({ preventScroll: true });
    }
  });

  // ---- the signal that travels the loop ----
  // The build stores path lengths in data-d; the browser length can differ a little.
  const total = route.getTotalLength();
  const scale = total / Number(svg.dataset.total || total);
  const lamps = [...svg.querySelectorAll<SVGRectElement>('[data-lamp]')];
  const stepEls = data.shipSteps.map((name) => svg.querySelector<SVGGElement>(`[data-id="step:${name}"]`));
  const hardenEl = svg.querySelector<SVGGElement>('[data-id="lane:harden"]');
  const planEl = svg.querySelector<SVGGElement>('[data-id="skill:plan"]');
  const preplanEl = svg.querySelector<SVGGElement>('[data-id="skill:preplan"]');
  const at = (el: Element | null) => Number((el as HTMLElement | null)?.dataset.d ?? 0) * scale;

  function flash(el: Element | null | undefined, cls: string, ms: number): void {
    if (!el) return;
    el.classList.remove(cls);
    void el.getBoundingClientRect();
    el.classList.add(cls);
    window.setTimeout(() => el.classList.remove(cls), ms);
  }
  const marks = [
    { d: at(preplanEl), hit: () => flash(preplanEl, 'ping', 900) },
    { d: at(planEl), hit: () => flash(planEl, 'ping', 900) },
    ...stepEls.map((el, i) => ({ d: at(el), hit: () => (flash(el, 'ping', 900), flash(lamps[i], 'lit', 1400)) })),
    { d: at(hardenEl), hit: () => (flash(hardenEl, 'ping', 900), flash(lamps[stepEls.length], 'lit', 1400)) },
    { d: total - 10, hit: () => flash(planEl, 'ping', 900) },
  ].sort((a, b) => a.d - b.d);
  const greenFrom = (stepEls.length > 0 ? at(stepEls[stepEls.length - 1]) : at(planEl)) + 12;

  let raf = 0;
  let elapsed = 0; // signal time in ms, kept across a pause
  let lastNow = 0;
  let lastMark = 0;
  let started = false; // the load sequence has started
  let introDone = false; // the load sequence has ended or was skipped

  function tick(now: number): void {
    elapsed += lastNow ? now - lastNow : 0;
    lastNow = now;
    const d = ((elapsed / 1000) * SPEED) % total;
    const p = route!.getPointAtLength(d);
    pulse!.setAttribute('transform', `translate(${p.x.toFixed(1)} ${p.y.toFixed(1)})`);
    pulse!.style.opacity = Math.min(1, Math.min(d, total - d) / 36).toFixed(2);
    pulse!.classList.toggle('is-loop', d > greenFrom);
    let n = 0;
    while (n < marks.length && marks[n].d <= d) n++;
    if (n < lastMark) lastMark = 0;
    for (; lastMark < n; lastMark++) marks[lastMark].hit();
    raf = requestAnimationFrame(tick);
  }
  function halt(): void {
    cancelAnimationFrame(raf);
    raf = 0;
    lastNow = 0;
  }
  function selectDefault(): void {
    if (!selected) pick(data.defaultId, false);
  }

  const motion = {
    run(): void {
      if (!started) {
        started = true;
        svg!.classList.add('intro');
        window.setTimeout(() => {
          svg!.classList.remove('intro');
          selectDefault();
          introDone = true;
          apply();
        }, INTRO_MS);
        return;
      }
      if (raf || !introDone) return;
      pulse!.classList.add('is-live');
      raf = requestAnimationFrame(tick);
    },
    pause(): void {
      halt();
    },
    stop(): void {
      halt();
      started = true;
      introDone = true;
      svg!.classList.remove('intro');
      pulse!.classList.remove('is-live');
      selectDefault();
    },
  };

  const env = { onScreen: false, tabHidden: document.hidden, reducedMotion: media.matches };
  function apply(): void {
    motion[loopMotion(env)]();
  }

  new IntersectionObserver((entries) => {
    env.onScreen = entries.some((e) => e.isIntersecting);
    apply();
  }).observe(svg);
  document.addEventListener('visibilitychange', () => {
    env.tabHidden = document.hidden;
    apply();
  });
  media.addEventListener('change', (e) => {
    env.reducedMotion = e.matches;
    apply();
  });
  apply();
}

document.querySelectorAll<HTMLElement>('[data-skills-root]').forEach(initMap);

// App shell: routing, the time range and live controls, and theme.

import { h, icon, state, RANGES, APIError } from './lib.js';
import { renderServices, renderConnect } from './services.js';
import { renderService } from './service.js';
import { renderExplore, invalidateExplore } from './explore.js';
import { renderMetrics } from './metrics.js';
import { renderSystem } from './system.js';

const REFRESH_MS = 15000;
const main = document.getElementById('main');
let charts = [];
let timer = null;
let rendering = false;

function route() {
  const hash = location.hash.slice(1) || '/';
  const [path, qs = ''] = hash.split('?');
  const params = new URLSearchParams(qs);
  const parts = path.split('/').filter(Boolean);
  return { page: parts[0] || 'services', arg: parts[1] ? decodeURIComponent(parts.slice(1).join('/')) : null, params };
}

function navigate(hash) {
  if (location.hash === hash) render();
  else location.hash = hash;
}

async function render({ quiet = false } = {}) {
  if (rendering) return;
  rendering = true;
  const r = route();
  document.querySelectorAll('.nav a').forEach(a => {
    const page = a.dataset.page;
    a.toggleAttribute('aria-current', page === r.page || (page === 'services' && r.page === 'service'));
    if (a.hasAttribute('aria-current')) a.setAttribute('aria-current', 'page');
  });
  // Keep the previous frame visible, dimmed, while new data loads.
  main.classList.add('loading');
  const old = charts;
  charts = [];
  try {
    switch (r.page) {
      case 'service': await renderService(main, r.arg, charts); break;
      case 'services': r.arg === 'connect' ? renderConnect(main) : await renderServices(main); break;
      case 'explore': if (!quiet) await renderExplore(main, r.params, charts, navigate); break;
      case 'metrics': await renderMetrics(main, r.params, navigate); break;
      case 'system': await renderSystem(main, charts); break;
      default: main.replaceChildren(h('div', { class: 'card empty' }, h('h2', {}, 'Page not found'), h('p', {}, h('a', { href: '#/' }, 'Back to services'))));
    }
    old.forEach(c => c.destroy());
    const titles = { services: 'Services', service: r.arg, explore: 'Explore', metrics: 'Metrics', system: 'System' };
    document.title = `${titles[r.page] || 'TinyObs'} · TinyObs`;
  } catch (e) {
    old.forEach(c => c.destroy());
    main.replaceChildren(h('div', { class: 'callout error', role: 'alert' },
      h('div', {}, h('b', {}, e instanceof APIError && e.type === 'network' ? 'Can’t reach TinyObs' : 'Something went wrong'), h('div', {}, e.message))));
    console.error(e);
  } finally {
    main.classList.remove('loading');
    rendering = false;
  }
}

// Controls

function rangeControl() {
  const group = h('div', { class: 'segmented', role: 'group', 'aria-label': 'Time range' });
  for (const r of RANGES) {
    const b = h('button', { type: 'button', 'aria-pressed': String(r.key === state.range) }, r.key);
    b.addEventListener('click', () => {
      state.range = r.key;
      try { localStorage.setItem('tinyobs.range', r.key); } catch {}
      group.querySelectorAll('button').forEach(x => x.setAttribute('aria-pressed', String(x === b)));
      render();
    });
    group.append(b);
  }
  return group;
}

function liveControl() {
  const b = h('button', { class: 'icon-btn', type: 'button', 'aria-pressed': String(state.live), title: 'Refresh every 15 seconds' }, h('span', { class: 'live' }), 'Live');
  b.addEventListener('click', () => {
    state.live = !state.live;
    b.setAttribute('aria-pressed', String(state.live));
    schedule();
  });
  const refresh = h('button', { class: 'icon-btn', type: 'button', title: 'Refresh now', 'aria-label': 'Refresh now' }, icon('refresh'));
  refresh.addEventListener('click', () => { invalidateExplore(); render(); });
  return [b, refresh];
}

function themeControl() {
  const modes = ['system', 'light', 'dark'];
  let mode = 'system';
  try { mode = localStorage.getItem('tinyobs.theme') || 'system'; } catch {}
  const b = h('button', { class: 'icon-btn', type: 'button' });
  const apply = () => {
    if (mode === 'system') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', mode);
    const dark = mode === 'dark' || (mode === 'system' && matchMedia('(prefers-color-scheme: dark)').matches);
    b.replaceChildren(icon(dark ? 'moon' : 'sun'));
    b.title = `Theme: ${mode}`;
    b.setAttribute('aria-label', `Theme: ${mode}. Click to change.`);
  };
  b.addEventListener('click', () => {
    mode = modes[(modes.indexOf(mode) + 1) % modes.length];
    try { localStorage.setItem('tinyobs.theme', mode); } catch {}
    apply();
    render({ quiet: true }); // charts read colors when drawn
  });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => { apply(); render({ quiet: true }); });
  apply();
  return b;
}

function schedule() {
  clearInterval(timer);
  if (!state.live) return;
  timer = setInterval(() => {
    // Explore reruns only on demand; everything else follows live data.
    if (document.visibilityState === 'visible' && route().page !== 'explore') render({ quiet: true });
  }, REFRESH_MS);
}

function init() {
  try {
    const saved = localStorage.getItem('tinyobs.range');
    if (RANGES.some(r => r.key === saved)) state.range = saved;
  } catch {}
  document.getElementById('controls').append(rangeControl(), ...liveControl(), themeControl());
  window.addEventListener('hashchange', () => render());
  schedule();
  render();
}

init();

// Shared helpers: DOM building, the API client, formatting and app state.

// h builds an element. Strings become text nodes (never HTML), so label
// values from the API are always inert.
export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children) {
    if (c === undefined || c === null || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

const SVG_NS = 'http://www.w3.org/2000/svg';
export function svg(tag, attrs, ...children) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs || {})) el.setAttribute(k, v);
  for (const c of children) if (c) el.append(c);
  return el;
}

// icon returns a small stroked SVG icon.
const ICONS = {
  refresh: 'M3 12a9 9 0 0 1 15.5-6.2M21 12a9 9 0 0 1-15.5 6.2M18 3v4h-4M6 21v-4h4',
  sun: 'M12 4V2M12 22v-2M4.9 4.9 3.5 3.5M20.5 20.5l-1.4-1.4M4 12H2M22 12h-2M4.9 19.1l-1.4 1.4M20.5 3.5l-1.4 1.4M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10z',
  moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z',
  copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
  back: 'M15 18l-6-6 6-6',
  external: 'M14 4h6v6M20 4l-9 9M19 14v6H4V5h6',
};
export function icon(name) {
  return svg('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.8', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' },
    svg('path', { d: ICONS[name] }));
}

// cssVar reads a design token, e.g. cssVar('--s1').
export function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// API

export class APIError extends Error {
  constructor(message, type) {
    super(message);
    this.type = type;
  }
}

async function request(path, params, method = 'GET') {
  let url = path;
  const init = { method, headers: {} };
  const body = new URLSearchParams();
  for (const [k, v] of Object.entries(params || {})) {
    if (v === undefined || v === null) continue;
    for (const x of Array.isArray(v) ? v : [v]) body.append(k, x);
  }
  if (method === 'GET') {
    if ([...body].length) url += '?' + body.toString();
  } else {
    init.body = body;
    init.headers['Content-Type'] = 'application/x-www-form-urlencoded';
  }
  let resp;
  try {
    resp = await fetch(url, init);
  } catch (e) {
    throw new APIError('TinyObs is not reachable. Is it still running?', 'network');
  }
  let json;
  try {
    json = await resp.json();
  } catch (e) {
    throw new APIError(`${resp.status} ${resp.statusText}`, 'http');
  }
  if (json.status !== 'success') throw new APIError(json.error || `${resp.status} ${resp.statusText}`, json.errorType);
  return json.data;
}

export const api = {
  get: (path, params) => request(path, params),
  post: (path, params) => request(path, params, 'POST'),
  // instant evaluates at the end of the selected range.
  instant: (query, time) => request('/api/v1/query', { query, time }, 'POST'),
  range: (query, start, end, step) => request('/api/v1/query_range', { query, start, end, step }, 'POST'),
};

// Time range and refresh state, shared by every page and kept in the URL.

export const RANGES = [
  { key: '15m', seconds: 15 * 60 },
  { key: '1h', seconds: 3600 },
  { key: '6h', seconds: 6 * 3600 },
  { key: '24h', seconds: 24 * 3600 },
  { key: '3d', seconds: 3 * 86400 },
];

export const state = {
  range: '1h',
  live: true,
};

export function rangeSeconds() {
  return (RANGES.find(r => r.key === state.range) || RANGES[1]).seconds;
}

// window returns the query window: start/end in seconds and a step giving
// about 120 points, aligned so refreshes line up.
export function timeWindow(points = 120) {
  const secs = rangeSeconds();
  const step = Math.max(5, Math.round(secs / points / 5) * 5);
  const end = Math.floor(Date.now() / 1000 / step) * step;
  return { start: end - secs, end, step };
}

// rateWindow is the range used inside rate(): long enough to smooth a 15s
// scrape, short enough to track changes over the selected range.
export function rateWindow() {
  const secs = rangeSeconds();
  if (secs <= 3600) return '1m';
  if (secs <= 6 * 3600) return '2m';
  return '5m';
}

// Formatting

const SI = [[1e12, 'T'], [1e9, 'G'], [1e6, 'M'], [1e3, 'k']];
function sig(v, digits = 3) {
  if (v === 0) return '0';
  const a = Math.abs(v);
  let out;
  if (a >= 100) out = v.toFixed(0);
  else if (a >= 10) out = v.toFixed(Math.max(0, digits - 2));
  else if (a >= 1) out = v.toFixed(Math.max(0, digits - 1));
  else out = v.toPrecision(digits);
  // "50.0k" reads as "50k"; "1.50 s" as "1.5 s".
  return out.includes('.') ? out.replace(/0+$/, '').replace(/\.$/, '') : out;
}

// fmtGoDuration tidies a Go duration string: "72h0m0s" becomes "72h".
export function fmtGoDuration(s) {
  return String(s).replace(/(\d)h0m0s$/, '$1h').replace(/(\d)m0s$/, '$1m');
}

export function fmtNumber(v) {
  if (v === null || v === undefined || Number.isNaN(v)) return '–';
  if (!Number.isFinite(v)) return v > 0 ? '+Inf' : '-Inf';
  if (Number.isInteger(v) && Math.abs(v) < 1000) return String(v);
  for (const [f, s] of SI) if (Math.abs(v) >= f) return sig(v / f) + s;
  return sig(v);
}

export function fmtDuration(seconds) {
  if (seconds === null || seconds === undefined || Number.isNaN(seconds)) return '–';
  const a = Math.abs(seconds);
  if (a === 0) return '0';
  if (a < 1e-3) return sig(seconds * 1e6) + ' µs';
  if (a < 1) return sig(seconds * 1e3) + ' ms';
  if (a < 60) return sig(seconds) + ' s';
  if (a < 3600) return sig(seconds / 60) + ' min';
  return sig(seconds / 3600) + ' h';
}

export function fmtBytes(v) {
  if (v === null || v === undefined || Number.isNaN(v)) return '–';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  let x = Math.abs(v);
  while (x >= 1024 && i < units.length - 1) { x /= 1024; i++; }
  return (v < 0 ? '-' : '') + sig(x) + ' ' + units[i];
}

export function fmtPercent(ratio) {
  if (ratio === null || ratio === undefined || Number.isNaN(ratio)) return '–';
  const p = ratio * 100;
  if (p === 0) return '0%';
  if (p < 0.01) return '<0.01%';
  return sig(p, p < 1 ? 2 : 3) + '%';
}

export function fmtRate(v) {
  if (v === null || v === undefined || Number.isNaN(v)) return '–';
  if (v !== 0 && Math.abs(v) < 0.1) return fmtNumber(v * 60) + '/min';
  return fmtNumber(v) + '/s';
}

// unitOf infers how to format a metric's values from its name.
export function unitOf(name, query = '') {
  const n = name || '';
  if (/_bytes(_total)?$/.test(n) || /bytes/.test(query)) return /rate\(|irate\(|deriv\(/.test(query) ? 'bytes/s' : 'bytes';
  if (/_seconds(_total|_sum)?$/.test(n) || /histogram_quantile\(.*_seconds/.test(query)) return 'seconds';
  if (/_ratio$|_percent$/.test(n)) return 'ratio';
  if (/rate\(|irate\(/.test(query)) return 'rate';
  return 'number';
}

export function formatter(unit) {
  switch (unit) {
    case 'bytes': return fmtBytes;
    case 'bytes/s': return v => fmtBytes(v) + '/s';
    case 'seconds': return fmtDuration;
    case 'ratio': return fmtPercent;
    case 'rate': return fmtRate;
    default: return fmtNumber;
  }
}

export function ago(date) {
  const s = Math.round((Date.now() - date.getTime()) / 1000);
  if (s < 5) return 'just now';
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

export function labelsString(metric, omit = []) {
  const parts = Object.entries(metric || {})
    .filter(([k]) => k !== '__name__' && !omit.includes(k))
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}="${v}"`);
  return (metric.__name__ || '') + (parts.length ? `{${parts.join(', ')}}` : (metric.__name__ ? '' : '{}'));
}

// metadataFor finds a metric's metadata, which histograms and summaries
// report under their family name rather than _bucket, _count or _sum.
export function metadataFor(meta, name) {
  const md = meta[name] || meta[name.replace(/_(bucket|count|sum)$/, '')];
  return md ? md[0] : null;
}

// esc quotes a string for use inside a PromQL label matcher.
export function esc(s) {
  return String(s).replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

export function toast(message) {
  const el = h('div', { class: 'toast', role: 'status' }, message);
  document.body.append(el);
  setTimeout(() => el.remove(), 3500);
}

export async function copyText(text, button) {
  try {
    await navigator.clipboard.writeText(text);
    if (button) {
      const old = button.textContent;
      button.textContent = 'Copied';
      setTimeout(() => { button.textContent = old; }, 1500);
    }
  } catch {
    toast('Copy failed: select the text and copy it manually');
  }
}

// scalarValue extracts a number from an instant vector with one element.
export function first(vector) {
  if (!vector || !vector.result || !vector.result.length) return null;
  return Number(vector.result[0].value[1]);
}

// byLabel maps an instant vector's results by one label.
export function byLabel(data, label) {
  const out = new Map();
  for (const r of (data && data.result) || []) out.set(r.metric[label] || '', Number(r.value[1]));
  return out;
}

// seriesByLabel maps a matrix's series by one label.
export function seriesByLabel(data, label) {
  const out = new Map();
  for (const r of (data && data.result) || []) out.set(r.metric[label] || '', r.values);
  return out;
}

// Explore: a PromQL editor with autocomplete, a graph and a table.

import { h, api, timeWindow, unitOf, formatter, labelsString, APIError, metadataFor } from './lib.js';
import { timeChart } from './chart.js';

const FUNCTIONS = [
  'rate', 'irate', 'increase', 'delta', 'idelta', 'deriv', 'predict_linear', 'resets', 'changes',
  'avg_over_time', 'min_over_time', 'max_over_time', 'sum_over_time', 'count_over_time', 'last_over_time',
  'quantile_over_time', 'stddev_over_time', 'present_over_time', 'absent_over_time',
  'histogram_quantile', 'abs', 'ceil', 'floor', 'round', 'sqrt', 'exp', 'ln', 'log2', 'log10', 'clamp', 'clamp_min', 'clamp_max',
  'time', 'timestamp', 'vector', 'scalar', 'absent', 'label_replace', 'label_join', 'sort', 'sort_desc',
];
const AGGREGATIONS = ['sum', 'avg', 'min', 'max', 'count', 'group', 'stddev', 'stdvar', 'topk', 'bottomk', 'quantile'];

let metricNames = null;
let metadata = {};

export async function renderExplore(main, params, charts, navigate) {
  if (!metricNames) {
    [metricNames, metadata] = await Promise.all([api.get('/api/v1/label/__name__/values'), api.get('/api/v1/metadata')]);
  }
  let query = params.get('q') || '';
  let view = params.get('view') || 'graph';

  const textarea = h('textarea', { class: 'query', rows: 1, spellcheck: 'false', autocomplete: 'off', 'aria-label': 'PromQL query', placeholder: 'Type a PromQL query, e.g. sum by (job) (rate(http_requests_total[5m]))' });
  textarea.value = query;
  const suggest = h('div', { class: 'suggest', role: 'listbox', hidden: true });
  const runBtn = h('button', { class: 'btn primary', type: 'button' }, 'Run');
  const results = h('div');
  const tabs = h('div', { class: 'tabs', role: 'tablist' });

  const run = () => {
    query = textarea.value.trim();
    navigate(`#/explore?q=${encodeURIComponent(query)}${view !== 'graph' ? '&view=' + view : ''}`);
  };
  runBtn.addEventListener('click', run);
  const autosize = () => { textarea.style.height = 'auto'; textarea.style.height = textarea.scrollHeight + 2 + 'px'; };
  textarea.addEventListener('input', () => { autosize(); complete(textarea, suggest); });
  textarea.addEventListener('keydown', e => {
    if (!suggest.hidden && handleSuggestKeys(e, textarea, suggest)) return;
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey || !e.shiftKey)) {
      e.preventDefault();
      run();
    }
  });
  textarea.addEventListener('blur', () => setTimeout(() => { suggest.hidden = true; }, 150));

  for (const [key, label] of [['graph', 'Graph'], ['table', 'Table']]) {
    tabs.append(h('button', {
      type: 'button', role: 'tab', 'aria-selected': String(view === key),
      onclick: () => { view = key; navigate(`#/explore?q=${encodeURIComponent(query)}${key !== 'graph' ? '&view=' + key : ''}`); },
    }, label));
  }

  main.replaceChildren(
    h('div', { class: 'page-head' }, h('div', {}, h('h1', {}, 'Explore'), h('div', { class: 'sub' }, `${metricNames.length} metrics · PromQL, checked against Prometheus's own test suite`))),
    h('div', { class: 'editor-row' }, h('div', { class: 'editor' }, textarea, suggest), runBtn),
    h('div', { class: 'editor-hint' }, 'Enter to run · Shift+Enter for a new line · Tab to accept a suggestion'),
    ...(query ? [tabs] : []),
    results,
  );
  autosize();
  if (!query) {
    results.append(examples(navigate));
    textarea.focus();
    return;
  }

  const hint = suggestion(query);
  if (hint) results.append(hint);
  try {
    if (view === 'table') {
      const data = await api.instant(query);
      results.append(h('div', { class: 'card', style: { marginTop: '16px' } }, table(data, query)));
    } else {
      const win = timeWindow(240);
      const data = await api.range(query, win.start, win.end, win.step);
      const body = h('div', { class: 'card-body' });
      const n = data.result.length;
      results.append(h('div', { class: 'card', style: { marginTop: '16px' } },
        h('div', { class: 'card-head' }, h('h3', {}, `${n} series`), h('span', { class: 'hint' }, `step ${win.step}s`)), body));
      const unit = unitOf(data.result[0] ? data.result[0].metric.__name__ : firstMetric(query), query);
      charts.push(timeChart(body, { result: data.result, window: win, unit, height: 320 }));
    }
  } catch (e) {
    results.append(errorCallout(e, query));
  }
}

function firstMetric(query) {
  const m = query.match(/[a-zA-Z_:][a-zA-Z0-9_:]*/g) || [];
  return m.find(x => metricNames.includes(x)) || '';
}

function errorCallout(e, query) {
  const msg = e.message || String(e);
  const pos = msg.match(/parse error at char (\d+)/);
  let caret = null;
  if (pos) {
    const i = Number(pos[1]) - 1;
    caret = h('pre', {}, query + '\n' + ' '.repeat(Math.max(0, i)) + '^');
  }
  const unsupported = msg.includes('not supported by TinyObs');
  return h('div', { class: 'callout error', role: 'alert' },
    h('div', {},
      h('b', {}, unsupported ? 'Not supported' : e instanceof APIError && e.type === 'bad_data' ? 'Invalid query' : 'Query failed'),
      h('div', {}, msg), caret,
      unsupported ? h('div', { class: 'muted', style: { marginTop: '6px' } }, 'TinyObs implements a tested subset of PromQL and refuses the rest rather than guessing.') : null));
}

// suggestion offers a better query for common first attempts.
function suggestion(query) {
  const bare = query.match(/^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^}]*\})?$/);
  if (!bare) return null;
  const name = bare[1];
  const sel = bare[0];
  const md = metadataFor(metadata, name);
  let better = null;
  let why = '';
  if (name.endsWith('_bucket')) {
    better = `histogram_quantile(0.95, sum by (le) (rate(${sel}[5m])))`;
    why = 'This is a histogram. Its buckets are cumulative counts; a quantile is usually what you want.';
  } else if ((md && md.type === 'counter') || name.endsWith('_total')) {
    better = `rate(${sel}[5m])`;
    why = 'This is a counter: it only goes up. rate() shows how fast it grows, per second.';
  }
  if (!better) return null;
  return h('div', { class: 'callout info' },
    h('div', { style: { flex: 1 } }, why, ' ', h('code', {}, better)),
    h('button', { class: 'btn', type: 'button', onclick: () => { location.hash = `#/explore?q=${encodeURIComponent(better)}`; } }, 'Use this'));
}

function table(data, query) {
  const fmt = formatter(unitOf(data.result && data.result[0] && data.result[0].metric ? data.result[0].metric.__name__ : '', query));
  if (data.resultType === 'scalar' || data.resultType === 'string') {
    return h('div', { class: 'card-body' }, h('div', { class: 'kpi-value' }, data.resultType === 'scalar' ? fmt(Number(data.result[1])) : data.result[1]));
  }
  if (!data.result.length) return h('div', { class: 'card-body muted' }, 'No series match at the current time.');
  const rows = data.result.map(r => ({
    series: labelsString(r.metric),
    value: data.resultType === 'matrix' ? `${r.values.length} samples` : r.value[1],
    num: data.resultType === 'matrix' ? null : Number(r.value[1]),
  }));
  return h('table', { class: 'data' },
    h('thead', {}, h('tr', {}, h('th', {}, 'Series'), h('th', { class: 'num' }, 'Value'))),
    h('tbody', {}, rows.map(r => h('tr', {},
      h('td', { class: 'mono wrap' }, r.series),
      h('td', { class: 'num', title: r.value }, r.num === null ? r.value : fmt(r.num))))));
}

function examples(navigate) {
  const has = n => metricNames.includes(n);
  const ex = [];
  if (has('http_server_request_duration_seconds_count')) ex.push(['Request rate by service', 'sum by (job) (rate(http_server_request_duration_seconds_count[5m]))']);
  if (has('http_requests_total')) ex.push(['Request rate by service', 'sum by (job) (rate(http_requests_total[5m]))']);
  if (has('http_request_duration_seconds_bucket')) ex.push(['p95 latency by service', 'histogram_quantile(0.95, sum by (job, le) (rate(http_request_duration_seconds_bucket[5m])))']);
  if (has('up')) ex.push(['Scrape targets that are down', 'up == 0']);
  ex.push(['Samples TinyObs stores per second', 'rate(tinyobs_samples_appended_total[1m])']);
  ex.push(['Series per metric (top 10)', 'topk(10, count by (__name__) ({__name__=~".+"}))']);
  return h('div', { class: 'card', style: { marginTop: '16px' } },
    h('div', { class: 'card-head' }, h('h3', {}, 'Try one of these')),
    h('div', { class: 'card-body' }, h('table', { class: 'data' }, h('tbody', {}, ex.map(([title, q]) =>
      h('tr', { class: 'link', onclick: () => navigate(`#/explore?q=${encodeURIComponent(q)}`) },
        h('td', {}, title), h('td', { class: 'mono wrap' }, q)))))));
}

// Autocomplete

let labelCache = new Map();

async function complete(textarea, box) {
  const pos = textarea.selectionStart;
  const before = textarea.value.slice(0, pos);
  let items = [];
  let replaceFrom = pos;

  const valueCtx = before.match(/([a-zA-Z_:][a-zA-Z0-9_:]*)?\{[^{}]*?([a-zA-Z_][a-zA-Z0-9_]*)\s*(=~|!~|!=|=)\s*"([^"]*)$/);
  const labelCtx = before.match(/([a-zA-Z_:][a-zA-Z0-9_:]*)?\{(?:[^{}]*,)?\s*([a-zA-Z_][a-zA-Z0-9_]*)?$/);
  const groupCtx = before.match(/\b(by|without|on|ignoring)\s*\(([^)]*,\s*)?([a-zA-Z_][a-zA-Z0-9_]*)?$/);
  try {
    if (valueCtx) {
      const [, metric, label, , prefix] = valueCtx;
      const values = await cached(`v:${metric}:${label}`, () => api.get(`/api/v1/label/${encodeURIComponent(label)}/values`, metric ? { 'match[]': metric } : {}));
      items = values.filter(v => v.startsWith(prefix)).slice(0, 50).map(v => ({ text: v, insert: v, kind: 'value' }));
      replaceFrom = pos - prefix.length;
    } else if (labelCtx) {
      const [, metric, prefix = ''] = labelCtx;
      const names = await cached(`l:${metric}`, () => api.get('/api/v1/labels', metric ? { 'match[]': metric } : {}));
      items = names.filter(n => n !== '__name__' && n.startsWith(prefix)).map(n => ({ text: n, insert: n + '="', kind: 'label' }));
      replaceFrom = pos - prefix.length;
    } else if (groupCtx) {
      const prefix = groupCtx[3] || '';
      const names = await cached('l:', () => api.get('/api/v1/labels'));
      items = names.filter(n => n !== '__name__' && n.startsWith(prefix)).map(n => ({ text: n, insert: n, kind: 'label' }));
      replaceFrom = pos - prefix.length;
    } else {
      const word = before.match(/[a-zA-Z_:][a-zA-Z0-9_:]*$/);
      if (!word || word[0].length < 1) {
        box.hidden = true;
        return;
      }
      const prefix = word[0];
      const lower = prefix.toLowerCase();
      const fns = [...AGGREGATIONS, ...FUNCTIONS].filter(f => f.startsWith(lower)).map(f => ({ text: f, insert: f + '(', kind: AGGREGATIONS.includes(f) ? 'aggregation' : 'function' }));
      const metrics = metricNames.filter(m => m.includes(prefix)).sort((a, b) => a.indexOf(prefix) - b.indexOf(prefix) || a.localeCompare(b)).slice(0, 40)
        .map(m => ({ text: m, insert: m, kind: (metadataFor(metadata, m) || {}).type || 'metric' }));
      items = [...fns, ...metrics];
      replaceFrom = pos - prefix.length;
    }
  } catch {
    items = [];
  }
  if (!items.length || (items.length === 1 && items[0].insert === textarea.value.slice(replaceFrom, pos))) {
    box.hidden = true;
    return;
  }
  box.replaceChildren(...items.slice(0, 60).map((it, i) => {
    const el = h('div', { role: 'option', 'aria-selected': String(i === 0) }, it.text, h('small', {}, it.kind));
    el.addEventListener('mousedown', e => { e.preventDefault(); accept(textarea, box, it, replaceFrom); });
    el._item = it;
    return el;
  }));
  box._from = replaceFrom;
  box.hidden = false;
}

async function cached(key, f) {
  if (!labelCache.has(key)) labelCache.set(key, f());
  return labelCache.get(key);
}

function accept(textarea, box, item, from) {
  const pos = textarea.selectionStart;
  const v = textarea.value;
  textarea.value = v.slice(0, from) + item.insert + v.slice(pos);
  const caret = from + item.insert.length;
  textarea.setSelectionRange(caret, caret);
  box.hidden = true;
  textarea.dispatchEvent(new Event('input'));
}

function handleSuggestKeys(e, textarea, box) {
  const opts = [...box.children];
  const i = opts.findIndex(o => o.getAttribute('aria-selected') === 'true');
  const select = j => {
    opts.forEach((o, k) => o.setAttribute('aria-selected', String(k === j)));
    opts[j].scrollIntoView({ block: 'nearest' });
  };
  switch (e.key) {
    case 'ArrowDown': e.preventDefault(); select((i + 1) % opts.length); return true;
    case 'ArrowUp': e.preventDefault(); select((i - 1 + opts.length) % opts.length); return true;
    case 'Tab': e.preventDefault(); accept(textarea, box, opts[Math.max(0, i)]._item, box._from); return true;
    case 'Escape': box.hidden = true; return true;
  }
  return false;
}

// Reset cached names when the page is re-entered after new data arrived.
export function invalidateExplore() {
  metricNames = null;
  labelCache = new Map();
}

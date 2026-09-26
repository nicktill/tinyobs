// Service detail: RED charts, a route table, runtime metrics and the
// service's metric list.

import { h, api, timeWindow, rateWindow, fmtRate, fmtPercent, fmtDuration, fmtNumber, esc, cssVar, metadataFor } from './lib.js';
import { timeChart, sparkline, gridValues } from './chart.js';
import { detect, serviceQueries } from './red.js';

export async function renderService(main, job, charts) {
  const win = timeWindow();
  const w = rateWindow();
  const sel = `job="${esc(job)}"`;
  const [d, instances, up, metricCounts, meta] = await Promise.all([
    detect(job),
    api.instant(`group by (instance) ({${sel}})`),
    api.instant(`up{${sel}}`),
    api.instant(`count by (__name__) ({${sel}})`),
    api.get('/api/v1/metadata'),
  ]);

  const upBy = new Map(up.result.map(r => [r.metric.instance, Number(r.value[1])]));
  const instNames = [...new Set([...instances.result.map(r => r.metric.instance).filter(Boolean), ...upBy.keys()])].sort();
  const head = h('div', { class: 'page-head' },
    h('div', {},
      h('div', { class: 'crumb' }, h('a', { href: '#/' }, 'Services'), ' / '),
      h('h1', {}, job),
      h('div', { class: 'row', style: { marginTop: '8px' } },
        instNames.length ? instNames.map(i => {
          const u = upBy.get(i);
          return h('span', { class: 'chip', title: u === 0 ? 'scrape failing' : 'reporting' },
            h('span', { class: 'dot ' + (u === 0 ? 'critical' : 'good') }), i, u === 0 ? ' · down' : '');
        }) : h('span', { class: 'status' }, 'no data in the last 5 minutes'))),
    h('a', { class: 'btn', href: `#/explore?q=${encodeURIComponent(`{${sel}}`)}` }, 'Explore metrics'),
  );

  const content = [head];
  if (d) {
    const q = serviceQueries(d, w);
    const [req, err, byRoute, byStatus, ...lat] = await Promise.all([
      api.range(q.requests, win.start, win.end, win.step),
      q.errors ? api.range(q.errors, win.start, win.end, win.step) : null,
      q.byRoute ? api.range(q.byRoute, win.start, win.end, win.step) : null,
      q.errorsByStatus ? api.range(q.errorsByStatus, win.start, win.end, win.step) : null,
      ...q.latency.map(l => api.range(l.query, win.start, win.end, win.step)),
    ]);
    const reqVals = gridValues(req.result[0] && req.result[0].values, win);
    const errVals = err ? gridValues(err.result[0] && err.result[0].values, win) : null;
    const ratio = reqVals.map((r, i) => (r === null ? null : r > 0 ? ((errVals && errVals[i]) || 0) / r : 0));
    const p95 = lat[1] ? gridValues(lat[1].result[0] && lat[1].result[0].values, win) : null;

    content.push(h('div', { class: 'grid kpis' },
      tile('Requests', lastVal(reqVals), fmtRate, reqVals, '--s1', 'per second'),
      tile('Errors (5xx)', d.status ? lastVal(ratio) : null, fmtPercent, d.status ? ratio : null, '--critical', d.status ? 'of requests' : 'no status label'),
      tile('p95 latency', lastVal(p95), fmtDuration, p95, '--s7', d.bucket ? '95% of requests are faster' : 'no latency histogram'),
    ));

    const grid = h('div', { class: 'grid two', style: { marginTop: '16px' } });
    content.push(grid);
    if (byRoute) grid.append(chartCard(charts, `Requests by ${d.route}`, 'per second', byRoute, win, 'rate', r => r.metric[d.route] || '(none)'));
    else grid.append(chartCard(charts, 'Requests', 'per second', req, win, 'rate', () => 'requests'));
    if (byStatus) grid.append(chartCard(charts, `Errors by ${d.status}`, '4xx and 5xx per second', byStatus, win, 'rate', r => r.metric[d.status], 'No 4xx or 5xx responses in this range'));
    if (lat.length) {
      const result = lat.map((m, i) => ({ metric: { quantile: `p${Math.round(q.latency[i].p * 100)}` }, values: (m.result[0] && m.result[0].values) || [] })).filter(r => r.values.length);
      grid.append(chartCard(charts, 'Latency', 'p50 · p95 · p99', { result }, win, 'seconds', r => r.metric.quantile, undefined,
        result.map(r => cssVar({ p50: '--s3', p95: '--s1', p99: '--s2' }[r.metric.quantile]))));
    }

    if (q.routeTable) content.push(await routeTable(d, q.routeTable));
  } else {
    content.push(h('div', { class: 'callout info' },
      'No HTTP metrics found for this service. Showing all of its metrics below; HTTP servers instrumented with OpenTelemetry or a Prometheus client get request, error and latency charts here automatically.'));
  }

  const runtime = runtimeSection(charts, sel, metricCounts, win);
  if (runtime) content.push(runtime);
  content.push(metricList(job, metricCounts, meta));
  main.replaceChildren(...content);
}

function lastVal(values) {
  if (!values) return null;
  for (let i = values.length - 1; i >= 0; i--) if (values[i] !== null && Number.isFinite(values[i])) return values[i];
  return null;
}

function tile(label, value, fmt, values, colorVar, foot) {
  return h('div', { class: 'card kpi-tile' },
    h('div', { class: 'kpi-label' }, label),
    h('div', { class: 'kpi-value' + (value === null ? ' none' : '') }, value === null ? '–' : fmt(value)),
    h('div', { class: 'kpi-foot' }, foot),
    values ? sparkline(values, cssVar(colorVar)) : null);
}

function chartCard(charts, title, hint, data, win, unit, labelFn, empty, colors) {
  const body = h('div', { class: 'card-body' });
  const card = h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, title), h('span', { class: 'hint' }, hint)), body);
  // Render after insertion so the chart can measure its width.
  requestAnimationFrame(() => {
    const labels = data.result.map(labelFn);
    charts.push(timeChart(body, { result: data.result, window: win, unit, labels, empty, colors }));
  });
  return card;
}

async function routeTable(d, q) {
  const [req, err, p95] = await Promise.all([
    api.instant(q.requests),
    q.errors ? api.instant(q.errors) : null,
    q.p95 ? api.instant(q.p95) : null,
  ]);
  const get = (data, route) => {
    if (!data) return null;
    const r = data.result.find(x => (x.metric[d.route] || '') === route);
    return r ? Number(r.value[1]) : (data === err ? 0 : null);
  };
  let rows = req.result.map(r => {
    const route = r.metric[d.route] || '';
    const rate = Number(r.value[1]);
    const e = get(err, route);
    return { route, rate, errors: err ? (rate > 0 ? e / rate : 0) : null, p95: get(p95, route) };
  });
  let sortKey = 'rate', desc = true;
  const tbody = h('tbody');
  const draw = () => {
    rows.sort((a, b) => {
      const x = a[sortKey], y = b[sortKey];
      const c = typeof x === 'string' ? x.localeCompare(y) : (x ?? -1) - (y ?? -1);
      return desc ? -c : c;
    });
    tbody.replaceChildren(...rows.map(r => h('tr', {},
      h('td', { class: 'mono wrap' }, r.route || '(none)'),
      h('td', { class: 'num' }, fmtRate(r.rate)),
      h('td', { class: 'num' }, r.errors === null ? '–' : h('span', { style: r.errors > 0.01 ? { color: 'var(--critical)', fontWeight: 600 } : {} }, fmtPercent(r.errors))),
      h('td', { class: 'num' }, fmtDuration(r.p95)))));
  };
  const th = (label, key, num) => h('th', {
    class: num ? 'num' : '', 'data-sort': key, scope: 'col',
    onclick: () => { desc = sortKey === key ? !desc : true; sortKey = key; draw(); },
  }, label);
  draw();
  return h('div', {},
    h('div', { class: 'section-title' }, `Routes · last 5 minutes`),
    h('div', { class: 'card' }, rows.length
      ? h('table', { class: 'data' }, h('thead', {}, h('tr', {}, th(d.route, 'route'), th('Requests', 'rate', true), th('Errors (5xx)', 'errors', true), th('p95', 'p95', true))), tbody)
      : h('div', { class: 'card-body muted' }, 'No requests in the last 5 minutes.')));
}

// runtimeSection charts Go and process metrics when the service exposes them.
function runtimeSection(charts, sel, counts, win) {
  const names = new Set(counts.result.map(r => r.metric.__name__));
  const panels = [
    ['Goroutines', 'go_goroutines', `sum(go_goroutines{${sel}})`, 'number'],
    ['Heap in use', names.has('go_memstats_heap_alloc_bytes') ? 'go_memstats_heap_alloc_bytes' : 'go_memory_heap_bytes', null, 'bytes'],
    ['Resident memory', 'process_resident_memory_bytes', null, 'bytes'],
    ['CPU', 'process_cpu_seconds_total', `sum(rate(process_cpu_seconds_total{${sel}}[${rateWindow()}]))`, 'number'],
  ].filter(([, metric]) => names.has(metric));
  if (!panels.length) return null;
  const grid = h('div', { class: 'grid two', style: { padding: '0 16px 16px' } });
  const details = h('details', { class: 'card', style: { marginTop: '16px' } }, h('summary', {}, 'Runtime', h('span', { class: 'muted', style: { fontWeight: 400 } }, `· ${panels.length} metrics`)), grid);
  details.addEventListener('toggle', async () => {
    if (!details.open || grid.childElementCount) return;
    for (const [title, metric, query, unit] of panels) {
      const qq = query || `sum(${metric}{${sel}})`;
      const data = await api.range(qq, win.start, win.end, win.step);
      grid.append(chartCard(charts, title, unit === 'number' && metric.includes('cpu') ? 'cores' : '', data, win, unit, () => title));
    }
  });
  return details;
}

function metricList(job, counts, meta) {
  const rows = counts.result
    .map(r => ({ name: r.metric.__name__, series: Number(r.value[1]), md: metadataFor(meta, r.metric.__name__) }))
    .sort((a, b) => a.name.localeCompare(b.name));
  const sel = `job="${esc(job)}"`;
  return h('div', {},
    h('div', { class: 'section-title' }, `All metrics · ${rows.length}`),
    h('div', { class: 'card' }, h('table', { class: 'data' },
      h('thead', {}, h('tr', {}, h('th', {}, 'Metric'), h('th', {}, 'Type'), h('th', { class: 'num' }, 'Series'), h('th', {}, 'Description'))),
      h('tbody', {}, rows.map(r => {
        const typ = r.md ? r.md.type : 'unknown';
        const query = typ === 'counter' || r.name.endsWith('_total') ? `sum by (instance) (rate(${r.name}{${sel}}[5m]))` : `${r.name}{${sel}}`;
        return h('tr', { class: 'link', onclick: () => { location.hash = `#/explore?q=${encodeURIComponent(query)}`; } },
          h('td', { class: 'mono' }, r.name),
          h('td', {}, h('span', { class: 'badge ' + typ }, typ)),
          h('td', { class: 'num' }, fmtNumber(r.series)),
          h('td', { class: 'help' }, r.md ? r.md.help : ''));
      })))));
}

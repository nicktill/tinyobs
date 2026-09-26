// Metrics: the catalog, with type, series count and label cardinality.

import { h, api, fmtNumber, metadataFor } from './lib.js';

export async function renderMetrics(main, params, navigate) {
  const [status, meta, config] = await Promise.all([
    api.get('/api/v1/status/tsdb', { limit: 100000 }),
    api.get('/api/v1/metadata'),
    api.get('/api/v1/status/config'),
  ]);
  const total = status.headStats.numSeries;
  const rows = status.seriesCountByMetricName.map(({ name, value }) => {
    const md = metadataFor(meta, name);
    return { name, series: value, type: md ? md.type : 'unknown', help: md ? md.help : '' };
  });
  const limit = config.maxSeries;
  const pct = limit ? total / limit : 0;

  const search = h('input', { class: 'input', type: 'search', placeholder: 'Filter by name or description', value: params.get('filter') || '', 'aria-label': 'Filter metrics', style: { maxWidth: '360px' } });
  let sortKey = 'series', desc = true;
  const tbody = h('tbody');
  const draw = () => {
    const f = search.value.trim().toLowerCase();
    const shown = rows.filter(r => !f || r.name.toLowerCase().includes(f) || r.help.toLowerCase().includes(f));
    shown.sort((a, b) => {
      const c = sortKey === 'series' ? a.series - b.series : String(a[sortKey]).localeCompare(String(b[sortKey]));
      return desc ? -c : c;
    });
    tbody.replaceChildren(...shown.slice(0, 500).flatMap(r => row(r, total, navigate)));
    if (shown.length > 500) tbody.append(h('tr', {}, h('td', { colspan: 5, class: 'muted' }, `${shown.length - 500} more; narrow the filter`)));
  };
  search.addEventListener('input', draw);
  const th = (label, key, cls) => h('th', {
    class: cls || '', 'data-sort': key, scope: 'col',
    onclick: () => { desc = sortKey === key ? !desc : key === 'series'; sortKey = key; draw(); },
  }, label);
  draw();

  const largest = rows.slice().sort((a, b) => b.series - a.series)[0];
  main.replaceChildren(
    h('div', { class: 'page-head' }, h('div', {}, h('h1', {}, 'Metrics'), h('div', { class: 'sub' }, 'Everything TinyObs stores, and where the series come from'))),
    h('div', { class: 'grid kpis' },
      h('div', { class: 'card kpi-tile' }, h('div', { class: 'kpi-label' }, 'Metrics'), h('div', { class: 'kpi-value' }, fmtNumber(rows.length))),
      h('div', { class: 'card kpi-tile' },
        h('div', { class: 'kpi-label' }, 'Series'),
        h('div', { class: 'kpi-value' }, fmtNumber(total), h('span', { class: 'unit' }, `/ ${fmtNumber(limit)}`)),
        h('div', { class: 'meter' + (pct > 0.9 ? ' crit' : pct > 0.7 ? ' warn' : ''), role: 'meter', 'aria-valuenow': total, 'aria-valuemax': limit, 'aria-label': 'Series used' }, h('span', { style: { width: Math.min(100, pct * 100) + '%' } })),
        h('div', { class: 'kpi-foot' }, pct > 0.7 ? 'Approaching the series limit: new series will be rejected' : 'New series are rejected past the limit')),
      largest ? h('div', { class: 'card kpi-tile' }, h('div', { class: 'kpi-label' }, 'Most series'), h('div', { class: 'kpi-value mono', style: { fontSize: '15px', paddingTop: '6px' } }, largest.name), h('div', { class: 'kpi-foot' }, `${fmtNumber(largest.series)} series · ${Math.round(largest.series / total * 100)}% of all`)) : null,
    ),
    h('div', { class: 'row', style: { margin: '24px 0 12px' } }, search, h('span', { class: 'muted', style: { fontSize: '12px' } }, 'Click a metric to explore it; ', h('b', {}, 'Labels'), ' shows which labels drive its series count.')),
    h('div', { class: 'card' }, h('table', { class: 'data' },
      h('thead', {}, h('tr', {}, th('Metric', 'name'), th('Type', 'type'), th('Series', 'series', 'num'), h('th', {}, 'Description'), h('th', {}))),
      tbody)),
  );
}

function row(r, total, navigate) {
  const detail = h('tr', { hidden: true }, h('td', { colspan: 5 }));
  const labelsBtn = h('button', { class: 'icon-btn', type: 'button', 'aria-expanded': 'false' }, 'Labels');
  labelsBtn.addEventListener('click', async e => {
    e.stopPropagation();
    const open = detail.hidden;
    detail.hidden = !open;
    labelsBtn.setAttribute('aria-expanded', String(open));
    if (open && !detail.firstChild.childElementCount) {
      detail.firstChild.append(h('span', { class: 'muted' }, 'Loading…'));
      detail.firstChild.replaceChildren(await labelBreakdown(r.name));
    }
  });
  const query = r.type === 'counter' || r.name.endsWith('_total') ? `sum by (job) (rate(${r.name}[5m]))`
    : r.name.endsWith('_bucket') ? `histogram_quantile(0.95, sum by (job, le) (rate(${r.name}[5m])))` : r.name;
  const tr = h('tr', { class: 'link', onclick: () => navigate(`#/explore?q=${encodeURIComponent(query)}`) },
    h('td', { class: 'mono wrap' }, r.name),
    h('td', {}, h('span', { class: 'badge ' + r.type }, r.type)),
    h('td', { class: 'num' }, fmtNumber(r.series), h('div', { class: 'meter', style: { width: '64px', marginLeft: 'auto', marginTop: '4px' } }, h('span', { style: { width: Math.max(2, r.series / total * 100) + '%' } }))),
    h('td', { class: 'help' }, r.help),
    h('td', { class: 'num' }, labelsBtn));
  return [tr, detail];
}

async function labelBreakdown(name) {
  const series = await api.get('/api/v1/series', { 'match[]': `${name}`, limit: 5000 });
  const values = new Map();
  for (const s of series) for (const [k, v] of Object.entries(s)) {
    if (k === '__name__') continue;
    if (!values.has(k)) values.set(k, new Set());
    values.get(k).add(v);
  }
  const labels = [...values.entries()].sort((a, b) => b[1].size - a[1].size);
  if (!labels.length) return h('span', { class: 'muted' }, 'No labels besides the metric name.');
  return h('div', { class: 'row', style: { gap: '6px 8px', padding: '4px 0' } },
    labels.map(([k, vs]) => h('a', {
      class: 'chip', href: `#/explore?q=${encodeURIComponent(`count by (${k}) (${name})`)}&view=table`,
      title: [...vs].slice(0, 20).join(', '),
    }, h('b', {}, k), `${vs.size} value${vs.size === 1 ? '' : 's'}`)),
    series.length >= 5000 ? h('span', { class: 'muted' }, '(first 5,000 series)') : null);
}


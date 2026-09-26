// System: TinyObs's own health, scrape targets and configuration.

import { h, api, timeWindow, rateWindow, fmtRate, fmtNumber, fmtBytes, fmtDuration, fmtGoDuration, first, ago, toast } from './lib.js';
import { timeChart } from './chart.js';

const SELF = 'job="tinyobs"';

export async function renderSystem(main, charts) {
  const win = timeWindow();
  const w = rateWindow();
  const [ingest, series, limit, disk, rejected, uptime, targets, config, build] = await Promise.all([
    api.instant(`sum(rate(tinyobs_samples_appended_total{${SELF}}[${w}]))`),
    api.instant(`max(tinyobs_series{${SELF}})`),
    api.instant(`max(tinyobs_series_limit{${SELF}})`),
    api.instant(`max(tinyobs_disk_bytes{${SELF}})`),
    api.instant(`sum by (reason) (increase(tinyobs_samples_rejected_total{${SELF}}[1h]))`),
    api.instant(`time() - max(process_start_time_seconds{${SELF}})`),
    api.get('/api/v1/targets'),
    api.get('/api/v1/status/config'),
    api.get('/api/v1/status/buildinfo'),
  ]);

  const seriesN = first(series), limitN = first(limit);
  const pct = seriesN !== null && limitN ? seriesN / limitN : 0;
  const rejectedTotal = rejected.result.reduce((n, r) => n + Number(r.value[1]), 0);
  const rejectedWhy = rejected.result.filter(r => Number(r.value[1]) > 0.5)
    .map(r => `${fmtNumber(Number(r.value[1]))} ${r.metric.reason.replace(/_/g, ' ')}`).join(' · ');

  const snapBtn = h('button', { class: 'btn', type: 'button' }, 'Create snapshot');
  snapBtn.addEventListener('click', async () => {
    snapBtn.disabled = true;
    try {
      const res = await api.post('/api/v1/admin/tsdb/snapshot');
      toast(`Snapshot written to ${res.path}`);
    } catch (e) {
      toast(`Snapshot failed: ${e.message}`);
    } finally {
      snapBtn.disabled = false;
    }
  });

  const tile = (label, value, foot, extra) => h('div', { class: 'card kpi-tile' },
    h('div', { class: 'kpi-label' }, label), h('div', { class: 'kpi-value' }, value), extra || null, foot ? h('div', { class: 'kpi-foot' }, foot) : null);

  const charts2 = h('div', { class: 'grid two', style: { marginTop: '16px' } });
  main.replaceChildren(
    h('div', { class: 'page-head' },
      h('div', {}, h('h1', {}, 'System'), h('div', { class: 'sub' }, `TinyObs ${build.version} · ${build.goVersion}`)),
      snapBtn),
    h('div', { class: 'grid kpis' },
      tile('Ingest', fmtRate(first(ingest)).replace('/s', ''), 'samples per second', null),
      tile('Series', h('span', {}, fmtNumber(seriesN), h('span', { class: 'unit' }, `/ ${fmtNumber(limitN)}`)), pct > 0.7 ? 'approaching the limit' : 'of the series limit',
        h('div', { class: 'meter' + (pct > 0.9 ? ' crit' : pct > 0.7 ? ' warn' : ''), role: 'meter', 'aria-label': 'Series used', 'aria-valuenow': seriesN ?? 0, 'aria-valuemax': limitN ?? 0 }, h('span', { style: { width: Math.min(100, pct * 100) + '%' } }))),
      tile('Disk', fmtBytes(first(disk)), `keeping ${fmtGoDuration(config.retention)} of data`),
      tile('Rejected samples', h('span', { style: rejectedTotal > 0.5 ? { color: 'var(--critical)' } : {} }, fmtNumber(Math.round(rejectedTotal))), rejectedTotal > 0.5 ? `last hour: ${rejectedWhy}` : 'none in the last hour'),
      tile('Uptime', fmtDuration(first(uptime)), 'since TinyObs started'),
    ),
    charts2,
    h('div', { class: 'section-title' }, `Scrape targets · ${targets.activeTargets.length}`),
    targetTable(targets.activeTargets),
    h('div', { class: 'section-title' }, 'Configuration'),
    h('div', { class: 'card card-body' }, h('dl', { class: 'kv' },
      kv('UI and API', config.listen), kv('OTLP port', config.otlpListen || 'off (OTLP is still served on the main port)'),
      kv('Data directory', config.dataDir), kv('Retention', fmtGoDuration(config.retention)), kv('Series limit', fmtNumber(config.maxSeries)),
      kv('Scrape interval', fmtGoDuration(config.scrapeInterval)), kv('Authentication', config.auth ? 'token required' : 'off'), kv('TLS', config.tls ? 'on' : 'off'))),
  );

  const panels = [
    ['Samples stored', 'per second', `sum(rate(tinyobs_samples_appended_total{${SELF}}[${w}]))`, 'rate', () => 'stored'],
    ['Rejected samples', 'per second, by reason', `sum by (reason) (rate(tinyobs_samples_rejected_total{${SELF}}[${w}])) > 0`, 'rate', r => r.metric.reason],
    ['Series', 'stored', `max(tinyobs_series{${SELF}})`, 'number', () => 'series'],
    ['API latency', 'p95 by route', `histogram_quantile(0.95, sum by (handler, le) (rate(tinyobs_http_request_duration_seconds_bucket{${SELF}}[${w}])))`, 'seconds', r => r.metric.handler],
  ];
  const datas = await Promise.all(panels.map(p => api.range(p[2], win.start, win.end, win.step)));
  panels.forEach(([title, hint, , unit, label], i) => {
    const body = h('div', { class: 'card-body' });
    charts2.append(h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, title), h('span', { class: 'hint' }, hint)), body));
    const result = datas[i].result;
    charts.push(timeChart(body, { result, window: win, unit, labels: result.map(label), empty: title === 'Rejected samples' ? 'No samples rejected in this range' : undefined }));
  });
}

function kv(k, v) {
  return [h('dt', {}, k), h('dd', { class: 'mono' }, v)];
}

function targetTable(targets) {
  if (!targets.length) {
    return h('div', { class: 'card card-body muted' }, 'No scrape targets. Add one with ', h('code', {}, '-scrape [job=]host:port'), ' or the TINYOBS_SCRAPE environment variable.');
  }
  return h('div', { class: 'card' }, h('table', { class: 'data' },
    h('thead', {}, h('tr', {}, h('th', {}, 'Job'), h('th', {}, 'Endpoint'), h('th', {}, 'State'), h('th', { class: 'num' }, 'Last scrape'), h('th', { class: 'num' }, 'Duration'), h('th', {}, 'Error'))),
    h('tbody', {}, targets.map(t => {
      const never = t.lastScrape.startsWith('0001');
      const cls = t.health === 'up' ? 'good' : t.health === 'down' ? 'critical' : '';
      return h('tr', {},
        h('td', {}, h('a', { href: `#/service/${encodeURIComponent(t.scrapePool)}` }, t.scrapePool)),
        h('td', { class: 'mono wrap' }, t.scrapeUrl),
        h('td', {}, h('span', { class: 'status ' + cls }, t.health)),
        h('td', { class: 'num' }, never ? '–' : ago(new Date(t.lastScrape))),
        h('td', { class: 'num' }, never ? '–' : fmtDuration(t.lastScrapeDuration)),
        h('td', { class: 'help wrap' }, t.lastError));
    }))));
}

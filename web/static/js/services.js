// Services: one card per job with request rate, error ratio and p95 latency.

import { h, api, timeWindow, rateWindow, fmtRate, fmtPercent, fmtDuration, fmtNumber, copyText, icon, cssVar } from './lib.js';
import { sparkline, gridValues } from './chart.js';
import { overviewQueries } from './red.js';

export async function renderServices(main) {
  const win = timeWindow(60);
  const q = overviewQueries(rateWindow());
  const [jobs, reqs, errs, p95, series, instances, up] = await Promise.all([
    api.get('/api/v1/label/job/values', { start: win.start, end: win.end }),
    api.range(q.requests, win.start, win.end, win.step),
    api.range(q.errors, win.start, win.end, win.step),
    api.range(q.p95, win.start, win.end, win.step),
    api.instant('count by (job) ({job!=""})'),
    api.instant('count by (job) (group by (job, instance) ({job!=""}))'),
    api.instant('up'),
  ]);

  const matrix = data => new Map(data.result.map(r => [r.metric.job, gridValues(r.values, win)]));
  const vector = data => new Map(data.result.map(r => [r.metric.job, Number(r.value[1])]));
  const reqBy = matrix(reqs), errBy = matrix(errs), p95By = matrix(p95);
  const seriesBy = vector(series), instBy = vector(instances);
  const downBy = new Map();
  for (const r of up.result) {
    const d = downBy.get(r.metric.job) || { up: 0, down: 0 };
    Number(r.value[1]) === 1 ? d.up++ : d.down++;
    downBy.set(r.metric.job, d);
  }

  const services = jobs.filter(j => j !== 'tinyobs');
  const down = [...downBy.values()].reduce((n, d) => n + d.down, 0);

  const head = h('div', { class: 'page-head' },
    h('div', {},
      h('h1', {}, 'Services'),
      h('div', { class: 'sub' },
        services.length ? `${services.length} service${services.length === 1 ? '' : 's'}` : 'Nothing reporting yet',
        down ? h('span', { class: 'status critical', style: { marginLeft: '10px' } }, `${down} scrape target${down === 1 ? '' : 's'} down`) : null)),
    services.length ? h('a', { class: 'btn', href: '#/services/connect' }, 'Connect a service') : null,
  );

  if (!services.length) {
    main.replaceChildren(head, onboarding(jobs.includes('tinyobs')));
    return;
  }

  // Services with HTTP metrics first, then the rest; alphabetical within each.
  const isHTTP = j => reqBy.has(j);
  services.sort((a, b) => (isHTTP(b) - isHTTP(a)) || a.localeCompare(b));

  const grid = h('div', { class: 'grid services' });
  for (const job of services) {
    const reqVals = reqBy.get(job);
    const liveInstances = instBy.get(job) || 0;
    const d = downBy.get(job);
    let status = h('span', { class: 'status' }, 'no data in last 5m');
    if (d && d.down && !d.up) status = h('span', { class: 'status critical' }, 'target down');
    else if (d && d.down) status = h('span', { class: 'status warning' }, `${d.down} of ${d.up + d.down} targets down`);
    else if (liveInstances) status = h('span', { class: 'status good' }, `${liveInstances} instance${liveInstances === 1 ? '' : 's'}`);

    let body;
    if (reqVals) {
      const errVals = errBy.get(job);
      const ratio = reqVals.map((r, i) => (r === null ? null : r > 0 ? ((errVals && errVals[i]) || 0) / r : 0));
      const lat = p95By.get(job);
      body = h('div', { class: 'svc-kpis' },
        kpi('Requests', last(reqVals), fmtRate, reqVals, '--s1'),
        kpi('Errors (5xx)', last(ratio), fmtPercent, ratio, '--critical', last(ratio) > 0.01),
        lat ? kpi('p95 latency', last(lat), fmtDuration, lat, '--s7') : kpi('p95 latency', null, () => 'no histogram', null),
      );
    } else {
      body = h('div', { class: 'svc-note' }, `No HTTP metrics · ${fmtNumber(seriesBy.get(job) || 0)} series`);
    }
    grid.append(h('a', { class: 'card svc', href: `#/service/${encodeURIComponent(job)}` },
      h('div', { class: 'svc-head' }, h('span', { class: 'svc-name', title: job }, job), h('span', { class: 'svc-meta' }, status)),
      body));
  }

  main.replaceChildren(head, grid,
    h('p', { class: 'muted', style: { fontSize: '12px', marginTop: '20px' } },
      'Rates are per second over the selected range. HTTP services are detected from ',
      h('code', {}, 'http_server_request_duration_seconds'), ' (OpenTelemetry) or ',
      h('code', {}, 'http_requests_total'), ' (Prometheus). TinyObs itself is on the ', h('a', { href: '#/system' }, 'System'), ' page.'));
}

function last(values) {
  if (!values) return null;
  for (let i = values.length - 1; i >= 0; i--) if (values[i] !== null && Number.isFinite(values[i])) return values[i];
  return null;
}

// kpi renders a labelled value with a sparkline drawn in the color token.
function kpi(label, value, fmt, values, colorVar, bad) {
  const text = value === null ? '–' : fmt(value);
  return h('div', { class: 'kpi' + (bad ? ' bad' : '') },
    h('div', { class: 'kpi-label' }, label),
    h('div', { class: 'kpi-value' + (value === null ? ' none' : '') }, text),
    values ? sparkline(values, cssVar(colorVar)) : null);
}

// Onboarding: shown until something other than TinyObs reports.

export function onboarding(selfOnly) {
  const host = location.host;
  const tabs = [
    {
      title: 'Prometheus /metrics',
      desc: 'Already exposing Prometheus metrics? Restart TinyObs with one -scrape flag per endpoint (the job name is optional):',
      code: `tinyobs -scrape api=localhost:2112 -scrape worker=localhost:9100`,
    },
    {
      title: 'OpenTelemetry',
      desc: 'TinyObs accepts OTLP/HTTP on the standard port 4318, so SDKs with default settings work as they are. Otherwise, point the exporter at it:',
      code: `OTEL_SERVICE_NAME=checkout \\\nOTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://${host}/v1/metrics \\\nOTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf \\\n  ./your-app`,
    },
    {
      title: 'Go SDK',
      desc: 'No instrumentation yet? The TinyObs Go SDK adds request metrics with one middleware:',
      code: `client, _ := sdk.New(sdk.ClientConfig{Service: "checkout"})\nclient.Start(ctx)\ndefer client.Stop()\n\nhandler := httpx.Middleware(client)(mux)`,
    },
  ];
  let active = 0;
  const panel = h('div');
  const tabBar = h('div', { class: 'tabs', role: 'tablist' });
  const show = () => {
    tabBar.querySelectorAll('button').forEach((b, i) => b.setAttribute('aria-selected', String(i === active)));
    const t = tabs[active];
    const copyBtn = h('button', { class: 'icon-btn copy', type: 'button' }, icon('copy'), 'Copy');
    copyBtn.addEventListener('click', () => copyText(t.code, copyBtn));
    panel.replaceChildren(h('div', { class: 'step-desc' }, t.desc), h('div', { class: 'snippet' }, h('pre', {}, t.code), copyBtn));
  };
  tabs.forEach((t, i) => tabBar.append(h('button', { type: 'button', role: 'tab', onclick: () => { active = i; show(); } }, t.title)));
  show();
  return h('div', { class: 'card empty' },
    logoMark(),
    h('h2', {}, 'Connect your first service'),
    h('p', {}, 'TinyObs is running. Send it metrics in whichever form your application already speaks; services appear here as soon as data arrives.'),
    h('div', { class: 'onboard' }, tabBar, panel),
    selfOnly ? h('p', { class: 'muted', style: { marginTop: '24px', marginBottom: 0, fontSize: '13px' } }, 'Meanwhile, TinyObs is monitoring itself: see ', h('a', { href: '#/system' }, 'System'), '.') : null);
}

export function logoMark() {
  const wrap = h('div', { style: { color: 'var(--accent)' } });
  wrap.innerHTML = '<svg width="40" height="40" viewBox="0 0 32 32" fill="none" aria-hidden="true"><rect x="1.5" y="1.5" width="29" height="29" rx="8" stroke="currentColor" stroke-width="2.2"/><path d="M7 19.5l4.5-5 4 3.5 5-7.5 4.5 4" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/></svg>';
  return wrap;
}

export function renderConnect(main) {
  main.replaceChildren(
    h('div', { class: 'page-head' }, h('div', {}, h('div', { class: 'crumb' }, h('a', { href: '#/' }, 'Services'), ' / '), h('h1', {}, 'Connect a service'))),
    onboarding(false));
}

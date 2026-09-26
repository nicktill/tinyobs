// RED (rate, errors, duration) signals for HTTP services.
//
// Services are detected by explicit rules, checked in this order:
//
//  1. OpenTelemetry semantic conventions: the histogram
//     http_server_request_duration_seconds, with http_response_status_code
//     and http_route labels.
//  2. Prometheus conventions: the counter http_requests_total with a code,
//     status or status_code label, and optionally the histogram
//     http_request_duration_seconds, with route, path, handler or endpoint.
//
// Errors are 5xx responses: 4xx means the client made a mistake.

import { api, esc } from './lib.js';

const OTEL = 'http_server_request_duration_seconds';
const PROM_COUNTER = 'http_requests_total';
const PROM_HIST = 'http_request_duration_seconds';
const STATUS_LABELS = ['http_response_status_code', 'code', 'status', 'status_code'];
const ROUTE_LABELS = ['http_route', 'route', 'path', 'handler', 'endpoint'];

// Queries covering every job at once, for the Services overview.
export function overviewQueries(w) {
  const five = l => `{${l}=~"5.."}`;
  const errorTerms = [
    `sum by (job) (rate(${OTEL}_count${five('http_response_status_code')}[${w}]))`,
    ...['code', 'status', 'status_code'].map(l => `sum by (job) (rate(${PROM_COUNTER}${five(l)}[${w}]))`),
    ...['code', 'status', 'status_code'].map(l => `sum by (job) (rate(${PROM_HIST}_count${five(l)}[${w}]))`),
  ];
  return {
    requests: `sum by (job) (rate(${OTEL}_count[${w}])) or sum by (job) (rate(${PROM_COUNTER}[${w}])) or sum by (job) (rate(${PROM_HIST}_count[${w}]))`,
    errors: errorTerms.join(' or '),
    p95: `histogram_quantile(0.95, sum by (job, le) (rate(${OTEL}_bucket[${w}]))) or histogram_quantile(0.95, sum by (job, le) (rate(${PROM_HIST}_bucket[${w}])))`,
  };
}

// detect works out which conventions and labels one job uses.
export async function detect(job) {
  const sel = `{job="${esc(job)}", __name__=~"${OTEL}_count|${OTEL}_bucket|${PROM_COUNTER}|${PROM_HIST}_count|${PROM_HIST}_bucket"}`;
  const now = Math.floor(Date.now() / 1000);
  const names = new Set(await api.get(`/api/v1/label/__name__/values`, { 'match[]': sel, start: now - 3 * 86400, end: now }));
  let count, bucket;
  if (names.has(`${OTEL}_count`)) {
    count = `${OTEL}_count`;
    bucket = `${OTEL}_bucket`;
  } else if (names.has(PROM_COUNTER)) {
    count = PROM_COUNTER;
    bucket = names.has(`${PROM_HIST}_bucket`) ? `${PROM_HIST}_bucket` : null;
  } else if (names.has(`${PROM_HIST}_count`)) {
    count = `${PROM_HIST}_count`;
    bucket = `${PROM_HIST}_bucket`;
  } else {
    return null;
  }
  const labelNames = new Set(await api.get('/api/v1/labels', { 'match[]': `${count}{job="${esc(job)}"}`, start: now - 3 * 86400, end: now }));
  return {
    job,
    count,
    bucket,
    status: STATUS_LABELS.find(l => labelNames.has(l)) || null,
    route: ROUTE_LABELS.find(l => labelNames.has(l)) || null,
    selector: `job="${esc(job)}"`,
  };
}

// serviceQueries builds the service page's queries from detect's result.
export function serviceQueries(d, w) {
  const q = {
    requests: `sum(rate(${d.count}{${d.selector}}[${w}]))`,
    errors: d.status ? `sum(rate(${d.count}{${d.selector}, ${d.status}=~"5.."}[${w}]))` : null,
    latency: d.bucket ? [0.5, 0.95, 0.99].map(p => ({
      p,
      query: `histogram_quantile(${p}, sum by (le) (rate(${d.bucket}{${d.selector}}[${w}])))`,
    })) : [],
    byRoute: d.route ? `sum by (${d.route}) (rate(${d.count}{${d.selector}}[${w}]))` : null,
    errorsByStatus: d.status ? `sum by (${d.status}) (rate(${d.count}{${d.selector}, ${d.status}=~"[45].."}[${w}]))` : null,
  };
  if (d.route) {
    q.routeTable = {
      requests: `sum by (${d.route}) (rate(${d.count}{${d.selector}}[5m]))`,
      errors: d.status ? `sum by (${d.route}) (rate(${d.count}{${d.selector}, ${d.status}=~"5.."}[5m]))` : null,
      p95: d.bucket ? `histogram_quantile(0.95, sum by (${d.route}, le) (rate(${d.bucket}{${d.selector}}[5m])))` : null,
    };
  }
  return q;
}

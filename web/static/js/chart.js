// Charts: a uPlot time series chart with a crosshair tooltip and legend, and
// a small SVG sparkline.

import { h, svg, formatter, cssVar as css } from './lib.js';

const SLOTS = ['--s1', '--s2', '--s3', '--s4', '--s5', '--s6', '--s7', '--s8'];

// seriesColor returns categorical slot i. Slots are assigned in a fixed order
// and never cycled: series past the eighth are drawn in the muted gray.
export function seriesColor(i) {
  return i < SLOTS.length ? css(SLOTS[i]) : css('--axis');
}

// compactLabels names each series by the labels that differ between them,
// so legends read "code=500" rather than the full label set.
export function compactLabels(metrics) {
  const keys = new Set();
  for (const m of metrics) for (const k of Object.keys(m)) keys.add(k);
  const varying = [...keys].filter(k => new Set(metrics.map(m => m[k])).size > 1).sort();
  return metrics.map(m => {
    if (metrics.length === 1 || !varying.length) {
      const parts = Object.entries(m).filter(([k]) => k !== '__name__').map(([k, v]) => `${k}=${v}`);
      return m.__name__ || parts.join(', ') || 'value';
    }
    return varying.filter(k => m[k] !== undefined).map(k => (k === '__name__' ? m[k] : `${k}=${m[k]}`)).join(', ');
  });
}

// align turns a Prometheus matrix into uPlot's columnar data on a fixed step grid.
function align(result, win) {
  const xs = [];
  for (let t = win.start; t <= win.end; t += win.step) xs.push(t);
  const index = new Map(xs.map((t, i) => [t, i]));
  const ys = result.map(r => {
    const col = new Array(xs.length).fill(null);
    for (const [t, v] of r.values) {
      const i = index.get(Math.round(t));
      const n = Number(v);
      // NaN (e.g. a quantile of zero requests) is a gap, not a point.
      if (i !== undefined && Number.isFinite(n)) col[i] = n;
    }
    return col;
  });
  return [xs, ...ys];
}

// The browser's locale, or en-US if the runtime reports one Intl rejects.
const LOCALE = (() => {
  try {
    new Intl.DateTimeFormat(navigator.language);
    return navigator.language;
  } catch {
    return 'en-US';
  }
})();

// timeLabel shows as much of the time as the span needs: seconds only for
// short ranges, the date only for multi-day ones.
function timeLabel(ts, spanSecs, tooltip) {
  const d = new Date(ts * 1000);
  const opts = { hour: 'numeric', minute: '2-digit' };
  if (tooltip || spanSecs <= 5 * 60) opts.second = '2-digit';
  if (spanSecs > 86400) Object.assign(opts, { month: 'short', day: 'numeric' });
  return d.toLocaleString(LOCALE, opts);
}

// timeChart renders series into container and returns a handle with destroy().
// opts: { result, window, unit, labels, height, colors, stacked }
export function timeChart(container, opts) {
  const { window: win } = opts;
  // Series without a single finite value would only add legend noise.
  const keep = opts.result.map(r => r.values.some(([, v]) => Number.isFinite(Number(v))));
  const result = opts.result.filter((_, i) => keep[i]);
  if (opts.labels) opts = { ...opts, labels: opts.labels.filter((_, i) => keep[i]) };
  if (opts.colors) opts = { ...opts, colors: opts.colors.filter((_, i) => keep[i]) };
  container.replaceChildren();
  if (!result.length) {
    container.append(h('div', { class: 'chart-empty' }, opts.empty || 'No data in this time range'));
    return { destroy() {} };
  }
  const fmt = formatter(opts.unit);
  const labels = opts.labels || compactLabels(result.map(r => r.metric));

  // Order series by label so each keeps its color across refreshes and filters.
  const order = result.map((_, i) => i).sort((a, b) => labels[a].localeCompare(labels[b], undefined, { numeric: true }));
  const sorted = order.map(i => result[i]);
  const names = order.map(i => labels[i]);
  const colors = opts.colors || names.map((_, i) => seriesColor(i));
  const data = align(sorted, win);

  const plotEl = h('div', { class: 'chart' });
  const tip = h('div', { class: 'tooltip', hidden: true });
  plotEl.append(tip);
  const legend = h('div', { class: 'legend' });
  container.append(plotEl, legend);

  const axisStyle = {
    stroke: css('--muted'),
    grid: { stroke: css('--grid'), width: 1 },
    ticks: { stroke: css('--grid'), width: 1, size: 4 },
    font: `11px ${css('--font')}`,
  };
  const span = win.end - win.start;
  const uopts = {
    width: Math.max(200, container.clientWidth || 600),
    height: opts.height || 220,
    legend: { show: false },
    padding: [8, 28, 0, 0],
    cursor: { drag: { x: false, y: false }, points: { size: 7, width: 2 }, focus: { prox: 24 } },
    focus: { alpha: 0.35 },
    scales: { x: { time: true, range: [win.start, win.end] }, y: { range: (u, min, max) => yRange(min, max) } },
    axes: [
      { ...axisStyle, values: (u, ticks) => ticks.map(t => timeLabel(t, span)), space: 80 },
      { ...axisStyle, values: (u, ticks) => ticks.map(fmt), size: 64, space: 36 },
    ],
    series: [
      {},
      ...names.map((name, i) => ({
        label: name,
        stroke: colors[i],
        width: 2,
        points: { show: false },
        spanGaps: false,
        fill: opts.fill ? colors[i] + '1f' : undefined,
      })),
    ],
    hooks: {
      setCursor: [u => showTooltip(u, tip, names, colors, fmt, span)],
    },
  };
  const plot = new uPlot(uopts, data, plotEl);

  // Legend: always present for two or more series; click toggles a series.
  if (names.length > 1) {
    const max = 24;
    names.slice(0, max).forEach((name, i) => {
      const btn = h('button', { type: 'button', 'aria-pressed': 'true', title: name },
        h('span', { class: 'key', style: { background: colors[i] } }),
        h('span', { class: 'label' }, name));
      btn.addEventListener('click', () => {
        const on = btn.getAttribute('aria-pressed') !== 'true';
        btn.setAttribute('aria-pressed', String(on));
        plot.setSeries(i + 1, { show: on });
      });
      legend.append(btn);
    });
    if (names.length > max) legend.append(h('span', { class: 'more' }, `+${names.length - max} more series`));
  }

  const ro = new ResizeObserver(() => {
    const w = container.clientWidth;
    if (w && Math.abs(w - plot.width) > 2) plot.setSize({ width: w, height: plot.height });
  });
  ro.observe(container);
  return { destroy() { ro.disconnect(); plot.destroy(); } };
}

function yRange(min, max) {
  if (min === null || max === null) return [0, 1];
  if (min >= 0) {
    // Anchor positive data at zero so magnitudes read honestly.
    const top = max === 0 ? 1 : max * 1.1;
    return [0, top];
  }
  const pad = (max - min) * 0.1 || Math.abs(max) * 0.1 || 1;
  return [min - pad, max + pad];
}

function showTooltip(u, tip, names, colors, fmt, span) {
  const idx = u.cursor.idx;
  if (idx === null || idx === undefined || u.cursor.left < 0) {
    tip.hidden = true;
    return;
  }
  const rows = [];
  for (let i = 1; i < u.series.length; i++) {
    if (!u.series[i].show) continue;
    const v = u.data[i][idx];
    if (v === null || v === undefined) continue;
    rows.push({ v, name: names[i - 1], color: colors[i - 1] });
  }
  if (!rows.length) {
    tip.hidden = true;
    return;
  }
  rows.sort((a, b) => b.v - a.v);
  const shown = rows.slice(0, 12);
  const children = [
    h('div', { class: 't-time' }, timeLabel(u.data[0][idx], span, true)),
    ...shown.map(r => h('div', { class: 't-row' },
      h('span', { class: 'key', style: { background: r.color } }),
      h('b', {}, fmt(r.v)),
      h('span', {}, r.name))),
  ];
  if (rows.length > shown.length) children.push(h('div', { class: 't-time' }, `+${rows.length - shown.length} more`));
  tip.replaceChildren(...children);
  tip.hidden = false;
  const left = u.cursor.left + u.over.offsetLeft;
  const w = tip.offsetWidth;
  const x = left + 16 + w > u.width ? left - w - 16 : left + 16;
  tip.style.left = Math.max(0, x) + 'px';
  tip.style.top = Math.max(0, u.cursor.top + u.over.offsetTop - 20) + 'px';
}

// sparkline draws values (with nulls for gaps) as a thin line with a faint area.
export function sparkline(values, color) {
  const w = 120, hgt = 34;
  // A service that started reporting mid-range shouldn't be squeezed into
  // the right edge: the sparkline has no time axis, so drop the leading gap.
  const firstIdx = values.findIndex(v => v !== null && Number.isFinite(v));
  if (firstIdx > 0) values = values.slice(firstIdx);
  const el = svg('svg', { class: 'spark', viewBox: `0 0 ${w} ${hgt}`, preserveAspectRatio: 'none', 'aria-hidden': 'true' });
  const nums = values.filter(v => v !== null && Number.isFinite(v));
  if (nums.length < 2) return el;
  const max = Math.max(...nums, 0);
  const min = Math.min(...nums, 0);
  const span = max - min || 1;
  const x = i => (i / (values.length - 1)) * w;
  const y = v => hgt - 2 - ((v - min) / span) * (hgt - 4);
  let line = '';
  let area = '';
  let open = false;
  let startX = 0;
  values.forEach((v, i) => {
    if (v === null || !Number.isFinite(v)) {
      if (open) area += `L${x(i - 1)},${hgt}L${startX},${hgt}Z`;
      open = false;
      return;
    }
    if (!open) {
      line += `M${x(i)},${y(v)}`;
      area += `M${x(i)},${hgt}L${x(i)},${y(v)}`;
      startX = x(i);
      open = true;
    } else {
      line += `L${x(i)},${y(v)}`;
      area += `L${x(i)},${y(v)}`;
    }
  });
  if (open) area += `L${x(values.length - 1)},${hgt}L${startX},${hgt}Z`;
  el.append(
    svg('path', { d: area, fill: color, opacity: '0.12', stroke: 'none' }),
    svg('path', { d: line, fill: 'none', stroke: color, 'stroke-width': '1.5', 'vector-effect': 'non-scaling-stroke', 'stroke-linejoin': 'round' }),
  );
  return el;
}

// gridValues aligns one matrix series onto the window's step grid.
export function gridValues(values, win) {
  const out = [];
  const m = new Map((values || []).map(([t, v]) => [Math.round(t), Number(v)]));
  for (let t = win.start; t <= win.end; t += win.step) out.push(m.has(t) ? m.get(t) : null);
  return out;
}

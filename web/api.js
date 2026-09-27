// The dashboard was written against TinyObs's V1 endpoints. v1Fetch answers
// those same URLs by calling the Prometheus HTTP API instead, and returns the
// V1 response shapes, so the server only has to serve one API.
(function () {
    const json = (status, body) => ({
        ok: status < 400,
        status,
        statusText: status < 400 ? 'OK' : 'Error',
        json: async () => body,
        text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
    });

    async function prom(path, params) {
        const res = await fetch('/api/v1/' + path + '?' + new URLSearchParams(params));
        const body = await res.json();
        if (body.status !== 'success') throw Object.assign(new Error(body.error || res.statusText), { status: res.status });
        return body.data;
    }

    // Counters are charted as their per-second rate: a cumulative total only
    // ever climbs and hides what is happening now.
    const isCounter = (name) => /(_total|_count|_sum|_bucket)$/.test(name);
    window.isCounter = isCounter;

    const withoutName = (m) => { const l = { ...m }; delete l.__name__; return l; };
    const seconds = (iso, fallback) => (iso ? Date.parse(iso) : fallback) / 1000;

    // Latest value of every series with data in the last hour of [start, end].
    // Capped at an hour so wide ranges don't load every stored sample just to
    // keep the last one; charts still query the full range.
    async function latest(q) {
        const end = seconds(q.get('end'), Date.now());
        const start = seconds(q.get('start'), Date.now() - 3600e3);
        const window = Math.max(1, Math.min(3600, Math.ceil(end - start)));
        const [data, meta] = await Promise.all([
            prom('query', { query: `last_over_time({__name__=~".+"}[${window}s])`, time: end }),
            prom('metadata', {}).catch(() => ({})),
        ]);
        const metrics = data.result.map((s) => ({
            name: s.metric.__name__,
            type: meta[s.metric.__name__]?.[0]?.type,
            value: parseFloat(s.value[1]),
            labels: withoutName(s.metric),
            timestamp: new Date(end * 1000).toISOString(),
        }));
        return { metrics, count: metrics.length };
    }

    // One metric's series over [start, end], at most maxPoints per series.
    async function range(q) {
        const name = q.get('metric');
        const end = seconds(q.get('end'), Date.now());
        const start = seconds(q.get('start'), Date.now() - 3600e3);
        const maxPoints = parseInt(q.get('maxPoints') || '1000', 10);
        const step = Math.max(15, Math.ceil((end - start) / maxPoints));
        const selector = `{__name__=${JSON.stringify(name)}}`;
        const query = isCounter(name) ? `rate(${selector}[${Math.max(300, 4 * step)}s])` : selector;
        const data = await prom('query_range', { query, start, end, step });
        return {
            data: data.result.map((s) => ({
                metric: name,
                labels: withoutName(s.metric),
                points: s.values.map(([t, v]) => ({ t: Math.round(t * 1000), v: parseFloat(v) })),
            })),
        };
    }

    // A PromQL range query over the last hour.
    async function execute(body) {
        const { query } = JSON.parse(body);
        const end = Date.now() / 1000;
        const data = await prom('query_range', { query, start: end - 3600, end, step: 15 });
        return { status: 'success', query, data };
    }

    // Server totals, read from TinyObs's own /metrics.
    async function metricsText() {
        const text = await (await fetch('/metrics')).text();
        const get = (name) => {
            const m = text.match(new RegExp('^' + name + ' (\\S+)$', 'm'));
            return m ? parseFloat(m[1]) : 0;
        };
        return get;
    }

    async function stats() {
        const get = await metricsText();
        return { TotalMetrics: get('tinyobs_samples_appended_total'), TotalSeries: get('tinyobs_series') };
    }

    // Disk is bounded by series limit and retention rather than a byte cap;
    // report that bound so the usage gauge stays meaningful.
    async function storage() {
        const get = await metricsText();
        const bound = get('tinyobs_series_limit') * (get('tinyobs_retention_seconds') / 15) * 14;
        return { used_bytes: get('tinyobs_storage_bytes'), max_bytes: bound };
    }

    window.v1Fetch = async function (url, opts) {
        const u = new URL(url, location.origin);
        try {
            switch (u.pathname) {
                case '/v1/query': return json(200, await latest(u.searchParams));
                case '/v1/query/range': return json(200, await range(u.searchParams));
                case '/v1/query/execute': return json(200, await execute(opts.body));
                case '/v1/stats': return json(200, await stats());
                case '/v1/storage': return json(200, await storage());
            }
            return json(404, { error: 'unknown endpoint ' + u.pathname });
        } catch (err) {
            return json(err.status || 500, err.message);
        }
    };
})();

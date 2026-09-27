        let metricsData = [];
        let selectedMetrics = new Set();
        let currentRange = '24h';
        let charts = {};
        let exploreChart = null;
        let currentView = 'dashboard';
        let comparisonMode = false;
        let chartsPerService = parseInt(localStorage.getItem('tinyobs-charts-per-service') || '6');
        let expandedServices = new Set();

        // Initialize theme from localStorage
        const savedTheme = localStorage.getItem('tinyobs-theme') || 'dark';
        document.documentElement.setAttribute('data-theme', savedTheme);

        // Chart colours come from the CSS custom properties in dashboard.css,
        // so both themes are defined in one place.
        function cssVar(name) {
            return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
        }

        function updateChartDefaults() {
            Chart.defaults.color = cssVar('--muted');
            Chart.defaults.borderColor = cssVar('--rule');
            Chart.defaults.font.family = '"JetBrains Mono", ui-monospace, monospace';
            Chart.defaults.font.size = 11;
            Chart.defaults.plugins.tooltip.backgroundColor = cssVar('--raised');
            Chart.defaults.plugins.tooltip.borderColor = cssVar('--rule');
            Chart.defaults.plugins.tooltip.borderWidth = 1;
            Chart.defaults.plugins.tooltip.titleColor = cssVar('--fg');
            Chart.defaults.plugins.tooltip.bodyColor = cssVar('--soft');
            Chart.defaults.plugins.tooltip.cornerRadius = 0;
            Chart.defaults.plugins.tooltip.boxPadding = 4;
            Chart.defaults.plugins.legend.labels.boxWidth = 10;
            Chart.defaults.plugins.legend.labels.boxHeight = 2;
            Chart.defaults.scale.ticks.maxRotation = 0;
            Chart.defaults.scale.ticks.autoSkipPadding = 24;
            Chart.defaults.elements.line.borderWidth = 1.5;
            Chart.defaults.elements.point.radius = 0;
            Chart.defaults.elements.point.hoverRadius = 3;
        }

        // 24-hour clock on time axes; the locale default ("4:33:42 p.m.") is
        // too wide for small charts.
        const TIME_AXIS = {
            tooltipFormat: 'HH:mm:ss',
            displayFormats: { millisecond: 'HH:mm:ss', second: 'HH:mm:ss', minute: 'HH:mm', hour: 'HH:mm', day: 'MMM d' },
        };
        updateChartDefaults();

        // Series colours: amber first, then muted hues that stay apart on
        // the warm background.
        const COLOR_PALETTE_DARK = ['#f5a524', '#6fb3a8', '#8aa8d8', '#d9776b', '#a8bd72', '#b99ad0', '#d8c39a', '#72c3d6'];
        const COLOR_PALETTE_LIGHT = ['#b86e00', '#2f7c70', '#3f64a6', '#b0473a', '#5d7a22', '#7d559c', '#8a6d2f', '#227f95'];

        function getColorPalette() {
            const theme = document.documentElement.getAttribute('data-theme');
            return theme === 'light' ? COLOR_PALETTE_LIGHT : COLOR_PALETTE_DARK;
        }

        // Hash function for stable color assignment
        function hashString(str) {
            let hash = 0;
            for (let i = 0; i < str.length; i++) {
                const char = str.charCodeAt(i);
                hash = ((hash << 5) - hash) + char;
                hash = hash & hash; // Convert to 32bit integer
            }
            return Math.abs(hash);
        }

        // Get stable color for a metric series
        function getStableColor(metricName, labels) {
            const key = metricName + JSON.stringify(labels || {});
            const hash = hashString(key);
            const palette = getColorPalette();
            return palette[hash % palette.length];
        }

        // Theme Toggle
        function toggleTheme() {
            const current = document.documentElement.getAttribute('data-theme');
            const newTheme = current === 'dark' ? 'light' : 'dark';
            document.documentElement.setAttribute('data-theme', newTheme);
            localStorage.setItem('tinyobs-theme', newTheme);

            // Update theme toggle button
            const toggle = document.getElementById('themeToggle');
            toggle.textContent = newTheme === 'dark' ? 'light' : 'dark';

            // Update chart defaults and re-render all charts
            updateChartDefaults();
            if (currentView === 'dashboard') {
                refreshDashboard();
            } else if (selectedMetrics.size > 0) {
                renderExploreChart();
            }
        }

        // Export dashboard configuration
        function exportConfig() {
            const config = {
                version: '2.2.0',
                exportedAt: new Date().toISOString(),
                view: currentView,
                timeRange: currentRange,
                theme: document.documentElement.getAttribute('data-theme'),
                comparisonMode: comparisonMode,
                filters: {
                    service: document.getElementById('serviceFilter').value,
                    endpoint: document.getElementById('endpointFilter').value,
                    metricName: document.getElementById('metricNameFilter').value
                },
                selectedMetrics: Array.from(selectedMetrics)
            };

            // Create downloadable JSON file
            const blob = new Blob([JSON.stringify(config, null, 2)], { type: 'application/json' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'tinyobs-config-' + new Date().toISOString().split('T')[0] + '.json';
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);

            console.log('Dashboard configuration exported');
        }

        // Import dashboard configuration
        function importConfig(event) {
            const file = event.target.files[0];
            if (!file) return;

            const reader = new FileReader();
            reader.onload = function(e) {
                try {
                    const config = JSON.parse(e.target.result);

                    // Validate config structure
                    if (!config.version || !config.view) {
                        alert('Invalid configuration file');
                        return;
                    }

                    // Restore theme
                    if (config.theme) {
                        document.documentElement.setAttribute('data-theme', config.theme);
                        localStorage.setItem('tinyobs-theme', config.theme);
                        const toggle = document.getElementById('themeToggle');
                        toggle.textContent = config.theme === 'dark' ? 'light' : 'dark';
                        updateChartDefaults();
                    }

                    // Restore time range
                    if (config.timeRange) {
                        currentRange = config.timeRange;
                        document.querySelectorAll('.time-btn').forEach(btn => {
                            btn.classList.toggle('active', btn.getAttribute('data-range') === config.timeRange);
                        });
                    }

                    // Restore filters
                    if (config.filters) {
                        if (config.filters.service) document.getElementById('serviceFilter').value = config.filters.service;
                        if (config.filters.endpoint) document.getElementById('endpointFilter').value = config.filters.endpoint;
                        if (config.filters.metricName) document.getElementById('metricNameFilter').value = config.filters.metricName;
                    }

                    // Restore comparison mode
                    if (config.hasOwnProperty('comparisonMode')) {
                        comparisonMode = config.comparisonMode;
                        const toggleBtn = document.getElementById('compareToggle');
                        if (toggleBtn) {
                            toggleBtn.classList.toggle('active', comparisonMode);
                        }
                    }

                    // Restore selected metrics
                    if (config.selectedMetrics) {
                        selectedMetrics = new Set(config.selectedMetrics);
                    }

                    // Switch to saved view and refresh
                    if (config.view) {
                        switchView(config.view);
                    }

                    console.log('Dashboard configuration imported successfully');
                } catch (error) {
                    alert('Failed to import configuration: ' + error.message);
                    console.error('Import error:', error);
                }
            };
            reader.readAsText(file);

            // Reset file input so same file can be imported again
            event.target.value = '';
        }

        // Toggle comparison mode
        function toggleComparison() {
            comparisonMode = !comparisonMode;
            const toggleBtn = document.getElementById('compareToggle');
            toggleBtn.classList.toggle('active', comparisonMode);

            // Refresh dashboard to show/hide comparison
            if (currentView === 'dashboard') {
                refreshDashboard();
            }
        }

        // View Switching
        function switchView(view) {
            currentView = view;
            document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
            document.querySelectorAll('.nav-tab').forEach(t => t.classList.remove('active'));

            document.getElementById(`${view}-view`).classList.add('active');
            document.getElementById(`${view}Tab`).classList.add('active');

            if (view === 'dashboard') {
                loadDashboard();
            } else {
                loadMetrics();
            }
        }

        // Time Range
        function setTimeRange(range) {
            currentRange = range;
            document.querySelectorAll('.time-btn').forEach(b => b.classList.remove('active'));
            event.target.classList.add('active');

            if (currentView === 'dashboard') {
                refreshDashboard();
            } else if (selectedMetrics.size > 0) {
                renderExploreChart();
            }
        }

        // Stats
        function formatBytes(bytes) {
            if (!bytes) return '0 B';
            const k = 1024;
            const sizes = ['B', 'KB', 'MB', 'GB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + sizes[i];
        }

        function formatValue(value) {
            if (typeof value !== 'number') return value;
            if (value >= 1000000) return (value / 1000000).toFixed(2) + 'M';
            if (value >= 1000) return (value / 1000).toFixed(2) + 'K';
            return value.toFixed(2);
        }

        async function loadStats() {
            try {
                const [statsRes, storageRes] = await Promise.all([
                    v1Fetch('/v1/stats'),
                    v1Fetch('/v1/storage')
                ]);

                if (statsRes.ok) {
                    const stats = await statsRes.json();
                    document.getElementById('totalMetrics').textContent = stats.TotalMetrics?.toLocaleString() || '0';
                    document.getElementById('totalSeries').textContent = stats.TotalSeries?.toLocaleString() || '0';
                }

                if (storageRes.ok) {
                    const storage = await storageRes.json();
                    const el = document.getElementById('storageSize');
                    el.textContent = formatBytes(storage.used_bytes);
                    el.title = 'Upper bound at the series limit and retention: ' + formatBytes(storage.max_bytes);
                }
            } catch (error) {
                console.error('Stats error:', error);
            }
        }

        // Dashboard View
        async function loadDashboard() {
            const container = document.getElementById('dashboardContent');
            container.innerHTML = '<div class="loading">Loading dashboard</div>';

            try {
                const now = Date.now();
                const ranges = { '1h': 1, '6h': 6, '24h': 24, '7d': 168 };
                const hours = ranges[currentRange];
                const start = new Date(now - hours * 60 * 60 * 1000).toISOString();
                const end = new Date(now).toISOString();

                const response = await v1Fetch(`/v1/query?start=${start}&end=${end}`);
                if (!response.ok) throw new Error(`Query failed: ${response.status} ${response.statusText}`);

                const data = await response.json();
                metricsData = data.metrics || [];

                // Check if we have any metrics at all
                if (metricsData.length === 0) {
                    container.innerHTML = `
                        <div class="empty-state">
                            <p>no series in this range</p>
                            <p>send some with the Go SDK or remote write, or start the example app:<br><code>go run ./cmd/example</code></p>
                        </div>
                    `;
                    return;
                }

                // Extract unique services, endpoints, and metric names
                const services = {};
                const endpoints = new Set();
                const metricNames = new Set();

                metricsData.forEach(m => {
                    const service = m.labels?.service || m.labels?.__service || 'default';
                    if (!services[service]) services[service] = [];
                    services[service].push(m);

                    // Collect endpoints
                    if (m.labels?.endpoint) endpoints.add(m.labels.endpoint);
                    if (m.labels?.path) endpoints.add(m.labels.path);

                    // Collect metric names
                    metricNames.add(m.name);
                });

                // Update all filters
                updateDashboardFilters(Object.keys(services), Array.from(endpoints).sort(), Array.from(metricNames).sort());

                // Render service sections
                renderDashboard(services);
            } catch (error) {
                console.error('Dashboard error:', error);
                const errorMsg = error.message.includes('Failed to fetch') || error.message.includes('NetworkError')
                    ? 'cannot reach the TinyObs server'
                    : `failed to load: ${error.message}`;
                container.innerHTML = `
                    <div class="empty-state">
                        <p>${errorMsg}</p>
                        <p>start the server with:<br><code>go run ./cmd/server</code></p>
                    </div>
                `;
            }
        }

        function updateDashboardFilters(services, endpoints, metricNames) {
            // Update service filters
            const serviceFilter = document.getElementById('serviceFilter');
            const exploreServiceFilter = document.getElementById('exploreServiceFilter');
            const serviceOptions = services.map(s => `<option value="${s}">${s}</option>`).join('');
            serviceFilter.innerHTML = '<option value="all">all services</option>' + serviceOptions;
            exploreServiceFilter.innerHTML = '<option value="all">all services</option>' + serviceOptions;

            // Update endpoint filter
            const endpointFilter = document.getElementById('endpointFilter');
            const endpointOptions = endpoints.map(e => `<option value="${e}">${e}</option>`).join('');
            endpointFilter.innerHTML = '<option value="all">all endpoints</option>' + endpointOptions;

            // Update metric name filter
            const metricNameFilter = document.getElementById('metricNameFilter');
            const metricOptions = metricNames.map(m => `<option value="${m}">${m}</option>`).join('');
            metricNameFilter.innerHTML = '<option value="all">all metrics</option>' + metricOptions;
        }

        function renderDashboard(services) {
            const container = document.getElementById('dashboardContent');
            const selectedService = document.getElementById('serviceFilter').value;
            const selectedEndpoint = document.getElementById('endpointFilter').value;
            const selectedMetricName = document.getElementById('metricNameFilter').value;
            const hideSystemMetrics = document.getElementById('hideSystemMetrics')?.checked ?? true;

            const servicesToShow = selectedService === 'all'
                ? Object.entries(services)
                : [[selectedService, services[selectedService]]].filter(([_, v]) => v);

            if (servicesToShow.length === 0) {
                container.innerHTML = '<div class="empty-state"><p>no series for this service</p></div>';
                return;
            }

            container.innerHTML = servicesToShow.map(([service, metrics]) => {
                // Apply filters
                let filteredMetrics = metrics;

                // Filter out system metrics if checkbox is checked
                if (hideSystemMetrics) {
                    filteredMetrics = filteredMetrics.filter(m =>
                        !m.name.startsWith('go_') &&
                        !m.name.startsWith('process_') &&
                        !m.name.startsWith('runtime_')
                    );
                }

                if (selectedEndpoint !== 'all') {
                    filteredMetrics = filteredMetrics.filter(m =>
                        m.labels?.endpoint === selectedEndpoint || m.labels?.path === selectedEndpoint
                    );
                }

                if (selectedMetricName !== 'all') {
                    filteredMetrics = filteredMetrics.filter(m => m.name === selectedMetricName);
                }

                // Group metrics by name
                const grouped = {};
                filteredMetrics.forEach(m => {
                    if (!grouped[m.name]) grouped[m.name] = [];
                    grouped[m.name].push(m);
                });

                if (Object.keys(grouped).length === 0) {
                    return ''; // Skip this service if no metrics match filters
                }

                const totalMetrics = Object.keys(grouped).length;
                const isExpanded = expandedServices.has(service);
                const limit = isExpanded ? totalMetrics : chartsPerService;
                const metricsToShow = Object.entries(grouped).slice(0, limit);
                const hasMore = totalMetrics > limit;

                const chartCards = metricsToShow.map(([name, data], idx) => {
                    const chartId = `chart-${service}-${name.replace(/[^a-zA-Z0-9]/g, '-')}-${idx}`;

                    const plotted = isCounter(name) ? 'rate / s' : (data[0]?.type || 'gauge');

                    return `
                        <div class="chart-card" onclick="openChartModal('${name.replace(/'/g, "\\'")}', '${service.replace(/'/g, "\\'")}')">
                            <div class="chart-header">
                                <div>
                                    <div class="chart-title">${name}</div>
                                    <div class="chart-subtitle">${data.length} series</div>
                                </div>
                                <div class="chart-status" title="${isCounter(name) ? 'Counter, charted as its per-second rate over 5m' : 'Charted as stored'}">${plotted}</div>
                            </div>
                            <div class="chart-wrapper">
                                <canvas id="${chartId}"></canvas>
                            </div>
                        </div>
                    `;
                }).join('');

                const showMoreButton = hasMore ? `
                    <div style="text-align: center; padding: 8px 0 4px;">
                        <button class="primary" onclick="expandService('${service}')">
                            show ${totalMetrics - limit} more
                        </button>
                    </div>
                ` : '';

                return `
                    <div class="service-section" id="service-${service}">
                        <div class="service-header" onclick="toggleService('${service}')">
                            <h3>
                                <span class="service-icon"></span>
                                ${service}
                                <span class="badge">${totalMetrics} metrics</span>
                                ${hasMore && !isExpanded ? `<span class="badge">showing ${limit} of ${totalMetrics}</span>` : ''}
                            </h3>
                            <span class="collapse-icon">▾</span>
                        </div>
                        <div class="service-charts">
                            ${chartCards}
                            ${showMoreButton}
                        </div>
                    </div>
                `;
            }).join('');

            // Render charts after DOM update
            setTimeout(() => {
                servicesToShow.forEach(([service, metrics]) => {
                    // Apply the same filters when rendering charts
                    let filteredMetrics = metrics;

                    if (hideSystemMetrics) {
                        filteredMetrics = filteredMetrics.filter(m =>
                            !m.name.startsWith('go_') &&
                            !m.name.startsWith('process_') &&
                            !m.name.startsWith('runtime_')
                        );
                    }

                    if (selectedEndpoint !== 'all') {
                        filteredMetrics = filteredMetrics.filter(m =>
                            m.labels?.endpoint === selectedEndpoint || m.labels?.path === selectedEndpoint
                        );
                    }

                    if (selectedMetricName !== 'all') {
                        filteredMetrics = filteredMetrics.filter(m => m.name === selectedMetricName);
                    }

                    const grouped = {};
                    filteredMetrics.forEach(m => {
                        if (!grouped[m.name]) grouped[m.name] = [];
                        grouped[m.name].push(m);
                    });

                    const totalMetrics = Object.keys(grouped).length;
                    const isExpanded = expandedServices.has(service);
                    const limit = isExpanded ? totalMetrics : chartsPerService;

                    Object.entries(grouped).slice(0, limit).forEach(([name, data], idx) => {
                        const chartId = `chart-${service}-${name.replace(/[^a-zA-Z0-9]/g, '-')}-${idx}`;
                        renderServiceChart(chartId, name, data, selectedEndpoint, selectedMetricName);
                    });
                });
            }, 100);
        }

        async function renderServiceChart(canvasId, metricName, data, selectedEndpoint, selectedMetricName) {
            const canvas = document.getElementById(canvasId);
            if (!canvas) return;

            try {
                const now = Date.now();
                const ranges = { '1h': 1, '6h': 6, '24h': 24, '7d': 168 };
                const hours = ranges[currentRange];
                const start = new Date(now - hours * 60 * 60 * 1000).toISOString();
                const end = new Date(now).toISOString();

                // Fetch current data
                const response = await v1Fetch(`/v1/query/range?metric=${encodeURIComponent(metricName)}&start=${start}&end=${end}&maxPoints=200`);
                if (!response.ok) return;

                const result = await response.json();
                if (!result.data || result.data.length === 0) return;

                // Filter series based on active filters
                let filteredSeries = result.data;
                if (selectedEndpoint !== 'all') {
                    filteredSeries = filteredSeries.filter(series =>
                        series.labels?.endpoint === selectedEndpoint || series.labels?.path === selectedEndpoint
                    );
                }

                if (filteredSeries.length === 0) return;

                // Create datasets for current data
                const datasets = filteredSeries.slice(0, 5).map((series) => {
                    const color = getStableColor(series.metric, series.labels);
                    return {
                        label: (series.labels && Object.keys(series.labels).length > 0
                            ? Object.entries(series.labels).filter(([k]) => !k.startsWith('__')).map(([k, v]) => `${k}="${v}"`).join(', ')
                            : metricName) + (comparisonMode ? ' (now)' : ''),
                        data: series.points.map(p => ({ x: p.t, y: p.v })),
                        borderColor: color,
                        backgroundColor: color + '20',
                        borderWidth: 2,
                        pointRadius: (ctx) => (ctx.dataset.data.length < 3 ? 2 : 0),
                        tension: 0.1,
                        borderDash: []
                    };
                });

                // If comparison mode is enabled, fetch 24h ago data
                if (comparisonMode) {
                    const compareStart = new Date(now - (hours + 24) * 60 * 60 * 1000).toISOString();
                    const compareEnd = new Date(now - 24 * 60 * 60 * 1000).toISOString();

                    const compareResponse = await v1Fetch(`/v1/query/range?metric=${encodeURIComponent(metricName)}&start=${compareStart}&end=${compareEnd}&maxPoints=200`);
                    if (compareResponse.ok) {
                        const compareResult = await compareResponse.json();
                        if (compareResult.data && compareResult.data.length > 0) {
                            let compareFilteredSeries = compareResult.data;
                            if (selectedEndpoint !== 'all') {
                                compareFilteredSeries = compareFilteredSeries.filter(series =>
                                    series.labels?.endpoint === selectedEndpoint || series.labels?.path === selectedEndpoint
                                );
                            }

                            // Add comparison datasets with dashed lines
                            compareFilteredSeries.slice(0, 5).forEach((series, idx) => {
                                if (idx < datasets.length) {
                                    const color = getStableColor(series.metric, series.labels);
                                    // Shift timestamps forward by 24h to align with current data
                                    const shiftedPoints = series.points.map(p => ({
                                        x: p.t + (24 * 60 * 60 * 1000),
                                        y: p.v
                                    }));

                                    datasets.push({
                                        label: (series.labels && Object.keys(series.labels).length > 0
                                            ? Object.entries(series.labels).filter(([k]) => !k.startsWith('__')).map(([k, v]) => `${k}="${v}"`).join(', ')
                                            : metricName) + ' (24h ago)',
                                        data: shiftedPoints,
                                        borderColor: color,
                                        backgroundColor: color + '10',
                                        borderWidth: 2,
                                        pointRadius: (ctx) => (ctx.dataset.data.length < 3 ? 2 : 0),
                                        tension: 0.1,
                                        borderDash: [5, 5] // Dashed line for comparison
                                    });
                                }
                            });
                        }
                    }
                }

                if (charts[canvasId]) charts[canvasId].destroy();

                charts[canvasId] = new Chart(canvas, {
                    type: 'line',
                    data: { datasets },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        interaction: { mode: 'index', intersect: false },
                        plugins: {
                            legend: {
                                display: comparisonMode,
                                position: 'top',
                                labels: {
                                    color: 'var(--text-secondary)',
                                    font: { size: 11 },
                                    boxWidth: 12,
                                    usePointStyle: true
                                }
                            },
                            tooltip: {
                                backgroundColor: cssVar('--raised'),
                                borderColor: cssVar('--rule'),
                                borderWidth: 1
                            }
                        },
                        scales: {
                            x: {
                                type: 'time',
                                time: TIME_AXIS,
                                grid: { display: false },
                                ticks: { color: cssVar('--faint'), maxTicksLimit: 6 }
                            },
                            y: {
                                grid: { color: cssVar('--rule') },
                                ticks: { color: cssVar('--faint') }
                            }
                        }
                    }
                });
            } catch (error) {
                console.error('Chart render error:', error);
            }
        }

        function setChartsPerService() {
            const value = document.getElementById('chartsPerServiceSelect').value;
            chartsPerService = parseInt(value);
            localStorage.setItem('tinyobs-charts-per-service', chartsPerService.toString());
            expandedServices.clear(); // Reset expansions
            filterDashboard();
        }

        function expandService(service) {
            expandedServices.add(service);
            filterDashboard();
        }

        function toggleService(service) {
            const section = document.getElementById(`service-${service}`);
            section.classList.toggle('collapsed');
        }

        function filterDashboard() {
            const services = {};
            metricsData.forEach(m => {
                const service = m.labels?.service || m.labels?.__service || 'default';
                if (!services[service]) services[service] = [];
                services[service].push(m);
            });
            renderDashboard(services);
        }

        // Status thresholds for smart indicators
        // Modal functionality
        let modalChart = null;
        let modalMetricData = null;

        async function openChartModal(metricName, service) {
            try {
                // Set modal title
                document.getElementById('modalTitle').textContent = metricName;

                // Fetch data for the modal chart
                const now = Date.now();
                const ranges = { '1h': 1, '6h': 6, '24h': 24, '7d': 168 };
                const hours = ranges[currentRange];
                const start = new Date(now - hours * 60 * 60 * 1000).toISOString();
                const end = new Date(now).toISOString();

                const response = await v1Fetch(`/v1/query/range?metric=${encodeURIComponent(metricName)}&start=${start}&end=${end}&maxPoints=500`);
                if (!response.ok) throw new Error('Failed to fetch data');

                const result = await response.json();
                modalMetricData = result;

                // Render chart in modal
                renderModalChart(metricName, result);

                // Pre-fill query editor
                document.getElementById('queryEditor').value = isCounter(metricName) ? `rate(${metricName}[5m])` : metricName;

                // Populate metric info
                populateMetricInfo(metricName, result, service);

                // Show modal
                document.getElementById('chartModal').showModal();
            } catch (error) {
                console.error('Error opening modal:', error);
                alert('Failed to load chart data');
            }
        }

        function renderModalChart(metricName, result) {
            const canvas = document.getElementById('modalChart');

            // Destroy existing chart
            if (modalChart) {
                modalChart.destroy();
                modalChart = null;
            }

            // Prepare datasets
            const datasets = [];
            const colors = getColorPalette();

            if (result.data && result.data.length > 0) {
                result.data.forEach((series, idx) => {
                    const color = colors[idx % colors.length];
                    const label = series.labels && Object.keys(series.labels).length > 0
                        ? Object.entries(series.labels).filter(([k]) => !k.startsWith('__')).map(([k, v]) => `${k}="${v}"`).join(', ')
                        : metricName;

                    datasets.push({
                        label: label,
                        data: series.points.map(p => ({ x: p.t, y: p.v })),
                        borderColor: color,
                        backgroundColor: color + '20',
                        borderWidth: 2,
                        pointRadius: 2,
                        pointHoverRadius: 4,
                        tension: 0.1
                    });
                });
            }

            // Create new chart
            modalChart = new Chart(canvas, {
                type: 'line',
                data: { datasets },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    interaction: { mode: 'index', intersect: false },
                    plugins: {
                        legend: {
                            display: datasets.length > 1,
                            position: 'top',
                            labels: {
                                color: cssVar('--soft'),
                                font: { size: 12 },
                                boxWidth: 12,
                                usePointStyle: true
                            }
                        },
                        tooltip: {
                            backgroundColor: cssVar('--raised'),
                            borderColor: cssVar('--rule'),
                            borderWidth: 1,
                            titleColor: cssVar('--fg'),
                            bodyColor: cssVar('--soft'),
                            callbacks: {
                                title: (items) => {
                                    if (items.length > 0) {
                                        return new Date(items[0].parsed.x).toLocaleString();
                                    }
                                    return '';
                                }
                            }
                        }
                    },
                    scales: {
                        x: {
                            type: 'time',
                            time: TIME_AXIS,
                            grid: { display: false },
                            ticks: { color: cssVar('--faint') }
                        },
                        y: {
                            grid: { color: cssVar('--rule') },
                            ticks: { color: cssVar('--faint') }
                        }
                    }
                }
            });
        }

        function populateMetricInfo(metricName, result, service) {
            const grid = document.getElementById('metricInfoGrid');

            // Calculate stats
            let totalPoints = 0;
            let minValue = Infinity;
            let maxValue = -Infinity;
            let avgValue = 0;
            let seriesCount = result.data ? result.data.length : 0;

            if (result.data) {
                result.data.forEach(series => {
                    series.points.forEach(p => {
                        totalPoints++;
                        minValue = Math.min(minValue, p.v);
                        maxValue = Math.max(maxValue, p.v);
                        avgValue += p.v;
                    });
                });
            }

            if (totalPoints > 0) {
                avgValue /= totalPoints;
            } else {
                minValue = 0;
                maxValue = 0;
            }

            grid.innerHTML = `
                <div class="metric-info-item">
                    <div class="metric-info-label">Metric Name</div>
                    <div class="metric-info-value">${metricName}</div>
                </div>
                <div class="metric-info-item">
                    <div class="metric-info-label">Service</div>
                    <div class="metric-info-value">${service || 'N/A'}</div>
                </div>
                <div class="metric-info-item">
                    <div class="metric-info-label">Series Count</div>
                    <div class="metric-info-value">${seriesCount}</div>
                </div>
                <div class="metric-info-item">
                    <div class="metric-info-label">Time Range</div>
                    <div class="metric-info-value">${currentRange}</div>
                </div>
                <div class="metric-info-item">
                    <div class="metric-info-label">Min Value</div>
                    <div class="metric-info-value">${minValue.toFixed(2)}</div>
                </div>
                <div class="metric-info-item">
                    <div class="metric-info-label">Max Value</div>
                    <div class="metric-info-value">${maxValue.toFixed(2)}</div>
                </div>
                <div class="metric-info-item">
                    <div class="metric-info-label">Avg Value</div>
                    <div class="metric-info-value">${avgValue.toFixed(2)}</div>
                </div>
                <div class="metric-info-item">
                    <div class="metric-info-label">Data Points</div>
                    <div class="metric-info-value">${totalPoints}</div>
                </div>
            `;
        }

        function closeChartModal() {
            const modal = document.getElementById('chartModal');
            modal.close();

            // Clean up chart
            if (modalChart) {
                modalChart.destroy();
                modalChart = null;
            }

            modalMetricData = null;
        }

        async function executeModalQuery() {
            const query = document.getElementById('queryEditor').value.trim();
            if (!query) {
                alert('Please enter a query');
                return;
            }

            try {
                // Use TinyQuery API
                const response = await v1Fetch('/v1/query/execute', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ query })
                });

                if (!response.ok) {
                    const error = await response.text();
                    throw new Error(error);
                }

                const result = await response.json();

                // Update modal chart with new data
                const metricName = query.split('(')[0].trim();
                renderModalChart(metricName, result);

                // Update metric info
                populateMetricInfo(metricName, result, 'custom');
            } catch (error) {
                console.error('Query execution error:', error);
                alert(`Query failed: ${error.message}`);
            }
        }

        async function copyQuery() {
            const query = document.getElementById('queryEditor').value;
            try {
                await navigator.clipboard.writeText(query);
                const btn = event.target;
                const originalText = btn.textContent;
                btn.textContent = 'copied';
                setTimeout(() => { btn.textContent = originalText; }, 2000);
            } catch (error) {
                console.error('Copy failed:', error);
                alert('Failed to copy to clipboard');
            }
        }

        function shareQuery() {
            const query = document.getElementById('queryEditor').value;
            const url = new URL(window.location.href);
            url.searchParams.set('query', query);
            url.searchParams.set('range', currentRange);

            const shareUrl = url.toString();

            // Copy to clipboard
            navigator.clipboard.writeText(shareUrl).then(() => {
                const btn = event.target;
                const originalText = btn.textContent;
                btn.textContent = 'link copied';
                setTimeout(() => { btn.textContent = originalText; }, 2000);
            }).catch(err => {
                console.error('Share failed:', err);
                alert(`Share URL: ${shareUrl}`);
            });
        }

        function exportChartPNG() {
            if (!modalChart) {
                alert('No chart to export');
                return;
            }

            const canvas = document.getElementById('modalChart');
            canvas.toBlob((blob) => {
                const url = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = `tinyobs-chart-${Date.now()}.png`;
                a.click();
                URL.revokeObjectURL(url);
            });
        }

        function exportChartCSV() {
            if (!modalMetricData || !modalMetricData.data) {
                alert('No data to export');
                return;
            }

            let csv = 'Timestamp,Series,Value\n';

            modalMetricData.data.forEach(series => {
                const seriesLabel = series.labels && Object.keys(series.labels).length > 0
                    ? Object.entries(series.labels).filter(([k]) => !k.startsWith('__')).map(([k, v]) => `${k}=${v}`).join(',')
                    : series.metric;

                series.points.forEach(p => {
                    const timestamp = new Date(p.t).toISOString();
                    csv += `${timestamp},"${seriesLabel}",${p.v}\n`;
                });
            });

            const blob = new Blob([csv], { type: 'text/csv' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `tinyobs-data-${Date.now()}.csv`;
            a.click();
            URL.revokeObjectURL(url);
        }

        function exportChartJSON() {
            if (!modalMetricData) {
                alert('No data to export');
                return;
            }

            const json = JSON.stringify(modalMetricData, null, 2);
            const blob = new Blob([json], { type: 'application/json' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `tinyobs-data-${Date.now()}.json`;
            a.click();
            URL.revokeObjectURL(url);
        }

        function refreshDashboard() {
            loadDashboard();
        }

        // Explore View
        async function loadMetrics() {
            try {
                const now = Date.now();
                const ranges = { '1h': 1, '6h': 6, '24h': 24, '7d': 168 };
                const hours = ranges[currentRange];
                const start = new Date(now - hours * 60 * 60 * 1000).toISOString();
                const end = new Date(now).toISOString();

                const response = await v1Fetch(`/v1/query?start=${start}&end=${end}`);
                if (!response.ok) throw new Error('Query failed');

                const data = await response.json();
                metricsData = data.metrics || [];

                renderMetricsBrowser();
            } catch (error) {
                console.error('Load metrics error:', error);
                document.getElementById('metricsBrowser').innerHTML =
                    '<div class="empty-state"><p>failed to load series</p></div>';
            }
        }

        function renderMetricsBrowser() {
            const browser = document.getElementById('metricsBrowser');
            const searchTerm = document.getElementById('searchBox').value.toLowerCase();
            const typeFilter = document.getElementById('metricTypeFilter').value;
            const serviceFilter = document.getElementById('exploreServiceFilter').value;

            if (!metricsData || metricsData.length === 0) {
                browser.innerHTML = '<div class="empty-state"><p>no series yet</p></div>';
                return;
            }

            // Group metrics
            const grouped = {};
            metricsData.forEach(m => {
                const key = seriesKey(m);
                if (!grouped[key]) {
                    grouped[key] = {
                        name: m.name,
                        labels: m.labels || {},
                        values: []
                    };
                }
                grouped[key].values.push(m);
            });

            // Filter
            const filtered = Object.entries(grouped).filter(([key, data]) => {
                const matchesSearch = data.name.toLowerCase().includes(searchTerm);
                const matchesType = typeFilter === 'all' || inferMetricType(data.name) === typeFilter;
                const service = data.labels?.service || data.labels?.__service || 'default';
                const matchesService = serviceFilter === 'all' || service === serviceFilter;
                return matchesSearch && matchesType && matchesService;
            });

            if (filtered.length === 0) {
                browser.innerHTML = '<div class="empty-state"><p>nothing matches these filters</p></div>';
                return;
            }

            browser.innerHTML = filtered.map(([key, data]) => {
                const latest = data.values[data.values.length - 1];
                const isSelected = selectedMetrics.has(key);
                const escapedKey = key.replace(/"/g, '&quot;');

                const labelsHtml = Object.keys(data.labels).length > 0
                    ? Object.entries(data.labels)
                        .filter(([k]) => !k.startsWith('__'))
                        .map(([k, v]) => `<div class="label"><span class="label-key">${k}</span>=<span class="label-value">"${v}"</span></div>`)
                        .join('')
                    : '<div class="label">no labels</div>';

                return `
                    <div class="metric-row ${isSelected ? 'selected' : ''}" data-key="${escapedKey}" onclick="toggleMetric(this, '${escapedKey}')">
                        <div class="metric-content">
                            <div class="metric-name">${data.name}</div>
                            <div class="metric-labels">${labelsHtml}</div>
                            <div class="metric-value">
                                <div><span class="value-label">latest</span>${formatValue(latest.value)}</div>
                                <div><span class="value-label">samples</span>${data.values.length}</div>
                            </div>
                        </div>
                    </div>
                `;
            }).join('');
        }

        function seriesKey(metric) {
            const labels = metric.labels ? JSON.stringify(metric.labels) : '';
            return `${metric.name}${labels}`;
        }

        function inferMetricType(name) {
            if (name.includes('_total') || name.includes('_count')) return 'counter';
            if (name.includes('_bucket') || name.includes('_duration')) return 'histogram';
            return 'gauge';
        }

        function toggleMetric(element, key) {
            const decodedKey = key.replace(/&quot;/g, '"');

            if (selectedMetrics.has(decodedKey)) {
                selectedMetrics.delete(decodedKey);
                element.classList.remove('selected');
            } else {
                selectedMetrics.add(decodedKey);
                element.classList.add('selected');
            }

            document.getElementById('selectedCount').textContent = `${selectedMetrics.size} selected`;

            if (selectedMetrics.size > 0) {
                renderExploreChart();
                const chartEl = document.getElementById('selectedChart');
                chartEl.style.display = 'block';
                // Smooth scroll to chart after a brief delay to ensure it's rendered
                setTimeout(() => {
                    chartEl.scrollIntoView({ behavior: 'smooth', block: 'start' });
                }, 100);
            } else {
                document.getElementById('selectedChart').style.display = 'none';
            }
        }

        async function renderExploreChart() {
            if (selectedMetrics.size === 0) return;

            const now = Date.now();
            const ranges = { '1h': 1, '6h': 6, '24h': 24, '7d': 168 };
            const hours = ranges[currentRange];
            const start = new Date(now - hours * 60 * 60 * 1000).toISOString();
            const end = new Date(now).toISOString();

            const metricNames = new Set();
            metricsData.forEach(m => {
                const key = seriesKey(m);
                if (selectedMetrics.has(key)) {
                    metricNames.add(m.name);
                }
            });

            try {
                const promises = Array.from(metricNames).map(async name => {
                    const response = await v1Fetch(`/v1/query/range?metric=${encodeURIComponent(name)}&start=${start}&end=${end}&maxPoints=500`);
                    if (!response.ok) throw new Error('Range query failed');
                    return response.json();
                });

                const results = await Promise.all(promises);
                const datasets = [];

                results.forEach(result => {
                    if (!result.data) return;

                    result.data.forEach(series => {
                        const key = seriesKey({ name: series.metric, labels: series.labels });
                        if (!selectedMetrics.has(key)) return;

                        const color = getStableColor(series.metric, series.labels);
                        const label = series.labels && Object.keys(series.labels).length > 0
                            ? `${series.metric}{${Object.entries(series.labels).filter(([k]) => !k.startsWith('__')).map(([k, v]) => `${k}="${v}"`).join(', ')}}`
                            : series.metric;

                        datasets.push({
                            label: label,
                            data: series.points.map(p => ({ x: p.t, y: p.v })),
                            borderColor: color,
                            backgroundColor: color + '20',
                            borderWidth: 2,
                            pointRadius: (ctx) => (ctx.dataset.data.length < 3 ? 2 : 0),
                            tension: 0.1
                        });
                    });
                });

                if (exploreChart) exploreChart.destroy();

                const ctx = document.getElementById('exploreChart').getContext('2d');
                exploreChart = new Chart(ctx, {
                    type: 'line',
                    data: { datasets },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        interaction: { mode: 'index', intersect: false },
                        plugins: {
                            legend: {
                                display: true,
                                position: 'bottom',
                                labels: {
                                    color: cssVar('--soft'),
                                    padding: 12,
                                    font: { size: 11, family: "'Monaco', 'Menlo', 'Consolas', monospace" }
                                }
                            }
                        },
                        scales: {
                            x: {
                                type: 'time',
                                time: TIME_AXIS,
                                grid: { display: false },
                                ticks: { color: cssVar('--faint') }
                            },
                            y: {
                                grid: { color: cssVar('--rule') },
                                ticks: { color: cssVar('--faint') }
                            }
                        }
                    }
                });
            } catch (error) {
                console.error('Explore chart error:', error);
            }
        }

        function filterMetrics() {
            renderMetricsBrowser();
        }

        function clearSelection() {
            selectedMetrics.clear();
            document.querySelectorAll('.metric-row').forEach(row => row.classList.remove('selected'));
            document.getElementById('selectedChart').style.display = 'none';
            document.getElementById('selectedCount').textContent = '0 selected';
        }

        function refreshMetrics() {
            selectedMetrics.clear();
            loadMetrics();
        }

        // Search
        document.addEventListener('DOMContentLoaded', () => {
            const searchBox = document.getElementById('searchBox');
            searchBox.addEventListener('input', filterMetrics);

            // Initialize theme toggle button icon
            const currentTheme = document.documentElement.getAttribute('data-theme');
            const themeToggleBtn = document.getElementById('themeToggle');
            themeToggleBtn.textContent = currentTheme === 'dark' ? 'light' : 'dark';

            // Initialize charts-per-service select
            const chartsSelect = document.getElementById('chartsPerServiceSelect');
            chartsSelect.value = chartsPerService.toString();

            // Enhanced Keyboard shortcuts
            document.addEventListener('keydown', (e) => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && e.target.id === 'queryEditor') {
                    e.preventDefault();
                    executeModalQuery();
                    return;
                }
                // Don't trigger shortcuts while typing in an input or textarea.
                const isInputField = e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA';
                if (isInputField && e.key !== 'Escape') return;

                // ESC to clear selection in Explore view or blur input/textarea
                if (e.key === 'Escape') {
                    if (currentView === 'explore') {
                        clearSelection();
                    }
                    // Blur any focused input or textarea
                    if (isInputField) {
                        document.activeElement.blur();
                    }
                }

                // D for Dashboard view
                if ((e.key === 'd' || e.key === 'D') && !isInputField) {
                    e.preventDefault();
                    switchView('dashboard');
                }

                // E for Explore view
                if ((e.key === 'e' || e.key === 'E') && !isInputField) {
                    e.preventDefault();
                    switchView('explore');
                }

                // R to refresh current view
                if ((e.key === 'r' || e.key === 'R') && !isInputField) {
                    e.preventDefault();
                    if (currentView === 'dashboard') {
                        refreshDashboard();
                    } else {
                        refreshMetrics();
                    }
                }

                // T to toggle theme
                if ((e.key === 't' || e.key === 'T') && !isInputField) {
                    e.preventDefault();
                    toggleTheme();
                }

                // / to focus search (if in explore view)
                if (e.key === '/' && currentView === 'explore' && !e.ctrlKey && !e.metaKey) {
                    e.preventDefault();
                    searchBox.focus();
                }

                // 1-4 for quick time range selection (Dashboard only)
                if (currentView === 'dashboard' && !isInputField) {
                    const timeRanges = { '1': '1h', '2': '6h', '3': '24h', '4': '7d' };
                    if (timeRanges[e.key]) {
                        e.preventDefault();
                        setTimeRange(timeRanges[e.key]);
                    }
                }
            });

            // Initial load
            loadStats();
            loadDashboard();
        });

        // Poll for fresh data. TinyObs has no push channel; 15s matches the SDK flush interval.
        setInterval(() => {
            if (document.hidden) return;
            loadStats();
            if (currentView === 'dashboard') {
                refreshDashboard();
            }
        }, 15000);

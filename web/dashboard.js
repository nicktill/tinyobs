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

        // Update chart defaults based on theme
        function updateChartDefaults() {
            const theme = document.documentElement.getAttribute('data-theme');
            Chart.defaults.color = theme === 'light' ? '#6c757d' : '#8b949e';
            Chart.defaults.borderColor = theme === 'light' ? '#dee2e6' : '#30363d';
        }
        updateChartDefaults();

        // Color palette for consistent chart colors (works in both themes)
        const COLOR_PALETTE_DARK = [
            '#58a6ff', '#3fb950', '#f0883e', '#f85149', '#bc8cff',
            '#ff7b72', '#79c0ff', '#56d364', '#ffa657', '#f778ba',
            '#a5d6ff', '#7ee787', '#ffbc6f', '#ff9492', '#d2a8ff'
        ];

        const COLOR_PALETTE_LIGHT = [
            '#0d6efd', '#198754', '#fd7e14', '#dc3545', '#6f42c1',
            '#0dcaf0', '#20c997', '#ffc107', '#d63384', '#6610f2',
            '#0a58ca', '#146c43', '#bb6d00', '#a71d2a', '#59359a'
        ];

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
            toggle.textContent = newTheme === 'dark' ? '☀️' : '🌙';

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
                        toggle.textContent = config.theme === 'dark' ? '☀️' : '🌙';
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
                    fetch('/v1/stats'),
                    fetch('/v1/storage')
                ]);

                if (statsRes.ok) {
                    const stats = await statsRes.json();
                    document.getElementById('totalMetrics').textContent = stats.TotalMetrics?.toLocaleString() || '0';
                    document.getElementById('totalSeries').textContent = stats.TotalSeries?.toLocaleString() || '0';
                }

                if (storageRes.ok) {
                    const storage = await storageRes.json();
                    const usedPct = ((storage.used_bytes / storage.max_bytes) * 100).toFixed(1);
                    document.getElementById('storageSize').textContent =
                        formatBytes(storage.used_bytes) + ' / ' + formatBytes(storage.max_bytes) + ' (' + usedPct + '%)';
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

                const response = await fetch(`/v1/query?start=${start}&end=${end}`);
                if (!response.ok) throw new Error('Query failed');

                const data = await response.json();
                metricsData = data.metrics || [];

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
                container.innerHTML = '<div class="empty-state"><div class="empty-icon">⚠️</div><p>Failed to load dashboard</p></div>';
            }
        }

        function updateDashboardFilters(services, endpoints, metricNames) {
            // Update service filters
            const serviceFilter = document.getElementById('serviceFilter');
            const exploreServiceFilter = document.getElementById('exploreServiceFilter');
            const serviceOptions = services.map(s => `<option value="${s}">${s}</option>`).join('');
            serviceFilter.innerHTML = '<option value="all">All Services</option>' + serviceOptions;
            exploreServiceFilter.innerHTML = '<option value="all">All Services</option>' + serviceOptions;

            // Update endpoint filter
            const endpointFilter = document.getElementById('endpointFilter');
            const endpointOptions = endpoints.map(e => `<option value="${e}">${e}</option>`).join('');
            endpointFilter.innerHTML = '<option value="all">All Endpoints</option>' + endpointOptions;

            // Update metric name filter
            const metricNameFilter = document.getElementById('metricNameFilter');
            const metricOptions = metricNames.map(m => `<option value="${m}">${m}</option>`).join('');
            metricNameFilter.innerHTML = '<option value="all">All Metrics</option>' + metricOptions;
        }

        function renderDashboard(services) {
            const container = document.getElementById('dashboardContent');
            const selectedService = document.getElementById('serviceFilter').value;
            const selectedEndpoint = document.getElementById('endpointFilter').value;
            const selectedMetricName = document.getElementById('metricNameFilter').value;

            const servicesToShow = selectedService === 'all'
                ? Object.entries(services)
                : [[selectedService, services[selectedService]]].filter(([_, v]) => v);

            if (servicesToShow.length === 0) {
                container.innerHTML = '<div class="empty-state"><div class="empty-icon">📊</div><p>No metrics for this service</p></div>';
                return;
            }

            container.innerHTML = servicesToShow.map(([service, metrics]) => {
                // Apply filters
                let filteredMetrics = metrics;

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

                    // Calculate basic stats for status indicator
                    let avgValue = 0;
                    let latestValue = 0;
                    let pointCount = 0;

                    data.forEach(m => {
                        avgValue += m.value;
                        latestValue = m.value;
                        pointCount++;
                    });

                    if (pointCount > 0) {
                        avgValue /= pointCount;
                    }

                    // Get status (we'll calculate trend properly when rendering the chart)
                    const status = getMetricStatus(name, avgValue, latestValue, 0);

                    return `
                        <div class="chart-card" onclick="openChartModal('${name.replace(/'/g, "\\'")}', '${service.replace(/'/g, "\\'")}')">
                            <div class="chart-header">
                                <div>
                                    <div class="chart-title">${name}</div>
                                    <div class="chart-subtitle">${data.length} series • Click to explore</div>
                                </div>
                                <div class="chart-status" style="color: ${status.color}; font-size: 1.25rem;" title="${status.label}">
                                    ${status.icon}
                                </div>
                            </div>
                            <div class="chart-wrapper">
                                <canvas id="${chartId}"></canvas>
                            </div>
                        </div>
                    `;
                }).join('');

                const showMoreButton = hasMore ? `
                    <div style="text-align: center; padding: 1rem;">
                        <button class="primary" onclick="expandService('${service}')">
                            Show ${totalMetrics - limit} more charts
                        </button>
                    </div>
                ` : '';

                return `
                    <div class="service-section" id="service-${service}">
                        <div class="service-header" onclick="toggleService('${service}')">
                            <h3>
                                <span class="service-icon">📦</span>
                                ${service}
                                <span class="badge">${totalMetrics} metrics</span>
                                ${hasMore && !isExpanded ? `<span class="badge" style="background: var(--bg-tertiary);">Showing ${limit} of ${totalMetrics}</span>` : ''}
                            </h3>
                            <span class="collapse-icon">▼</span>
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
                const response = await fetch(`/v1/query/range?metric=${encodeURIComponent(metricName)}&start=${start}&end=${end}&maxPoints=200`);
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
                        pointRadius: 0,
                        tension: 0.1,
                        borderDash: []
                    };
                });

                // If comparison mode is enabled, fetch 24h ago data
                if (comparisonMode) {
                    const compareStart = new Date(now - (hours + 24) * 60 * 60 * 1000).toISOString();
                    const compareEnd = new Date(now - 24 * 60 * 60 * 1000).toISOString();

                    const compareResponse = await fetch(`/v1/query/range?metric=${encodeURIComponent(metricName)}&start=${compareStart}&end=${compareEnd}&maxPoints=200`);
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
                                        pointRadius: 0,
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
                                backgroundColor: '#161b22',
                                borderColor: '#30363d',
                                borderWidth: 1
                            }
                        },
                        scales: {
                            x: {
                                type: 'time',
                                grid: { color: '#30363d' },
                                ticks: { color: '#8b949e', maxTicksLimit: 6 }
                            },
                            y: {
                                grid: { color: '#30363d' },
                                ticks: { color: '#8b949e' }
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
        const statusThresholds = {
            error_rate: { warning: 0.01, critical: 0.05 }, // 1% warning, 5% critical
            latency: { warning: 100, critical: 500 }, // milliseconds
            response_time: { warning: 200, critical: 1000 },
            cpu_usage: { warning: 70, critical: 90 }, // percentage
            memory_usage: { warning: 75, critical: 90 },
            disk_usage: { warning: 80, critical: 95 },
            request_duration: { warning: 1000, critical: 5000 },
            failure_rate: { warning: 0.02, critical: 0.1 }
        };

        function getMetricStatus(metricName, avgValue, latestValue, trend) {
            // Find matching threshold
            let threshold = null;
            for (const [key, value] of Object.entries(statusThresholds)) {
                if (metricName.toLowerCase().includes(key)) {
                    threshold = value;
                    break;
                }
            }

            if (!threshold) {
                // No specific threshold, use generic trend-based status
                if (Math.abs(trend) < 0.05) return { icon: '→', color: '#8b949e', label: 'Stable' };
                if (trend > 0.2) return { icon: '↑', color: '#f85149', label: 'Rising' };
                if (trend < -0.2) return { icon: '↓', color: '#56d364', label: 'Declining' };
                return { icon: '→', color: '#8b949e', label: 'Stable' };
            }

            // Use threshold-based status
            const value = latestValue !== undefined ? latestValue : avgValue;
            if (value >= threshold.critical) {
                return { icon: '🚨', color: '#f85149', label: 'Critical' };
            } else if (value >= threshold.warning) {
                return { icon: '⚠️', color: '#ffa657', label: 'Warning' };
            } else {
                return { icon: '✅', color: '#56d364', label: 'Healthy' };
            }
        }

        function calculateTrend(points) {
            if (!points || points.length < 2) return 0;

            // Compare first half vs second half
            const midpoint = Math.floor(points.length / 2);
            const firstHalf = points.slice(0, midpoint);
            const secondHalf = points.slice(midpoint);

            const firstAvg = firstHalf.reduce((sum, p) => sum + p.y, 0) / firstHalf.length;
            const secondAvg = secondHalf.reduce((sum, p) => sum + p.y, 0) / secondHalf.length;

            if (firstAvg === 0) return 0;
            return (secondAvg - firstAvg) / firstAvg;
        }

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

                const response = await fetch(`/v1/query/range?metric=${encodeURIComponent(metricName)}&start=${start}&end=${end}&maxPoints=500`);
                if (!response.ok) throw new Error('Failed to fetch data');

                const result = await response.json();
                modalMetricData = result;

                // Render chart in modal
                renderModalChart(metricName, result);

                // Pre-fill query editor
                document.getElementById('queryEditor').value = metricName;

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
            const colors = ['#58a6ff', '#f78166', '#56d364', '#d2a8ff', '#ffa657', '#f85149'];

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
                                color: '#c9d1d9',
                                font: { size: 12 },
                                boxWidth: 12,
                                usePointStyle: true
                            }
                        },
                        tooltip: {
                            backgroundColor: '#161b22',
                            borderColor: '#30363d',
                            borderWidth: 1,
                            titleColor: '#c9d1d9',
                            bodyColor: '#8b949e',
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
                            grid: { color: '#30363d' },
                            ticks: { color: '#8b949e' }
                        },
                        y: {
                            grid: { color: '#30363d' },
                            ticks: { color: '#8b949e' }
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
                const response = await fetch('/v1/query/execute', {
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
                btn.textContent = '✓ Copied!';
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
                btn.textContent = '✓ Link Copied!';
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

                const response = await fetch(`/v1/query?start=${start}&end=${end}`);
                if (!response.ok) throw new Error('Query failed');

                const data = await response.json();
                metricsData = data.metrics || [];

                renderMetricsBrowser();
            } catch (error) {
                console.error('Load metrics error:', error);
                document.getElementById('metricsBrowser').innerHTML =
                    '<div class="empty-state"><div class="empty-icon">⚠️</div><p>Failed to load metrics</p></div>';
            }
        }

        function renderMetricsBrowser() {
            const browser = document.getElementById('metricsBrowser');
            const searchTerm = document.getElementById('searchBox').value.toLowerCase();
            const typeFilter = document.getElementById('metricTypeFilter').value;
            const serviceFilter = document.getElementById('exploreServiceFilter').value;

            if (!metricsData || metricsData.length === 0) {
                browser.innerHTML = '<div class="empty-state"><div class="empty-icon">📊</div><p>No metrics available</p></div>';
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
                browser.innerHTML = '<div class="empty-state"><p>No metrics match your filters</p></div>';
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
                                <div><span class="value-label">Latest:</span>${formatValue(latest.value)}</div>
                                <div><span class="value-label">Samples:</span>${data.values.length}</div>
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
                    const response = await fetch(`/v1/query/range?metric=${encodeURIComponent(name)}&start=${start}&end=${end}&maxPoints=500`);
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
                            pointRadius: 0,
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
                                    color: '#c9d1d9',
                                    padding: 12,
                                    font: { size: 11, family: "'Monaco', 'Menlo', 'Consolas', monospace" }
                                }
                            }
                        },
                        scales: {
                            x: {
                                type: 'time',
                                grid: { color: '#30363d' },
                                ticks: { color: '#8b949e' }
                            },
                            y: {
                                grid: { color: '#30363d' },
                                ticks: { color: '#8b949e' }
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
            themeToggleBtn.textContent = currentTheme === 'dark' ? '☀️' : '🌙';

            // Initialize charts-per-service select
            const chartsSelect = document.getElementById('chartsPerServiceSelect');
            chartsSelect.value = chartsPerService.toString();

            // Enhanced Keyboard shortcuts
            document.addEventListener('keydown', (e) => {
                // Don't trigger shortcuts if user is typing in an input or textarea
                // FIXED: Include TEXTAREA (query editor) in the check
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

                // M for Service Map (Topology) view
                if ((e.key === 'm' || e.key === 'M') && !isInputField) {
                    e.preventDefault();
                    switchView('topology');
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

            // Connect to WebSocket for real-time updates
            connectWebSocket();
        });

        // WebSocket connection management
        let ws = null;
        let wsReconnectInterval = null;
        let wsConnected = false;

        function connectWebSocket() {
            // Clear any existing reconnect interval to prevent multiple intervals
            if (wsReconnectInterval) {
                clearInterval(wsReconnectInterval);
                wsReconnectInterval = null;
            }

            // Close existing connection if any to prevent race conditions
            if (ws) {
                ws.close();
                ws = null;
            }

            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const wsUrl = `${protocol}//${window.location.host}/v1/ws`;

            try {
                ws = new WebSocket(wsUrl);

                ws.onopen = () => {
                    wsConnected = true;
                    console.log('📡 WebSocket connected - real-time updates enabled');

                    // Clear reconnect attempts
                    if (wsReconnectInterval) {
                        clearInterval(wsReconnectInterval);
                        wsReconnectInterval = null;
                    }

                    // Show connection indicator
                    updateConnectionStatus(true);
                };

                ws.onmessage = (event) => {
                    try {
                        const data = JSON.parse(event.data);

                        if (data.type === 'metrics_update') {
                            // Real-time metrics update received
                            console.log(`📊 Received ${data.count} metrics updates`);

                            // Update header stats only (don't re-render entire dashboard)
                            loadStats();

                            // Note: We don't auto-refresh the dashboard view to avoid
                            // disrupting the user's interaction with charts/filters.
                            // Stats in the header will update in real-time.
                        }
                    } catch (err) {
                        console.error('Failed to parse WebSocket message:', err);
                    }
                };

                ws.onerror = (error) => {
                    console.error('❌ WebSocket error:', error);
                };

                ws.onclose = () => {
                    wsConnected = false;
                    console.log('📡 WebSocket disconnected - attempting reconnect...');
                    updateConnectionStatus(false);

                    // Attempt to reconnect every 5 seconds
                    if (!wsReconnectInterval) {
                        wsReconnectInterval = setInterval(() => {
                            console.log('🔄 Reconnecting WebSocket...');
                            connectWebSocket();
                        }, 5000);
                    }
                };

            } catch (err) {
                console.error('Failed to create WebSocket:', err);
                wsConnected = false;
                updateConnectionStatus(false);
            }
        }

        function updateConnectionStatus(connected) {
            // Update UI to show connection status
            const statusEl = document.querySelector('.header-stats');
            if (statusEl && !document.getElementById('wsStatus')) {
                const statusBadge = document.createElement('div');
                statusBadge.id = 'wsStatus';
                statusBadge.className = 'stat-item';
                statusBadge.innerHTML = `
                    <div class="stat-label">Connection</div>
                    <div class="stat-value" style="font-size: 0.75rem; color: ${connected ? 'var(--accent-green)' : 'var(--accent-orange)'}">
                        ${connected ? '● Live' : '○ Reconnecting'}
                    </div>
                `;
                statusEl.appendChild(statusBadge);
            } else if (document.getElementById('wsStatus')) {
                const wsStatus = document.getElementById('wsStatus');
                wsStatus.innerHTML = `
                    <div class="stat-label">Connection</div>
                    <div class="stat-value" style="font-size: 0.75rem; color: ${connected ? 'var(--accent-green)' : 'var(--accent-orange)'}">
                        ${connected ? '● Live' : '○ Reconnecting'}
                    </div>
                `;
            }
        }

        // Fallback polling in case WebSocket fails (every 60s instead of 30s)
        setInterval(() => {
            if (!wsConnected) {
                console.log('⏰ Fallback polling (WebSocket not connected)');
                loadStats();
                if (currentView === 'dashboard') {
                    refreshDashboard();
                }
            }
        }, 60000);

        // ============================================================================
        // TOPOLOGY VIEW - Service Dependency Map
        // ============================================================================

        let topologyData = null;

        function refreshTopology() {
            const hours = document.getElementById('topologyTimeRange').value;
            fetch(`/v1/topology?hours=${hours}`)
                .then(res => res.json())
                .then(data => {
                    topologyData = data;
                    renderTopology(data);
                })
                .catch(err => {
                    console.error('Failed to load topology:', err);
                    document.getElementById('topologyLoading').textContent = 'Failed to load topology';
                });
        }

        function renderTopology(data) {
            const svg = document.getElementById('topologySvg');
            const loadingEl = document.getElementById('topologyLoading');

            loadingEl.style.display = 'none';

            // Update stats
            document.getElementById('nodeCount').textContent = `${data.nodes.length} services`;
            document.getElementById('edgeCount').textContent = `${data.edges.length} connections`;

            // Clear SVG
            svg.innerHTML = '';

            const width = svg.clientWidth || 1200;
            const height = 600;

            // Simple force-directed layout (no D3 needed!)
            const nodes = data.nodes.map(n => ({
                ...n,
                x: Math.random() * width,
                y: Math.random() * height,
                vx: 0,
                vy: 0
            }));

            const edges = data.edges.map(e => ({
                ...e,
                source: nodes.find(n => n.id === e.source),
                target: nodes.find(n => n.id === e.target)
            })).filter(e => e.source && e.target);

            // Physics simulation
            function simulate() {
                const alpha = 0.3;
                const iterations = 100;

                for (let iter = 0; iter < iterations; iter++) {
                    // Repulsion between nodes
                    for (let i = 0; i < nodes.length; i++) {
                        for (let j = i + 1; j < nodes.length; j++) {
                            const dx = nodes[j].x - nodes[i].x;
                            const dy = nodes[j].y - nodes[i].y;
                            const dist = Math.sqrt(dx * dx + dy * dy) || 1;
                            const force = 5000 / (dist * dist);

                            nodes[i].vx -= (dx / dist) * force;
                            nodes[i].vy -= (dy / dist) * force;
                            nodes[j].vx += (dx / dist) * force;
                            nodes[j].vy += (dy / dist) * force;
                        }
                    }

                    // Attraction along edges
                    edges.forEach(edge => {
                        const dx = edge.target.x - edge.source.x;
                        const dy = edge.target.y - edge.source.y;
                        const dist = Math.sqrt(dx * dx + dy * dy) || 1;
                        const force = dist / 100;

                        edge.source.vx += (dx / dist) * force;
                        edge.source.vy += (dy / dist) * force;
                        edge.target.vx -= (dx / dist) * force;
                        edge.target.vy -= (dy / dist) * force;
                    });

                    // Center gravity
                    const centerX = width / 2;
                    const centerY = height / 2;
                    nodes.forEach(node => {
                        node.vx += (centerX - node.x) * 0.01;
                        node.vy += (centerY - node.y) * 0.01;
                    });

                    // Update positions
                    nodes.forEach(node => {
                        node.x += node.vx * alpha;
                        node.y += node.vy * alpha;
                        node.vx *= 0.9;
                        node.vy *= 0.9;

                        // Keep in bounds
                        const margin = 50;
                        node.x = Math.max(margin, Math.min(width - margin, node.x));
                        node.y = Math.max(margin, Math.min(height - margin, node.y));
                    });
                }
            }

            simulate();

            // Render edges
            edges.forEach(edge => {
                // Calculate edge metrics
                const edgeLatency = edge.avg_latency_ms || 0;
                const edgeErrorRate = edge.error_rate || 0;
                const strokeWidth = Math.max(1.5, Math.min(8, edge.request_rate / 10));

                // Color based on health (error rate)
                let edgeColor = '#58a6ff'; // blue - healthy
                if (edgeErrorRate > 0.05) {
                    edgeColor = '#f85149'; // red - critical
                } else if (edgeErrorRate > 0.01) {
                    edgeColor = '#ffa657'; // orange - warning
                }

                const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
                line.setAttribute('x1', edge.source.x);
                line.setAttribute('y1', edge.source.y);
                line.setAttribute('x2', edge.target.x);
                line.setAttribute('y2', edge.target.y);
                line.setAttribute('stroke', edgeColor);
                line.setAttribute('stroke-width', strokeWidth);
                line.setAttribute('opacity', '0.6');
                line.style.cursor = 'pointer';

                // Enhanced edge tooltip
                const title = document.createElementNS('http://www.w3.org/2000/svg', 'title');
                title.textContent = `${edge.source.id} → ${edge.target.id}\n` +
                    `━━━━━━━━━━━━━━━━━━\n` +
                    `📊 Traffic: ${edge.request_rate.toFixed(1)} req/s\n` +
                    `⚠️  Error Rate: ${(edgeErrorRate * 100).toFixed(2)}%\n` +
                    `⏱️  Avg Latency: ${edgeLatency.toFixed(0)}ms`;
                line.appendChild(title);

                svg.appendChild(line);

                // Add arrow
                const angle = Math.atan2(edge.target.y - edge.source.y, edge.target.x - edge.source.x);
                const arrowSize = 10;
                const arrowDist = 35; // Distance from target
                const arrowX = edge.target.x - arrowDist * Math.cos(angle);
                const arrowY = edge.target.y - arrowDist * Math.sin(angle);

                const arrow = document.createElementNS('http://www.w3.org/2000/svg', 'polygon');
                const p1x = arrowX + arrowSize * Math.cos(angle - 2.5);
                const p1y = arrowY + arrowSize * Math.sin(angle - 2.5);
                const p2x = arrowX;
                const p2y = arrowY;
                const p3x = arrowX + arrowSize * Math.cos(angle + 2.5);
                const p3y = arrowY + arrowSize * Math.sin(angle + 2.5);

                arrow.setAttribute('points', `${p1x},${p1y} ${p2x},${p2y} ${p3x},${p3y}`);
                arrow.setAttribute('fill', edgeColor);
                arrow.setAttribute('opacity', '0.8');
                svg.appendChild(arrow);

                // Add edge label for high-traffic or high-latency connections
                if (edge.request_rate > 5 || edgeLatency > 100) {
                    const midX = (edge.source.x + edge.target.x) / 2;
                    const midY = (edge.source.y + edge.target.y) / 2;

                    // Background for label
                    const labelBg = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
                    const labelText = edgeLatency > 100 ? `${edgeLatency.toFixed(0)}ms` : `${edge.request_rate.toFixed(0)}/s`;
                    const labelWidth = labelText.length * 5 + 8;

                    labelBg.setAttribute('x', midX - labelWidth / 2);
                    labelBg.setAttribute('y', midY - 10);
                    labelBg.setAttribute('width', labelWidth);
                    labelBg.setAttribute('height', 14);
                    labelBg.setAttribute('rx', 3);
                    labelBg.setAttribute('fill', '#161b22');
                    labelBg.setAttribute('opacity', '0.9');
                    svg.appendChild(labelBg);

                    const label = document.createElementNS('http://www.w3.org/2000/svg', 'text');
                    label.setAttribute('x', midX);
                    label.setAttribute('y', midY);
                    label.setAttribute('text-anchor', 'middle');
                    label.setAttribute('fill', edgeColor);
                    label.setAttribute('font-size', '8');
                    label.setAttribute('font-weight', '600');
                    label.textContent = labelText;
                    svg.appendChild(label);
                }
            });

            // Render nodes
            nodes.forEach(node => {
                const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
                g.setAttribute('transform', `translate(${node.x},${node.y})`);
                g.style.cursor = 'pointer';

                // Determine health status based on error rate
                let healthColor, healthStatus;
                if (node.error_rate < 0.01) { // < 1%
                    healthColor = '#56d364'; // green
                    healthStatus = 'Healthy';
                } else if (node.error_rate < 0.05) { // 1-5%
                    healthColor = '#ffa657'; // orange
                    healthStatus = 'Warning';
                } else {
                    healthColor = '#f85149'; // red
                    healthStatus = 'Critical';
                }

                // Health indicator ring
                const healthRing = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
                healthRing.setAttribute('r', 30);
                healthRing.setAttribute('fill', 'none');
                healthRing.setAttribute('stroke', healthColor);
                healthRing.setAttribute('stroke-width', '3');
                healthRing.setAttribute('opacity', '0.8');
                g.appendChild(healthRing);

                // Node circle with gradient
                const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
                circle.setAttribute('r', 25);

                let color = '#58a6ff';
                if (node.type === 'database') color = '#56d364';
                if (node.type === 'external') color = '#ffa657';

                circle.setAttribute('fill', color);
                circle.setAttribute('fill-opacity', '0.9');
                circle.setAttribute('stroke', '#ffffff');
                circle.setAttribute('stroke-width', '2');

                // Enhanced tooltip
                const avgLatency = node.avg_latency_ms || 0;
                const title = document.createElementNS('http://www.w3.org/2000/svg', 'title');
                title.textContent = `${node.label} [${healthStatus}]\n` +
                    `━━━━━━━━━━━━━━━━━━\n` +
                    `📊 Throughput: ${node.request_rate.toFixed(1)} req/s\n` +
                    `⚠️  Error Rate: ${(node.error_rate * 100).toFixed(2)}%\n` +
                    `⏱️  Avg Latency: ${avgLatency.toFixed(0)}ms\n` +
                    `📦 Type: ${node.type}`;
                circle.appendChild(title);

                g.appendChild(circle);

                // Service name label
                const text = document.createElementNS('http://www.w3.org/2000/svg', 'text');
                text.setAttribute('y', 48);
                text.setAttribute('text-anchor', 'middle');
                text.setAttribute('fill', 'var(--text-primary)');
                text.setAttribute('font-size', '12');
                text.setAttribute('font-weight', '600');
                text.textContent = node.label;
                g.appendChild(text);

                // Metrics display - throughput
                if (node.request_rate > 0) {
                    const throughput = document.createElementNS('http://www.w3.org/2000/svg', 'text');
                    throughput.setAttribute('y', -35);
                    throughput.setAttribute('text-anchor', 'middle');
                    throughput.setAttribute('fill', '#56d364');
                    throughput.setAttribute('font-size', '9');
                    throughput.setAttribute('font-weight', '600');
                    throughput.textContent = `${node.request_rate.toFixed(1)} req/s`;
                    g.appendChild(throughput);
                }

                // Metrics display - error rate (if > 0)
                if (node.error_rate > 0) {
                    const errorText = document.createElementNS('http://www.w3.org/2000/svg', 'text');
                    errorText.setAttribute('y', 0);
                    errorText.setAttribute('text-anchor', 'middle');
                    errorText.setAttribute('fill', healthColor);
                    errorText.setAttribute('font-size', '8');
                    errorText.setAttribute('font-weight', '700');
                    errorText.textContent = `${(node.error_rate * 100).toFixed(1)}%`;
                    g.appendChild(errorText);
                }

                // Metrics display - latency (if available)
                if (avgLatency > 0) {
                    const latencyText = document.createElementNS('http://www.w3.org/2000/svg', 'text');
                    latencyText.setAttribute('y', 60);
                    latencyText.setAttribute('text-anchor', 'middle');
                    latencyText.setAttribute('fill', '#8b949e');
                    latencyText.setAttribute('font-size', '9');
                    latencyText.textContent = `${avgLatency.toFixed(0)}ms`;
                    g.appendChild(latencyText);
                }

                svg.appendChild(g);
            });
        }

        // Load topology when switching to topology view
        const originalSwitchView = switchView;
        switchView = function(view) {
            originalSwitchView(view);
            if (view === 'topology' && !topologyData) {
                refreshTopology();
            }
        };

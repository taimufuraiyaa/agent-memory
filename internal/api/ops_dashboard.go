package api

// OperatorDashboardHTML contains the embedded HTML, styling, and client-side logic
// for the premium, read-only agent-memory operator dashboard.
const OperatorDashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>agent-memory Operator Dashboard</title>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
    <script type="text/javascript" src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
    <style>
        :root {
            --bg-base: #06060c;
            --bg-surface: rgba(18, 18, 38, 0.65);
            --bg-card: rgba(30, 30, 60, 0.35);
            --border-glow: rgba(139, 92, 246, 0.25);
            --border-color: rgba(255, 255, 255, 0.08);
            --text-primary: #f3f4f6;
            --text-secondary: #9ca3af;
            --accent-purple: #8b5cf6;
            --accent-cyan: #06b6d4;
            --accent-green: #10b981;
            --accent-red: #ef4444;
            --accent-amber: #f59e0b;
        }

        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
        }

        body {
            font-family: 'Inter', sans-serif;
            background-color: var(--bg-base);
            background-image: 
                radial-gradient(circle at 10% 20%, rgba(139, 92, 246, 0.1) 0%, transparent 40%),
                radial-gradient(circle at 90% 80%, rgba(6, 182, 212, 0.1) 0%, transparent 40%);
            color: var(--text-primary);
            min-height: 100vh;
            padding: 2rem;
            display: flex;
            flex-direction: column;
            gap: 2rem;
        }

        header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            background: var(--bg-surface);
            border: 1px solid var(--border-color);
            padding: 1.5rem 2rem;
            border-radius: 16px;
            backdrop-filter: blur(12px);
            box-shadow: 0 8px 32px 0 rgba(0, 0, 0, 0.37);
        }

        h1 {
            font-family: 'Outfit', sans-serif;
            font-size: 1.75rem;
            font-weight: 700;
            background: linear-gradient(135deg, #fff 0%, #a78bfa 100%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            display: flex;
            align-items: center;
            gap: 0.75rem;
        }

        .workspace-selector {
            display: flex;
            align-items: center;
            gap: 0.75rem;
            background: rgba(255, 255, 255, 0.05);
            padding: 0.5rem 1rem;
            border-radius: 8px;
            border: 1px solid var(--border-color);
        }

        .workspace-selector span {
            font-size: 0.875rem;
            color: var(--text-secondary);
        }

        .workspace-selector input {
            background: transparent;
            border: none;
            color: #fff;
            font-weight: 600;
            outline: none;
            font-family: inherit;
        }

        .dashboard-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
            gap: 1.5rem;
        }

        .card {
            background: var(--bg-surface);
            border: 1px solid var(--border-color);
            border-radius: 16px;
            padding: 1.5rem;
            backdrop-filter: blur(12px);
            box-shadow: 0 4px 20px rgba(0, 0, 0, 0.2);
            transition: transform 0.3s cubic-bezier(0.4, 0, 0.2, 1), box-shadow 0.3s ease, border-color 0.3s ease;
            position: relative;
            overflow: hidden;
        }

        .card::before {
            content: '';
            position: absolute;
            top: 0;
            left: 0;
            width: 100%;
            height: 4px;
            background: transparent;
            transition: background 0.3s ease;
        }

        .card:hover {
            transform: translateY(-4px);
            box-shadow: 0 12px 30px rgba(139, 92, 246, 0.15);
            border-color: var(--border-glow);
        }

        .card.provider::before { background: var(--accent-purple); }
        .card.storage::before { background: var(--accent-cyan); }
        .card.cache::before { background: var(--accent-green); }
        .card.scheduler::before { background: var(--accent-amber); }
        .card.graph::before { background: linear-gradient(90deg, var(--accent-purple), var(--accent-cyan)); }

        .card-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 1.25rem;
            border-bottom: 1px solid rgba(255, 255, 255, 0.05);
            padding-bottom: 0.75rem;
        }

        .card-title {
            font-family: 'Outfit', sans-serif;
            font-size: 1.125rem;
            font-weight: 600;
            color: #fff;
        }

        .metric-group {
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 1rem;
            margin-bottom: 1rem;
        }

        .metric-item {
            background: var(--bg-card);
            padding: 0.75rem 1rem;
            border-radius: 8px;
            border: 1px solid rgba(255, 255, 255, 0.03);
            display: flex;
            flex-direction: column;
            gap: 0.25rem;
        }

        .metric-label {
            font-size: 0.75rem;
            color: var(--text-secondary);
            text-transform: uppercase;
            letter-spacing: 0.05em;
        }

        .metric-value {
            font-size: 1.25rem;
            font-weight: 700;
            color: #fff;
            font-family: 'Outfit', sans-serif;
        }

        .metric-value.mono {
            font-family: 'JetBrains Mono', monospace;
            font-size: 1.1rem;
            font-weight: 500;
        }

        .status-indicator {
            display: inline-flex;
            align-items: center;
            gap: 0.5rem;
            font-size: 0.875rem;
            font-weight: 600;
        }

        .indicator-dot {
            width: 8px;
            height: 8px;
            border-radius: 50%;
            background-color: var(--accent-green);
            box-shadow: 0 0 10px var(--accent-green);
        }

        .indicator-dot.active {
            animation: pulse 2s infinite;
        }

        .indicator-dot.warning {
            background-color: var(--accent-amber);
            box-shadow: 0 0 10px var(--accent-amber);
        }

        .indicator-dot.error {
            background-color: var(--accent-red);
            box-shadow: 0 0 10px var(--accent-red);
        }

        .progress-bar-container {
            margin-top: 1rem;
            display: flex;
            flex-direction: column;
            gap: 0.5rem;
        }

        .progress-label-row {
            display: flex;
            justify-content: space-between;
            font-size: 0.8125rem;
            color: var(--text-secondary);
        }

        .progress-bar {
            width: 100%;
            height: 8px;
            background: rgba(255, 255, 255, 0.05);
            border-radius: 4px;
            overflow: hidden;
            position: relative;
        }

        .progress-fill {
            height: 100%;
            border-radius: 4px;
            transition: width 0.8s cubic-bezier(0.4, 0, 0.2, 1);
        }

        .progress-fill.purple { background: linear-gradient(90deg, var(--accent-purple), #c084fc); }
        .progress-fill.cyan { background: linear-gradient(90deg, var(--accent-cyan), #22d3ee); }
        .progress-fill.green { background: linear-gradient(90deg, var(--accent-green), #34d399); }

        .list-unstyled {
            display: flex;
            flex-direction: column;
            gap: 0.5rem;
        }

        .list-item {
            display: flex;
            justify-content: space-between;
            align-items: center;
            font-size: 0.875rem;
            padding: 0.5rem 0;
            border-bottom: 1px solid rgba(255, 255, 255, 0.03);
        }

        .list-item:last-child {
            border-bottom: none;
        }

        .list-item-key {
            color: var(--text-secondary);
        }

        .list-item-val {
            font-weight: 500;
        }

        .alert-banner {
            grid-column: 1 / -1;
            background: rgba(239, 68, 68, 0.1);
            border: 1px solid rgba(239, 68, 68, 0.2);
            border-radius: 12px;
            padding: 1rem;
            display: flex;
            align-items: center;
            gap: 1rem;
            font-size: 0.875rem;
            display: none;
        }

        .alert-banner.active {
            display: flex;
        }

        @keyframes pulse {
            0% {
                transform: scale(0.95);
                box-shadow: 0 0 0 0 rgba(16, 185, 129, 0.7);
            }
            70% {
                transform: scale(1);
                box-shadow: 0 0 0 8px rgba(16, 185, 129, 0);
            }
            100% {
                transform: scale(0.95);
                box-shadow: 0 0 0 0 rgba(16, 185, 129, 0);
            }
        }

        .footer {
            text-align: center;
            margin-top: auto;
            color: rgba(255, 255, 255, 0.25);
            font-size: 0.75rem;
            padding: 2rem 0 0 0;
            border-top: 1px solid rgba(255, 255, 255, 0.03);
        }
    </style>
</head>
<body>
    <header>
        <div>
            <h1>agent-memory <span>•</span> Operator Dashboard</h1>
        </div>
        <div class="workspace-selector">
            <span>Active Workspace:</span>
            <input type="text" id="workspaceInput" value="" placeholder="workspace-name" />
        </div>
    </header>

    <div class="alert-banner" id="alertBanner">
        <span class="indicator-dot error active"></span>
        <span id="alertMessage">Operator Warning: Mismatched vector model version detected on some rows.</span>
    </div>

    <div class="dashboard-grid">
        <!-- Provider Info Card -->
        <div class="card provider">
            <div class="card-header">
                <h3 class="card-title">Embedding Provider</h3>
                <div class="status-indicator" id="providerStatus">
                    <span class="indicator-dot active" id="providerDot"></span>
                    <span id="providerText">Active</span>
                </div>
            </div>
            <div class="list-unstyled">
                <div class="list-item">
                    <span class="list-item-key">Provider Name</span>
                    <span class="list-item-val" id="valProviderName">-</span>
                </div>
                <div class="list-item">
                    <span class="list-item-key">Model Version</span>
                    <span class="list-item-val" id="valModelVersion">-</span>
                </div>
                <div class="list-item">
                    <span class="list-item-key">ONNX Runtime Status</span>
                    <span class="list-item-val" id="valOnnxStatus">-</span>
                </div>
                <div class="list-item">
                    <span class="list-item-key">Vector Dimension</span>
                    <span class="list-item-val">384 (MiniLM-L6)</span>
                </div>
            </div>
        </div>

        <!-- Storage Card -->
        <div class="card storage">
            <div class="card-header">
                <h3 class="card-title">Storage & SQLite Health</h3>
                <div class="status-indicator">
                    <span class="indicator-dot active"></span>
                    <span>WAL Mode Enabled</span>
                </div>
            </div>
            <div class="metric-group">
                <div class="metric-item">
                    <span class="metric-label">Memory Count</span>
                    <span class="metric-value" id="valMemoryCount">-</span>
                </div>
                <div class="metric-item">
                    <span class="metric-label">Database Size</span>
                    <span class="metric-value" id="valDbSize">-</span>
                </div>
            </div>
            <div class="progress-bar-container">
                <div class="progress-label-row">
                    <span>Episodic / Semantic Split</span>
                    <span id="valSplitPct">-</span>
                </div>
                <div class="progress-bar">
                    <div class="progress-fill cyan" id="splitProgress" style="width: 0%;"></div>
                </div>
            </div>
        </div>

        <!-- Cache Performance Card -->
        <div class="card cache">
            <div class="card-header">
                <h3 class="card-title">Query Cache Performance</h3>
                <div class="status-indicator" id="cacheStatus">
                    <span class="indicator-dot active"></span>
                    <span>Enabled</span>
                </div>
            </div>
            <div class="metric-group">
                <div class="metric-item">
                    <span class="metric-label">Embedding Hit Rate</span>
                    <span class="metric-value" id="valEmbeddingHitRate">-</span>
                </div>
                <div class="metric-item">
                    <span class="metric-label">Result Hit Rate</span>
                    <span class="metric-value" id="valResultHitRate">-</span>
                </div>
            </div>
            <div class="list-unstyled">
                <div class="list-item">
                    <span class="list-item-key">Embedding Cache Size</span>
                    <span class="list-item-val" id="valEmbeddingEntries">-</span>
                </div>
                <div class="list-item">
                    <span class="list-item-key">Result Cache Size</span>
                    <span class="list-item-val" id="valResultEntries">-</span>
                </div>
                <div class="list-item">
                    <span class="list-item-key">Total Cache Hits / Misses</span>
                    <span class="list-item-val" id="valCacheRatio">-</span>
                </div>
            </div>
        </div>

        <!-- Lifecycle Card -->
        <div class="card scheduler">
            <div class="card-header">
                <h3 class="card-title">Background Maintenance</h3>
                <div class="status-indicator" id="schedulerStatus">
                    <span class="indicator-dot active"></span>
                    <span id="schedulerText">Healthy</span>
                </div>
            </div>
            <div class="list-unstyled">
                <div class="list-item">
                    <span class="list-item-key">Last Completed Run</span>
                    <span class="list-item-val mono" id="valLastRun">-</span>
                </div>
                <div class="list-item">
                    <span class="list-item-key">Last Run Duration</span>
                    <span class="list-item-val" id="valLastRunDuration">-</span>
                </div>
                <div class="list-item">
                    <span class="list-item-key">Decay / Evicted Count</span>
                    <span class="list-item-val" id="valDecayEvicted">-</span>
                </div>
                <div class="list-item">
                    <span class="list-item-key">Promoted / Demoted</span>
                    <span class="list-item-val" id="valPromoteDemote">-</span>
                </div>
            </div>
        </div>

        <!-- Graph Visualization Card -->
        <div class="card graph" style="grid-column: 1 / -1; min-height: 520px; display: flex; flex-direction: column;">
            <div class="card-header">
                <h3 class="card-title">Memory Relationship Network</h3>
                <div class="status-indicator">
                    <span class="indicator-dot active"></span>
                    <span>Interactive 2D Physics Graph</span>
                </div>
            </div>
            <div id="networkGraph" style="flex: 1; width: 100%; height: 420px; background: rgba(10, 10, 25, 0.3); border-radius: 12px; border: 1px solid rgba(255, 255, 255, 0.05); overflow: hidden; margin-top: 1rem;"></div>
        </div>
    </div>

    <div class="footer">
        agent-memory-engine v2.6.4 • Running on Local Host • Dark Mode Enabled
    </div>

    <script>
        // Get workspace from query params or default
        const urlParams = new URLSearchParams(window.location.search);
        let workspace = urlParams.get('workspace') || '';
        
        const workspaceInput = document.getElementById('workspaceInput');
        
        // Auto-refresh timer
        let refreshTimer;

        async function fetchHealth() {
            try {
                const response = await fetch('/health?workspace=' + encodeURIComponent(workspace));
                if (!response.ok) throw new Error('Network response not ok');
                const data = await response.json();
                
                document.getElementById('valProviderName').textContent = data.embedding_provider || '-';
                document.getElementById('valModelVersion').textContent = data.embedding_model_version || '-';
                document.getElementById('valOnnxStatus').textContent = data.onnx_runtime_available ? 'Available' : 'Unavailable';
                if (!workspace) {
                    workspace = data.workspace || 'agent-memory';
                    workspaceInput.value = workspace;
                }
            } catch (err) {
                console.error('Failed to fetch health:', err);
                document.getElementById('providerText').textContent = 'Disconnected';
                document.getElementById('providerDot').className = 'indicator-dot error active';
            }
        }

        async function fetchStats() {
            if (!workspace) return;
            try {
                const response = await fetch('/api/v1/stats?workspace=' + encodeURIComponent(workspace));
                if (!response.ok) throw new Error('Network stats response not ok');
                const data = await response.json();

                // Storage stats
                document.getElementById('valMemoryCount').textContent = data.memory_count;
                const dbMB = (data.db_size_bytes / (1024 * 1024)).toFixed(2);
                document.getElementById('valDbSize').textContent = dbMB + ' MB';

                const types = data.memory_type_counts || {};
                const episodic = types.episodic || 0;
                const semantic = types.semantic || 0;
                const total = episodic + semantic || 1;
                const semPct = ((semantic / total) * 100).toFixed(0);
                document.getElementById('valSplitPct').textContent = semPct + '% Semantic / ' + (100 - semPct).toFixed(0) + '% Episodic';
                document.getElementById('splitProgress').style.width = semPct + '%';

                // Cache stats
                if (data.cache) {
                    const c = data.cache;
                    document.getElementById('valEmbeddingHitRate').textContent = (c.embedding_hit_rate * 100).toFixed(1) + '%';
                    document.getElementById('valResultHitRate').textContent = (c.result_hit_rate * 100).toFixed(1) + '%';
                    document.getElementById('valEmbeddingEntries').textContent = c.embedding_entries;
                    document.getElementById('valResultEntries').textContent = c.result_entries;
                    document.getElementById('valCacheRatio').textContent = (c.embedding_hits + c.result_hits) + ' / ' + (c.embedding_misses + c.result_misses);
                }

                // Scheduler stats
                if (data.scheduler) {
                    const sched = data.scheduler;
                    const date = sched.last_completed_at ? new Date(sched.last_completed_at).toLocaleTimeString() : 'Never';
                    document.getElementById('valLastRun').textContent = date;
                    document.getElementById('valLastRunDuration').textContent = sched.last_duration_ms ? (sched.last_duration_ms / 1000).toFixed(2) + ' s' : '-';
                    document.getElementById('valDecayEvicted').textContent = 'Evicted: ' + (sched.last_evicted || 0);
                    document.getElementById('valPromoteDemote').textContent = 'P: ' + (sched.last_promoted || 0) + ' / D: ' + (sched.last_demoted || 0);
                }
            } catch (err) {
                console.error('Failed to fetch stats:', err);
            }
        }

        let networkInstance = null;
        let graphInitialized = false;

        async function fetchAndRenderGraph() {
            if (!workspace) return;
            try {
                const response = await fetch('/api/v1/graph?workspace=' + encodeURIComponent(workspace));
                if (!response.ok) throw new Error('Graph fetch failed');
                const data = await response.json();

                const container = document.getElementById('networkGraph');
                
                const typeColors = {
                    'episodic': '#8b5cf6',   // Purple
                    'semantic': '#06b6d4',   // Cyan
                    'procedural': '#10b981', // Green
                    'outcome': '#ef4444'     // Red
                };

                const nodes = data.nodes.map(n => {
                    const cleanContent = n.content.length > 50 ? n.content.substring(0, 47) + '...' : n.content;
                    return {
                        id: n.id,
                        label: cleanContent,
                        title: `ID: ${n.id}\nType: ${n.type}\nTier: ${n.storage_tier}\nContent: ${n.content}`,
                        color: {
                            background: typeColors[n.type] || '#6b7280',
                            border: 'rgba(255, 255, 255, 0.2)',
                            highlight: {
                                background: typeColors[n.type] || '#6b7280',
                                border: '#8b5cf6'
                            }
                        },
                        font: { color: '#f3f4f6', face: 'Inter', size: 12 },
                        shape: 'dot',
                        size: n.storage_tier === 'markdown' ? 22 : 14
                    };
                });

                const edgeStyles = {
                    'calls': { color: '#8b5cf6', dashes: false },       // purple for temporal
                    'depends_on': { color: '#10b981', dashes: true },   // green dashed for entities
                    'led_to': { color: '#ef4444', dashes: false },       // red for outcomes
                    'supersedes': { color: '#f59e0b', dashes: false },   // amber for supersedes
                    'derived_from': { color: '#3b82f6', dashes: true }, // blue dashed
                    'contradicts': { color: '#ef4444', dashes: true }   // red dashed
                };

                const edges = data.edges.map(e => {
                    const style = edgeStyles[e.type] || { color: '#9ca3af', dashes: false };
                    return {
                        from: e.source_id,
                        to: e.target_id,
                        label: e.type,
                        font: { color: '#9ca3af', size: 9, align: 'top', face: 'Inter' },
                        color: { color: style.color, highlight: '#ffffff' },
                        arrows: 'to',
                        value: e.weight,
                        dashes: style.dashes
                    };
                });

                const graphData = {
                    nodes: new vis.DataSet(nodes),
                    edges: new vis.DataSet(edges)
                };

                const options = {
                    nodes: {
                        shape: 'dot',
                        scaling: { min: 10, max: 30 },
                        borderWidth: 2,
                        shadow: { enabled: true, color: 'rgba(0,0,0,0.5)', size: 4 }
                    },
                    edges: {
                        width: 2,
                        shadow: { enabled: true, color: 'rgba(0,0,0,0.3)', size: 3 },
                        smooth: { type: 'continuous' }
                    },
                    physics: {
                        stabilization: { iterations: 150 },
                        barnesHut: {
                            gravitationalConstant: -1800,
                            centralGravity: 0.35,
                            springLength: 90,
                            springConstant: 0.05,
                            damping: 0.09,
                            avoidOverlap: 0.5
                        }
                    },
                    interaction: {
                        hover: true,
                        tooltipDelay: 100
                    }
                };

                if (networkInstance) {
                    networkInstance.destroy();
                }
                networkInstance = new vis.Network(container, graphData, options);
            } catch (err) {
                console.error('Failed to fetch graph:', err);
            }
        }

        function updateWorkspace() {
            workspace = workspaceInput.value.trim();
            if (workspace) {
                // Update URL parameter without reloading
                const newurl = window.location.protocol + '//' + window.location.host + window.location.pathname + '?workspace=' + encodeURIComponent(workspace);
                window.history.pushState({path:newurl},'',newurl);
                graphInitialized = false; // Reset to force re-render on new workspace
                refresh();
            }
        }

        workspaceInput.addEventListener('keypress', function (e) {
            if (e.key === 'Enter') {
                updateWorkspace();
            }
        });
        
        workspaceInput.addEventListener('blur', updateWorkspace);

        async function refresh() {
            await fetchHealth();
            await fetchStats();
            if (!graphInitialized) {
                await fetchAndRenderGraph();
                graphInitialized = true;
            }
        }

        // Initialize and setup polling
        refresh();
        refreshTimer = setInterval(refresh, 5000);
    </script>
</body>
</html>`

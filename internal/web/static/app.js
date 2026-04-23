(function () {
    'use strict';

    // --- State ---
    let currentSessions = [];
    let currentView = 'live';
    let historyData = [];
    let usageData = null;
    let sseSource = null;
    let reconnectTimer = null;
    let claudeStatusData = null;

    // --- DOM refs ---
    const statusBar = document.getElementById('status-bar');
    const sessionsList = document.getElementById('sessions-list');
    const historyList = document.getElementById('history-list');
    const historySearch = document.getElementById('history-search');
    const historyDays = document.getElementById('history-days');
    const usageContent = document.getElementById('usage-content');
    const detailOverlay = document.getElementById('detail-overlay');
    const detailTitle = document.getElementById('detail-title');
    const detailClose = document.getElementById('detail-close');
    const detailMetrics = document.getElementById('detail-metrics');
    const detailTimeline = document.getElementById('detail-timeline');
    const connStatus = document.getElementById('connection-status');
    const claudeStatusEl = document.getElementById('claude-status');

    // --- Tab navigation ---
    document.querySelectorAll('.tab').forEach(tab => {
        tab.addEventListener('click', e => {
            e.preventDefault();
            switchView(tab.dataset.tab);
        });
    });

    function switchView(view) {
        currentView = view;
        document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === view));
        document.querySelectorAll('.view').forEach(v => v.classList.toggle('active', v.id === view + '-view'));
        statusBar.style.display = view === 'live' ? '' : 'none';
        if (view === 'history') loadHistory();
        if (view === 'usage') loadUsage();
        window.location.hash = view;
    }

    // Init from hash
    const initHash = window.location.hash.replace('#', '');
    if (['history', 'usage'].includes(initHash)) switchView(initHash);

    // --- Claude service status ---
    let claudeStatusInterval = null;
    let claudeStatusFetchedAt = 0;

    async function loadClaudeStatus() {
        try {
            const resp = await fetch('/api/claude-status');
            claudeStatusData = await resp.json();
            claudeStatusFetchedAt = Date.now();
        } catch (err) {
            claudeStatusData = { available: false, error: 'fetch failed' };
        }
        renderClaudeStatus();
    }

    function startClaudeStatusPolling() {
        stopClaudeStatusPolling();
        claudeStatusInterval = setInterval(loadClaudeStatus, 60000);
    }

    function stopClaudeStatusPolling() {
        if (claudeStatusInterval) { clearInterval(claudeStatusInterval); claudeStatusInterval = null; }
    }

    document.addEventListener('visibilitychange', () => {
        if (document.hidden) {
            stopClaudeStatusPolling();
        } else {
            if (Date.now() - claudeStatusFetchedAt > 60000) loadClaudeStatus();
            startClaudeStatusPolling();
        }
    });

    function renderClaudeStatus() {
        if (!claudeStatusEl) return;
        const s = claudeStatusData;
        if (!s) {
            claudeStatusEl.innerHTML = '';
            return;
        }

        let dotCls = 'claude-status-dot';
        let text = '';

        if (s.available) {
            switch (s.indicator) {
                case 'minor':
                    dotCls += ' warning';
                    text = s.description || 'Degraded Performance';
                    break;
                case 'major':
                case 'critical':
                    dotCls += ' outage';
                    text = s.description || 'Service Disruption';
                    break;
                default:
                    dotCls += ' operational';
                    text = s.description || 'All Systems Operational';
                    break;
            }
        } else {
            dotCls += ' unavailable';
            text = 'Status unavailable';
        }

        claudeStatusEl.innerHTML = `<a href="https://status.claude.com/" target="_blank" rel="noopener" class="claude-status-link"><span class="${dotCls}"></span>${esc(text)}</a>`;
    }

    loadClaudeStatus();
    startClaudeStatusPolling();

    // --- SSE ---
    function connectSSE() {
        if (sseSource) sseSource.close();
        sseSource = new EventSource('/api/events');

        sseSource.addEventListener('sessions', e => {
            try {
                currentSessions = JSON.parse(e.data);
                if (currentView === 'live') renderSessions();
            } catch (err) { /* ignore parse errors */ }
        });

        sseSource.addEventListener('heartbeat', () => {});

        sseSource.addEventListener('open', () => {
            connStatus.className = 'connected';
            connStatus.title = 'SSE connected';
            if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
        });

        sseSource.addEventListener('error', () => {
            connStatus.className = 'disconnected';
            connStatus.title = 'SSE disconnected - reconnecting...';
            sseSource.close();
            sseSource = null;
            reconnectTimer = setTimeout(connectSSE, 3000);
        });
    }

    connectSSE();

    // --- Render live sessions ---
    function renderSessions() {
        if (!currentSessions || currentSessions.length === 0) {
            sessionsList.innerHTML = '<div class="empty-state">No active sessions found</div>';
            statusBar.innerHTML = '';
            return;
        }

        // Status summary
        const counts = {};
        currentSessions.forEach(s => {
            const label = s.status === 'Inactive' ? 'Stopped' : s.status;
            counts[label] = (counts[label] || 0) + 1;
        });
        statusBar.innerHTML = Object.entries(counts).map(([status, count]) => {
            const cls = statusClass(status);
            return `<span class="status-badge"><span class="status-dot ${cls}"></span>${count} ${status}</span>`;
        }).join('');

        sessionsList.innerHTML = currentSessions.map(s => {
            const isInactive = s.status === 'Inactive';
            const cls = statusClass(s.status);
            const symbol = statusSymbol(s.status);
            const age = s.status === 'Working' ? 'Now' : formatAge(s.last_activity);
            const pct = s.context_percent || 0;
            const ctxCls = pct > 90 ? 'high' : pct > 75 ? 'medium' : 'low';
            const cardCls = isInactive ? 'session-card stopped' : 'session-card';
            const stoppedBadge = isInactive ? `<span class="stopped-badge">Stopped</span>` : '';

            return `<div class="${cardCls}" data-logfile="${esc(s.log_file || '')}" data-project="${esc(s.project)}">
                <div class="session-top">
                    <span class="session-status ${cls}" title="${esc(s.status)}">${symbol}</span>
                    <span class="session-project">${esc(s.project)}</span>
                    ${stoppedBadge}
                    ${s.git_branch ? `<span class="session-branch">${esc(s.git_branch)}</span>` : ''}
                    ${s.session_title ? `<span class="session-title">${esc(s.session_title)}</span>` : ''}
                    ${s.origin && s.origin.category ? `<span class="session-origin origin-${esc(s.origin.category)}" title="${esc(s.origin.app || '')}">${esc(s.origin.display || s.origin.app || '')}</span>` : ''}
                    <span class="session-context">
                        <span class="context-bar"><span class="context-fill ${ctxCls}" style="width:${Math.min(pct, 100)}%"></span></span>
                        <span>${pct > 0 ? Math.round(pct) + '%' : '-'}</span>
                    </span>
                    <span class="session-activity">${age}</span>
                    <a class="session-history-link" title="View project history">&#x29D6;</a>
                </div>
                ${s.last_message ? `<div class="session-bottom">${esc(s.last_message)}</div>` : ''}
            </div>`;
        }).join('');

        // Attach click handlers
        sessionsList.querySelectorAll('.session-card').forEach(card => {
            card.addEventListener('click', (e) => {
                // If the history link was clicked, navigate to history instead
                if (e.target.classList.contains('session-history-link')) {
                    e.preventDefault();
                    const project = card.dataset.project;
                    showProjectHistory(project);
                    return;
                }
                const logFile = card.dataset.logfile;
                const project = card.querySelector('.session-project').textContent;
                if (logFile) openDetail(logFile, project);
            });
        });
    }

    // Navigate to history tab filtered by project
    function showProjectHistory(project) {
        historySearch.value = project;
        switchView('history');
    }

    // --- History ---
    async function loadHistory() {
        const days = historyDays.value;
        try {
            const resp = await fetch(`/api/history?days=${days}`);
            historyData = (await resp.json()) || [];
            renderHistory();
        } catch (err) {
            historyList.innerHTML = `<div class="empty-state">Failed to load history</div>`;
        }
    }

    function renderHistory() {
        const query = (historySearch.value || '').toLowerCase();
        const filtered = historyData.filter(s =>
            !query || s.project.toLowerCase().includes(query) ||
            (s.git_branch && s.git_branch.toLowerCase().includes(query)) ||
            (s.first_prompt && s.first_prompt.toLowerCase().includes(query))
        );

        if (filtered.length === 0) {
            historyList.innerHTML = '<div class="empty-state">No sessions found</div>';
            return;
        }

        // Group by project, then by date within each project
        const projectGroups = {};
        filtered.forEach(s => {
            const proj = s.project || 'Unknown';
            if (!projectGroups[proj]) projectGroups[proj] = [];
            projectGroups[proj].push(s);
        });

        // Sort projects by most recent session first
        const sortedProjects = Object.entries(projectGroups).sort((a, b) => {
            const aTime = a[1][0] ? new Date(a[1][0].start_time) : 0;
            const bTime = b[1][0] ? new Date(b[1][0].start_time) : 0;
            return bTime - aTime;
        });

        let html = '';
        sortedProjects.forEach(([project, sessions]) => {
            const isCollapsed = query ? '' : ' collapsed';
            html += `<div class="project-group${isCollapsed}">`;
            const lastStarted = sessions[0] && sessions[0].start_time ? formatAge(sessions[0].start_time) : '';
            html += `<div class="project-group-header">
                <span class="project-group-toggle">&#x25B6;</span>
                <span class="project-group-name">${esc(project)}</span>
                <span class="project-group-count">${sessions.length} session${sessions.length !== 1 ? 's' : ''}</span>
                <span class="project-group-age">${lastStarted || ''}</span>
            </div>`;
            html += `<div class="project-group-body">`;
            html += `<div class="history-row history-header">
                <div class="history-row-main">
                    <span class="history-branch">Branch</span>
                    <span class="history-date">Date</span>
                    <span class="history-messages">Prompts</span>
                    <span class="history-duration">Duration</span>
                </div>
            </div>`;
            sessions.forEach(s => {
                const dur = formatDuration(s.duration);
                const date = s.start_time ? dateGroup(s.start_time) + ' ' + new Date(s.start_time).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '-';
                const promptLine = s.first_prompt ? `<div class="history-prompt">${esc(s.first_prompt)}</div>` : '';
                html += `<div class="history-row" data-logfile="${esc(s.log_file || '')}">
                    <div class="history-row-main">
                        <span class="history-branch">${s.git_branch ? esc(s.git_branch) : '-'}</span>
                        <span class="history-date">${date}</span>
                        <span class="history-messages">${s.message_count || 0}</span>
                        <span class="history-duration">${dur}</span>
                    </div>
                    ${promptLine}
                </div>`;
            });
            html += `</div></div>`;
        });

        historyList.innerHTML = html;

        // Attach collapse/expand handlers
        historyList.querySelectorAll('.project-group-header').forEach(header => {
            header.addEventListener('click', () => {
                header.parentElement.classList.toggle('collapsed');
            });
        });

        historyList.querySelectorAll('.history-row:not(.history-header)').forEach(row => {
            row.addEventListener('click', () => {
                const logFile = row.dataset.logfile;
                const project = row.closest('.project-group').querySelector('.project-group-name').textContent;
                if (logFile) openDetail(logFile, project);
            });
        });
    }

    historySearch.addEventListener('input', renderHistory);
    historyDays.addEventListener('change', loadHistory);

    // --- Usage view ---
    let usageLoading = false;
    let usageLastUpdated = null;

    async function loadUsage() {
        if (usageLoading) return;
        usageLoading = true;
        try {
            const resp = await fetch('/api/usage');
            usageData = await resp.json();
            usageLastUpdated = new Date();
            renderUsageView(usageData);
        } catch (err) {
            usageContent.innerHTML = '<div class="empty-state">Failed to load usage data</div>';
        } finally {
            usageLoading = false;
        }
    }

    function renderUsageView(data) {
        if (!data) {
            usageContent.innerHTML = '<div class="empty-state">No usage data available</div>';
            return;
        }

        const apiQuota = data.api_quota;
        const local = data.local;
        let html = '';

        // Refresh header
        html += '<div class="usage-header">';
        if (usageLastUpdated) {
            html += '<span class="usage-last-updated">Updated ' + formatAge(usageLastUpdated.toISOString()) + '</span>';
        }
        html += '<button class="usage-refresh-btn" id="usage-refresh-btn">\u21BB Refresh</button>';
        html += '</div>';

        // API Quota section
        html += '<div class="usage-section">';
        html += '<h2 class="usage-section-title">API Quota</h2>';

        if (apiQuota && apiQuota.available) {
            html += '<div class="usage-bars">';
            if (apiQuota.five_hour) {
                html += renderUsageBar('5-hour', apiQuota.five_hour);
            }
            if (apiQuota.seven_day) {
                html += renderUsageBar('7-day', apiQuota.seven_day);
            }
            if (apiQuota.seven_day_sonnet) {
                html += renderUsageBar('Sonnet', apiQuota.seven_day_sonnet);
            }
            if (apiQuota.seven_day_opus) {
                html += renderUsageBar('Opus', apiQuota.seven_day_opus);
            }
            html += '</div>';
            if (apiQuota.extra_usage && apiQuota.extra_usage.is_enabled) {
                html += '<div class="usage-note">Extra usage: enabled</div>';
            }
        } else {
            const errMsg = apiQuota && apiQuota.error ? apiQuota.error : 'OAuth token not found';
            html += `<div class="usage-unavailable">Not available (${esc(errMsg)})</div>`;
        }
        html += '</div>';

        // Local usage section
        html += '<div class="usage-section">';
        html += '<h2 class="usage-section-title">Local Usage (5h window)</h2>';

        if (local && local.total_tokens > 0) {
            html += '<div class="usage-summary">';
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Total</div><div class="usage-summary-value">${fmtNum(local.total_tokens)}</div></div>`;
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Input</div><div class="usage-summary-value blue">${fmtNum(local.input_tokens)}</div></div>`;
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Output</div><div class="usage-summary-value green">${fmtNum(local.output_tokens)}</div></div>`;
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Cache</div><div class="usage-summary-value yellow">${fmtNum(local.cache_tokens)}</div></div>`;
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Sessions</div><div class="usage-summary-value">${local.sessions ? local.sessions.length : 0}</div></div>`;
            html += '</div>';

            if (local.sessions && local.sessions.length > 0) {
                html += '<div class="usage-table">';
                html += '<div class="usage-table-header">';
                html += '<span class="usage-col-project">Project</span>';
                html += '<span class="usage-col-tokens">Input</span>';
                html += '<span class="usage-col-tokens">Output</span>';
                html += '<span class="usage-col-tokens">Cache</span>';
                html += '<span class="usage-col-tokens">Total</span>';
                html += '</div>';
                local.sessions.forEach(s => {
                    html += '<div class="usage-table-row">';
                    html += `<span class="usage-col-project">${esc(s.project)}</span>`;
                    html += `<span class="usage-col-tokens">${fmtNum(s.input_tokens)}</span>`;
                    html += `<span class="usage-col-tokens">${fmtNum(s.output_tokens)}</span>`;
                    html += `<span class="usage-col-tokens">${fmtNum(s.cache_tokens)}</span>`;
                    html += `<span class="usage-col-tokens">${fmtNum(s.total_tokens)}</span>`;
                    html += '</div>';
                });
                html += '</div>';
            }
        } else {
            html += '<div class="usage-unavailable">No token usage in the past 5 hours.</div>';
        }
        html += '</div>';

        usageContent.innerHTML = html;

        const refreshBtn = document.getElementById('usage-refresh-btn');
        if (refreshBtn) refreshBtn.addEventListener('click', loadUsage);
    }

    function renderUsageBar(label, bucket) {
        const pct = Math.min(bucket.utilization || 0, 100);
        const cls = pct >= 90 ? 'high' : pct >= 75 ? 'medium' : 'low';
        let resetHtml = '';
        if (bucket.resets_at) {
            const remaining = new Date(bucket.resets_at) - Date.now();
            if (remaining > 0) {
                resetHtml = `<span class="usage-bar-reset">resets in ${formatDurationHuman(remaining * 1e6)}</span>`;
            }
        }
        return `<div class="usage-bar-row">
            <span class="usage-bar-label">${esc(label)}</span>
            <span class="usage-bar"><span class="usage-bar-fill ${cls}" style="width:${pct}%"></span></span>
            <span class="usage-bar-pct">${Math.round(pct)}%</span>
            ${resetHtml}
        </div>`;
    }

    // --- Detail panel ---
    let timelineOffset = 0;
    let timelineTotal = 0;
    let timelineEntries = [];
    let currentLogFile = '';
    let timelineFilter = 'all'; // all, assistant, user

    function openDetail(logFile, project) {
        currentLogFile = logFile;
        timelineOffset = 0;
        timelineEntries = [];
        timelineFilter = 'all';
        detailTitle.textContent = project;
        detailOverlay.classList.remove('hidden');

        // Reset to metrics tab
        document.querySelectorAll('.detail-tab').forEach(t => t.classList.toggle('active', t.dataset.detail === 'metrics'));
        detailMetrics.classList.add('active');
        detailTimeline.classList.remove('active');

        loadMetrics(logFile);
        loadTimeline(logFile, true);
    }

    detailClose.addEventListener('click', () => detailOverlay.classList.add('hidden'));
    detailOverlay.addEventListener('click', e => {
        if (e.target === detailOverlay) detailOverlay.classList.add('hidden');
    });
    document.addEventListener('keydown', e => {
        if (e.key === 'Escape') detailOverlay.classList.add('hidden');
    });

    document.querySelectorAll('.detail-tab').forEach(tab => {
        tab.addEventListener('click', () => {
            document.querySelectorAll('.detail-tab').forEach(t => t.classList.toggle('active', t === tab));
            detailMetrics.classList.toggle('active', tab.dataset.detail === 'metrics');
            detailTimeline.classList.toggle('active', tab.dataset.detail === 'timeline');
        });
    });

    async function loadMetrics(logFile) {
        detailMetrics.innerHTML = '<div class="loading">Loading metrics...</div>';
        try {
            const resp = await fetch(`/api/sessions/metrics?file=${encodeURIComponent(logFile)}`);
            if (!resp.ok) throw new Error(await resp.text());
            const m = await resp.json();
            renderMetrics(m);
        } catch (err) {
            detailMetrics.innerHTML = `<div class="empty-state">Failed to load metrics</div>`;
        }
    }

    function renderMetrics(m) {
        const duration = m.last_timestamp && m.first_timestamp
            ? formatDuration((new Date(m.last_timestamp) - new Date(m.first_timestamp)) * 1000000)
            : '-';
        const totalTokens = m.total_input_tokens + m.total_output_tokens + m.total_cache_creation_tokens + m.total_cache_read_tokens;
        const maxToken = Math.max(m.total_input_tokens, m.total_output_tokens, m.total_cache_creation_tokens, m.total_cache_read_tokens, 1);

        let html = `<div class="metrics-grid">
            <div class="metric-card"><div class="metric-label">Turns</div><div class="metric-value blue">${m.turn_count}</div></div>
            <div class="metric-card"><div class="metric-label">User Prompts</div><div class="metric-value green">${m.user_prompt_count}</div></div>
            <div class="metric-card"><div class="metric-label">Tool Results</div><div class="metric-value">${m.tool_result_count}</div></div>
            <div class="metric-card"><div class="metric-label">Assistant Messages</div><div class="metric-value purple">${m.assistant_message_count}</div></div>
            <div class="metric-card"><div class="metric-label">Duration</div><div class="metric-value">${duration}</div></div>
            <div class="metric-card"><div class="metric-label">Total Tokens</div><div class="metric-value yellow">${fmtNum(totalTokens)}</div></div>
            <div class="metric-card"><div class="metric-label">Context Usage</div><div class="metric-value ${m.context_percent > 90 ? 'yellow' : 'green'}">${Math.round(m.context_percent)}%</div></div>
            ${m.compact_count > 0 ? `<div class="metric-card"><div class="metric-label">Compactions</div><div class="metric-value">${m.compact_count}</div></div>` : ''}
        </div>`;

        html += `<div class="token-breakdown"><h3>Token Breakdown</h3>`;
        const bars = [
            { label: 'Input', value: m.total_input_tokens, color: 'var(--blue)' },
            { label: 'Output', value: m.total_output_tokens, color: 'var(--green)' },
            { label: 'Cache Create', value: m.total_cache_creation_tokens, color: 'var(--yellow)' },
            { label: 'Cache Read', value: m.total_cache_read_tokens, color: 'var(--purple)' },
        ];
        const logMax = Math.log(maxToken + 1);
        bars.forEach(b => {
            const pct = b.value > 0 ? (Math.log(b.value + 1) / logMax) * 100 : 0;
            html += `<div class="token-bar-row">
                <span class="token-bar-label">${b.label}</span>
                <div class="token-bar-track"><div class="token-bar-fill" style="width:${pct}%;background:${b.color}"></div></div>
                <span class="token-bar-value">${fmtNum(b.value)}</span>
            </div>`;
        });
        html += '</div>';

        // Tool usage
        const tools = Object.entries(m.tool_usage_counts || {}).sort((a, b) => b[1] - a[1]);
        if (tools.length > 0) {
            html += `<div class="tool-usage"><h3>Tool Usage</h3><div class="tool-list">`;
            tools.forEach(([name, count]) => {
                html += `<span class="tool-chip"><span class="tool-name">${esc(name)}</span><span class="tool-count">${count}</span></span>`;
            });
            html += '</div></div>';
        }

        detailMetrics.innerHTML = html;
    }

    async function loadTimeline(logFile, reset) {
        if (reset) {
            timelineOffset = 0;
            timelineEntries = [];
            detailTimeline.innerHTML = '<div class="loading">Loading timeline...</div>';
        }

        try {
            const resp = await fetch(`/api/sessions/timeline?file=${encodeURIComponent(logFile)}&offset=${timelineOffset}&limit=50`);
            if (!resp.ok) throw new Error(await resp.text());
            const data = await resp.json();
            timelineTotal = data.total;
            timelineEntries = timelineEntries.concat(data.entries || []);
            timelineOffset += (data.entries || []).length;
            renderTimeline();
        } catch (err) {
            detailTimeline.innerHTML = `<div class="empty-state">Failed to load timeline</div>`;
        }
    }

    function renderTimeline() {
        if (timelineEntries.length === 0) {
            detailTimeline.innerHTML = '<div class="empty-state">No entries</div>';
            return;
        }

        const filters = ['all', 'assistant', 'user'];
        let html = '<div class="timeline-filters">';
        filters.forEach(f => {
            const active = f === timelineFilter ? ' active' : '';
            html += `<button class="filter-btn${active}" data-filter="${f}">${f.charAt(0).toUpperCase() + f.slice(1)}</button>`;
        });
        html += '</div>';

        const filtered = timelineFilter === 'all'
            ? timelineEntries
            : timelineEntries.filter(e => e.type === timelineFilter);

        if (filtered.length === 0) {
            html += '<div class="empty-state">No matching entries</div>';
        }

        html += '<div class="timeline">';
        filtered.forEach(e => {
            const cls = e.type;
            const time = e.timestamp ? new Date(e.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '';

            html += `<div class="timeline-entry ${esc(cls)}">`;
            html += `<div class="timeline-header">`;
            html += `<span class="timeline-role">${esc(e.type)}${e.subtype ? '/' + esc(e.subtype) : ''}</span>`;
            if (time) html += `<span class="timeline-time">${time}</span>`;
            if (e.model) html += `<span class="timeline-model">${esc(e.model)}</span>`;
            html += '</div>';

            if (e.summary) {
                html += `<div class="timeline-text">${esc(e.summary)}</div>`;
            }

            if (e.content) {
                e.content.forEach(c => {
                    if (c.type === 'text' && c.text) {
                        html += `<div class="timeline-text">${esc(c.text)}</div>`;
                    } else if (c.type === 'tool_use') {
                        html += `<details class="timeline-tool"><summary>${esc(c.tool || 'tool')}</summary>`;
                        if (c.input) {
                            let formatted = c.input;
                            try { formatted = JSON.stringify(JSON.parse(c.input), null, 2); } catch (e) { /* keep raw */ }
                            html += `<div class="timeline-tool-input">${esc(formatted)}</div>`;
                        }
                        html += '</details>';
                    } else if (c.type === 'tool_result' && c.text) {
                        html += `<details class="timeline-tool"><summary>tool result</summary>`;
                        html += `<div class="timeline-tool-input">${esc(c.text)}</div>`;
                        html += '</details>';
                    }
                });
            }

            if (e.usage) {
                const u = e.usage;
                const total = u.input_tokens + (u.cache_creation_input_tokens || 0) + (u.cache_read_input_tokens || 0);
                html += `<div class="timeline-usage">in: ${fmtNum(total)} | out: ${fmtNum(u.output_tokens)}</div>`;
            }

            html += '</div>';
        });
        html += '</div>';

        if (timelineOffset < timelineTotal) {
            html += `<button class="load-more" id="load-more-btn">Load more (${timelineOffset}/${timelineTotal})</button>`;
        }

        detailTimeline.innerHTML = html;

        detailTimeline.querySelectorAll('.filter-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                timelineFilter = btn.dataset.filter;
                renderTimeline();
            });
        });

        const loadMoreBtn = document.getElementById('load-more-btn');
        if (loadMoreBtn) {
            loadMoreBtn.addEventListener('click', () => loadTimeline(currentLogFile, false));
        }
    }

    // --- Helpers ---
    function statusClass(status) {
        switch (status) {
            case 'Working': return 'working';
            case 'Needs Input': return 'needs-input';
            case 'Waiting': return 'waiting';
            case 'Idle': return 'idle';
            case 'Inactive': return 'inactive';
            default: return 'inactive';
        }
    }

    function statusSymbol(status) {
        switch (status) {
            case 'Working': return '\u25CF';     // ●
            case 'Needs Input': return '\u25B2';  // ▲
            case 'Waiting': return '\u25C9';      // ◉
            case 'Idle': return '\u25CB';          // ○
            case 'Inactive': return '\u25CC';      // ◌
            default: return '\u25CC';
        }
    }

    function formatAge(ts) {
        if (!ts) return '-';
        const ms = Date.now() - new Date(ts).getTime();
        const sec = Math.floor(ms / 1000);
        if (sec < 60) return sec + 's ago';
        const min = Math.floor(sec / 60);
        if (min < 60) return min + 'min ago';
        const hr = Math.floor(min / 60);
        if (hr < 24) return hr + 'h ago';
        const days = Math.floor(hr / 24);
        if (days < 7) return days + 'd ago';
        if (days < 30) return Math.floor(days / 7) + 'w ago';
        if (days < 365) return Math.floor(days / 30) + 'mo ago';
        return Math.floor(days / 365) + 'y ago';
    }

    function formatDuration(nanos) {
        if (!nanos || nanos <= 0) return '-';
        const sec = Math.floor(nanos / 1e9);
        if (sec < 60) return sec + 's';
        const min = Math.floor(sec / 60);
        if (min < 60) return min + 'm';
        const hr = Math.floor(min / 60);
        const remMin = min % 60;
        return hr + 'h ' + remMin + 'm';
    }

    function formatDurationHuman(nanos) {
        if (!nanos || nanos <= 0) return 'now';
        const totalMin = Math.floor(nanos / 6e10);
        const h = Math.floor(totalMin / 60);
        const m = totalMin % 60;
        const d = Math.floor(h / 24);
        const remH = h % 24;
        if (d > 0) return d + 'd ' + remH + 'h';
        if (h > 0) return h + 'h ' + m + 'm';
        return m + 'm';
    }

    function dateGroup(ts) {
        const d = new Date(ts);
        const now = new Date();
        const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
        const sessionDate = new Date(d.getFullYear(), d.getMonth(), d.getDate());
        const diff = Math.floor((today - sessionDate) / 86400000);
        if (diff === 0) return 'Today';
        if (diff === 1) return 'Yesterday';
        return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
    }

    function fmtNum(n) {
        if (n == null) return '0';
        if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
        if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
        return String(n);
    }

    function esc(s) {
        if (!s) return '';
        const d = document.createElement('div');
        d.textContent = s;
        return d.innerHTML;
    }
})();

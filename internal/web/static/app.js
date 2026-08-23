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
    const headerQuotaEl = document.getElementById('header-quota');

    // --- Header API quota ---
    // /api/oauth/usage is an undocumented Anthropic endpoint reported to get
    // stuck returning 429 for the rest of the session once rate-limited (no
    // documented safe interval, no recovery). Poll conservatively -- well
    // above the ~30-60s interval reported to trigger it, and above the
    // server's own 60s cache TTL so most polls just hit that cache -- but
    // circuit-break the moment a fetch fails for any reason other than "no
    // OAuth token configured": stop polling and say so, rather than either
    // hammering an already-broken endpoint or silently going stale forever
    // (which a tab left open on a second monitor would otherwise do, since
    // visibilitychange never fires for a tab that's visible but unfocused).
    //
    // HEADER_QUOTA_POLL_MS is both the poll interval and the staleness
    // threshold every path that can refresh early (tab switch, tab regaining
    // visibility) checks against, so no sequence of user actions can drive
    // the widget faster than the interval.
    const HEADER_QUOTA_POLL_MS = 5 * 60 * 1000;
    let headerQuotaInterval = null;
    let headerQuotaFetchedAt = 0;
    let headerQuotaBroken = false;

    async function loadHeaderQuota() {
        if (headerQuotaBroken) return;
        // Stamp the attempt rather than the completion. The guards exist to
        // limit how often a request goes out, and a request in flight has
        // already gone out -- stamping on completion leaves a window where
        // init and a #history/#usage deep link both see a stale 0 and fire.
        headerQuotaFetchedAt = Date.now();
        try {
            const resp = await fetch('/api/quota');
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            renderHeaderQuota(await resp.json());
        } catch (err) {
            breakHeaderQuota(err.message || 'fetch failed');
        }
    }

    // breakHeaderQuota trips the circuit breaker: stop polling and replace the
    // widget with a visible "unavailable" marker until the page is reloaded.
    function breakHeaderQuota(reason) {
        headerQuotaBroken = true;
        stopHeaderQuotaPolling();
        if (!headerQuotaEl) return;
        headerQuotaEl.innerHTML = `<span class="header-quota-item header-quota-error" title="${esc(reason)}">quota unavailable</span>`;
    }

    function renderHeaderQuota(apiQuota) {
        if (!headerQuotaEl) return;
        if (!apiQuota || !apiQuota.available) {
            // No OAuth token is a normal local configuration state, not a
            // broken endpoint -- show nothing and keep polling, in case one
            // shows up later.
            if (apiQuota && apiQuota.error === 'OAuth token not found') {
                headerQuotaEl.innerHTML = '';
                return;
            }
            breakHeaderQuota((apiQuota && apiQuota.error) || 'unknown error');
            return;
        }
        let html = '';
        if (apiQuota.five_hour) html += renderHeaderQuotaBar('5h', '5-hour', apiQuota.five_hour);
        if (apiQuota.seven_day) html += renderHeaderQuotaBar('7d', '7-day', apiQuota.seven_day);
        headerQuotaEl.innerHTML = html;
    }

    function renderHeaderQuotaBar(shortLabel, fullLabel, bucket) {
        const pct = Math.min(bucket.utilization || 0, 100);
        const cls = pct >= 90 ? 'high' : pct >= 75 ? 'medium' : 'low';
        let title = `${fullLabel} quota: ${Math.round(pct)}%`;
        if (bucket.resets_at) {
            const remaining = new Date(bucket.resets_at) - Date.now();
            if (remaining > 0) title += `, resets in ${formatDurationHuman(remaining * 1e6)}`;
        }
        return `<span class="header-quota-item" title="${esc(title)}">
            <span class="header-quota-label">${esc(shortLabel)}</span>
            <span class="header-quota-bar"><span class="header-quota-fill ${cls}" style="width:${pct}%"></span></span>
            <span class="header-quota-pct">${Math.round(pct)}%</span>
        </span>`;
    }

    function startHeaderQuotaPolling() {
        stopHeaderQuotaPolling();
        if (headerQuotaBroken) return;
        headerQuotaInterval = setInterval(loadHeaderQuota, HEADER_QUOTA_POLL_MS);
    }

    function stopHeaderQuotaPolling() {
        if (headerQuotaInterval) { clearInterval(headerQuotaInterval); headerQuotaInterval = null; }
    }

    loadHeaderQuota();
    startHeaderQuotaPolling();

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
        if (Date.now() - headerQuotaFetchedAt > HEADER_QUOTA_POLL_MS) loadHeaderQuota();
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
            stopHeaderQuotaPolling();
        } else {
            if (Date.now() - claudeStatusFetchedAt > 60000) loadClaudeStatus();
            startClaudeStatusPolling();
            if (Date.now() - headerQuotaFetchedAt > HEADER_QUOTA_POLL_MS) loadHeaderQuota();
            startHeaderQuotaPolling();
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
                connStatus.className = 'connected';
                connStatus.title = 'SSE connected';
            } catch (err) { /* ignore parse errors */ }
        });

        // The connection stays up and the heartbeat keeps arriving when a scan
        // fails, so without this the page would keep showing the last good
        // state under a "connected" indicator, with the ages frozen.
        sseSource.addEventListener('scan_error', () => {
            connStatus.className = 'stale';
            connStatus.title = 'Connected, but the last session scan failed - data may be out of date';
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
                    ${s.origin && s.origin.category ? `<span class="badge session-origin origin-${esc(s.origin.category)}" title="${esc(s.origin.app || '')}">${esc(s.origin.display || s.origin.app || '')}</span>` : ''}
                    ${(s.context_window || 0) > 200000 ? `<span class="badge session-model-badge" title="${esc(s.model)}">1M</span>` : ''}
                    <span class="session-context" title="${esc(s.model || '')}">
                        <span class="context-bar"><span class="context-fill ${ctxCls}" style="width:${Math.min(pct, 100)}%"></span></span>
                        <span>${pct > 0 ? Math.round(pct) + '%' : '-'}</span>
                    </span>
                    <span class="session-activity">${age}</span>
                    <a class="session-history-link" title="View project history">&#x29D6;</a>
                </div>
                ${s.last_message ? `<div class="session-bottom">${esc(s.last_message)}</div>` : ''}
                ${renderSubagents(s.subagents)}
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
                // A click on a nested subagent opens that agent's log, not the parent's
                const subagent = e.target.closest('.subagent');
                if (subagent && subagent.dataset.logfile) {
                    openDetail(subagent.dataset.logfile, subagent.dataset.label);
                    return;
                }
                const logFile = card.dataset.logfile;
                const project = card.querySelector('.session-project').textContent;
                if (logFile) openDetail(logFile, project);
            });
        });
    }

    // Render a session's live subagents as rows nested under its card.
    // Only running agents reach here — the backend drops finished ones.
    function renderSubagents(subagents) {
        if (!subagents || subagents.length === 0) return '';

        return `<div class="session-subagents">` + subagents.map(a => {
            const label = a.agent_type || (a.id || '').slice(0, 8);
            // a.description is the short label the agent was spawned with; a.task
            // is its latest freeform status message. The label is the more useful
            // title, with the live status (if any) elaborating on the second line.
            const title = a.description || a.task || '';
            const detail = a.description && a.task ? a.task : '';

            return `<div class="subagent" data-logfile="${esc(a.log_file || '')}" data-label="${esc(label)}">
                <div class="subagent-top">
                    <span class="session-status working">&#x25CF;</span>
                    <span class="subagent-label">${esc(label)}</span>
                    ${a.blocking ? `<span class="badge subagent-blocking" title="The parent turn cannot continue until this agent finishes">blocking</span>` : ''}
                    ${title ? `<span class="subagent-description">${esc(title)}</span>` : ''}
                    <span class="session-activity">${formatAge(a.last_activity)}</span>
                </div>
                ${detail ? `<div class="subagent-bottom">${esc(detail)}</div>` : ''}
            </div>`;
        }).join('') + `</div>`;
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
    // Changing the filter starts a new fetch, and 'all' mode fetches in a loop,
    // so a load can still be running when the next one starts. Only the newest
    // one is allowed to write to the timeline state.
    let timelineLoadToken = 0;
    let timelineError = '';
    // The same guard for the metrics panel, which openDetail loads alongside
    // the timeline: opening session A and switching to B before A's request
    // lands would otherwise render A's metrics under B's title.
    let metricsLoadToken = 0;
    let timelineLoadMoreClicks = 0;

    function openDetail(logFile, project) {
        currentLogFile = logFile;
        timelineOffset = 0;
        timelineEntries = [];
        timelineFilter = 'all';
        timelineLoadMoreClicks = 0;
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
        const token = ++metricsLoadToken;
        detailMetrics.innerHTML = '<div class="loading">Loading metrics...</div>';
        try {
            const resp = await fetch(`/api/sessions/metrics?file=${encodeURIComponent(logFile)}`);
            if (!resp.ok) throw new Error(await resp.text());
            const m = await resp.json();
            if (token !== metricsLoadToken) return;
            renderMetrics(m);
        } catch (err) {
            if (token !== metricsLoadToken) return;
            detailMetrics.innerHTML = `<div class="empty-state">Failed to load metrics</div>`;
        }
    }

    // The four kinds of token a session spends. Fixed order, fixed colour --
    // a slot never changes hue because a value is zero or the list is filtered.
    // Colours are the Tokyo Night hues stepped down into the band that reads on
    // the panel surface; yellow is deliberately absent, because yellow against
    // green is the pair a deuteranopic reader cannot separate.
    const TOKEN_KINDS = [
        { key: 'total_cache_read_tokens', label: 'Cache read', color: '#9f7ed8' },
        { key: 'total_cache_creation_tokens', label: 'Cache create', color: '#d3763b' },
        { key: 'total_output_tokens', label: 'Output', color: '#698fe3' },
        { key: 'total_input_tokens', label: 'Input', color: '#75a33e' },
    ];

    // splitToolName separates an MCP tool id into the tool and the server that
    // provides it: mcp__claude-in-chrome__navigate -> navigate, claude-in-chrome.
    // A built-in tool (Bash, Write) has no server part.
    //
    // The id is mcp__<server>__<tool>, and neither half is forbidden from
    // containing __ itself, so with more than two separators the split is a
    // guess. It splits on the last one, which keeps a server id containing __
    // whole: server ids are user-chosen configuration keys, while tool names
    // come from the server's own API and have not been seen to contain __.
    function splitToolName(name) {
        const m = /^mcp__(.+)__(.+)$/.exec(name);
        return m ? { tool: m[2], server: m[1] } : { tool: name, server: '' };
    }

    // formatShare renders a part of a whole as a percentage that never overstates
    // it. Shares are floored rather than rounded, so a 99.96% cache read does not
    // print as 100.0% beside three siblings that are visibly non-zero. Every row
    // is then a lower bound -- the claim "<0.1" already makes -- and the column
    // cannot sum past the whole.
    function formatShare(value, total) {
        const share = (value / total) * 100;
        if (value > 0 && share < 0.1) return '<0.1%';
        return (Math.floor(share * 10) / 10).toFixed(1) + '%';
    }

    function renderMetrics(m) {
        const duration = m.last_timestamp && m.first_timestamp
            ? formatDuration((new Date(m.last_timestamp) - new Date(m.first_timestamp)) * 1000000)
            : '-';
        const totalTokens = TOKEN_KINDS.reduce((sum, k) => sum + (m[k.key] || 0), 0);

        // Context usage is the one metric here with a consequence -- the session
        // compacts as it approaches the limit -- so it leads, as a figure with a
        // meter. Everything else is volume, and reads as supporting detail.
        const pct = Math.min(Math.max(m.context_percent || 0, 0), 100);
        const severity = pct > 90 ? 'danger' : pct > 75 ? 'warning' : 'ok';

        // Colour is the only thing separating ok from warning from danger, so the
        // state has to be said in words somewhere too. The meter says it: it is
        // the one element reporting both the value and what the value means. The
        // figure beside it is that same number again, so it stays out of the
        // accessibility tree rather than being announced twice.
        const severityText = { ok: 'normal', warning: 'high', danger: 'critical' }[severity];
        const shownPct = Math.round(pct);
        let html = `<section class="metrics-lead">
            <div class="lead-figure" aria-hidden="true">
                <div class="lead-label">Context used</div>
                <div class="lead-value ${severity}">${shownPct}<span class="lead-unit">%</span></div>
            </div>
            <div class="lead-meter">
                <div class="meter-track" role="progressbar" aria-label="Context used"
                     aria-valuemin="0" aria-valuemax="100" aria-valuenow="${shownPct}"
                     aria-valuetext="${shownPct}% of context used, ${severityText}">
                    <div class="meter-fill ${severity}" style="width:${pct}%"></div>
                </div>
                <div class="lead-support">
                    <span><b>${esc(duration)}</b> elapsed</span>
                    <span><b>${fmtNum(totalTokens)}</b> tokens</span>
                    ${m.compact_count > 0 ? `<span><b>${m.compact_count}</b> compaction${m.compact_count === 1 ? '' : 's'}</span>` : ''}
                </div>
            </div>
        </section>`;

        // Counts describe volume, not state, so they carry no colour and no card
        // of their own -- one line, in the order a turn actually happens.
        //
        // A count is a button when the timeline can filter to what it counts,
        // and plain text otherwise. Turns and tool results have no matching
        // filter, so nothing about them is clickable.
        html += `<section class="metrics-counts">
            <span class="count"><b>${m.turn_count}</b> turns</span>
            ${countButton(m.user_prompt_count, 'prompts', 'user')}
            ${countButton(m.assistant_message_count, 'replies', 'assistant')}
            <span class="count"><b>${m.tool_result_count}</b> tool results</span>
        </section>`;

        // Part-to-whole, so one stacked bar rather than four independent bars.
        // A session is routinely 99% cache reads, so the small kinds render as
        // slivers or as nothing at all. That is the true shape, and padding them
        // to a minimum width would misstate the total -- the legend below carries
        // the exact value and share for every kind, including the invisible ones.
        if (totalTokens > 0) {
            html += `<section class="token-composition">
                <h3>Token composition</h3>
                <div class="token-stack">`;
            TOKEN_KINDS.forEach(k => {
                const v = m[k.key] || 0;
                if (v <= 0) return;
                html += `<span class="token-seg" style="flex-basis:${(v / totalTokens) * 100}%;background:${k.color}"></span>`;
            });
            html += `</div><ul class="token-legend">`;
            TOKEN_KINDS.forEach(k => {
                const v = m[k.key] || 0;
                html += `<li>
                    <span class="token-key" style="background:${k.color}"></span>
                    <span class="token-name">${esc(k.label)}</span>
                    <span class="token-value">${fmtNum(v)}</span>
                    <span class="token-share">${formatShare(v, totalTokens)}</span>
                </li>`;
            });
            html += `</ul></section>`;
        }

        // Tools are listed by the name a person recognises. The MCP server that
        // provides the tool is a separate, quieter label -- it groups the rows
        // without taking the width the tool name needs.
        const tools = Object.entries(m.tool_usage_counts || {}).sort((a, b) => b[1] - a[1]);
        if (tools.length > 0) {
            const max = tools[0][1];
            html += `<section class="tool-usage"><h3>Tools</h3><ul class="tool-list">`;
            tools.forEach(([name, count]) => {
                const { tool, server } = splitToolName(name);
                // Every row emits the server cell, empty or not. The list is one
                // grid, so a row that skipped a cell would slide its bar out of
                // the shared column.
                html += `<li class="tool-row">
                    <span class="tool-name">${esc(tool)}</span>
                    <span class="tool-count">${count}</span>
                    <span class="tool-server">${server ? esc(server) : ''}</span>
                    <span class="tool-bar"><span class="tool-bar-fill" style="width:${(count / max) * 100}%"></span></span>
                </li>`;
            });
            html += '</ul></section>';
        }

        detailMetrics.innerHTML = html;

        detailMetrics.querySelectorAll('[data-timeline-filter]').forEach(btn => {
            btn.addEventListener('click', () => showFilteredTimeline(btn.dataset.timelineFilter));
        });
    }

    // countButton renders a count that opens the timeline filtered to the
    // entries it counts. label is what the count is called; filter is the
    // timeline type that holds those entries.
    function countButton(value, label, filter) {
        return `<button type="button" class="count count-action" data-timeline-filter="${filter}" title="Show ${label} in timeline">
                <b>${value}</b> ${label}
            </button>`;
    }

    function showFilteredTimeline(filter) {
        document.querySelectorAll('.detail-tab').forEach(t => {
            t.classList.toggle('active', t.dataset.detail === 'timeline');
        });
        detailMetrics.classList.remove('active');
        detailTimeline.classList.add('active');

        timelineFilter = filter;
        loadTimeline(currentLogFile, true, 'page').then(() => {
            detailTimeline.scrollTop = 0;
        });
    }

    async function loadTimeline(logFile, reset, mode = 'page') {
        const token = ++timelineLoadToken;
        if (reset) {
            timelineOffset = 0;
            timelineTotal = 0;
            timelineEntries = [];
            timelineLoadMoreClicks = 0;
            detailTimeline.innerHTML = '<div class="loading">Loading timeline...</div>';
        }

        // The server pages in filtered space, so the filter travels with every
        // request. Asking it for one type and paging over all of them is what
        // made "Load more" land on stretches with nothing to show.
        const typeParam = timelineFilter === 'all' ? '' : `&type=${encodeURIComponent(timelineFilter)}`;

        const SERVER_MAX = 500;
        timelineError = '';
        try {
            do {
                // Before the first response the total is unknown, so ask for a
                // full page rather than deriving a size from a total of zero.
                const remaining = timelineTotal > 0 ? Math.max(1, timelineTotal - timelineOffset) : SERVER_MAX;
                const limit = mode === 'all' ? Math.min(SERVER_MAX, remaining) : 50;
                const resp = await fetch(`/api/sessions/timeline?file=${encodeURIComponent(logFile)}&offset=${timelineOffset}&limit=${limit}${typeParam}`);
                if (!resp.ok) throw new Error(await resp.text());
                const data = await resp.json();
                if (token !== timelineLoadToken) return;
                timelineTotal = data.total;
                const batch = data.entries || [];
                timelineEntries = timelineEntries.concat(batch);
                timelineOffset += batch.length;
                if (batch.length === 0) break;
            } while (mode === 'all' && timelineOffset < timelineTotal);
            renderTimeline();
        } catch (err) {
            if (token !== timelineLoadToken) return;
            timelineError = 'Failed to load timeline';
            renderTimeline();
        }
    }

    function renderTimeline() {
        const filters = ['all', 'assistant', 'user'];
        let html = '<div class="timeline-filters">';
        filters.forEach(f => {
            const active = f === timelineFilter ? ' active' : '';
            html += `<button class="filter-btn${active}" data-filter="${f}">${f.charAt(0).toUpperCase() + f.slice(1)}</button>`;
        });
        html += '</div>';

        // The filter bar renders even when there is nothing to list under it.
        // timelineEntries now holds only what the active filter matched, so an
        // empty set usually means "none of this type" rather than "empty
        // session" -- and taking the panel away would remove the only control
        // that can pick a different type. Same for a failed load: a filter click
        // sends a request now, so it can fail, and wiping the bar would leave
        // nothing to retry with.
        if (timelineError) {
            html += `<div class="empty-state">${esc(timelineError)}</div>`;
        } else if (timelineEntries.length === 0) {
            html += timelineFilter === 'all'
                ? '<div class="empty-state">No entries</div>'
                : '<div class="empty-state">No matching entries</div>';
        }

        html += '<div class="timeline">';
        timelineEntries.forEach(e => {
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
            const loadAll = timelineLoadMoreClicks >= 2;
            const label = loadAll
                ? `Load all remaining (${timelineTotal - timelineOffset})`
                : `Load more (${timelineOffset}/${timelineTotal})`;
            html += `<button class="load-more" id="load-more-btn" data-mode="${loadAll ? 'all' : 'page'}">${label}</button>`;
        }

        detailTimeline.innerHTML = html;

        detailTimeline.querySelectorAll('.filter-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                if (btn.dataset.filter === timelineFilter) return;
                timelineFilter = btn.dataset.filter;
                // offset and total are counts of matching entries, so they mean
                // something different under a different filter. Page from the
                // start rather than carrying a position across.
                loadTimeline(currentLogFile, true);
            });
        });

        const loadMoreBtn = document.getElementById('load-more-btn');
        if (loadMoreBtn) {
            loadMoreBtn.addEventListener('click', () => {
                const mode = loadMoreBtn.dataset.mode === 'all' ? 'all' : 'page';
                timelineLoadMoreClicks += 1;
                loadTimeline(currentLogFile, false, mode);
            });
        }
    }

    // --- Helpers ---
    function statusClass(status) {
        switch (status) {
            case 'Working': return 'working';
            case 'Needs Input': return 'needs-input';
            case 'Waiting': return 'waiting';
            case 'Inactive': return 'inactive';
            default: return 'inactive';
        }
    }

    function statusSymbol(status) {
        switch (status) {
            case 'Working': return '\u25CF';     // ●
            case 'Needs Input': return '\u25B2';  // ▲
            case 'Waiting': return '\u25C9';      // ◉
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
        // textContent -> innerHTML escapes &, <, > but not quotes; callers also use
        // this inside double-quoted HTML attributes, so escape those too.
        return d.innerHTML.replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }

    // Mirrors session.contextWindowForModel in Go: opus/sonnet from generation 4.6
    // onward use the 1M extended context window.
})();

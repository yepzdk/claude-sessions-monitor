(function () {
    'use strict';

    // --- State ---
    let currentSessions = [];
    // Whether rows name their agent. The server decides it from every session
    // on the machine and sends it on the `harnesses` event, which precedes the
    // `sessions` event that renders them.
    let mixedHarnesses = false;
    let currentView = 'live';
    let historyData = [];
    let usageData = null;
    let sseSource = null;
    let reconnectTimer = null;
    let claudeStatusData = null;

    // --- DOM refs ---
    const statusBar = document.getElementById('status-bar');
    const sessionsList = document.getElementById('sessions-list');
    const liveSummary = document.getElementById('live-summary');
    const historySummary = document.getElementById('history-summary');
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
            renderHeaderQuota(await fetchJSON('/api/quota'));
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

    // Credential-side failures never reach Anthropic: csm gives up before the
    // request when it has no usable token. There is no endpoint to protect and
    // nothing to hammer, so these keep polling -- signing in, or omp refreshing
    // its own token, brings the widget back without a reload.
    const QUOTA_LOCAL_REASONS = new Set(['no_credentials', 'expired']);

    function renderHeaderQuota(apiQuota) {
        if (!headerQuotaEl) return;
        if (!apiQuota || !apiQuota.available) {
            if (apiQuota && QUOTA_LOCAL_REASONS.has(apiQuota.reason)) {
                // Never signed in is a normal local configuration state and
                // says nothing. A token that has lapsed is worth a word: the
                // numbers are missing for a reason the user can act on.
                headerQuotaEl.innerHTML = apiQuota.reason === 'expired'
                    ? `<span class="header-quota-item header-quota-error" title="${esc(apiQuota.error || '')}">token expired</span>`
                    : '';
                return;
            }
            breakHeaderQuota((apiQuota && apiQuota.error) || 'unknown error');
            return;
        }
        // The Usage tab draws both windows in full, with their resets and their
        // source. The header shows one, because on that tab the two were
        // otherwise drawn twice on the same screen. The one worth the space is
        // whichever is nearer its limit: that is the one about to stop work.
        const windows = [];
        if (apiQuota.five_hour) windows.push(['5h', '5-hour', apiQuota.five_hour]);
        if (apiQuota.seven_day) windows.push(['7d', '7-day', apiQuota.seven_day]);
        windows.sort((a, b) => (b[2].utilization || 0) - (a[2].utilization || 0));
        headerQuotaEl.innerHTML = windows.length
            ? renderHeaderQuotaBar(windows[0][0], windows[0][1], windows[0][2], apiQuota.source)
            : '';
    }

    // The header chip and the usage row must agree on how full a bucket is and
    // when it lifts, so both read it from here rather than each doing the math.
    function quotaBarParts(bucket) {
        const pct = Math.min(bucket.utilization || 0, 100);
        const cls = severityClass(pct);
        let resetsIn = '';
        if (bucket.resets_at) {
            const remaining = new Date(bucket.resets_at) - Date.now();
            if (remaining > 0) resetsIn = formatDurationHuman(remaining * 1e6);
        }
        return { pct, cls, resetsIn };
    }

    function renderHeaderQuotaBar(shortLabel, fullLabel, bucket, source) {
        const { pct, cls, resetsIn } = quotaBarParts(bucket);
        let title = `${fullLabel} quota: ${Math.round(pct)}%`;
        if (resetsIn) title += `, resets in ${resetsIn}`;
        // Whose token these numbers came from. The header has no room to say it
        // outright, and the usage tab does, but a chip that cannot be traced at
        // all is worse than one that needs hovering.
        if (source) title += ` (via ${source})`;
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
            claudeStatusData = await fetchJSON('/api/claude-status');
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

        sseSource.addEventListener('harnesses', e => {
            try {
                mixedHarnesses = JSON.parse(e.data).mixed === true;
            } catch (err) { /* leave the previous answer in place */ }
        });

        sseSource.addEventListener('sessions', e => {
            let payload;
            try {
                payload = JSON.parse(e.data);
            } catch (err) {
                // A frame we cannot read is a frame we cannot draw, and the
                // next one is two seconds away. Say the view is stale rather
                // than leaving a green dot over the last good render.
                connStatus.className = 'stale';
                connStatus.title = 'Connected, but the last update could not be read - data may be out of date';
                return;
            }
            // Deliberately outside the catch: a throw from renderSessions is a
            // bug in this file, and swallowing it would freeze the list under a
            // "connected" dot with nothing in the console to find it by.
            currentSessions = payload;
            if (currentView === 'live') renderSessions();
            connStatus.className = 'connected';
            connStatus.title = 'SSE connected';
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

    // The live list is rebuilt from scratch on every scan, so what the user has
    // opened or closed cannot live in the DOM the way history's collapse state
    // does -- it would reset every two seconds. Both are keyed by project name
    // rather than position, because groups reorder as activity moves between
    // them, and neither is pruned: a set of project names cannot grow past the
    // number of projects the user has touched.
    const collapsedProjects = new Set();   // projects the user closed
    const openStoppedFolds = new Set();    // projects whose stopped rows they opened

    // The order a person triages in: what is blocked on them, then what is
    // moving, then what is parked. Fixed rather than data order, so the status
    // bar and the list do not reshuffle between scans. These are every status
    // session.Status defines; anything else sorts last and wears the Inactive
    // styling that statusClass falls back to.
    const STATUSES = [
        { name: 'Needs Input', cls: 'needs-input', symbol: '\u25B2', word: 'needs input' },
        { name: 'Working', cls: 'working', symbol: '\u25CF', word: 'working' },
        { name: 'Waiting', cls: 'waiting', symbol: '\u25C9', word: 'waiting' },
        { name: 'Inactive', cls: 'inactive', symbol: '\u25CC', word: 'stopped' },
    ];
    const STATUS_ORDER = STATUSES.map(s => s.name);
    const STATUS_BY_NAME = new Map(STATUSES.map(s => [s.name, s]));

    // The class, glyph and word a status is spelled with, in one place:
    // "Inactive" is the API's word and the one a card's tooltip shows,
    // "stopped" is what the badge and the status bar say. A status this
    // page does not know wears the last row.
    function statusInfo(status) {
        return STATUS_BY_NAME.get(status) || STATUSES[STATUSES.length - 1];
    }

    function statusRank(status) {
        const i = STATUS_ORDER.indexOf(status);
        return i === -1 ? STATUS_ORDER.length : i;
    }

    // 0 for anything unreadable rather than NaN: one NaN poisons the Math.max
    // a group's age is built from, and a NaN in the sort comparator makes the
    // whole project fall through to the alphabetical tiebreak with nothing on
    // screen to say why it moved.
    function activityTime(s) {
        const t = s.last_activity ? new Date(s.last_activity).getTime() : 0;
        return Number.isNaN(t) ? 0 : t;
    }

    function renderSessions() {
        if (!currentSessions || currentSessions.length === 0) {
            // An empty dashboard looks broken, so say what would fill it.
            sessionsList.innerHTML = stateBlock({
                title: 'No active sessions',
                hint: 'Start a session in any project and it shows up here.',
            });
            statusBar.innerHTML = '';
            liveSummary.textContent = '';
            return;
        }

        const counts = countByStatus(currentSessions);
        statusBar.innerHTML = STATUS_ORDER
            .filter(status => counts[status])
            .map(status => `<span class="status-badge"><span class="status-dot ${statusClass(status)}"></span>${counts[status]} ${statusWord(status)}</span>`)
            .join('');

        const groups = groupSessions();
        liveSummary.textContent = countSummary(currentSessions.length, groups.length);
        sessionsList.innerHTML = groups.map(renderGroup).join('');
    }

    // groupSessions splits the flat list into per-project groups and puts both
    // the groups and the rows inside them in triage order.
    function groupSessions() {
        const groups = new Map();
        currentSessions.forEach(s => {
            // Same fallback name history uses, so a project with no name is one
            // group across both tabs rather than two differently-labelled ones.
            const project = s.project || 'Unknown';
            if (!groups.has(project)) groups.set(project, []);
            groups.get(project).push(s);
        });

        const ordered = [...groups.entries()].map(([project, sessions]) => {
            sessions.sort((a, b) => statusRank(a.status) - statusRank(b.status) || activityTime(b) - activityTime(a));
            return {
                project,
                sessions,
                active: sessions.filter(s => s.status !== 'Inactive'),
                stopped: sessions.filter(s => s.status === 'Inactive'),
                // Newest activity anywhere in the project, stopped sessions
                // included: a project whose sessions have all stopped still
                // needs an age, and the header is the only place left to show
                // one once the rows are folded away.
                age: Math.max(...sessions.map(activityTime)),
                blocked: sessions.some(s => s.status === 'Needs Input'),
            };
        });

        // A project waiting on the user outranks a busy one: it is the only
        // state that will not move again without them. Ties fall back to the
        // name so the order is stable when two groups have no timestamps.
        ordered.sort((a, b) =>
            (b.blocked - a.blocked) || (b.age - a.age) || a.project.localeCompare(b.project));
        return ordered;
    }

    function renderGroup(g) {
        const collapsed = collapsedProjects.has(g.project);
        const foldOpen = openStoppedFolds.has(g.project);

        const counts = countByStatus(g.sessions);
        const stats = STATUS_ORDER
            .filter(status => counts[status])
            .map(status => `<span class="group-stat"><span class="status-dot ${statusClass(status)}"></span>${counts[status]}</span>`)
            .join('');

        // A closed project's cards are never painted, so they are not built.
        // Opening one calls renderSessions, which rebuilds this group with them.
        let body = '';
        if (!collapsed) {
            body = g.active.map(renderSessionCard).join('');
            if (g.stopped.length > 0) {
                const lastActive = Math.max(...g.stopped.map(activityTime));
                body += `<div class="session-fold${foldOpen ? ' open' : ''}">
                    <span class="group-toggle">&#x25B6;</span>
                    <span class="session-fold-glyph">${statusSymbol('Inactive')}</span>
                    <span>${plural(g.stopped.length, 'stopped session')}</span>
                    <span class="session-fold-age">last active ${lastActive ? formatAge(lastActive) : '-'}</span>
                </div>`;
                if (foldOpen) body += g.stopped.map(renderSessionCard).join('');
            }
        }

        return groupShell({
            project: g.project,
            stats,
            count: g.sessions.length,
            age: g.age ? formatAge(g.age) : '',
            collapsed,
            body,
        });
    }

    function renderSessionCard(s) {
        const isInactive = s.status === 'Inactive';
        const cls = statusClass(s.status);
        const symbol = statusSymbol(s.status);
        const age = s.status === 'Working' ? 'Now' : formatAge(s.last_activity);
        const pct = s.context_percent || 0;
        const ctxCls = severityClass(pct);
        const cardCls = isInactive ? 'session-card stopped' : 'session-card';
        const stoppedBadge = isInactive ? `<span class="stopped-badge">Stopped</span>` : '';
        // Whether to name each card's agent is the server's call: it is decided
        // from every session on the machine, and this list is only the last
        // hour of it, so deriving it here would drop the badge whenever the
        // other agent happened to be idle.
        //
        // It renders with the origin and context-window chips rather than
        // before the branch: it belongs to that cluster of "what this session
        // is" identifiers, and alone at the front it reads as a stray word.
        const harnessBadge = mixedHarnesses && s.harness
            ? `<span class="badge session-harness-badge" title="${esc(harnessName(s.harness))}">${esc(s.harness)}</span>`
            : '';
        // The project name lives in the group header now, so the branch leads
        // the row. A session outside a git checkout has no branch, and falls
        // back to the project name rather than to nothing -- it repeats the
        // header, which is why it is not dressed as a branch.
        const lead = s.git_branch
            ? `<span class="session-branch">${esc(s.git_branch)}</span>`
            : `<span class="session-lead-plain">${esc(s.project)}</span>`;

        return `<div class="${cardCls}" data-logfile="${esc(s.log_file || '')}" data-project="${esc(s.project)}" data-branch="${esc(s.git_branch || '')}" data-status="${esc(s.status)}">
            <div class="session-top">
                <span class="session-status ${cls}" title="${esc(s.status)}">${symbol}</span>
                ${lead}
                ${stoppedBadge}
                ${s.session_title ? `<span class="session-title">${esc(s.session_title)}</span>` : ''}
                <span class="session-meta">
                    ${harnessBadge}
                    ${s.origin && s.origin.category ? `<span class="badge session-origin origin-${esc(s.origin.category)}" title="${esc(s.origin.app || '')}">${esc(s.origin.display || s.origin.app || '')}</span>` : ''}
                    ${(s.context_window || 0) > 200000 ? `<span class="badge session-model-badge" title="${esc(s.model)}">1M</span>` : ''}
                    ${s.degraded ? `<span class="badge session-degraded-badge" title="${esc(s.degraded)}">?</span>` : ''}
                    <span class="session-context" title="${esc(s.model || '')}">
                        <span class="context-bar"><span class="context-fill ${ctxCls}" style="width:${Math.min(pct, 100)}%"></span></span>
                        <span>${pct > 0 ? Math.round(pct) + '%' : '-'}</span>
                    </span>
                    <span class="session-activity">${age}</span>
                    <a class="session-history-link" title="View project history">&#x29D6;</a>
                </span>
            </div>
            ${s.last_message ? `<div class="session-bottom">${esc(s.last_message)}</div>` : ''}
            ${renderSubagents(s.subagents)}
        </div>`;
    }

    // One listener on the container, bound once, rather than one per card on
    // every frame: the card list is rebuilt from scratch every two seconds, so
    // per-card binding re-walks and re-binds the whole list that often.
    sessionsList.addEventListener('click', (e) => {
        const header = e.target.closest('.group-header');
        if (header) {
            toggleMembership(collapsedProjects, header.parentElement.dataset.project);
            renderSessions();
            return;
        }
        const fold = e.target.closest('.session-fold');
        if (fold) {
            toggleMembership(openStoppedFolds, fold.closest('.group').dataset.project);
            renderSessions();
            return;
        }
        const card = e.target.closest('.session-card');
        if (!card) return;
        // If the history link was clicked, navigate to history instead
        if (e.target.classList.contains('session-history-link')) {
            e.preventDefault();
            showProjectHistory(card.dataset.project);
            return;
        }
        // A click on a nested subagent opens that agent's log, not the parent's
        const subagent = e.target.closest('.subagent');
        if (subagent && subagent.dataset.logfile) {
            openDetail(subagent.dataset.logfile, { project: subagent.dataset.label });
            return;
        }
        const logFile = card.dataset.logfile;
        if (logFile) {
            openDetail(logFile, {
                project: card.dataset.project,
                branch: card.dataset.branch,
                status: card.dataset.status,
            });
        }
    });

    function toggleMembership(set, key) {
        if (set.has(key)) set.delete(key); else set.add(key);
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
        historyList.innerHTML = stateBlock({ title: 'Loading history' });
        historySummary.textContent = '';
        try {
            historyData = (await fetchJSON(`/api/history?days=${days}`)) || [];
            renderHistory();
        } catch (err) {
            historySummary.textContent = '';
            historyList.innerHTML = stateBlock({
                title: 'Could not load history',
                hint: esc(err.message || 'the request failed'),
                error: true,
                retry: true,
            });
            wireRetry(historyList, loadHistory);
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
            historySummary.textContent = '';
            // Two filters can empty this list, and the fix differs, so say which
            // one is in the way rather than only that nothing was found.
            historyList.innerHTML = query
                ? stateBlock({
                    title: `No sessions match &quot;${esc(historySearch.value)}&quot;`,
                    hint: 'Clear the search to see every project.',
                })
                : stateBlock({
                    title: 'No sessions in this range',
                    hint: 'Try a wider range.',
                });
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
            const lastStarted = sessions[0] && sessions[0].start_time ? formatAge(sessions[0].start_time) : '';
            let body = `<div class="history-row history-header">
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
                body += `<div class="history-row" data-logfile="${esc(s.log_file || '')}" data-branch="${esc(s.git_branch || '')}">
                    <div class="history-row-main">
                        <span class="history-branch">${s.git_branch ? esc(s.git_branch) : '-'}</span>
                        ${s.degraded ? `<span class="badge session-degraded-badge" title="${esc(s.degraded)}">?</span>` : ''}
                        <span class="history-date">${date}</span>
                        <span class="history-messages">${s.message_count || 0}</span>
                        <span class="history-duration">${dur}</span>
                    </div>
                    ${promptLine}
                </div>`;
            });
            // The same shell the live list draws, minus the status dots: a
            // finished session has no live status to count.
            html += groupShell({
                project,
                count: sessions.length,
                age: lastStarted,
                collapsed: !query,
                body,
            });
        });

        historyList.innerHTML = html;
        historySummary.textContent = countSummary(filtered.length, sortedProjects.length);
    }

    // One delegated listener, bound once, rather than one per row on every
    // render: the search box re-renders the whole list on each keystroke, and
    // a 30-day range is hundreds of rows. Same reason the live list delegates.
    historyList.addEventListener('click', e => {
        const header = e.target.closest('.group-header');
        if (header) {
            header.parentElement.classList.toggle('collapsed');
            return;
        }
        const row = e.target.closest('.history-row:not(.history-header)');
        if (!row || !row.dataset.logfile) return;
        openDetail(row.dataset.logfile, {
            project: row.closest('.group').dataset.project,
            branch: row.dataset.branch,
        });
    });

    historySearch.addEventListener('input', renderHistory);
    historyDays.addEventListener('change', loadHistory);

    // --- Usage view ---
    let usageLoading = false;
    let usageLastUpdated = null;

    async function loadUsage() {
        if (usageLoading) return;
        usageLoading = true;
        usageContent.innerHTML = stateBlock({ title: 'Loading usage' });
        try {
            usageData = await fetchJSON('/api/usage');
            usageLastUpdated = new Date();
            renderUsageView(usageData);
        } catch (err) {
            usageContent.innerHTML = stateBlock({
                title: 'Could not load usage',
                hint: esc(err.message || 'the request failed'),
                error: true,
                retry: true,
            });
            wireRetry(usageContent, loadUsage);
        } finally {
            usageLoading = false;
        }
    }

    function renderUsageView(data) {
        if (!data) {
            usageContent.innerHTML = stateBlock({
                title: 'No usage data in the response',
                hint: 'The request succeeded but carried nothing to show.',
                error: true,
                retry: true,
            });
            wireRetry(usageContent, loadUsage);
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
        html += sectionLabel('API quota &#183; Anthropic account');

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
            if (apiQuota.source) {
                html += `<div class="usage-note">via ${esc(apiQuota.source)}</div>`;
            }
        } else {
            // No invented default. Every reason csm can give is more specific
            // than a guess at the most common one, and the guess this used to
            // print was a Go error string that Go had already stopped emitting.
            let errMsg = apiQuota && apiQuota.error ? apiQuota.error : 'reason unknown';
            if (apiQuota && apiQuota.source) errMsg += `, via ${apiQuota.source}`;
            html += `<div class="usage-unavailable">Not available (${esc(errMsg)})</div>`;
        }
        html += '</div>';

        // Local usage section
        html += '<div class="usage-section">';
        html += sectionLabel('Local usage &#183; 5h window, Claude Code');

        if (local && local.total_tokens > 0) {
            html += '<div class="usage-summary">';
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Total</div><div class="usage-summary-value">${fmtNum(local.total_tokens)}</div></div>`;
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Input</div><div class="usage-summary-value">${fmtNum(local.input_tokens)}</div></div>`;
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Output</div><div class="usage-summary-value">${fmtNum(local.output_tokens)}</div></div>`;
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Cache</div><div class="usage-summary-value">${fmtNum(local.cache_tokens)}</div></div>`;
            html += `<div class="usage-summary-card"><div class="usage-summary-label">Sessions</div><div class="usage-summary-value">${local.sessions ? local.sessions.length : 0}</div></div>`;
            html += '</div>';

            if (local.sessions && local.sessions.length > 0) {
                html += '<div class="usage-table">';
                html += '<div class="usage-table-header">';
                html += '<span class="usage-col-project">Project</span>';
                html += '<span class="usage-col-share">Share of total</span>';
                html += '<span class="usage-col-tokens">Input</span>';
                html += '<span class="usage-col-tokens">Output</span>';
                html += '<span class="usage-col-tokens">Cache</span>';
                html += '<span class="usage-col-tokens">Total</span>';
                html += '</div>';
                local.sessions.forEach(s => {
                    html += '<div class="usage-table-row">';
                    html += `<span class="usage-col-project">${esc(s.project)}</span>`;
                    // The block only renders when local.total_tokens > 0, so
                    // this cannot divide by zero.
                    const share = (s.total_tokens / local.total_tokens) * 100;
                    html += `<span class="usage-col-share"><span class="share-bar"><span class="share-bar-fill" style="width:${share}%"></span></span><span class="share-pct">${formatShare(s.total_tokens, local.total_tokens)}</span></span>`;
                    html += `<span class="usage-col-tokens">${fmtNum(s.input_tokens)}</span>`;
                    html += `<span class="usage-col-tokens">${fmtNum(s.output_tokens)}</span>`;
                    html += `<span class="usage-col-tokens">${fmtNum(s.cache_tokens)}</span>`;
                    html += `<span class="usage-col-tokens">${fmtNum(s.total_tokens)}</span>`;
                    html += '</div>';
                });
                html += '</div>';
            }
        } else if (local && local.error) {
            // Saying "no usage" here would be a positive claim invented from a
            // failure to look.
            html += `<div class="usage-unavailable">Local usage unavailable (${esc(local.error)})</div>`;
        } else if (local) {
            // A measured zero, so muted ink. The amber above is for numbers the
            // app could not reach, and painting a true zero in it would say
            // something is wrong when nothing is.
            html += '<div class="usage-none">No token usage in the past 5 hours.</div>';
        } else {
            // Neither a reading nor a stated error: the same rule applies, so
            // this reports what is missing rather than inventing a zero.
            html += '<div class="usage-unavailable">Local usage is missing from the response.</div>';
        }
        if (local && local.partial_logs > 0) {
            html += `<div class="usage-partial">${local.partial_logs} log(s) could not be read in full; totals are a lower bound.</div>`;
        }
        html += '</div>';

        usageContent.innerHTML = html;

        const refreshBtn = document.getElementById('usage-refresh-btn');
        if (refreshBtn) refreshBtn.addEventListener('click', loadUsage);
    }

    function renderUsageBar(label, bucket) {
        const { pct, cls, resetsIn } = quotaBarParts(bucket);
        const resetHtml = resetsIn
            ? `<span class="usage-bar-reset">resets in ${esc(resetsIn)}</span>`
            : '';
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

    function openDetail(logFile, { project = '', branch = '', status = '' } = {}) {
        currentLogFile = logFile;
        timelineOffset = 0;
        timelineEntries = [];
        timelineFilter = 'all';
        timelineLoadMoreClicks = 0;
        // Several live sessions can share a project now that the list is
        // grouped by project, so the panel names the row that was clicked.
        // Subagents open with a label and no branch, and get just the label.
        detailTitle.innerHTML = `${status ? `<span class="session-status ${statusClass(status)}" title="${esc(status)}">${statusSymbol(status)}</span>` : ''}<span class="detail-project">${esc(project)}</span>${branch ? `<span class="session-branch">${esc(branch)}</span>` : ''}`;
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
        detailMetrics.innerHTML = stateBlock({ title: 'Loading metrics' });
        try {
            const m = await fetchJSON(`/api/sessions/metrics?file=${encodeURIComponent(logFile)}`);
            if (token !== metricsLoadToken) return;
            renderMetrics(m);
        } catch (err) {
            if (token !== metricsLoadToken) return;
            detailMetrics.innerHTML = stateBlock({
                title: 'Could not load metrics',
                hint: esc(err.message || 'the log file could not be read'),
                error: true,
                retry: true,
            });
            // metricsLoadToken already drops a response for a session the user
            // has since navigated away from, so the retry needs no guard of its
            // own beyond asking for the file it failed on.
            wireRetry(detailMetrics, () => loadMetrics(logFile));
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
        const severity = SEVERITY_WORDS[severityClass(pct)];

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
                ${sectionLabel('Token composition')}
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
            html += `<section class="tool-usage">${sectionLabel('Tools')}<ul class="tool-list">`;
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
            detailTimeline.innerHTML = stateBlock({ title: 'Loading timeline' });
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
                const data = await fetchJSON(`/api/sessions/timeline?file=${encodeURIComponent(logFile)}&offset=${timelineOffset}&limit=${limit}${typeParam}`);
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
            timelineError = err.message || 'the request failed';
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
            // The filter bar alone is not a way back: clicking the filter you
            // are already on returns early, so a failure under it would leave
            // no control that retries.
            html += stateBlock({
                title: 'Could not load the timeline',
                hint: esc(timelineError),
                error: true,
                retry: true,
            });
        } else if (timelineEntries.length === 0) {
            html += stateBlock({
                title: timelineFilter === 'all' ? 'No entries' : 'No matching entries',
            });
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

        wireRetry(detailTimeline, () => loadTimeline(currentLogFile, true));

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
        return statusInfo(status).cls;
    }

    function statusSymbol(status) {
        return statusInfo(status).symbol;
    }

    // Every endpoint here answers a failure with {"error": "..."} and a status
    // code, so a plain resp.json() on a 500 hands the caller that envelope as
    // if it were data -- history did exactly that, and the list then failed on
    // an object with no .filter. Reading the reason out of the envelope is also
    // what stops a panel printing raw JSON at the user.
    async function fetchJSON(url) {
        const resp = await fetch(url);
        let body = null;
        try {
            body = await resp.json();
        } catch (err) {
            // A non-JSON body is only worth reporting through the status below.
        }
        if (!resp.ok) throw new Error((body && body.error) || `HTTP ${resp.status}`);
        if (body === null) throw new Error(`HTTP ${resp.status} with a body that is not JSON`);
        return body;
    }

    // The heading used by the two usage sections and both halves of the metrics
    // panel. Live and history build the same markup in index.html instead,
    // because their meta span needs a stable id for later text updates.
    // `label` is markup the caller controls, never session data, so it is not
    // escaped here.
    function sectionLabel(label) {
        return `<div class="section-label">
            <span class="section-label-text">${label}</span>
            <span class="section-label-rule"></span>
        </div>`;
    }

    // One spelling of the plural rule, which was written inline seven times
    // in two different forms.
    function plural(n, word) {
        return `${n} ${word}${n === 1 ? '' : 's'}`;
    }

    // The "N sessions / M projects" line both the live and history tabs show.
    function countSummary(sessions, projects) {
        return `${plural(sessions, 'session')} \u00b7 ${plural(projects, 'project')}`;
    }

    // One severity ramp for every bar and figure that has one, so a threshold
    // moves in one place. The metrics panel spells the same three steps with
    // its own words and maps the result rather than repeating the numbers.
    const SEVERITY_WORDS = { low: 'ok', medium: 'warning', high: 'danger' };

    function severityClass(pct) {
        return pct >= 90 ? 'high' : pct >= 75 ? 'medium' : 'low';
    }

    // The empty, loading and error block every panel shows. It returns markup
    // rather than assigning innerHTML, because the timeline builds its state
    // into a larger string. `title` and `hint` are markup the caller controls,
    // like sectionLabel's label, so they are not escaped here. A block asking
    // for retry needs wireRetry on whatever received the markup.
    function stateBlock({ title, hint, error, retry }) {
        return `<div class="state">
            <div class="state-title${error ? ' error' : ''}">${title}</div>
            ${hint ? `<div class="state-hint">${hint}</div>` : ''}
            ${retry ? `<button type="button" class="state-retry">Retry</button>` : ''}
        </div>`;
    }

    function wireRetry(container, fn) {
        const button = container.querySelector('.state-retry');
        if (button) button.addEventListener('click', fn);
    }

    // One pass for the per-status tally. The group stats row used to ask the
    // same list eight separate questions, on every scan.
    function countByStatus(sessions) {
        const counts = {};
        sessions.forEach(s => { counts[s.status] = (counts[s.status] || 0) + 1; });
        return counts;
    }

    // The project group shell both tabs draw. The live list passes status
    // dots; history has no live status to count and passes none.
    function groupShell({ project, stats, count, age, collapsed, body }) {
        return `<div class="group${collapsed ? ' collapsed' : ''}" data-project="${esc(project)}">
            <div class="group-header">
                <span class="group-toggle">&#x25B6;</span>
                <span class="group-name">${esc(project)}</span>
                ${stats ? `<span class="group-stats">${stats}</span>` : ''}
                <span class="group-count">${plural(count, 'session')}</span>
                <span class="group-age">${age || ''}</span>
            </div>
            <div class="group-body">${body}</div>
        </div>`;
    }

    function statusWord(status) {
        return statusInfo(status).word;
    }

    // The badge shows the short id the API sends; the tooltip spells it out, so
    // "omp" on a card is never a mystery.
    function harnessName(harness) {
        switch (harness) {
            case 'claude': return 'Claude Code';
            case 'omp': return 'Oh My Pi';
            default: return harness;
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

})();

// One entry per job card: the last state seen, its socket and where its log has got to.
const jobs = new Map();

const FINISHED = new Set(['completed', 'failed', 'cancelled']);
const MAX_RETRY_DELAY = 30000;
// The server keeps as many; past them it drops the oldest too.
const MAX_LOG_LINES = 5000;

// A 401 means the session is gone: back to login, not an error on every refresh.
async function apiFetch(url, options) {
    const response = await fetch(url, options);
    if (response.status === 401) {
        window.location.href = '/login.html';
        throw new Error('Not authenticated');
    }
    return response;
}

async function logout() {
    try {
        await fetch('/api/logout', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
        });
    } catch (error) {
        // Back to the login page either way.
    }
    window.location.href = '/login.html';
}

document.addEventListener('DOMContentLoaded', () => {
    document.getElementById('logout-btn').addEventListener('click', logout);
    document.getElementById('download-btn').addEventListener('click', startDownload);
    refreshJobs();
    setInterval(refreshJobs, 10000);
});

async function startDownload() {
    const urlInput = document.getElementById('url-input');
    const url = urlInput.value.trim();
    const downloadBtn = document.getElementById('download-btn');
    const errorMsg = document.getElementById('error-message');

    if (!url) {
        showError('Please enter a YouTube playlist URL');
        return;
    }

    downloadBtn.disabled = true;
    errorMsg.classList.add('hidden');

    try {
        const response = await apiFetch('/api/download', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ url }),
        });

        if (!response.ok) {
            const error = await response.text();
            throw new Error(error);
        }

        const job = await response.json();
        urlInput.value = '';
        const entry = entryFor(job.id);
        entry.job = job;
        render(entry);
        document.getElementById('jobs-list').prepend(entry.card);
        updateEmptyState();
        connect(job.id);
    } catch (error) {
        showError(error.message);
    } finally {
        downloadBtn.disabled = false;
    }
}

// Polling catches jobs started elsewhere and those whose socket is not open; a socket's updates are fresher.
async function refreshJobs() {
    let list;
    try {
        const response = await apiFetch('/api/jobs');
        if (!response.ok) {
            throw new Error('Failed to load jobs');
        }
        list = (await response.json()) || [];
    } catch (error) {
        console.error('Failed to load jobs:', error);
        return;
    }

    const listed = new Set();
    for (const job of list) {
        listed.add(job.id);
        const entry = entryFor(job.id);
        if (entry.ws || entry.retrying) {
            continue;
        }
        entry.job = job;
        render(entry);
        if (!FINISHED.has(job.status)) {
            connect(job.id);
        }
    }
    placeCards(list);

    for (const [id, entry] of jobs) {
        if (!listed.has(id) && !entry.ws && !entry.retrying && !entry.lost && !entry.logOpen) {
            entry.card.remove();
            jobs.delete(id);
        }
    }
    updateEmptyState();
}

// list is newest first. Cards already shown stay where they are: moving one would reset its log's scroll.
function placeCards(list) {
    const jobsList = document.getElementById('jobs-list');
    let older = null;
    for (let i = list.length - 1; i >= 0; i--) {
        const card = jobs.get(list[i].id).card;
        if (!card.isConnected) {
            jobsList.insertBefore(card, older);
        }
        older = card;
    }
}

function updateEmptyState() {
    document.getElementById('jobs-empty').classList.toggle('hidden', jobs.size > 0);
}

function entryFor(id) {
    let entry = jobs.get(id);
    if (!entry) {
        entry = {
            id,
            job: null,
            ws: null,
            retrying: false,
            retryDelay: 1000,
            nextSeq: 0,
            dropped: 0,
            logOpen: false,
            logLoaded: false,
            cancelling: false,
            lost: false,
        };
        entry.card = buildCard(entry);
        jobs.set(id, entry);
    }
    return entry;
}

function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) {
        node.className = className;
    }
    if (text) {
        node.textContent = text;
    }
    return node;
}

function buildCard(entry) {
    const card = el('div', 'job-card');
    card.dataset.jobId = entry.id;

    const header = el('div', 'job-header');
    header.append(el('span', 'job-url'), el('span', 'status'));

    const bar = el('div', 'progress-bar');
    bar.append(el('div', 'progress-fill'));

    const info = el('div', 'job-info');
    info.append(el('span', 'job-progress'), el('span', 'job-time'));

    const actions = el('div', 'job-actions');
    const cancelBtn = el('button', 'cancel-btn', 'Cancel');
    cancelBtn.addEventListener('click', () => cancelJob(entry));
    const logBtn = el('button', 'log-btn', 'Log');
    logBtn.addEventListener('click', () => toggleLog(entry));
    actions.append(cancelBtn, logBtn);

    const log = el('div', 'log hidden');
    const toolbar = el('div', 'log-toolbar');
    const details = el('label', 'log-details');
    const checkbox = el('input');
    checkbox.type = 'checkbox';
    details.append(checkbox, document.createTextNode(' details'));
    toolbar.append(el('span', 'log-dropped hidden'), details);
    const pane = el('div', 'log-pane');
    checkbox.addEventListener('change', () => keepAtBottom(pane, () => pane.classList.toggle('show-debug', checkbox.checked)));
    log.append(toolbar, pane);

    card.append(header, bar, info, el('div', 'job-error hidden'), el('ul', 'job-warnings hidden'), actions, log);
    return card;
}

function render(entry) {
    const job = entry.job;
    const card = entry.card;

    card.querySelector('.job-url').textContent = job.url;

    const status = card.querySelector('.status');
    const shown = entry.lost ? 'lost' : job.status;
    status.textContent = shown;
    status.className = `status ${shown}`;

    const progress = job.total > 0 ? (job.progress / job.total) * 100 : 0;
    card.querySelector('.progress-fill').style.width = `${progress}%`;
    card.querySelector('.job-progress').textContent = `${job.progress}/${job.total}`;
    card.querySelector('.job-time').textContent = job.started_at ? `Started: ${job.started_at}` : `Queued: ${job.created_at}`;

    const error = card.querySelector('.job-error');
    const errorText = entry.lost ? 'Lost when the server restarted' : job.error;
    error.textContent = errorText || '';
    error.classList.toggle('hidden', !errorText);

    const warnings = card.querySelector('.job-warnings');
    warnings.replaceChildren(...(job.warnings || []).map(w => el('li', '', w)));
    warnings.classList.toggle('hidden', !job.warnings || job.warnings.length === 0);

    const cancelBtn = card.querySelector('.cancel-btn');
    cancelBtn.classList.toggle('hidden', entry.lost || FINISHED.has(job.status));
    cancelBtn.disabled = entry.cancelling;
    cancelBtn.textContent = entry.cancelling ? 'Cancelling…' : 'Cancel';
}

function connect(id) {
    const entry = jobs.get(id);
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${protocol}//${window.location.host}/ws?job_id=${encodeURIComponent(id)}`);
    entry.ws = ws;

    ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        entry.retryDelay = 1000;
        entry.job = msg.job;
        if (FINISHED.has(msg.job.status)) {
            entry.cancelling = false;
        }
        render(entry);
        entry.dropped = msg.log_dropped;
        entry.logLoaded = true;
        addLines(entry, msg.log || []);
    };

    ws.onclose = () => {
        entry.ws = null;
        if (!entry.job || !FINISHED.has(entry.job.status)) {
            reconnect(id);
        }
    };
}

// Asks first whether the job still exists: after a restart the server no longer knows it.
function reconnect(id) {
    const entry = jobs.get(id);
    const delay = entry.retryDelay;
    entry.retryDelay = Math.min(delay * 2, MAX_RETRY_DELAY);
    entry.retrying = true;

    setTimeout(async () => {
        let response;
        try {
            response = await apiFetch(`/api/jobs/${encodeURIComponent(id)}`);
        } catch (error) {
            if (error.message !== 'Not authenticated') {
                reconnect(id);
            }
            return;
        }
        entry.retrying = false;

        if (response.status === 404) {
            entry.lost = true;
            render(entry);
            return;
        }
        if (!response.ok) {
            reconnect(id);
            return;
        }
        entry.job = await response.json();
        render(entry);
        if (!FINISHED.has(entry.job.status)) {
            connect(id);
            return;
        }
        // The job ended while the socket was down: the end of its log never arrived.
        entry.logLoaded = false;
        if (entry.logOpen) {
            loadLog(entry);
        }
    }, delay);
}

async function cancelJob(entry) {
    entry.cancelling = true;
    render(entry);

    try {
        const response = await apiFetch(`/api/jobs/${encodeURIComponent(entry.id)}/cancel`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
        });

        if (!response.ok) {
            throw new Error('Failed to cancel job');
        }
    } catch (error) {
        entry.cancelling = false;
        render(entry);
        showError(error.message);
    }
}

async function toggleLog(entry) {
    entry.logOpen = !entry.logOpen;
    const log = entry.card.querySelector('.log');
    log.classList.toggle('hidden', !entry.logOpen);
    entry.card.querySelector('.log-btn').textContent = entry.logOpen ? 'Hide log' : 'Log';
    if (!entry.logOpen) {
        return;
    }

    if (!entry.logLoaded) {
        await loadLog(entry);
    }
    const pane = log.querySelector('.log-pane');
    pane.scrollTop = pane.scrollHeight;
}

async function loadLog(entry) {
    try {
        const response = await apiFetch(`/api/jobs/${encodeURIComponent(entry.id)}/log`);
        if (!response.ok) {
            throw new Error(response.status === 404 ? 'The log is gone with its job' : 'Failed to load the log');
        }
        const data = await response.json();
        entry.dropped = data.dropped;
        entry.logLoaded = true;
        addLines(entry, data.lines || []);
    } catch (error) {
        showError(error.message);
    }
}

function addLines(entry, lines) {
    const pane = entry.card.querySelector('.log-pane');
    keepAtBottom(pane, () => appendLines(entry, pane, lines));

    const dropped = entry.card.querySelector('.log-dropped');
    dropped.textContent = `${entry.dropped} earlier lines dropped; the full log is in the server's log file`;
    dropped.classList.toggle('hidden', entry.dropped === 0);
}

// Lines carry their number in the job's log: one already shown, sent again after a reconnect, is skipped.
function appendLines(entry, pane, lines) {
    const fragment = document.createDocumentFragment();
    for (const line of lines) {
        if (line.seq < entry.nextSeq) {
            continue;
        }
        entry.nextSeq = line.seq + 1;
        const text = [line.time, line.level.padEnd(5), line.prefix, line.text].filter(Boolean).join(' ');
        fragment.append(el('div', `log-line lvl-${line.level}`, text));
    }
    pane.append(fragment);
    while (pane.childElementCount > MAX_LOG_LINES) {
        pane.firstElementChild.remove();
    }
}

// Whoever reads at the bottom keeps following the log; whoever scrolled up stays where they are.
function keepAtBottom(pane, change) {
    const atBottom = pane.scrollTop + pane.clientHeight >= pane.scrollHeight - 4;
    change();
    if (atBottom) {
        pane.scrollTop = pane.scrollHeight;
    }
}

function showError(message) {
    const errorMsg = document.getElementById('error-message');
    errorMsg.textContent = message;
    errorMsg.classList.remove('hidden');

    setTimeout(() => {
        errorMsg.classList.add('hidden');
    }, 5000);
}

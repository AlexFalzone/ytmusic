let currentJobId = null;

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
    loadJobHistory();
    setInterval(loadJobHistory, 10000);
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
        currentJobId = job.id;
        urlInput.value = '';
        displayCurrentJob(job);
        connectWebSocket(job.id);
    } catch (error) {
        showError(error.message);
        downloadBtn.disabled = false;
    }
}

function connectWebSocket(jobId) {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws?job_id=${jobId}`;

    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
        const job = JSON.parse(event.data);
        updateCurrentJob(job);

        if (job.status === 'completed' || job.status === 'failed' || job.status === 'cancelled') {
            loadJobHistory();
            setTimeout(() => {
                document.getElementById('current-job').classList.add('hidden');
                document.getElementById('download-btn').disabled = false;
                currentJobId = null;
            }, 3000);
        }
    };

    ws.onerror = (error) => {
        console.error('WebSocket error:', error);
    };

    ws.onclose = () => {
        document.getElementById('download-btn').disabled = false;
    };
}

function displayCurrentJob(job) {
    const currentJobDiv = document.getElementById('current-job');
    currentJobDiv.classList.remove('hidden');

    document.getElementById('job-url').textContent = job.url;
    updateCurrentJob(job);
}

function updateCurrentJob(job) {
    const statusElem = document.getElementById('job-status');
    statusElem.textContent = job.status;
    statusElem.className = `status ${job.status}`;

    const progress = job.total > 0 ? (job.progress / job.total) * 100 : 0;
    document.getElementById('progress-fill').style.width = `${progress}%`;
    document.getElementById('job-progress').textContent = `${job.progress}/${job.total}`;

    if (job.created_at) {
        document.getElementById('job-time').textContent = `Started: ${job.created_at}`;
    }

    const cancelBtn = document.getElementById('cancel-btn');
    if (job.status === 'pending' || job.status === 'running') {
        cancelBtn.classList.remove('hidden');
    } else {
        cancelBtn.classList.add('hidden');
    }
}

async function cancelJob() {
    if (!currentJobId) return;

    try {
        const response = await apiFetch(`/api/jobs/${currentJobId}/cancel`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
        });

        if (!response.ok) {
            throw new Error('Failed to cancel job');
        }
    } catch (error) {
        showError(error.message);
    }
}

async function loadJobHistory() {
    try {
        const response = await apiFetch('/api/jobs');
        if (!response.ok) {
            throw new Error('Failed to load jobs');
        }

        const jobs = await response.json();
        displayJobHistory(jobs);
    } catch (error) {
        console.error('Failed to load job history:', error);
    }
}

function displayJobHistory(jobs) {
    const jobsList = document.getElementById('jobs-list');

    if (!jobs || jobs.length === 0) {
        jobsList.innerHTML = '<p style="color: #999;">No jobs yet</p>';
        return;
    }

    const history = jobs
        .filter(job => job.id !== currentJobId)
        .slice(0, 10);

    jobsList.innerHTML = history.map(job => {
        const progress = job.total > 0 ? (job.progress / job.total) * 100 : 0;
        return `
            <div class="job-list-item">
                <div class="job-header">
                    <span class="job-url">${escapeHTML(job.url)}</span>
                    <span class="status ${job.status}">${job.status}</span>
                </div>
                ${job.total > 0 ? `
                    <div class="progress-bar">
                        <div class="progress-fill" style="width: ${progress}%"></div>
                    </div>
                ` : ''}
                <div class="job-info">
                    <span>${job.progress}/${job.total}</span>
                    <span>${job.created_at}</span>
                </div>
            </div>
        `;
    }).join('');
}

function escapeHTML(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

function showError(message) {
    const errorMsg = document.getElementById('error-message');
    errorMsg.textContent = message;
    errorMsg.classList.remove('hidden');

    setTimeout(() => {
        errorMsg.classList.add('hidden');
    }, 5000);
}

const $ = id => document.getElementById(id);

function updateStatusFromData(data) {
    const state = data.encoder.state;
    const running = state === 'running';
    const pill = $('status-pill');

    // Status pill styling
    if (state === 'running') {
        pill.className = 'running';
    } else if (state === 'stopped') {
        pill.className = 'stopped';
    } else {
        pill.className = 'warning';
    }
    $('status-text').textContent = state.charAt(0).toUpperCase() + state.slice(1);

    // Source status
    const sourceStatus = $('source-status');
    const hasSourceIssue = (data.encoder.source_retry_count > 0 && state !== 'stopped') ||
                          (data.encoder.last_error && state !== 'running');

    if (hasSourceIssue) {
        sourceStatus.hidden = false;
        $('source-retry').textContent = data.encoder.source_retry_count > 0
            ? `Retry ${data.encoder.source_retry_count}/${data.encoder.source_max_retries}`
            : 'Error';
        const errorEl = $('source-error');
        errorEl.textContent = data.encoder.last_error || '';
        errorEl.hidden = !data.encoder.last_error;
    } else {
        sourceStatus.hidden = true;
    }

    if (!running) resetVuMeter();

    currentOutputs = data.outputs || [];
    currentStatuses = data.output_status || {};
    $('output-count').textContent = currentOutputs.length;
    renderOutputs(currentOutputs, currentStatuses);

    if (data.devices) {
        updateAudioDevices(data.devices, data.settings?.audio_input);
    }
}

function renderOutputs(outputs, statuses) {
    const list = $('outputs-list');
    if (!outputs?.length) {
        list.innerHTML = '';
        return;
    }

    // Clean up deletingOutputs - remove if output no longer exists OR if createdAt changed (ID reused)
    for (const [id, createdAt] of deletingOutputs) {
        const output = outputs.find(o => o.id === id);
        if (!output || output.created_at !== createdAt) {
            deletingOutputs.delete(id);
        }
    }

    list.innerHTML = outputs.map(o => {
        const status = statuses[o.id] || {};
        const isDeleting = deletingOutputs.get(o.id) === o.created_at;
        const isRetrying = status.retry_count > 0 && !status.given_up;
        const givenUp = status.given_up;
        const isConnected = status.running && !isDeleting;

        let dotClass, statusClass, statusText;
        if (isDeleting) {
            dotClass = statusClass = 'warning';
            statusText = 'Stopping...';
        } else if (isConnected) {
            dotClass = statusClass = 'success';
            statusText = 'Connected';
        } else if (givenUp) {
            dotClass = statusClass = 'danger';
            statusText = 'Failed';
        } else {
            dotClass = statusClass = 'warning';
            statusText = isRetrying ? `Retry ${status.retry_count}` : 'Connecting';
        }
        const showError = !isDeleting && (givenUp || isRetrying) && status.last_error;

        return `
        <div class="output-item${isDeleting ? ' deleting' : ''}">
            <div class="output-row">
                <span class="output-dot ${dotClass}"></span>
                <span class="output-host">${escapeHtml(o.host)}:${o.port}</span>
                <button class="output-delete" data-id="${o.id}" title="Delete"${isDeleting ? ' disabled' : ''}>
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6L6 18M6 6l12 12"/></svg>
                </button>
            </div>
            <div class="output-details">
                <span class="output-codec">${o.codec.toUpperCase()}</span>
                <span class="output-streamid">#${escapeHtml(o.streamid)}</span>
                <span class="output-status ${statusClass}">${statusText}</span>
            </div>
            ${showError ? `<p class="output-error">${escapeHtml(status.last_error)}</p>` : ''}
        </div>`;
    }).join('');
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

function wsCommand(type, id, data) {
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type, id, data }));
    }
}

let currentOutputs = [];
let currentStatuses = {};
const deletingOutputs = new Map(); // id -> createdAt (to detect ID reuse)

function showModal() {
    $('modal').hidden = false;
    $('input-host').value = '';
    $('input-port').value = '8080';
    $('input-streamid').value = '';
    $('input-password').value = '';
    $('input-codec').value = 'mp3';
    $('input-host').focus();
}

function hideModal() {
    $('modal').hidden = true;
}

function addOutput() {
    const host = $('input-host').value.trim();
    const port = Number.parseInt($('input-port').value, 10);
    const streamid = $('input-streamid').value.trim() || 'studio';
    const password = $('input-password').value;
    const codec = $('input-codec').value;

    if (!host) {
        $('input-host').focus();
        return;
    }

    wsCommand('add_output', null, { host, port, streamid, password, codec });
    hideModal();
}

// VU Meter
let vuMode = localStorage.vuMode || 'peak';

function dbToPercent(db) {
    return Math.max(0, Math.min(100, ((db + 60) / 60) * 100));
}

function updateLevelsFromData(levels) {
    const showPeak = vuMode === 'peak';
    const displayL = showPeak ? levels.peak_left : levels.left;
    const displayR = showPeak ? levels.peak_right : levels.right;

    $('vu-left-cover').style.width = `${100 - dbToPercent(levels.left)}%`;
    $('vu-right-cover').style.width = `${100 - dbToPercent(levels.right)}%`;
    $('peak-left').style.left = `${dbToPercent(levels.peak_left)}%`;
    $('peak-right').style.left = `${dbToPercent(levels.peak_right)}%`;
    $('db-left').textContent = `${displayL.toFixed(1)} dB`;
    $('db-right').textContent = `${displayR.toFixed(1)} dB`;
}

function getVuModeLabel() {
    return vuMode === 'peak' ? 'Peak' : 'RMS';
}

function toggleVuMode() {
    vuMode = vuMode === 'peak' ? 'rms' : 'peak';
    localStorage.vuMode = vuMode;
    $('vu-mode-toggle').textContent = getVuModeLabel();
}

function resetVuMeter() {
    $('vu-left-cover').style.width = $('vu-right-cover').style.width = '100%';
    $('peak-left').style.left = $('peak-right').style.left = '0%';
    $('db-left').textContent = $('db-right').textContent = '-60 dB';
}

// Audio Input
let currentAudioInput = '';

function updateAudioDevices(devices, selectedInput) {
    const select = $('audio-input');

    if (selectedInput && selectedInput !== currentAudioInput) {
        currentAudioInput = selectedInput;
    }

    if (select.options.length === 0) {
        if (!devices || devices.length === 0) {
            select.innerHTML = '<option value="">No devices found</option>';
            return;
        }

        for (const device of devices) {
            const option = document.createElement('option');
            option.value = device.id;
            option.textContent = device.name;
            if (device.id === currentAudioInput) option.selected = true;
            select.appendChild(option);
        }
    }
}

// WebSocket
let ws = null;

function showConnecting() {
    $('status-pill').className = '';
    $('status-text').textContent = 'Connecting';
    resetVuMeter();
}

function connectWebSocket() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${protocol}//${location.host}/ws`);

    ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.type === 'levels') {
            updateLevelsFromData(msg.levels);
        } else if (msg.type === 'status') {
            updateStatusFromData(msg);
        }
    };

    ws.onclose = () => {
        showConnecting();
        setTimeout(connectWebSocket, 1000);
    };

    ws.onerror = () => ws.close();
}

// Event listeners
$('theme-toggle').onclick = () => {
    document.documentElement.classList.toggle('dark');
    localStorage.theme = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
};

$('audio-input').onchange = (e) => {
    wsCommand('update_settings', null, { audio_input: e.target.value });
};

$('outputs-list').onclick = (e) => {
    const btn = e.target.closest('.output-delete');
    if (btn && !btn.disabled && confirm('Delete this output?')) {
        const output = currentOutputs.find(o => o.id === btn.dataset.id);
        if (output) deletingOutputs.set(btn.dataset.id, output.created_at);
        wsCommand('delete_output', btn.dataset.id);
        // Immediately re-render to show "Stopping..." state
        renderOutputs(currentOutputs, currentStatuses);
    }
};

$('add-btn').onclick = showModal;
$('cancel-btn').onclick = hideModal;
$('save-btn').onclick = addOutput;
$('vu-mode-toggle').onclick = toggleVuMode;
document.querySelector('.modal-overlay').onclick = hideModal;

for (const input of document.querySelectorAll('.modal-content input')) {
    input.addEventListener('keydown', e => {
        if (e.key === 'Enter') addOutput();
    });
}

// Init
$('vu-mode-toggle').textContent = getVuModeLabel();
connectWebSocket();

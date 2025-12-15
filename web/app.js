const $ = id => document.getElementById(id);

function toggleDarkMode() {
    document.documentElement.classList.toggle('dark');
    localStorage.theme = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
}

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
        sourceStatus.classList.remove('hidden');
        $('source-retry').textContent = data.encoder.source_retry_count > 0
            ? `Retry ${data.encoder.source_retry_count}/${data.encoder.source_max_retries}`
            : 'Error';
        const errorEl = $('source-error');
        errorEl.textContent = data.encoder.last_error || '';
        errorEl.classList.toggle('hidden', !data.encoder.last_error);
    } else {
        sourceStatus.classList.add('hidden');
    }

    if (!running) resetVuMeter();

    currentOutputs = data.outputs || [];
    $('output-count').textContent = currentOutputs.length;
    renderOutputs(currentOutputs, data.output_status || {});

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

    list.innerHTML = outputs.map(o => {
        const status = statuses[o.id] || {};
        const isRetrying = status.retry_count > 0 && !status.given_up;
        const givenUp = status.given_up;
        const isConnected = status.running;

        const dotClass = isConnected ? 'success' : (givenUp ? 'danger' : 'warning');
        const statusClass = isConnected ? 'success' : (givenUp ? 'danger' : 'warning');
        const statusText = isConnected ? 'Connected' : (givenUp ? 'Failed' : (isRetrying ? `Retry ${status.retry_count}` : 'Connecting'));
        const showError = (givenUp || isRetrying) && status.last_error;

        return `
        <div class="output-item">
            <div class="output-row">
                <span class="output-dot ${dotClass}"></span>
                <span class="output-host">${escapeHtml(o.host)}</span>
                <button class="output-delete" onclick="deleteOutput('${o.id}')" title="Delete">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6L6 18M6 6l12 12"/></svg>
                </button>
            </div>
            <div class="output-details">
                <span>Port ${o.port}</span>
                <span class="sep">-</span>
                <span>${escapeHtml(o.streamid)}</span>
                <span class="sep">-</span>
                <span>${o.codec.toUpperCase()}</span>
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

function deleteOutput(id) {
    if (confirm('Delete this output?')) {
        wsCommand('delete_output', id);
    }
}

function showModal() {
    $('modal').classList.remove('hidden');
    $('input-host').value = '';
    $('input-port').value = '8080';
    $('input-streamid').value = '';
    $('input-password').value = '';
    $('input-codec').value = 'mp3';
    $('input-host').focus();
}

function hideModal() {
    $('modal').classList.add('hidden');
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
let peakHoldLeft = -60;
let peakHoldRight = -60;
const peakDecay = 0.3;

function dbToPercent(db) {
    return Math.max(0, Math.min(100, ((db + 60) / 60) * 100));
}

function updateLevelsFromData(levels) {
    peakHoldLeft = Math.max(levels.peak_left, peakHoldLeft - peakDecay);
    peakHoldRight = Math.max(levels.peak_right, peakHoldRight - peakDecay);

    $('vu-left-cover').style.width = `${100 - dbToPercent(levels.left)}%`;
    $('vu-right-cover').style.width = `${100 - dbToPercent(levels.right)}%`;
    $('peak-left').style.left = `${dbToPercent(peakHoldLeft)}%`;
    $('peak-right').style.left = `${dbToPercent(peakHoldRight)}%`;
    $('db-left').textContent = `${levels.left.toFixed(1)} dB`;
    $('db-right').textContent = `${levels.right.toFixed(1)} dB`;
}

function resetVuMeter() {
    peakHoldLeft = peakHoldRight = -60;
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

function updateAudioInput(deviceId) {
    wsCommand('update_settings', null, { audio_input: deviceId });
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
$('add-btn').onclick = showModal;
$('cancel-btn').onclick = hideModal;
$('save-btn').onclick = addOutput;
document.querySelector('.modal-overlay').onclick = hideModal;

// Handle Enter key in modal
for (const input of document.querySelectorAll('.modal-content input')) {
    input.addEventListener('keydown', e => {
        if (e.key === 'Enter') addOutput();
    });
}

// Init
connectWebSocket();

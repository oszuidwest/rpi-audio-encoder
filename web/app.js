const $ = id => document.getElementById(id);

function toggleDarkMode() {
    document.documentElement.classList.toggle('dark');
    localStorage.theme = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
}

function updateStatusFromData(data) {
    const state = data.encoder.state;
    const running = state === 'running';

    // Status pill
    const pill = $('status-pill');
    const dot = $('status-dot');
    pill.className = 'flex items-center gap-2 px-3 py-1.5 rounded-full text-sm font-semibold ';
    if (state === 'running') {
        pill.className += 'bg-success/10 text-success';
        dot.classList.add('animate-pulse');
    } else if (state === 'stopped') {
        pill.className += 'bg-danger/10 text-danger';
        dot.classList.remove('animate-pulse');
    } else {
        pill.className += 'bg-warning/10 text-warning';
        dot.classList.remove('animate-pulse');
    }
    $('status-text').textContent = state.charAt(0).toUpperCase() + state.slice(1);

    // Source status (combined retry + error in one block)
    const sourceStatus = $('source-status');
    const sourceRetry = $('source-retry');
    const sourceError = $('source-error');
    const hasSourceIssue = (data.encoder.source_retry_count > 0 && state !== 'stopped') ||
                          (data.encoder.last_error && state !== 'running');

    if (hasSourceIssue) {
        sourceStatus.classList.remove('hidden');
        if (data.encoder.source_retry_count > 0) {
            sourceRetry.textContent = 'Retry ' + data.encoder.source_retry_count + '/' + data.encoder.source_max_retries;
        } else {
            sourceRetry.textContent = 'Error';
        }
        sourceError.textContent = data.encoder.last_error || '';
        sourceError.classList.toggle('hidden', !data.encoder.last_error);
    } else {
        sourceStatus.classList.add('hidden');
    }

    // Reset VU meter if not running
    if (!running) {
        resetVuMeter();
    }

    // Outputs - track for later updates
    currentOutputs = data.outputs || [];
    $('output-count').textContent = currentOutputs.length;
    renderOutputs(currentOutputs, data.output_status || {});

    // Update audio devices dropdown (only if devices changed)
    if (data.devices) {
        updateAudioDevices(data.devices, data.settings?.audio_input);
    }
}

function renderOutputs(outputs, statuses) {
    const list = $('outputs-list');
    if (!outputs?.length) {
        list.innerHTML = '<p class="text-center py-8 text-gray-500 dark:text-gray-400">No outputs configured</p>';
        return;
    }
    list.innerHTML = outputs.map(o => {
        const status = statuses[o.id] || {};
        const isRetrying = status.retry_count > 0 && !status.given_up;
        const givenUp = status.given_up;
        const isConnected = status.running;

        // Status styling
        const statusBg = isConnected ? 'bg-success/10 text-success' : (givenUp ? 'bg-danger/10 text-danger' : 'bg-warning/10 text-warning');
        const statusDot = isConnected ? 'bg-success' : (givenUp ? 'bg-danger' : 'bg-warning');
        const statusText = isConnected ? 'Connected' : (givenUp ? 'Failed' : (isRetrying ? 'Retry ' + status.retry_count : 'Connecting'));

        return `
        <div class="py-3 border-b border-gray-100 dark:border-gray-700/50 last:border-b-0">
            <!-- Row 1: Status + Host + Delete -->
            <div class="flex items-center gap-3">
                <div class="w-2 h-2 rounded-full flex-shrink-0 ${statusDot} ${!isConnected && !givenUp ? 'animate-pulse' : ''}"></div>
                <span class="flex-1 font-medium text-gray-900 dark:text-white">${o.host}</span>
                <button onclick="deleteOutput('${o.id}')" class="p-1 text-gray-300 dark:text-gray-600 hover:text-red-500 dark:hover:text-red-400 transition-colors" title="Delete">
                    <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6L6 18M6 6l12 12"/></svg>
                </button>
            </div>
            <!-- Row 2: Details -->
            <div class="flex items-center gap-2 mt-1.5 ml-5 text-sm">
                <span class="text-gray-500 dark:text-gray-400">Port ${o.port}</span>
                <span class="text-gray-300 dark:text-gray-600">·</span>
                <span class="text-gray-500 dark:text-gray-400">${o.streamid}</span>
                <span class="text-gray-300 dark:text-gray-600">·</span>
                <span class="text-gray-500 dark:text-gray-400">${o.codec.toUpperCase()}</span>
                <span class="ml-auto px-2 py-0.5 text-xs font-medium rounded ${statusBg}">${statusText}</span>
            </div>
            ${(givenUp || isRetrying) && status.last_error ? `<p class="text-xs text-gray-400 dark:text-gray-500 mt-1.5 ml-5">${escapeHtml(status.last_error)}</p>` : ''}
        </div>
    `}).join('');
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Send command via WebSocket
function wsCommand(type, id, data) {
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type, id, data }));
    }
}

// Track current outputs for updates
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
    const port = parseInt($('input-port').value);
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
let peakHoldLeft = -60, peakHoldRight = -60;
let peakDecay = 0.3; // dB per update (faster decay)

function dbToPercent(db) {
    // Convert dB (-60 to 0) to percentage (0 to 100)
    return Math.max(0, Math.min(100, ((db + 60) / 60) * 100));
}

function updateLevelsFromData(levels) {
    // Update peak hold (decay slowly)
    peakHoldLeft = Math.max(levels.peak_left, peakHoldLeft - peakDecay);
    peakHoldRight = Math.max(levels.peak_right, peakHoldRight - peakDecay);

    // Update bars (cover shrinks from right as level increases)
    $('vu-left-cover').style.width = (100 - dbToPercent(levels.left)) + '%';
    $('vu-right-cover').style.width = (100 - dbToPercent(levels.right)) + '%';

    // Update peak indicators
    $('peak-left').style.left = dbToPercent(peakHoldLeft) + '%';
    $('peak-right').style.left = dbToPercent(peakHoldRight) + '%';

    // Update dB display
    $('db-left').textContent = levels.left.toFixed(1) + ' dB';
    $('db-right').textContent = levels.right.toFixed(1) + ' dB';
}

function resetVuMeter() {
    peakHoldLeft = -60;
    peakHoldRight = -60;
    $('vu-left-cover').style.width = '100%';
    $('vu-right-cover').style.width = '100%';
    $('peak-left').style.left = '0%';
    $('peak-right').style.left = '0%';
    $('db-left').textContent = '-60 dB';
    $('db-right').textContent = '-60 dB';
}

// Audio Input
let currentAudioInput = '';

function updateAudioDevices(devices, selectedInput) {
    const select = $('audio-input');

    // Only update if selection changed (avoid resetting during user interaction)
    if (selectedInput && selectedInput !== currentAudioInput) {
        currentAudioInput = selectedInput;
    }

    // Only rebuild dropdown if empty (first load)
    if (select.options.length === 0) {
        if (!devices || devices.length === 0) {
            select.innerHTML = '<option value="">No devices found</option>';
            return;
        }

        devices.forEach(device => {
            const option = document.createElement('option');
            option.value = device.id;
            option.textContent = device.name;
            if (device.id === currentAudioInput) {
                option.selected = true;
            }
            select.appendChild(option);
        });
    }
}

function updateAudioInput(deviceId) {
    wsCommand('update_settings', null, { audio_input: deviceId });
}

// WebSocket connection
let ws = null;

function showConnecting() {
    const pill = $('status-pill');
    const dot = $('status-dot');
    pill.className = 'flex items-center gap-2 px-3 py-1.5 rounded-full text-sm font-semibold bg-gray-200 dark:bg-gray-700 text-gray-500 dark:text-gray-400';
    dot.classList.add('animate-pulse');
    $('status-text').textContent = 'Connecting';
    resetVuMeter();
}

function connectWebSocket() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(protocol + '//' + location.host + '/ws');

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
        // Reconnect after 1 second
        setTimeout(connectWebSocket, 1000);
    };

    ws.onerror = () => ws.close();
}

// Event listeners
$('add-btn').onclick = showModal;
$('cancel-btn').onclick = hideModal;
$('save-btn').onclick = addOutput;
document.querySelector('.modal-overlay').onclick = hideModal;

// Init
connectWebSocket();

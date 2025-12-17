/**
 * ZuidWest FM Encoder - Alpine.js Web Application
 *
 * Real-time audio monitoring, encoder control, and multi-output stream
 * management via WebSocket connection to Go backend.
 *
 * Architecture:
 *   - Single Alpine.js component (encoderApp) manages all UI state
 *   - WebSocket connection at /ws for bidirectional communication
 *   - Three views: dashboard (monitoring), settings (config), add-output (form)
 *
 * WebSocket Message Types (incoming):
 *   - levels: Audio RMS/peak levels, ~4 updates per second
 *   - status: Encoder state, outputs, devices, settings (every 3s)
 *   - test_result: Unified notification test result with test_type field
 *
 * WebSocket Commands (outgoing):
 *   - start/stop: Control encoder
 *   - update_settings: Persist configuration changes
 *   - add_output/remove_output: Manage stream outputs
 *   - test_<type>: Trigger notification test (webhook, log, email)
 *
 * Dependencies:
 *   - Alpine.js 3.x (loaded before this script)
 *   - icons.js (window.icons object for SVG rendering)
 *
 * @see index.html for markup structure
 * @see icons.js for SVG icon definitions
 */

// === Constants ===
const DB_MINIMUM = -60;           // Minimum dB level for VU meter range
const DB_RANGE = 60;              // dB range (0 to -60)
const CLIP_TIMEOUT_MS = 1500;     // Peak hold / clip indicator timeout
const WS_RECONNECT_MS = 1000;     // WebSocket reconnection delay
const EMAIL_FEEDBACK_MS = 2000;   // Email test result display duration

/**
 * Converts decibel value to percentage for VU meter display.
 * Maps -60dB to 0% and 0dB to 100%.
 *
 * @param {number} db - Decibel value (typically -60 to 0)
 * @returns {number} Percentage value (0-100), clamped to valid range
 */
window.dbToPercent = (db) => Math.max(0, Math.min(100, (db - DB_MINIMUM) / DB_RANGE * 100));

// Default values for new outputs
const DEFAULT_OUTPUT = {
    host: '',
    port: 8080,
    streamid: '',
    password: '',
    codec: 'mp3',
    max_retries: 99
};

const DEFAULT_LEVELS = {
    left: -60,
    right: -60,
    peak_left: -60,
    peak_right: -60,
    silence_level: null
};

// Settings field mapping for WebSocket status sync
const SETTINGS_MAP = [
    { msgKey: 'silence_threshold', path: 'silenceThreshold', default: -40 },
    { msgKey: 'silence_duration', path: 'silenceDuration', default: 15 },
    { msgKey: 'silence_recovery', path: 'silenceRecovery', default: 5 },
    { msgKey: 'silence_webhook', path: 'silenceWebhook', default: '' },
    { msgKey: 'silence_log_path', path: 'silenceLogPath', default: '' },
    { msgKey: 'email_smtp_host', path: 'email.host', default: '' },
    { msgKey: 'email_smtp_port', path: 'email.port', default: 587 },
    { msgKey: 'email_username', path: 'email.username', default: '' },
    { msgKey: 'email_recipients', path: 'email.recipients', default: '' }
];

/**
 * Sets a nested property value using dot-notation path.
 *
 * @param {Object} obj - Target object to modify
 * @param {string} path - Dot-notation path (e.g., 'email.host')
 * @param {*} value - Value to set
 */
function setNestedValue(obj, path, value) {
    const keys = path.split('.');
    let current = obj;
    for (let i = 0; i < keys.length - 1; i++) {
        if (!Object.hasOwn(current, keys[i])) return;
        current = current[keys[i]];
    }
    const finalKey = keys[keys.length - 1];
    if (!Object.hasOwn(current, finalKey)) return;
    current[finalKey] = value;
}

document.addEventListener('alpine:init', () => {
    Alpine.data('encoderApp', () => ({
        // View state: 'dashboard', 'settings', 'add-output'
        view: 'dashboard',
        settingsTab: 'audio',

        // VU meter channel definitions
        vuChannels: [
            { label: 'L', level: 'left', peak: 'peak_left' },
            { label: 'R', level: 'right', peak: 'peak_right' }
        ],

        // Settings tab definitions
        settingsTabs: [
            { id: 'audio', label: 'Audio', icon: 'audio' },
            { id: 'alerts', label: 'Alerts', icon: 'bell' },
            { id: 'about', label: 'About', icon: 'info' }
        ],

        // New output form data
        newOutput: { ...DEFAULT_OUTPUT },

        // Encoder state
        encoder: {
            state: 'connecting',
            uptime: '',
            sourceRetryCount: 0,
            sourceMaxRetries: 10,
            lastError: ''
        },

        // Outputs
        outputs: [],
        outputStatuses: {},
        deletingOutputs: {},

        // Audio
        devices: [],
        levels: { ...DEFAULT_LEVELS },
        vuMode: localStorage.getItem('vuMode') || 'peak',
        clipActive: false,
        clipTimeout: null,

        // Settings
        settings: {
            audioInput: '',
            silenceThreshold: -40,
            silenceDuration: 15,
            silenceRecovery: 5,
            silenceWebhook: '',
            silenceLogPath: '',
            email: { host: '', port: 587, username: '', password: '', recipients: '' },
            platform: ''
        },
        originalSettings: null,
        settingsDirty: false,

        // Version
        version: { current: '', latest: '', updateAvail: false, commit: '', build_time: '' },

        // Notification test state (unified object for all test types)
        testStates: {
            webhook: { pending: false, text: 'Test' },
            log: { pending: false, text: 'Test' },
            email: { pending: false, text: 'Test' }
        },

        // Silence log modal state
        silenceLogModal: {
            visible: false,
            loading: false,
            entries: [],
            path: '',
            error: ''
        },

        // WebSocket
        ws: null,

        // Computed properties
        /**
         * Computes CSS class for status pill based on encoder state.
         * @returns {string} CSS class: 'running', 'stopped', 'connecting', or 'starting'
         */
        get statusPillClass() {
            const s = this.encoder.state;
            if (s === 'running') return 'state-success';
            if (s === 'stopped') return 'state-danger';
            return 'state-warning';
        },

        /**
         * Checks if audio source has issues (no device or capture error).
         * @returns {boolean} True if source needs attention
         */
        get hasSourceIssue() {
            return (this.encoder.sourceRetryCount > 0 && this.encoder.state !== 'stopped') ||
                   (this.encoder.lastError && this.encoder.state !== 'running');
        },

        /**
         * Checks if encoder is actively running.
         * @returns {boolean} True if status is 'running'
         */
        get encoderRunning() {
            return this.encoder.state === 'running';
        },

        // Lifecycle
        /**
         * Alpine.js lifecycle hook - initializes WebSocket connection.
         * Called automatically when component mounts.
         */
        init() {
            this.connectWebSocket();
        },

        // WebSocket
        /**
         * Establishes WebSocket connection to backend.
         * Handles incoming messages by type and auto-reconnects on close.
         * Reconnection uses WS_RECONNECT_MS delay to prevent rapid retries.
         */
        connectWebSocket() {
            const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
            this.ws = new WebSocket(`${protocol}//${location.host}/ws`);

            this.ws.onmessage = (e) => {
                const msg = JSON.parse(e.data);
                if (msg.type === 'levels') {
                    this.handleLevels(msg.levels);
                } else if (msg.type === 'status') {
                    this.handleStatus(msg);
                } else if (msg.type === 'test_result') {
                    this.handleTestResult(msg);
                } else if (msg.type === 'silence_log_result') {
                    this.handleSilenceLogResult(msg);
                }
            };

            this.ws.onclose = () => {
                this.encoder.state = 'connecting';
                this.resetVuMeter();
                setTimeout(() => this.connectWebSocket(), WS_RECONNECT_MS);
            };

            this.ws.onerror = () => this.ws.close();
        },

        /**
         * Sends command to backend via WebSocket.
         *
         * @param {string} type - Command type (start, stop, update_settings, etc.)
         * @param {string} [id] - Optional output ID for output-specific commands
         * @param {Object} [data] - Optional payload data
         */
        send(type, id, data) {
            if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                this.ws.send(JSON.stringify({ type: type, id: id, data: data }));
            }
        },

        /**
         * Processes incoming audio level data.
         * Updates VU meter display and manages clip detection state.
         * Clip indicator activates when levels exceed threshold and holds
         * for CLIP_TIMEOUT_MS before auto-clearing.
         *
         * @param {Object} levels - Audio levels (left, right, peak_left, peak_right, silence_level)
         */
        handleLevels(levels) {
            this.levels = levels;
            const totalClips = (levels.clip_left || 0) + (levels.clip_right || 0);
            if (totalClips > 0) {
                this.clipActive = true;
                clearTimeout(this.clipTimeout);
                this.clipTimeout = setTimeout(() => { this.clipActive = false; }, CLIP_TIMEOUT_MS);
            }
        },

        /**
         * Processes encoder status updates from backend.
         * Updates encoder state, output statuses, available devices, and settings.
         * Settings sync is skipped when user is on settings view to prevent
         * overwriting in-progress edits.
         *
         * @param {Object} msg - Status message with state, outputs, devices, settings
         */
        handleStatus(msg) {
            // Encoder state
            this.encoder.state = msg.encoder.state;
            this.encoder.uptime = msg.encoder.uptime || '';
            this.encoder.sourceRetryCount = msg.encoder.source_retry_count || 0;
            this.encoder.sourceMaxRetries = msg.encoder.source_max_retries || 10;
            this.encoder.lastError = msg.encoder.last_error || '';

            if (!this.encoderRunning) {
                this.resetVuMeter();
            }

            // Outputs
            this.outputs = msg.outputs || [];
            this.outputStatuses = msg.output_status || {};

            // Clean up deletingOutputs
            for (const id in this.deletingOutputs) {
                const output = this.outputs.find(o => o.id === id);
                if (!output || output.created_at !== this.deletingOutputs[id]) {
                    delete this.deletingOutputs[id];
                }
            }

            // Devices
            if (msg.devices) {
                this.devices = msg.devices;
            }

            // Only update settings from status when not on settings view to prevent
            // overwriting user input while editing
            if (this.view !== 'settings') {
                if (msg.settings?.audio_input) {
                    this.settings.audioInput = msg.settings.audio_input;
                }
                // Sync remaining settings from status message
                for (const field of SETTINGS_MAP) {
                    if (msg[field.msgKey] !== undefined) {
                        setNestedValue(this.settings, field.path, msg[field.msgKey] || field.default);
                    }
                }
                if (msg.settings?.platform !== undefined) {
                    this.settings.platform = msg.settings.platform;
                }
            }

            // Version
            if (msg.version) {
                this.version = msg.version;
            }
        },

        /**
         * Handles notification test result from backend.
         * Updates UI feedback and auto-clears after EMAIL_FEEDBACK_MS.
         *
         * @param {Object} msg - Result with test_type, success, and optional error
         */
        handleTestResult(msg) {
            const type = msg.test_type;
            if (!Object.hasOwn(this.testStates, type)) return;

            this.testStates[type].pending = false;
            this.testStates[type].text = msg.success ? 'Sent!' : 'Failed';
            if (!msg.success) alert(`${type} test failed: ${msg.error || 'Unknown error'}`);
            setTimeout(() => { this.testStates[type].text = 'Test'; }, EMAIL_FEEDBACK_MS);
        },

        // Navigation
        /**
         * Returns to dashboard view and clears settings state.
         */
        showDashboard() {
            this.view = 'dashboard';
            this.settingsDirty = false;
            this.originalSettings = null;
        },

        /**
         * Navigates to settings view and captures current settings snapshot.
         * Snapshot enables cancel/restore functionality.
         */
        showSettings() {
            // Save a copy of current settings to allow cancel
            this.originalSettings = JSON.parse(JSON.stringify(this.settings));
            this.settingsDirty = false;
            this.view = 'settings';
        },

        /**
         * Marks settings as modified, enabling Save button.
         * Called on any settings input change.
         */
        markSettingsDirty() {
            this.settingsDirty = true;
        },

        /**
         * Reverts settings to snapshot taken when entering settings view.
         * Returns to dashboard without saving changes.
         */
        cancelSettings() {
            // Restore original settings
            if (this.originalSettings) {
                this.settings = JSON.parse(JSON.stringify(this.originalSettings));
            }
            this.showDashboard();
        },

        /**
         * Persists all settings to backend via WebSocket.
         * Builds payload with all current values, only including password
         * if it was modified (non-empty). Resets dirty state on send.
         */
        saveSettings() {
            // Build update payload with all settings
            const update = {
                silence_threshold: this.settings.silenceThreshold,
                silence_duration: this.settings.silenceDuration,
                silence_recovery: this.settings.silenceRecovery,
                silence_webhook: this.settings.silenceWebhook,
                silence_log_path: this.settings.silenceLogPath,
                email_smtp_host: this.settings.email.host,
                email_smtp_port: this.settings.email.port,
                email_username: this.settings.email.username,
                email_recipients: this.settings.email.recipients
            };
            // Only include password if it was changed
            if (this.settings.email.password) {
                update.email_password = this.settings.email.password;
            }
            this.send('update_settings', null, update);
            this.showDashboard();
        },

        /**
         * Opens add output form with default values.
         */
        showAddOutput() {
            this.newOutput = { ...DEFAULT_OUTPUT };
            this.view = 'add-output';
        },

        /**
         * Switches active settings tab.
         * @param {string} tabId - Tab identifier (audio, alerts, about)
         */
        showTab(tabId) {
            this.settingsTab = tabId;
        },

        // Output management
        /**
         * Validates and submits new output configuration.
         * Requires host and port; other fields have defaults.
         */
        submitNewOutput() {
            if (!this.newOutput.host) {
                return;
            }
            this.send('add_output', null, {
                host: this.newOutput.host.trim(),
                port: this.newOutput.port,
                streamid: this.newOutput.streamid.trim() || 'studio',
                password: this.newOutput.password,
                codec: this.newOutput.codec,
                max_retries: this.newOutput.max_retries
            });
            this.view = 'dashboard';
        },

        /**
         * Initiates output deletion with optimistic UI update.
         * Tracks deletion state to prevent double-clicks.
         * @param {string} id - Output ID to delete
         */
        deleteOutput(id) {
            if (!confirm('Delete this output?')) return;
            const output = this.outputs.find(o => o.id === id);
            if (output) this.deletingOutputs[id] = output.created_at;
            this.send('delete_output', id, null);
        },

        /**
         * Gets output status and deletion state.
         *
         * @param {Object} output - Output object with id and created_at
         * @returns {Object} Object with status and isDeleting properties
         */
        getOutputStatus(output) {
            return {
                status: this.outputStatuses[output.id] || {},
                isDeleting: this.deletingOutputs[output.id] === output.created_at
            };
        },

        /**
         * Determines CSS state class for output status indicator.
         * Priority: deleting > encoder stopped > failed > retrying > connected.
         *
         * @param {Object} output - Output configuration object
         * @returns {string} CSS class for state styling
         */
        getOutputStateClass(output) {
            const { status, isDeleting } = this.getOutputStatus(output);
            if (isDeleting) return 'state-warning';
            if (status.stable) return 'state-success';
            if (status.given_up) return 'state-danger';
            if (status.retry_count > 0) return 'state-warning';
            if (status.running) return 'state-warning';
            if (!this.encoderRunning) return 'state-stopped';
            return 'state-warning';
        },

        /**
         * Generates human-readable status text for output.
         *
         * @param {Object} output - Output configuration object
         * @returns {string} Status text (e.g., 'Connected', 'Retry 2/5')
         */
        getOutputStatusText(output) {
            const { status, isDeleting } = this.getOutputStatus(output);
            if (isDeleting) return 'Stopping...';
            if (status.stable) return 'Connected';
            if (status.given_up) return 'Failed';
            if (status.retry_count > 0) return `Retry ${status.retry_count}/${status.max_retries}`;
            if (status.running) return 'Connecting...';
            if (!this.encoderRunning) return 'Offline';
            return 'Connecting...';
        },

        /**
         * Determines if error message should be shown for output.
         * Shows error when output has failed state with error message.
         *
         * @param {Object} output - Output configuration object
         * @returns {boolean} True if error should be displayed
         */
        shouldShowError(output) {
            const { status, isDeleting } = this.getOutputStatus(output);
            return !isDeleting && (status.given_up || status.retry_count > 0) && status.last_error;
        },

        // VU Meter
        /**
         * Toggles VU meter display mode between peak and RMS.
         */
        toggleVuMode() {
            this.vuMode = this.vuMode === 'peak' ? 'rms' : 'peak';
            localStorage.setItem('vuMode', this.vuMode);
        },

        /**
         * Resets VU meter to default zero state.
         * Called when encoder stops or on initialization.
         */
        resetVuMeter() {
            this.levels = { ...DEFAULT_LEVELS };
        },

        // Notification Tests
        /**
         * Triggers a notification test via WebSocket.
         * Temporarily disables button and shows testing state.
         *
         * @param {string} type - Test type: 'webhook', 'log', or 'email'
         */
        sendTest(type) {
            if (!this.testStates[type]) return;
            this.testStates[type].pending = true;
            this.testStates[type].text = 'Testing...';
            this.send(`test_${type}`, null, null);
        },

        // Silence Log Modal
        /**
         * Handles silence log view result from backend.
         * Updates modal state with log entries or error message.
         *
         * @param {Object} msg - Result with success, entries[], path, error
         */
        handleSilenceLogResult(msg) {
            this.silenceLogModal.loading = false;
            if (msg.success) {
                this.silenceLogModal.entries = msg.entries || [];
                this.silenceLogModal.path = msg.path || '';
                this.silenceLogModal.error = '';
            } else {
                this.silenceLogModal.entries = [];
                this.silenceLogModal.error = msg.error || 'Unknown error';
            }
        },

        /**
         * Opens the silence log modal and fetches log entries.
         */
        viewSilenceLog() {
            this.silenceLogModal.visible = true;
            this.silenceLogModal.loading = true;
            this.silenceLogModal.entries = [];
            this.silenceLogModal.error = '';
            this.send('view_silence_log', null, null);
        },

        /**
         * Closes the silence log modal.
         */
        closeSilenceLog() {
            this.silenceLogModal.visible = false;
        },

        /**
         * Refreshes the silence log entries.
         */
        refreshSilenceLog() {
            this.silenceLogModal.loading = true;
            this.send('view_silence_log', null, null);
        },

        /**
         * Formats duration in human-readable format.
         * @param {number} seconds - Duration in seconds
         * @returns {string} Formatted duration (e.g., "1m 6s" or "45s")
         */
        formatDuration(seconds) {
            if (seconds < 60) return `${Math.round(seconds)}s`;
            const mins = Math.floor(seconds / 60);
            const secs = Math.round(seconds % 60);
            return secs > 0 ? `${mins}m ${secs}s` : `${mins}m`;
        },

        /**
         * Formats a silence log entry for display.
         * For "ended" events, duration is the key metric (total silence time).
         * For "started" events, duration is just detection delay (not shown).
         * @param {Object} entry - Log entry with timestamp, event, duration_sec, threshold_db
         * @returns {Object} Formatted entry with human-readable values
         */
        formatLogEntry(entry) {
            const date = new Date(entry.timestamp);
            const isEnd = entry.event === 'silence_end';
            const isStart = entry.event === 'silence_start';
            const isTest = entry.event === 'test';

            // For ended events, show duration prominently in the event name
            let eventText = 'Unknown Event';
            if (isEnd) {
                const dur = entry.duration_sec > 0 ? this.formatDuration(entry.duration_sec) : '';
                eventText = dur ? `Silence Ended · ${dur}` : 'Silence Ended';
            } else if (isStart) {
                eventText = 'Silence Detected';
            } else if (isTest) {
                eventText = 'Test Entry';
            }

            return {
                time: date.toLocaleString(),
                event: eventText,
                eventClass: isStart ? 'log-silence' : isEnd ? 'log-recovery' : 'log-test',
                // Only show threshold, duration is now in the event name for ended events
                threshold: `${entry.threshold_db.toFixed(0)} dB`
            };
        }
    }));
});

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
 *   - command_result: Command success/error feedback
 *   - silence_log_result: Silence log entries response
 *
 * WebSocket Commands (outgoing):
 *   - update_settings: Persist configuration changes
 *   - add_output/delete_output: Manage stream outputs
 *   - add_recording/delete_recording: Manage compliance recordings
 *   - start_recording/stop_recording: Control manual recordings
 *   - test_<type>: Trigger notification test (webhook, log, email)
 *   - view_silence_log: Request silence log entries
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

// Default values for new recordings
const DEFAULT_RECORDING = {
    name: '',
    path: '/var/lib/encoder/recordings',
    codec: 'mp3',
    mode: 'auto',
    retention_days: 30
};

// Settings field mapping for WebSocket status sync
// All settings now come from msg.settings object
const SETTINGS_MAP = [
    { srcKey: 'silence_threshold', path: 'silenceThreshold', default: -40 },
    { srcKey: 'silence_duration', path: 'silenceDuration', default: 15 },
    { srcKey: 'silence_recovery', path: 'silenceRecovery', default: 5 },
    { srcKey: 'silence_webhook', path: 'silenceWebhook', default: '' },
    { srcKey: 'silence_log_path', path: 'silenceLogPath', default: '' },
    { srcKey: 'email_smtp_host', path: 'email.host', default: '' },
    { srcKey: 'email_smtp_port', path: 'email.port', default: 587 },
    { srcKey: 'email_username', path: 'email.username', default: '' },
    { srcKey: 'email_recipients', path: 'email.recipients', default: '' }
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
        // View state: 'dashboard', 'settings', 'add-output', 'add-recording'
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

        // New recording form data
        newRecording: { ...DEFAULT_RECORDING },

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
        previousOutputStatuses: {},
        deletingOutputs: {},

        // Recordings
        recordings: [],
        recordingStatuses: {},
        deletingRecordings: {},

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

        // Alert banner state
        banner: {
            visible: false,
            message: '',
            type: 'info', // info, warning, danger
            persistent: false
        },

        // WebSocket
        ws: null,

        // Computed properties
        /**
         * Computes CSS class for status pill based on encoder state.
         * @returns {string} State class: 'state-success', 'state-danger', or 'state-warning'
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
                } else if (msg.type === 'command_result') {
                    this.handleCommandResult(msg);
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
            const prevSilenceClass = this.getSilenceClass();
            this.levels = levels;
            const newSilenceClass = this.getSilenceClass();

            // Handle silence state transitions
            if (newSilenceClass !== prevSilenceClass) {
                this.handleSilenceTransition(prevSilenceClass, newSilenceClass);
            }

            // Update banner message with current duration if silence banner is showing
            if (this.banner.visible && this.banner.type !== 'info' && levels.silence_duration) {
                const duration = this.formatDuration(levels.silence_duration);
                if (newSilenceClass === 'critical') {
                    this.banner.message = `Silence detected: ${duration}`;
                } else if (newSilenceClass === 'warning') {
                    this.banner.message = `Silence warning: ${duration}`;
                }
            }

            const totalClips = (levels.clip_left || 0) + (levels.clip_right || 0);
            if (totalClips > 0) {
                this.clipActive = true;
                clearTimeout(this.clipTimeout);
                this.clipTimeout = setTimeout(() => { this.clipActive = false; }, CLIP_TIMEOUT_MS);
            }
        },

        /**
         * Handles silence state transitions and shows appropriate banners.
         * @param {string} prev - Previous silence class
         * @param {string} next - New silence class
         */
        handleSilenceTransition(prev, next) {
            const duration = this.formatDuration(this.levels.silence_duration || 0);
            if (next === 'warning' && prev === 'active') {
                this.showBanner(`Silence warning: ${duration}`, 'warning', false);
            } else if (next === 'critical') {
                this.showBanner(`Silence detected: ${duration}`, 'danger', true);
            } else if (next === '' && prev !== '') {
                // Silence recovered
                this.hideBanner();
            }
        },

        /**
         * Returns escalating CSS class based on silence duration.
         * Thresholds are based on configured silenceDuration:
         * - active: silenceDuration (alert triggered)
         * - warning: silenceDuration * 2
         * - critical: silenceDuration * 4
         * @returns {string} CSS class: '' | 'active' | 'warning' | 'critical'
         */
        getSilenceClass() {
            if (!this.levels.silence_level) return '';
            const duration = this.levels.silence_duration || 0;
            const threshold = this.settings.silenceDuration || 15;
            if (duration >= threshold * 4) return 'critical';
            if (duration >= threshold * 2) return 'warning';
            return 'active';
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
            const newOutputStatuses = msg.output_status || {};

            // Detect status transitions to "connected" and trigger animation
            for (const id in newOutputStatuses) {
                const oldStatus = this.previousOutputStatuses[id] || {};
                const newStatus = newOutputStatuses[id] || {};

                // Check if status just became stable (connected)
                const wasNotConnected = !oldStatus.stable;
                const isNowConnected = newStatus.stable;

                if (wasNotConnected && isNowConnected) {
                    // Trigger animation by temporarily adding class
                    this.$nextTick(() => {
                        const dotElement = document.querySelector(`[data-output-id="${id}"] .output__dot`);
                        if (dotElement) {
                            dotElement.classList.add('output__dot--just-connected');
                            setTimeout(() => {
                                dotElement.classList.remove('output__dot--just-connected');
                            }, 400);
                        }
                    });
                }
            }

            // Update status tracking
            this.previousOutputStatuses = JSON.parse(JSON.stringify(newOutputStatuses));
            this.outputStatuses = newOutputStatuses;

            // Clean up deletingOutputs
            for (const id in this.deletingOutputs) {
                const output = this.outputs.find(o => o.id === id);
                if (!output || output.created_at !== this.deletingOutputs[id]) {
                    delete this.deletingOutputs[id];
                }
            }

            // Recordings
            this.recordings = msg.recordings || [];
            this.recordingStatuses = msg.recording_status || {};

            // Clean up deletingRecordings
            for (const id in this.deletingRecordings) {
                const recording = this.recordings.find(r => r.id === id);
                if (!recording || recording.created_at !== this.deletingRecordings[id]) {
                    delete this.deletingRecordings[id];
                }
            }

            // Devices
            if (msg.devices) {
                this.devices = msg.devices;
            }

            // Only update settings from status when not on settings view to prevent
            // overwriting user input while editing
            if (this.view !== 'settings' && msg.settings) {
                // All settings now come from msg.settings object
                if (msg.settings.audio_input) {
                    this.settings.audioInput = msg.settings.audio_input;
                }
                if (msg.settings.platform !== undefined) {
                    this.settings.platform = msg.settings.platform;
                }
                // Sync remaining settings from msg.settings
                for (const field of SETTINGS_MAP) {
                    if (msg.settings[field.srcKey] !== undefined) {
                        setNestedValue(this.settings, field.path, msg.settings[field.srcKey] ?? field.default);
                    }
                }
            }

            // Version
            if (msg.version) {
                const wasUpdateAvail = this.version.updateAvail;
                this.version = msg.version;
                // Show banner once when update becomes available
                if (msg.version.updateAvail && !wasUpdateAvail) {
                    this.showBanner(`Update beschikbaar: ${msg.version.latest}`, 'info', false);
                }
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

        /**
         * Handles command result feedback from backend.
         * Shows error alert for failed commands.
         *
         * @param {Object} msg - Result with command, success, and optional error
         */
        handleCommandResult(msg) {
            if (!msg.success && msg.error) {
                alert(`${msg.command} failed: ${msg.error}`);
            }
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
         * Shows success state on save button, then navigates to dashboard.
         */
        saveAndClose() {
            const viewId = this.view === 'settings' ? 'settings-view' : 'add-output-view';
            const saveBtn = document.querySelector(`#${viewId} .nav-btn--save`);
            if (saveBtn) {
                saveBtn.classList.add('nav-btn--saved');
                setTimeout(() => {
                    saveBtn.classList.remove('nav-btn--saved');
                    this.showDashboard();
                }, 600);
            } else {
                this.showDashboard();
            }
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
            this.saveAndClose();
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
            this.saveAndClose();
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

        // Recording management
        /**
         * Opens add recording form with default values.
         */
        showAddRecording() {
            this.newRecording = { ...DEFAULT_RECORDING };
            this.view = 'add-recording';
        },

        /**
         * Validates and submits new recording configuration.
         * Requires name and path; other fields have defaults.
         */
        submitNewRecording() {
            if (!this.newRecording.name || !this.newRecording.path) {
                return;
            }
            this.send('add_recording', null, {
                name: this.newRecording.name.trim(),
                path: this.newRecording.path.trim(),
                codec: this.newRecording.codec,
                mode: this.newRecording.mode,
                retention_days: this.newRecording.retention_days
            });
            this.saveAndClose();
        },

        /**
         * Toggles a manual recording on/off.
         * @param {string} id - Recording ID to toggle
         */
        toggleRecording(id) {
            const status = this.recordingStatuses[id] || {};
            if (status.running) {
                this.send('stop_recording', id, null);
            } else {
                this.send('start_recording', id, null);
            }
        },

        /**
         * Initiates recording deletion with optimistic UI update.
         * Tracks deletion state to prevent double-clicks.
         * @param {string} id - Recording ID to delete
         */
        deleteRecording(id) {
            if (!confirm('Delete this recording?')) return;
            const recording = this.recordings.find(r => r.id === id);
            if (recording) this.deletingRecordings[id] = recording.created_at;
            this.send('delete_recording', id, null);
        },

        /**
         * Gets recording status and deletion state.
         *
         * @param {Object} recording - Recording object with id and created_at
         * @returns {Object} Object with status and isDeleting properties
         */
        getRecordingStatus(recording) {
            if (!recording || !recording.id) {
                return { status: {}, isDeleting: false };
            }
            return {
                status: this.recordingStatuses[recording.id] || {},
                isDeleting: this.deletingRecordings[recording.id] === recording.created_at
            };
        },

        /**
         * Determines CSS state class for recording status indicator.
         *
         * @param {Object} recording - Recording configuration object
         * @returns {string} CSS class for state styling
         */
        getRecordingStateClass(recording) {
            if (!recording || !recording.id) return 'state-stopped';
            const { status, isDeleting } = this.getRecordingStatus(recording);
            if (isDeleting) return 'state-warning';
            if (status.running) return 'state-success';
            if (status.last_error) return 'state-danger';
            if (!this.encoderRunning) return 'state-stopped';
            // Manual recordings show neutral state when idle
            if ((recording.mode || 'auto') === 'manual') return 'state-stopped';
            return 'state-warning';
        },

        /**
         * Generates human-readable status text for recording.
         *
         * @param {Object} recording - Recording configuration object
         * @returns {string} Status text (e.g., 'Recording', 'Error', 'Idle')
         */
        getRecordingStatusText(recording) {
            if (!recording || !recording.id) return 'Unknown';
            const { status, isDeleting } = this.getRecordingStatus(recording);
            if (isDeleting) return 'Stopping...';
            if (status.running) return 'Recording';
            if (status.last_error) return 'Error';
            if (!this.encoderRunning) return 'Offline';
            // Manual recordings show Idle when not running
            if ((recording.mode || 'auto') === 'manual') return 'Idle';
            return 'Starting...';
        },

        /**
         * Determines if error message should be shown for recording.
         *
         * @param {Object} recording - Recording configuration object
         * @returns {boolean} True if error should be displayed
         */
        shouldShowRecordingError(recording) {
            if (!recording || !recording.id) return false;
            const { status, isDeleting } = this.getRecordingStatus(recording);
            return !isDeleting && status.last_error;
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

        // Alert banner notifications
        /**
         * Shows an alert banner notification.
         * @param {string} message - Message to display
         * @param {string} type - Banner type: 'info', 'warning', 'danger'
         * @param {boolean} persistent - If true, banner stays until dismissed
         */
        showBanner(message, type = 'info', persistent = false) {
            this.banner = { visible: true, message, type, persistent };
            if (!persistent) {
                setTimeout(() => this.hideBanner(), 10000);
            }
        },

        /**
         * Hides the current alert banner.
         */
        hideBanner() {
            this.banner.visible = false;
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
                eventClass: isStart ? 'log__entry--silence' : isEnd ? 'log__entry--recovery' : 'log__entry--test',
                // Only show threshold, duration is now in the event name for ended events
                threshold: `${entry.threshold_db.toFixed(0)} dB`
            };
        }
    }));
});

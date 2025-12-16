// Utility function - exposed globally for Alpine template expressions
window.dbToPercent = (db) => Math.max(0, Math.min(100, ((db + 60) / 60) * 100));

document.addEventListener('alpine:init', () => {
    Alpine.data('encoderApp', () => ({
        // View state: 'dashboard', 'settings', 'add-output'
        view: 'dashboard',
        settingsTab: 'audio',

        // New output form data
        newOutput: {
            host: '',
            port: 8080,
            streamid: '',
            password: '',
            codec: 'mp3',
            max_retries: 99
        },

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
        levels: { left: -60, right: -60, peak_left: -60, peak_right: -60, silence_level: null },
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
            email: { host: '', port: 587, username: '', password: '', recipients: '' },
            platform: ''
        },

        // Version
        version: { current: '', latest: '', updateAvail: false, commit: '' },

        // Email test state
        emailTestPending: false,
        emailTestText: 'Send Test Email',

        // WebSocket
        ws: null,

        // Computed properties
        get statusPillClass() {
            const s = this.encoder.state;
            if (s === 'running') return 'state-success';
            if (s === 'stopped') return 'state-danger';
            return 'state-warning';
        },

        get hasSourceIssue() {
            return (this.encoder.sourceRetryCount > 0 && this.encoder.state !== 'stopped') ||
                   (this.encoder.lastError && this.encoder.state !== 'running');
        },

        get encoderRunning() {
            return this.encoder.state === 'running';
        },

        // Lifecycle
        init() {
            this.connectWebSocket();
        },

        // WebSocket
        connectWebSocket() {
            const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
            this.ws = new WebSocket(`${protocol}//${location.host}/ws`);

            this.ws.onmessage = (e) => {
                const msg = JSON.parse(e.data);
                if (msg.type === 'levels') {
                    this.handleLevels(msg.levels);
                } else if (msg.type === 'status') {
                    this.handleStatus(msg);
                } else if (msg.type === 'test_email_result') {
                    this.handleTestEmailResult(msg);
                }
            };

            this.ws.onclose = () => {
                this.encoder.state = 'connecting';
                this.resetVuMeter();
                setTimeout(() => this.connectWebSocket(), 1000);
            };

            this.ws.onerror = () => this.ws.close();
        },

        send(type, id, data) {
            if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                this.ws.send(JSON.stringify({ type: type, id: id, data: data }));
            }
        },

        handleLevels(levels) {
            this.levels = levels;
            const totalClips = (levels.clip_left || 0) + (levels.clip_right || 0);
            if (totalClips > 0) {
                this.clipActive = true;
                clearTimeout(this.clipTimeout);
                this.clipTimeout = setTimeout(() => { this.clipActive = false; }, 1500);
            }
        },

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
            if (msg.settings?.audio_input) {
                this.settings.audioInput = msg.settings.audio_input;
            }

            // Settings from status
            if (msg.silence_threshold !== undefined) {
                this.settings.silenceThreshold = msg.silence_threshold;
            }
            if (msg.silence_duration !== undefined) {
                this.settings.silenceDuration = msg.silence_duration;
            }
            if (msg.silence_recovery !== undefined) {
                this.settings.silenceRecovery = msg.silence_recovery;
            }
            if (msg.silence_webhook !== undefined) {
                this.settings.silenceWebhook = msg.silence_webhook || '';
            }
            if (msg.email_smtp_host !== undefined) {
                this.settings.email.host = msg.email_smtp_host || '';
            }
            if (msg.email_smtp_port !== undefined) {
                this.settings.email.port = msg.email_smtp_port || 587;
            }
            if (msg.email_username !== undefined) {
                this.settings.email.username = msg.email_username || '';
            }
            if (msg.email_recipients !== undefined) {
                this.settings.email.recipients = msg.email_recipients || '';
            }
            if (msg.settings && msg.settings.platform !== undefined) {
                this.settings.platform = msg.settings.platform;
            }

            // Version
            if (msg.version) {
                this.version = msg.version;
            }
        },

        handleTestEmailResult(msg) {
            this.emailTestPending = false;
            this.emailTestText = msg.success ? 'Sent!' : 'Failed';
            if (!msg.success) alert(`Test email failed: ${msg.error || 'Unknown error'}`);
            setTimeout(() => { this.emailTestText = 'Send Test Email'; }, 2000);
        },

        // Navigation
        showDashboard() {
            this.view = 'dashboard';
        },

        showSettings() {
            this.view = 'settings';
        },

        showAddOutput() {
            this.newOutput = {
                host: '',
                port: 8080,
                streamid: '',
                password: '',
                codec: 'mp3',
                max_retries: 99
            };
            this.view = 'add-output';
        },

        showTab(tabId) {
            this.settingsTab = tabId;
        },

        // Output management
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

        deleteOutput(id) {
            if (!confirm('Delete this output?')) return;
            const output = this.outputs.find(o => o.id === id);
            if (output) this.deletingOutputs[id] = output.created_at;
            this.send('delete_output', id, null);
        },

        getOutputStateClass(output) {
            const status = this.outputStatuses[output.id] || {};
            const isDeleting = this.deletingOutputs[output.id] === output.created_at;
            if (isDeleting) return 'state-warning';
            if (status.stable) return 'state-success';
            if (status.given_up) return 'state-danger';
            if (status.retry_count > 0) return 'state-warning';
            if (status.running) return 'state-warning';
            if (!this.encoderRunning) return 'state-stopped';
            return 'state-warning';
        },

        getOutputStatusText(output) {
            const status = this.outputStatuses[output.id] || {};
            const isDeleting = this.deletingOutputs[output.id] === output.created_at;
            if (isDeleting) return 'Stopping...';
            if (status.stable) return 'Connected';
            if (status.given_up) return 'Failed';
            if (status.retry_count > 0) return `Retry ${status.retry_count}/${status.max_retries}`;
            if (status.running) return 'Connecting...';
            if (!this.encoderRunning) return 'Offline';
            return 'Connecting...';
        },

        shouldShowError(output) {
            const status = this.outputStatuses[output.id] || {};
            const isDeleting = this.deletingOutputs[output.id] === output.created_at;
            return !isDeleting && (status.given_up || status.retry_count > 0) && status.last_error;
        },

        // VU Meter
        toggleVuMode() {
            this.vuMode = this.vuMode === 'peak' ? 'rms' : 'peak';
            localStorage.setItem('vuMode', this.vuMode);
        },

        resetVuMeter() {
            this.levels = { left: -60, right: -60, peak_left: -60, peak_right: -60, silence_level: null };
        },

        // Settings
        updateAudioInput() {
            this.send('update_settings', null, { audio_input: this.settings.audioInput });
        },

        updateSilenceThreshold() {
            if (this.settings.silenceThreshold >= -60 && this.settings.silenceThreshold <= 0) {
                this.send('update_settings', null, { silence_threshold: this.settings.silenceThreshold });
            }
        },

        updateSilenceDuration() {
            if (this.settings.silenceDuration >= 1 && this.settings.silenceDuration <= 300) {
                this.send('update_settings', null, { silence_duration: this.settings.silenceDuration });
            }
        },

        updateSilenceRecovery() {
            if (this.settings.silenceRecovery >= 1 && this.settings.silenceRecovery <= 60) {
                this.send('update_settings', null, { silence_recovery: this.settings.silenceRecovery });
            }
        },

        updateSilenceWebhook() {
            this.send('update_settings', null, { silence_webhook: this.settings.silenceWebhook });
        },

        saveEmailSettings() {
            const update = {
                email_smtp_host: this.settings.email.host,
                email_smtp_port: this.settings.email.port,
                email_username: this.settings.email.username,
                email_recipients: this.settings.email.recipients
            };
            const pw = document.getElementById('email-password');
            if (pw?.value) update.email_password = pw.value;
            this.send('update_settings', null, update);
        },

        sendTestEmail() {
            this.emailTestPending = true;
            this.emailTestText = 'Sending...';
            this.send('test_email', null, null);
        }
    }));
});

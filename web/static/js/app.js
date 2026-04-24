function app() {
    const api = {
        async json(url, options = {}) {
            const response = await fetch(url, options);
            return response.json();
        }
    };

    return {
        currentTab: 'dashboard',
        loading: false,
        testing: false,
        status: {
            state: 'stopped',
            node_count: 0,
            node_testing: false,
            selected_node: 'auto',
            selected_mode: 'auto',
            effective_node: '',
            recommended_node: '',
            testing_total: 0,
            testing_completed: 0,
            node_selection_preference: 'auto',
            proxy_mode: 'rule',
            tester_state: 'stopped',
            tester_error: ''
        },
        navItems: [
            { key: 'dashboard', label: '仪表盘', icon: '◫', desc: '总览与快捷控制' },
            { key: 'nodes', label: '节点', icon: '◎', desc: '节点选择与测速' },
            { key: 'subscriptions', label: '订阅', icon: '⛁', desc: '订阅源与更新' },
            { key: 'rules', label: '路由', icon: '⇄', desc: '规则与模式' },
            { key: 'bypass', label: '绕过', icon: '↷', desc: '完全绕过 TUN' },
            { key: 'settings', label: '设置', icon: '⚙', desc: '系统与测速配置' },
            { key: 'connections', label: '连接', icon: '≋', desc: '流量与会话' },
            { key: 'logs', label: '日志', icon: '▤', desc: '实时运行日志' }
        ],
        pageMeta: {
            dashboard: { title: '仪表盘', subtitle: '运行状态、模式切换与全局快捷操作' },
            nodes: { title: '节点管理', subtitle: '选择节点、切换推荐候选与执行测速' },
            subscriptions: { title: '订阅管理', subtitle: '管理订阅源、更新时间与节点来源' },
            rules: { title: '规则与路由', subtitle: '内置规则、自定义规则与代理模式' },
            bypass: { title: '绕过管理', subtitle: '配置完全绕过 TUN 的地址与路由刷新' },
            settings: { title: '系统设置', subtitle: '测速目标、节点偏好、DNS 与代理参数' },
            connections: { title: '连接管理', subtitle: '查看实时连接、流量统计与出口链路' },
            logs: { title: '日志查看', subtitle: '跟踪运行日志、DNS 错误与服务事件' }
        },
        nodes: [],
        subscriptions: [],
        rulesInfo: {
            default_rules: [],
            custom_rules: [],
            geosite_values: [],
            geoip_values: [],
            last_rule_update: ''
        },
        appConfig: null,
        logs: [],
        logLevel: 'info',
        logFilter: 'all',
        logSearch: '',
        toasts: [],
        connections: [],
        connStats: { download: 0, upload: 0 },
        connInterval: null,
        uiRefreshInterval: null,
        showAddSubscription: false,
        showEditSubscription: false,
        showAddRule: false,
        showAddBypass: false,
        newSubscription: { name: '', url: '', auto_update: true, update_interval: 60 },
        editingSubscription: { id: '', name: '', url: '', auto_update: true, update_interval: 60 },
        newRule: { type: 'domain', value: '', outbound: 'proxy' },
        newBypass: { address: '', comment: '' },
        bypassInfo: { bypass_list: [], gateway: '', interface: '' },
        bypassLoading: false,
        eventSource: null,
        clearingCache: false,
        nodeSort: {
            field: null
        },

        async init() {
            this.loadNodeSort();
            this.loadCurrentTab();
            await this.fetchStatus();
            await this.fetchNodes();
            await this.fetchSubscriptions();
            await this.fetchRules();
            await this.fetchConfig();
            await this.fetchLogLevel();
            await this.fetchBypass();

            this.uiRefreshInterval = setInterval(async () => {
                await this.fetchStatus();
                if (this.currentTab === 'nodes') {
                    await this.fetchNodes();
                }
                if (this.currentTab === 'subscriptions') {
                    await this.fetchSubscriptions();
                }
            }, 5000);

            this.$watch('currentTab', (newPage, oldPage) => {
                this.saveCurrentTab();
                if (newPage === 'logs') {
                    this.fetchLogs();
                    this.connectSSE();
                } else if (oldPage === 'logs') {
                    this.disconnectSSE();
                }
                if (newPage === 'connections') {
                    this.fetchConnections();
                    this.startConnPolling();
                } else if (oldPage === 'connections') {
                    this.stopConnPolling();
                }
                if (newPage === 'nodes') {
                    this.fetchNodes();
                }
                if (newPage === 'subscriptions') {
                    this.fetchSubscriptions();
                }
            });
        },

        setTab(tab) {
            this.currentTab = tab;
        },

        loadCurrentTab() {
            try {
                const saved = localStorage.getItem('singboxA.currentTab');
                if (saved && this.navItems.some((item) => item.key === saved)) {
                    this.currentTab = saved;
                }
            } catch (e) {
                console.error('Failed to load current tab:', e);
            }
        },

        saveCurrentTab() {
            try {
                localStorage.setItem('singboxA.currentTab', this.currentTab);
            } catch (e) {
                console.error('Failed to save current tab:', e);
            }
        },

        async fetchStatus() {
            try {
                const data = await api.json('/api/status');
                if (data.success) {
                    this.status = data.data;
                    this.testing = Boolean(data.data.node_testing);
                }
            } catch (e) {
                console.error('Failed to fetch status:', e);
            }
        },

        async fetchNodes() {
            try {
                const data = await api.json('/api/nodes');
                if (data.success) {
                    this.nodes = (data.data || []).map((node) => ({
                        ...node,
                        testing: false,
                        latency: typeof node.latency === 'number' ? node.latency : null,
                        subscription_names: Array.isArray(node.subscription_names) ? node.subscription_names : []
                    }));
                }
            } catch (e) {
                console.error('Failed to fetch nodes:', e);
            }
        },

        async fetchSubscriptions() {
            try {
                const data = await api.json('/api/subscriptions');
                if (data.success) {
                    this.subscriptions = data.data || [];
                }
            } catch (e) {
                console.error('Failed to fetch subscriptions:', e);
            }
        },

        async fetchRules() {
            try {
                const data = await api.json('/api/rules');
                if (data.success) {
                    this.rulesInfo = data.data;
                }
            } catch (e) {
                console.error('Failed to fetch rules:', e);
            }
        },

        async refreshRules() {
            try {
                const data = await api.json('/api/rules/refresh', { method: 'POST' });
                if (data.success) {
                    this.showToast('规则已更新', 'success');
                    await this.fetchRules();
                } else {
                    this.showToast(data.error || '更新失败', 'error');
                }
            } catch (e) {
                this.showToast('更新失败: ' + e.message, 'error');
            }
        },

        async fetchConfig() {
            try {
                const data = await api.json('/api/config');
                if (data.success) {
                    this.appConfig = data.data;
                    if (!this.newSubscription.update_interval) {
                        this.newSubscription.update_interval = this.appConfig?.subscription?.update_interval || 60;
                    }
                }
            } catch (e) {
                console.error('Failed to fetch config:', e);
            }
        },

        async fetchLogs() {
            try {
                const data = await api.json('/api/logs');
                if (data.success) {
                    this.logs = data.data || [];
                }
            } catch (e) {
                console.error('Failed to fetch logs:', e);
            }
        },

        connectSSE() {
            if (this.eventSource) {
                this.eventSource.close();
            }

            this.eventSource = new EventSource('/api/logs/stream');
            let scrollTimeout = null;

            this.eventSource.onmessage = (event) => {
                const log = JSON.parse(event.data);
                this.logs.push(log);
                if (this.logs.length > 200) {
                    this.logs = this.logs.slice(-200);
                }
                if (this.currentTab === 'logs') {
                    clearTimeout(scrollTimeout);
                    scrollTimeout = setTimeout(() => {
                        const container = document.getElementById('log-container');
                        if (container) {
                            container.scrollTop = container.scrollHeight;
                        }
                    }, 100);
                }
            };

            this.eventSource.onerror = () => {
                this.eventSource.close();
                if (this.currentTab === 'logs') {
                    setTimeout(() => this.connectSSE(), 5000);
                }
            };
        },

        disconnectSSE() {
            if (this.eventSource) {
                this.eventSource.close();
                this.eventSource = null;
            }
        },

        async fetchConnections() {
            try {
                const data = await api.json('/api/clash/connections');
                this.connections = data.connections || [];
                this.connStats = {
                    download: data.downloadTotal || 0,
                    upload: data.uploadTotal || 0
                };
            } catch (e) {
                console.error('Failed to fetch connections:', e);
            }
        },

        startConnPolling() {
            this.stopConnPolling();
            this.connInterval = setInterval(() => this.fetchConnections(), 2000);
        },

        stopConnPolling() {
            if (this.connInterval) {
                clearInterval(this.connInterval);
                this.connInterval = null;
            }
        },

        async closeConnection(id) {
            try {
                await fetch(`/api/clash/connections/${id}`, { method: 'DELETE' });
                await this.fetchConnections();
            } catch (e) {
                this.showToast('断开连接失败', 'error');
            }
        },

        async closeAllConnections() {
            if (!confirm('确定断开所有连接?')) return;
            try {
                await fetch('http://127.0.0.1:9091/connections', { method: 'DELETE' });
                await this.fetchConnections();
                this.showToast('已断开所有连接', 'success');
            } catch (e) {
                this.showToast('断开失败', 'error');
            }
        },

        async startService() {
            this.loading = true;
            try {
                const data = await api.json('/api/start', { method: 'POST' });
                if (data.success) {
                    this.showToast('服务已启动', 'success');
                    await this.fetchStatus();
                } else {
                    this.showToast(data.error || '启动失败', 'error');
                }
            } catch (e) {
                this.showToast('启动失败: ' + e.message, 'error');
            }
            this.loading = false;
        },

        async stopService() {
            this.loading = true;
            try {
                const data = await api.json('/api/stop', { method: 'POST' });
                if (data.success) {
                    this.showToast('服务已停止', 'success');
                    await this.fetchStatus();
                } else {
                    this.showToast(data.error || '停止失败', 'error');
                }
            } catch (e) {
                this.showToast('停止失败: ' + e.message, 'error');
            }
            this.loading = false;
        },

        async restartService() {
            this.loading = true;
            try {
                const data = await api.json('/api/restart', { method: 'POST' });
                if (data.success) {
                    this.showToast('服务已重启', 'success');
                    await this.fetchStatus();
                    await this.fetchNodes();
                    setTimeout(() => {
                        this.fetchStatus();
                        this.fetchNodes();
                    }, 1200);
                } else {
                    this.showToast(data.error || '重启失败', 'error');
                }
            } catch (e) {
                this.showToast('重启失败: ' + e.message, 'error');
            }
            this.loading = false;
        },

        async refreshSubscriptions() {
            this.loading = true;
            try {
                const data = await api.json('/api/subscriptions/refresh', { method: 'POST' });
                if (data.success) {
                    this.showToast('订阅已刷新', 'success');
                    await this.fetchSubscriptions();
                    await this.fetchNodes();
                } else {
                    this.showToast(data.error || '刷新失败', 'error');
                }
            } catch (e) {
                this.showToast('刷新失败: ' + e.message, 'error');
            }
            this.loading = false;
        },

        async refreshSubscription() {
            try {
                const data = await api.json('/api/subscriptions/refresh', { method: 'POST' });
                if (data.success) {
                    this.showToast('订阅已刷新', 'success');
                    await this.fetchSubscriptions();
                    await this.fetchNodes();
                } else {
                    this.showToast(data.error || '刷新失败', 'error');
                }
            } catch (e) {
                this.showToast('刷新失败: ' + e.message, 'error');
            }
        },

        async addSubscription() {
            const payload = {
                ...this.newSubscription,
                update_interval: this.newSubscription.auto_update
                    ? (this.newSubscription.update_interval || this.appConfig?.subscription?.update_interval || 60)
                    : 0
            };
            try {
                const data = await api.json('/api/subscriptions', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                if (data.success) {
                    this.showToast('订阅已添加', 'success');
                    this.showAddSubscription = false;
                    this.newSubscription = {
                        name: '',
                        url: '',
                        auto_update: true,
                        update_interval: this.appConfig?.subscription?.update_interval || 60
                    };
                    await this.fetchSubscriptions();
                    setTimeout(() => this.fetchNodes(), 2000);
                } else {
                    this.showToast(data.error || '添加失败', 'error');
                }
            } catch (e) {
                this.showToast('添加失败: ' + e.message, 'error');
            }
        },

        openEditSubscription(sub) {
            this.editingSubscription = {
                id: sub.id,
                name: sub.name,
                url: sub.url,
                auto_update: sub.auto_update !== false,
                update_interval: sub.update_interval || this.appConfig?.subscription?.update_interval || 60
            };
            this.showEditSubscription = true;
        },

        async updateSubscription() {
            const payload = {
                ...this.editingSubscription,
                update_interval: this.editingSubscription.auto_update
                    ? (this.editingSubscription.update_interval || this.appConfig?.subscription?.update_interval || 60)
                    : 0
            };
            try {
                const data = await api.json(`/api/subscriptions/${this.editingSubscription.id}`, {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });
                if (data.success) {
                    this.showToast('订阅已更新', 'success');
                    this.showEditSubscription = false;
                    await this.fetchSubscriptions();
                } else {
                    this.showToast(data.error || '更新失败', 'error');
                }
            } catch (e) {
                this.showToast('更新失败: ' + e.message, 'error');
            }
        },

        async deleteSubscription(id) {
            if (!confirm('确定删除此订阅?')) return;
            try {
                const data = await api.json(`/api/subscriptions/${id}`, { method: 'DELETE' });
                if (data.success) {
                    this.showToast('订阅已删除', 'success');
                    await this.fetchSubscriptions();
                    await this.fetchNodes();
                } else {
                    this.showToast(data.error || '删除失败', 'error');
                }
            } catch (e) {
                this.showToast('删除失败: ' + e.message, 'error');
            }
        },

        async selectNode(name) {
            try {
                const data = await api.json(`/api/nodes/${encodeURIComponent(name)}/select`, { method: 'POST' });
                if (data.success) {
                    this.showToast('节点已切换', 'success');
                    await this.fetchNodes();
                    await this.fetchStatus();
                    setTimeout(() => {
                        this.fetchNodes();
                        this.fetchStatus();
                    }, 800);
                } else {
                    this.showToast(data.error || '切换失败', 'error');
                }
            } catch (e) {
                this.showToast('切换失败: ' + e.message, 'error');
            }
        },

        async applyRecommendedNode() {
            try {
                const data = await api.json('/api/nodes/auto/apply-recommended', { method: 'POST' });
                if (data.success) {
                    this.showToast('已切换到推荐节点', 'success');
                    await this.fetchNodes();
                    await this.fetchStatus();
                    setTimeout(() => {
                        this.fetchNodes();
                        this.fetchStatus();
                    }, 800);
                } else {
                    this.showToast(data.error || '切换失败', 'error');
                }
            } catch (e) {
                this.showToast('切换失败: ' + e.message, 'error');
            }
        },

        async testNodeByName(nodeName, silent = false) {
            if (nodeName === 'auto') {
                return;
            }

            if (!silent) {
                this.showToast('开始测速: ' + nodeName, 'info');
            }

            const nodeIndex = this.nodes.findIndex((n) => n.name === nodeName);
            if (nodeIndex === -1) {
                if (!silent) {
                    this.showToast('未找到节点', 'error');
                }
                return;
            }

            this.nodes[nodeIndex].testing = true;
            this.nodes[nodeIndex].latency = null;
            try {
                const data = await api.json(`/api/nodes/${encodeURIComponent(nodeName)}/test`, { method: 'POST' });
                if (data.success) {
                    this.nodes[nodeIndex].latency = data.data.latency;
                    if (!silent) {
                        await this.fetchStatus();
                        await this.fetchNodes();
                    }
                    if (!silent) {
                        this.showToast('测速完成', 'success');
                    }
                } else {
                    this.nodes[nodeIndex].latency = -1;
                    if (!silent) {
                        this.showToast('测速失败', 'error');
                    }
                }
            } catch (e) {
                this.nodes[nodeIndex].latency = -1;
                if (!silent) {
                    this.showToast('测速失败: ' + e.message, 'error');
                }
            }
            this.nodes[nodeIndex].testing = false;
        },

        async testAllNodes() {
            const realNodes = this.nodes.filter((node) => !node.virtual);
            if (realNodes.length === 0) {
                this.showToast('没有可测速的节点', 'error');
                return;
            }

            this.showToast('开始测速...', 'info');
            this.testing = true;

            try {
                const data = await api.json('/api/nodes/test-all', { method: 'POST' });
                if (data.success) {
                    await this.fetchStatus();
                    await this.fetchNodes();
                } else {
                    this.testing = false;
                    this.showToast(data.error || '测速失败', 'error');
                }
            } catch (e) {
                this.testing = false;
                this.showToast('测速失败: ' + e.message, 'error');
            }
        },

        async setProxyMode(mode) {
            try {
                const data = await api.json('/api/rules/mode', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ mode })
                });
                if (data.success) {
                    this.showToast('模式已切换', 'success');
                    await this.fetchStatus();
                } else {
                    this.showToast(data.error || '切换失败', 'error');
                }
            } catch (e) {
                this.showToast('切换失败: ' + e.message, 'error');
            }
        },

        async addCustomRule() {
            const rules = [...(this.rulesInfo.custom_rules || []), { ...this.newRule }];
            try {
                const data = await api.json('/api/rules', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(rules)
                });
                if (data.success) {
                    this.showToast('规则已添加', 'success');
                    this.showAddRule = false;
                    this.newRule = { type: 'domain', value: '', outbound: 'proxy' };
                    await this.fetchRules();
                } else {
                    this.showToast(data.error || '添加失败', 'error');
                }
            } catch (e) {
                this.showToast('添加失败: ' + e.message, 'error');
            }
        },

        async removeCustomRule(index) {
            const rules = [...this.rulesInfo.custom_rules];
            rules.splice(index, 1);
            try {
                const data = await api.json('/api/rules', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(rules)
                });
                if (data.success) {
                    this.showToast('规则已删除', 'success');
                    await this.fetchRules();
                } else {
                    this.showToast(data.error || '删除失败', 'error');
                }
            } catch (e) {
                this.showToast('删除失败: ' + e.message, 'error');
            }
        },

        async fetchBypass() {
            try {
                const data = await api.json('/api/bypass');
                if (data.success) {
                    this.bypassInfo = data.data;
                }
            } catch (e) {
                console.error('Failed to fetch bypass:', e);
            }
        },

        async addBypass() {
            try {
                const data = await api.json('/api/bypass', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(this.newBypass)
                });
                if (data.success) {
                    this.showToast('绕过地址已添加', 'success');
                    this.showAddBypass = false;
                    this.newBypass = { address: '', comment: '' };
                    await this.fetchBypass();
                } else {
                    this.showToast(data.error || '添加失败', 'error');
                }
            } catch (e) {
                this.showToast('添加失败: ' + e.message, 'error');
            }
        },

        async removeBypass(address) {
            if (!confirm('确定删除此绕过地址?')) return;
            try {
                const data = await api.json('/api/bypass', {
                    method: 'DELETE',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ address })
                });
                if (data.success) {
                    this.showToast('绕过地址已删除', 'success');
                    await this.fetchBypass();
                } else {
                    this.showToast(data.error || '删除失败', 'error');
                }
            } catch (e) {
                this.showToast('删除失败: ' + e.message, 'error');
            }
        },

        async refreshBypass() {
            this.bypassLoading = true;
            try {
                const data = await api.json('/api/bypass/refresh', { method: 'POST' });
                if (data.success) {
                    this.showToast('路由已刷新', 'success');
                    await this.fetchBypass();
                } else {
                    this.showToast(data.error || '刷新失败', 'error');
                }
            } catch (e) {
                this.showToast('刷新失败: ' + e.message, 'error');
            }
            this.bypassLoading = false;
        },

        async saveConfig() {
            try {
                const data = await api.json('/api/config', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(this.appConfig)
                });
                if (data.success) {
                    this.showToast('设置已保存', 'success');
                    await this.fetchConfig();
                    await this.fetchStatus();
                    await this.fetchNodes();
                } else {
                    this.showToast(data.error || '保存失败', 'error');
                }
            } catch (e) {
                this.showToast('保存失败: ' + e.message, 'error');
            }
        },

        async clearLogs() {
            try {
                await fetch('/api/logs/clear', { method: 'POST' });
                this.logs = [];
                this.showToast('日志已清空', 'success');
            } catch (e) {
                this.showToast('清空失败: ' + e.message, 'error');
            }
        },

        async clearCache() {
            if (this.clearingCache) return;
            this.clearingCache = true;
            try {
                const data = await api.json('/api/cache/clear', { method: 'POST' });
                if (data.success) {
                    this.showToast('DNS 缓存已清空', 'success');
                    await this.fetchStatus();
                } else {
                    this.showToast(data.error || '清空缓存失败', 'error');
                }
            } catch (e) {
                this.showToast('清空缓存失败: ' + e.message, 'error');
            } finally {
                this.clearingCache = false;
            }
        },

        async fetchLogLevel() {
            try {
                const data = await api.json('/api/logs/level');
                if (data.success) {
                    this.logLevel = data.data.level || 'info';
                }
            } catch (e) {
                console.error('Failed to fetch log level:', e);
            }
        },

        async setLogLevel() {
            try {
                const data = await api.json('/api/logs/level', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ level: this.logLevel })
                });
                if (data.success) {
                    this.showToast('日志级别已设置为 ' + this.logLevel + '，重启后生效', 'success');
                } else {
                    this.showToast(data.error || '设置失败', 'error');
                }
            } catch (e) {
                this.showToast('设置失败: ' + e.message, 'error');
            }
        },

        showToast(message, type = 'info') {
            this.toasts.push({ message, type });
            setTimeout(() => {
                this.toasts.shift();
            }, 3000);
        },

        getModeText(mode) {
            const modes = { rule: '规则', global: '全局', direct: '直连' };
            return modes[mode] || mode;
        },

        getPageTitle() {
            return this.pageMeta[this.currentTab]?.title || 'SingBoxA';
        },

        getPageSubtitle() {
            return this.pageMeta[this.currentTab]?.subtitle || '基于 sing-box 的轻量代理管理面板';
        },

        getStatusBadgeClass() {
            return this.status.state === 'running' ? 'is-running' : 'is-stopped';
        },

        getTesterBadgeClass() {
            if (this.status.tester_state === 'running') return 'is-running';
            if (this.status.tester_state === 'error') return 'is-error';
            return 'is-idle';
        },

        getTesterLabel() {
            const labels = {
                running: '测速内核正常',
                starting: '测速内核启动中',
                error: '测速内核异常',
                stopped: '测速内核已停止'
            };
            return labels[this.status.tester_state] || '测速状态未知';
        },

        toggleNodeSort(field) {
            if (this.nodeSort.field === field) {
                this.nodeSort.field = null;
            } else {
                this.nodeSort.field = field;
            }
            this.saveNodeSort();
        },

        getNodeSortIndicator(field) {
            return this.nodeSort.field === field ? '↑' : '';
        },

        loadNodeSort() {
            try {
                const saved = localStorage.getItem('singboxA.nodeSort');
                if (!saved) return;
                const parsed = JSON.parse(saved);
                if (parsed && (parsed.field === 'name' || parsed.field === 'latency' || parsed.field === null)) {
                    this.nodeSort = { field: parsed.field };
                }
            } catch (e) {
                console.error('Failed to load node sort:', e);
            }
        },

        saveNodeSort() {
            try {
                localStorage.setItem('singboxA.nodeSort', JSON.stringify(this.nodeSort));
            } catch (e) {
                console.error('Failed to save node sort:', e);
            }
        },

        getSelectedNodeText() {
            if (this.status.selected_mode === 'auto') {
                return '自动选择';
            }
            return this.status.selected_node || '未选择';
        },

        getTestURLModeLabel(mode) {
            const labels = {
                gstatic: 'Google Gstatic',
                youtube_ggpht: 'YouTube',
                skk: 'SKK',
                jsdelivr: 'jsDelivr',
                github: 'GitHub'
            };
            return labels[mode] || mode || 'Google Gstatic';
        },

        getCurrentTestURLMode() {
            return this.appConfig && this.appConfig.proxy
                ? this.appConfig.proxy.test_url_mode
                : 'gstatic';
        },

        getCurrentTestURLModeLabel() {
            return this.getTestURLModeLabel(this.getCurrentTestURLMode());
        },

        getCurrentTestWorkerCount() {
            const count = this.appConfig && this.appConfig.proxy
                ? Number(this.appConfig.proxy.test_workers)
                : 3;
            if (Number.isNaN(count) || count < 1) {
                return 1;
            }
            if (count > 5) {
                return 5;
            }
            return count;
        },

        getTestedNodeCount() {
            return this.nodes.filter((node) => !node.virtual && typeof node.latency === 'number' && node.latency >= 0).length;
        },

        getHealthyNodeCount() {
            return this.nodes.filter((node) => !node.virtual && typeof node.latency === 'number' && node.latency > 0 && node.latency < 500).length;
        },

        getAutoUpdateSubscriptionCount() {
            return this.subscriptions.filter((sub) => sub.auto_update !== false).length;
        },

        getRecentSubscriptionCount() {
            const now = Date.now();
            return this.subscriptions.filter((sub) => {
                if (!sub.updated_at) return false;
                const updated = new Date(sub.updated_at).getTime();
                return Number.isFinite(updated) && now - updated <= 24 * 60 * 60 * 1000;
            }).length;
        },

        getDashboardAdvice() {
            if (this.status.state !== 'running') {
                return '主服务当前未启动，先启动 sing-box 再进行节点选择和分流测试。';
            }
            if (this.status.selected_mode === 'auto' && this.status.recommended_node && this.status.recommended_node !== this.status.effective_node) {
                return '自动选择检测到了更优候选，但为了稳定性暂未自动切换。你可以到节点页手动切到推荐。';
            }
            if (this.testing) {
                return '后台测速正在运行，推荐节点和自动选择结果会在本轮测速完成后更新。';
            }
            if (this.status.tester_state === 'error') {
                return '测速内核当前异常，建议检查测速目标与系统资源占用。';
            }
            return '当前服务状态稳定，可以优先使用自动选择，并结合测速目标筛选更适合当前网络环境的节点。';
        },

        getSubscriptionStatusClass(sub) {
            return sub.auto_update !== false ? 'is-success' : 'is-neutral';
        },

        getNodeLatencyClass(latency) {
            if (latency === 999 || latency === -1) return 'is-bad';
            if (typeof latency !== 'number' || latency <= 0) return 'is-idle';
            if (latency < 200) return 'is-good';
            if (latency < 500) return 'is-warn';
            return 'is-bad';
        },

        canApplyRecommended(node) {
            return Boolean(
                node &&
                node.virtual &&
                node.effective_node &&
                node.recommended_node &&
                node.effective_node !== node.recommended_node
            );
        },

        formatDate(dateStr) {
            if (!dateStr) return '';
            const date = new Date(dateStr);
            return date.toLocaleString('zh-CN');
        },

        formatLogTime(timeStr) {
            if (!timeStr) return '';
            const date = new Date(timeStr);
            return date.toLocaleTimeString('zh-CN');
        },

        get filteredLogs() {
            let filtered = this.logs;

            switch (this.logFilter) {
                case 'dns':
                    filtered = filtered.filter((log) =>
                        log.message.includes('dns:') ||
                        log.message.includes('exchanged') ||
                        log.message.includes('cached'));
                    break;
                case 'outbound':
                    filtered = filtered.filter((log) =>
                        log.message.includes('outbound/') ||
                        log.message.includes('outbound connection'));
                    break;
                case 'urltest':
                    filtered = filtered.filter((log) =>
                        log.message.includes('urltest') ||
                        log.message.includes('selected'));
                    break;
                case 'important':
                    filtered = filtered.filter((log) =>
                        log.level === 'warn' ||
                        log.level === 'error' ||
                        log.level === 'fatal' ||
                        log.message.includes('selected') ||
                        log.message.includes('started') ||
                        log.message.includes('stopped'));
                    break;
            }

            if (this.logSearch.trim()) {
                const search = this.logSearch.toLowerCase();
                filtered = filtered.filter((log) =>
                    log.message.toLowerCase().includes(search));
            }

            return filtered;
        },

        get sortedNodes() {
            const autoNode = this.nodes.find((node) => node.virtual);
            const realNodes = this.nodes.filter((node) => !node.virtual);

            if (this.nodeSort.field === 'name') {
                realNodes.sort((a, b) => {
                    const nameA = a.name === 'auto' ? '自动选择' : a.name;
                    const nameB = b.name === 'auto' ? '自动选择' : b.name;
                    return nameA.localeCompare(nameB, 'zh-CN');
                });
            } else if (this.nodeSort.field === 'latency') {
                realNodes.sort((a, b) => {
                    const latencyA = typeof a.latency === 'number' && a.latency >= 0
                        ? a.latency
                        : Number.POSITIVE_INFINITY;
                    const latencyB = typeof b.latency === 'number' && b.latency >= 0
                        ? b.latency
                        : Number.POSITIVE_INFINITY;
                    if (latencyA !== latencyB) {
                        return latencyA - latencyB;
                    }
                    return a.name.localeCompare(b.name, 'zh-CN');
                });
            }

            return autoNode ? [autoNode, ...realNodes] : realNodes;
        },

        highlightLog(message) {
            if (!this.logSearch.trim()) return message;
            const search = this.logSearch.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
            const regex = new RegExp(`(${search})`, 'gi');
            return message.replace(regex, '<span class="log-highlight">$1</span>');
        },

        formatBytes(bytes) {
            if (bytes === 0) return '0 B';
            const k = 1024;
            const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
        },

        formatDuration(startTime) {
            if (!startTime) return '-';
            const start = new Date(startTime);
            const now = new Date();
            const seconds = Math.floor((now - start) / 1000);
            if (seconds < 60) return seconds + 's';
            if (seconds < 3600) return Math.floor(seconds / 60) + 'm ' + (seconds % 60) + 's';
            const hours = Math.floor(seconds / 3600);
            const mins = Math.floor((seconds % 3600) / 60);
            return hours + 'h ' + mins + 'm';
        }
    };
}

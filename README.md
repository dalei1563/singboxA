# SingBoxA

SingBoxA 是一个基于 [sing-box](https://github.com/SagerNet/sing-box) 的轻量代理管理面板。它把常用操作集中到 Web 控制台里，负责订阅管理、节点选择、运行控制、规则分流、绕过路由和诊断日志，适合部署在家用网关、旁路由或 Linux 服务器上统一管理代理服务。

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.18.1%2B-blue.svg)
![sing-box](https://img.shields.io/badge/sing--box-1.13.7-green.svg)

## 当前定位

SingBoxA 不是一个复杂的规则编辑器，而是一个日常控制台：

- 首页只处理高频动作：查看状态、启动/停止/重启 sing-box、刷新订阅、切换代理模式、查看当前节点。
- 节点页以节点列表和测速为主，自动选择默认强调稳定，不会因为一次测速结果就频繁跳来跳去。
- 订阅页负责添加、编辑、刷新、删除订阅，并支持每个订阅独立设置自动更新。
- 设置页收纳低频能力，包括 DNS、端口、TUN、测速配置、路由规则、绕过地址、连接和日志诊断。

## 功能

- Web 控制台：`仪表盘 / 节点 / 订阅 / 设置` 四个主入口，支持桌面和手机浏览器。
- sing-box 进程管理：启动、停止、重启、状态查询、版本检测、自动启动状态保存。
- 订阅管理：支持 Clash 订阅导入、缓存、手动刷新、自动刷新和删除。
- 节点管理：支持手动节点、自动选择节点、单节点测速、一键测速、节点来源展示。
- 节点推荐：根据 HTTP TTFB、历史测速结果和地区偏好推荐节点，支持自动、美国、新加坡、台湾、香港偏好。
- 稳定优先的自动选择：自动模式下优先沿用当前可用节点，只在节点失效、订阅变化或当前节点明显不可用时切换。
- 代理模式：支持规则模式、全局模式、直连模式。
- DNS 与 FakeIP：内置国内 DNS、代理 DNS、DoH 防泄漏、FakeIP 和 DNS 缓存清理。
- 路由规则：支持自定义域名、域名后缀、IP CIDR、geosite、geoip 规则。
- 本地规则集缓存：内置规则集会下载到本地 `.srs` 文件，生成配置时只引用本地文件；远端规则源短暂 503 时不会导致 sing-box 启动失败。
- 绕过列表：支持添加需要完全绕过 TUN 的域名或 IP，并刷新系统路由。
- DNS 自愈：连续 DNS 超时后会清理 DNS 缓存并重启 sing-box；自动模式下可结合推荐节点做恢复。
- 诊断能力：查看实时日志、历史日志、连接列表、清空日志、设置日志级别。
- 日志接口：提供 `/api/logs/history` 历史日志接口和 `/api/logs/stream` HTTP NDJSON 长连接日志流接口，方便远程排查或接入 Flink。
- Debian 打包：打包脚本会把 `singboxA`、`sing-box` 内核和 `libcronet.so` 一起放进安装包。

## 核心逻辑

### 运行控制

SingBoxA 自身作为 systemd 服务运行，默认监听 `0.0.0.0:3333`。Web 操作会生成 sing-box 配置文件，然后调用配置中的 sing-box 二进制启动内核。

默认路径：

```text
singboxA: /usr/local/bin/singboxA
sing-box: /usr/local/bin/sing-box
数据目录: /var/lib/singboxA
配置文件: /var/lib/singboxA/config.yaml
状态文件: /var/lib/singboxA/state.yaml
sing-box 配置: /var/lib/singboxA/singbox/config.json
```

### 订阅刷新与节点选择

订阅刷新后，程序会重新解析并转换节点，同时清理旧测速结果，避免旧节点延迟污染新订阅。

自动模式下有两层状态：

- `recommended_node`：当前测速和偏好下推荐的节点。
- `applied_auto_node`：自动模式实际正在使用的节点。

这样做是为了稳定。推荐节点可以更新，但实际节点不会无意义地频繁切换。只有当前节点不存在、测速不可用、订阅刷新导致有效节点变化，或用户手动应用推荐节点时，才会切到新节点并按需重启 sing-box。

### 测速与推荐

测速由独立的 sing-box 测试核心完成，不直接复用主代理进程。默认并发为 3，最多 5。测速目标可在设置中选择，例如 `gstatic`、`youtube_ggpht`、`skk`、`jsdelivr`、`github`。

推荐顺序大致为：

1. 有有效质量结果的节点，按 HTTP TTFB 排序。
2. 有有效历史延迟的节点，按延迟排序。
3. 未测速节点。
4. 明显不可用节点。

地区偏好会先筛选匹配地区的节点；如果没有匹配节点，则回退到全部节点。

### 规则与 DNS

规则模式下，配置会包含：

- DNS 劫持。
- 浏览器 DoH 防绕过规则。
- 私有地址直连。
- 中国域名、Apple 中国服务、Google 中国服务和中国 BGP IP 直连。
- 广告规则拒绝。
- 海外域名 FakeIP。
- 用户自定义规则。

内置规则集不再以远程 `rule_set` 形式写入 sing-box 配置，而是先缓存到本地：

```text
/var/lib/singboxA/singbox/*.srs
```

刷新规则时不会先删除旧缓存。新文件下载失败时会保留旧文件；如果某个规则集本地不可用，生成器会跳过对应引用，避免 sing-box 因规则集初始化失败直接退出。

### DNS 自愈

程序会观察 sing-box 日志。如果短时间内连续出现 DNS lookup 超时，会触发自愈流程：

1. 记录自愈日志。
2. 自动模式下尝试使用当前推荐节点。
3. 清理 DNS 缓存。
4. 重新生成配置并重启 sing-box。
5. 后台重新测速，更新推荐结果。

自愈有冷却时间，避免网络波动时频繁重启。

### 日志与远程排查

普通日志可以在设置的诊断页查看。远程排查可使用历史日志接口：

```bash
curl 'http://<设备IP>:3333/api/logs/history?limit=500'
```

实时日志流接口使用 NDJSON：

```bash
curl -N -H 'Accept: application/x-ndjson' \
  'http://<设备IP>:3333/api/logs/stream?include=all'
```

接口特性：

- 一行一条 JSON。
- 每条日志后带换行并立即 flush。
- 默认只输出连接日志，`include=all` 输出全部日志。
- 支持 `sinceTime` 或 `sinceId` 补发历史日志后继续长连接。
- 无日志时每 20 秒发送 heartbeat。
- 最大 5 个并发日志流客户端。

更详细说明见 [docs/log-stream.md](docs/log-stream.md)。

## 安装

### Debian 安装包

适用于 Debian / Ubuntu。安装包会自带 sing-box 内核。

```bash
sudo dpkg -i singboxa_<version>_amd64.deb
sudo systemctl enable --now singboxA
```

访问 Web UI：

```text
http://<设备IP>:3333
```

常用命令：

```bash
sudo systemctl status singboxA
sudo systemctl restart singboxA
sudo journalctl -u singboxA -f
```

卸载程序：

```bash
sudo dpkg -r singboxa
```

如需清理数据：

```bash
sudo rm -rf /var/lib/singboxA
```

### 源码运行

源码运行需要本机已有 sing-box，或者把 sing-box 路径写入 `config.yaml`。

```bash
git clone https://github.com/dalei1563/singboxA.git
cd singboxA
go build -o singboxA .
sudo ./singboxA
```

首次运行会创建默认配置。默认 Web 端口为 `3333`。

## 打包

打包脚本支持 `amd64`、`arm64`、`armhf`，需要对应架构的 sing-box 压缩包放在 `scripts/` 目录下：

```text
scripts/sing-box-1.13.7-linux-amd64.tar.gz
scripts/sing-box-1.13.7-linux-arm64.tar.gz
scripts/sing-box-1.13.7-linux-armv7.tar.gz
```

构建当前架构：

```bash
VERSION=1.0.8 scripts/build-deb.sh
```

构建指定架构：

```bash
DEB_ARCH=arm64 VERSION=1.0.8 scripts/build-deb.sh
```

产物会输出到 `dist/`：

```text
dist/singboxa_<version>_<arch>.deb
```

## 默认配置

```text
Web 监听: 0.0.0.0:3333
SOCKS5: 10808
HTTP: 10809
TUN: 默认关闭
代理模式: rule
自动订阅刷新: 开启
默认订阅刷新间隔: 60 分钟
国内 DNS: 223.5.5.5, 119.29.29.29
代理 DNS: 8.8.8.8, 1.1.1.1
```

## API 摘要

```text
GET  /api/status
POST /api/start
POST /api/stop
POST /api/restart

GET/POST /api/subscriptions
PUT/DELETE /api/subscriptions/{id}
POST     /api/subscriptions/refresh

GET  /api/nodes
POST /api/nodes/test-all
POST /api/nodes/{name}/select
POST /api/nodes/{name}/test
POST /api/nodes/auto/apply-recommended

GET/PUT /api/config
GET/PUT /api/rules
GET/PUT /api/rules/mode
POST    /api/rules/refresh

GET/POST/DELETE /api/bypass
POST /api/bypass/refresh

GET  /api/logs
GET  /api/logs/history
GET  /api/logs/stream
POST /api/logs/clear
GET/POST /api/logs/level

POST /api/cache/clear
ALL  /api/clash/*
```

## 安全说明

SingBoxA 目前没有内置登录鉴权。默认配置监听 `0.0.0.0:3333`，适合在可信局域网使用。公网部署前请自行加防火墙、VPN、反向代理鉴权或改为只监听本机地址。

## 许可证

MIT

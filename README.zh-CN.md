<div align="center">

# NodeHub

### 一个面板，管理所有机器，不用半夜救火。

轻量、自托管的 Xray 节点、Traefik 路由与无人值守运维控制面。

[![Release](https://img.shields.io/github/v/release/NatShiftN/multi-node-hub?style=flat-square)](https://github.com/NatShiftN/multi-node-hub/releases)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![Vue](https://img.shields.io/badge/Vue-3-42b883?style=flat-square&logo=vue.js)](https://vuejs.org)
[![Docker](https://img.shields.io/badge/Docker-ready-2496ED?style=flat-square&logo=docker)](Dockerfile)

[**在线 Demo**](https://demo.hkvps.xyz/) · [快速开始](#-快速开始) · [English](README.md) · [繁體中文](README.zh-TW.md)

<img src="docs/screenshots/dashboard.png" alt="NodeHub Dashboard" width="92%" />

</div>

---

## NodeHub 是什么？

NodeHub 是给“不只有一台服务器”的人使用的**单一控制面**。

不用再逐台 SSH 安装 Xray、修改 Nginx/Traefik、查看 CPU、换 IP、抢救被墙节点；打开一个页面就能：

- 查看所有节点在线 / 离线
- 创建 Xray 入站、复制订阅链接
- 查看 CPU、RAM、流量、Access Log
- 推送证书与伪装站
- 安全更换被墙 IP
- 用可视化 Route Map 管理 Traefik

适合 **homelab、小型主机商、私人代理节点**，不是给大型 SaaS 用的。

> **不用 Kubernetes、不用插件市场、不用学 40 分钟。**
> 一个 binary、一个 SQLite、一个面板。

---

## 谁适合用？

| 你是… | NodeHub 帮你… |
|---|---|
| 管理 2–50 台 VPS | 不用逐台登录 |
| 销售 Xray / VPN 服务 | 给用户订阅链接与流量配额 |
| 使用 Traefik | 在一个页面查看路由与日志 |
| 经常被墙 | 用有防护的 Auto-Heal，而不是盲目脚本 |
| 不熟 Linux | 复制一行命令就能安装 Agent |

---

## 先看 Demo

```text
https://demo.hkvps.xyz/
```

Demo 是只读的，内置三个虚拟节点、模拟流量、Traefik 示例路由与日志。登录页已自动填入账号密码。

---

## 🚀 快速开始

### 方法 A：Docker 一键安装（推荐）

需求：Linux、Docker、Docker Compose v2。

```sh
git clone https://github.com/NatShiftN/multi-node-hub.git && cd multi-node-hub && ./install.sh
```

安装器会：

1. 创建 `.env`，自动生成 `AGENT_TOKEN`、`JWT_SECRET`、管理员密码
2. 构建 image
3. 启动 NodeHub（端口 8080）
4. 打印网址与管理员密码

打开：

```text
http://你的服务器:8080
```

然后：

```text
Nodes → Add Node → 复制安装命令
```

把命令粘贴到干净的 Debian/Ubuntu VPS，Agent 会自动回连，无需手动改配置文件。

### 方法 B：手动 Docker Compose

```sh
cp .env.example .env
# 修改 AGENT_TOKEN、ADMIN_PASSWORD、JWT_SECRET、NODEHUB_PUBLIC_URL
docker compose up -d --build
```

### 方法 C：从源码构建

```sh
go build -trimpath -buildvcs=false -ldflags "-s -w" -o bin/nodehub-server ./server
go build -trimpath -buildvcs=false -ldflags "-s -w" -o bin/nodehub-agent ./agent
cd web && pnpm install && pnpm build && cd ..
./bin/nodehub-server --addr 127.0.0.1:8080 --token change-me \
  --public-url http://127.0.0.1:8080 --db nodehub.db \
  --web-dir web/dist --admin-password 'change-this-password'
```

---

## 界面预览

### Simple 模式 — 新手只需要这些

<img src="docs/screenshots/login.png" alt="登录" width="48%" />
<img src="docs/screenshots/dashboard.png" alt="Dashboard" width="48%" />

### Traefik Manager — Route Map 与 Access Log

<img src="docs/screenshots/traefik.png" alt="Traefik Route Map" width="92%" />

<img src="docs/screenshots/traefik-logs.png" alt="Traefik Access Log" width="92%" />

---

## ✨ 功能

### 节点管理

- 所有机器集中在一个面板
- 在线 / 离线 / 网络错误 / GFW 封锁
- CPU、RAM、网络、Xray 流量
- 标签、搜索、分页、手机卡片
- Simple / Pro 渐进式模式

### Xray

- VLESS / Trojan / Shadowsocks
- TCP / WebSocket / gRPC
- TLS、REALITY、UUID、密码生成
- 分享链接、QR、订阅
- 用户流量、配额、封禁
- Xray 状态与重启

### 无人值守运维

- 海外 TCP 成功 + 国内多探针交叉验证才 Auto-Heal
- 24 小时最多 2 次换 IP / 重建，SQLite 持久化
- Vultr 先建新机、拿到 IP 才删旧机
- Orphan 自动重试清理
- ADSL / PPPoE 重拨
- iptables 端口转发，本地状态恢复
- 一键 DD 重装系统

### Traefik Manager

- `EntryPoint → Router → Middleware → Service → Backend` 拓扑
- Backend 健康、TLS、Middleware 标记
- Access Log 分析：4xx/5xx、Error Rate、Duration、Top Paths/Routers、每分钟图
- 实时过滤、暂停、下载
- Traefik API / Basic Auth / Access Log 路径设置

> 这是独立实现，灵感来自 Traefik Manager，不是复制它的 GPL 源码。

### 安全

- TOTP 两步验证
- RBAC：SuperAdmin / Operator / Viewer
- API Keys、Audit Log
- Cloud Key、证书私钥、Telegram/Webhook 密钥 AES-GCM 加密
- Release binary 使用 `-s -w -buildvcs=false`
- Demo 写入 API 一律 403

### 多语言与手机

- 中文 / English 切换
- `?lang=en`、`?lang=zh`
- 手机节点卡片

---

## 架构

```text
浏览器 (Vue 3)
      │ HTTPS / JSON / SSE
NodeHub Server (Go)
SQLite · RBAC · Traefik API
      │ WebSocket
┌─────┼─────┐
Agent A   Agent B   Agent C
Xray      Xray      Xray
iptables  iptables  iptables
```

数据面留在节点。控制面挂掉时节点继续运行；Agent 用指数退避重连，并从本地恢复 Xray / iptables。

---

## 系统需求

| 组件 | 需求 |
|---|---|
| Server | Linux / Windows / Docker，1 vCPU，512 MB RAM |
| Database | SQLite（内置） |
| Agent | Linux amd64 / arm64 |
| Browser | Chrome / Edge / Safari / Firefox |
| Optional | Traefik v2/v3 API + access log |

---

## 常用操作

### 添加第一台节点

1. `Nodes → Add Node`
2. 输入主机与 root 密码，或粘贴 SSH 私钥
3. 可选自定义 SSH 端口
4. 复制生成的安装命令，到 VPS 执行

### 创建订阅

1. 节点 → Subscriptions
2. 添加 VLESS / Trojan / Shadowsocks
3. 复制分享链接或 QR
4. 发给客户端

### 接入 Traefik Manager

1. 开启 Traefik API
2. `Traefik → Settings`
3. 填 API URL、Basic Auth、Access Log Path
4. 保存后 Route Map / Logs 自动加载

### 重置数据库

```sh
docker compose down
docker volume rm multi-node-hub_nodehub-data
docker compose up -d
```

---

## 安全检查

- [ ] 修改 `ADMIN_PASSWORD`
- [ ] 使用随机 `JWT_SECRET`（32+ 字符）并备份
- [ ] 修改 `AGENT_TOKEN`
- [ ] 面板放在 HTTPS 后面
- [ ] 防火墙限制 8080
- [ ] 不要 commit `.env`、`nodehub.db`、`backups/`、证书
- [ ] 曾粘贴到聊天或日志的凭证全部轮换

详见 [SECURITY.md](SECURITY.md)。

---

## 开发

```sh
make test          # go test
make vet           # go vet
make build         # server + agent
cd web && pnpm dev # 前端 :5173
```

---

## FAQ

**不用 Docker 可以吗？**  
可以，编译 Go binary，`--web-dir` 指向 `web/dist`。

**NodeHub 会代理流量吗？**  
不会。Xray 在节点上跑，NodeHub 只负责控制。

**Server 挂掉会怎样？**  
节点继续运行，Agent 会重连。

**可以商用吗？**  
GPL-3.0 允许商用，但衍生发布需遵守 GPL。

**一定要 Traefik 吗？**  
不用，Traefik Manager 是选配。

---

## License

GPL-3.0，见 [LICENSE](LICENSE)。

<div align="center">

**如果 NodeHub 帮你省时间，帮我点个 ⭐**

[Live Demo](https://demo.hkvps.xyz/) · [Releases](https://github.com/NatShiftN/multi-node-hub/releases) · [Issues](https://github.com/NatShiftN/multi-node-hub/issues)

</div>

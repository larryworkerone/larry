# Larry 探针

> 一个独一无二的开源服务器监控探针:不仅看每台机器的状态,还看机器之间如何相互到达。

Larry 把传统的「Dashboard + Agent」监控做对、做轻,并加上了**别的探针不做的事**:一个由 Agent 两两 TCP 探测编织而成的**节点互联延迟网格**。你能一眼看出任意两台服务器之间的网络延迟和可达性,而不只是各自的 CPU 和内存。

## 为什么是 Larry

| | Larry | 哪吒 (Nezha) | Komari |
|---|---|---|---|
| 节点间延迟网格 | ✅ **招牌特性** | ❌ | ❌ |
| 单二进制自带 UI | ✅ Go embed,无前端构建 | ❌ 独立前端 | ❌ 独立前端 |
| 外部数据库 | ❌ 不需要 | ✅ MySQL/TSDB | ✅ SQLite |
| 通信协议 | WebSocket + JSON | gRPC | WebSocket |
| 跨平台 Agent | ✅ Linux/mac/Win/*BSD | ✅ | ✅ |
| 部署成本 | 一条命令 | 需配 DB + 前端 | 较轻 |
| 代码量 | ~2000 行,可读可改 | 大型项目 | 中型 |

Larry 不追求功能最全。它追求:**部署最快、概念最少、但多给了你一张别人没有的延迟网格图**。

## 招牌特性:节点互联延迟网格

每个 Agent 监听一个 TCP 探测端口。Dashboard 定期让每个在线 Agent 去探测其余所有 Agent,测量 TCP 握手往返延迟,结果汇聚成一张 `N×N` 的网格,在仪表盘上渲染成热力图:

```
            node-1   node-2   node-3   node-4
node-1      —        1.2ms    38ms     ✗
node-2      1.1ms    —        37ms     ✗
node-3      38ms     37ms     —        142ms
node-4      ✗        ✗        141ms    —
```

这张图能回答传统探针回答不了的问题:**我的东京节点到法兰克福节点为什么慢?是法兰克福本身慢,还是东京出口的问题?哪台机器的入站端口被防火墙挡了?**

## 功能

**服务器监控**(每台 Agent 采集并上报):
- CPU、内存、Swap 使用率
- 磁盘(每个挂载点)
- 网络上下行速率(bytes/s,非累计值)
- 负载均值(1/5/15 分钟)
- TCP 连接数、进程数、运行时长
- 温度传感器(平台支持时)

**核心能力**:
- 节点互联延迟网格(招牌特性,见上)
- 实时仪表盘(WebSocket 推送,无需刷新)
- Token 鉴权:只有预注册的 Agent 才能上报
- 单二进制:UI 内嵌(`go:embed`),无独立前端构建
- 零外部依赖:不需要 MySQL/Redis,配置存 JSON 文件
- 跨平台:Linux / macOS / Windows / FreeBSD,静态链接(CGO 关闭)

## 架构

```
┌─────────────────────────────────────────────────────┐
│                Larry Dashboard (Server)              │
│                                                       │
│   HTTP :80  ┌─ /             → 内嵌仪表盘 (HTML+JS)  │
│             ├─ /ws            → 浏览器实时快照         │
│             ├─ /api/agent     → Agent WebSocket       │
│             └─ /api/snapshot  → REST 导出             │
│                                                       │
│   内存 Store:当前指标 + 64 点历史 + mesh 矩阵        │
│   Mesh 调度器:每 15s 下发 pairwise 探测指令          │
└──────────────┬───────────────────┬───────────────────┘
               │ WebSocket          │ WebSocket
        ┌──────▼──────┐      ┌──────▼──────┐
        │  Agent #1   │◀────▶│  Agent #2   │   ← Agent 间 TCP 探测
        │  :36510     │ mesh │  :36510     │     (延迟网格的数据来源)
        └─────────────┘      └─────────────┘
```

Agent 主动连接 Dashboard(出站 WebSocket),所以 Agent 不需要公网入站端口即可上报指标。**延迟网格**需要 Agent 的探测端口(`:36510` 默认)对其他 Agent 可达——这一要求本身就揭示了哪些节点的入站是被挡住的。

## 快速开始

### 1. 构建

```bash
git clone <your-repo-url> larry
cd larry
make            # 产出 bin/larry-server 和 bin/larry-agent
```

或交叉编译(生成各平台二进制到 `bin/`):

```bash
make cross
```

### 2. 启动 Dashboard

```bash
./bin/larry-server --addr :80 --registry agents.json
```

打开浏览器访问 `http://<服务器IP>/`,此时还没有数据。

### 3. 注册 Agent(生成 Token)

Token 只在创建时显示一次:

```bash
./bin/larry-server token add tokyo-1
# 输出:
#   Agent registered.
#     ID:    1
#     Name:  tokyo-1
#     Token: 3f9c...e2a1
#   Configure the agent:
#     larry-agent --server ws://<dashboard-ip>:80 --token 3f9c...e2a1
```

其他 token 管理命令:

```bash
./bin/larry-server token list           # 查看已注册 Agent
./bin/larry-server token remove 2       # 吊销某 Agent 的 token
```

### 4. 在被监控机器上运行 Agent

```bash
# 下载 larry-agent 到目标服务器,然后:
./larry-agent --server ws://<dashboard-ip>:80 --token <token>
```

如果 Agent 在 NAT 后,探测端口需要能被其他 Agent 访问到延迟网格才会填满。可显式指定可达地址:

```bash
./larry-agent --server ws://<dashboard-ip>:80 --token <tok> --probe-host <本机公网IP>
```

回到 Dashboard,几秒后服务器就上线了;等 2 个以上 Agent 在线,延迟网格热力图就会渲染出来。

## CLI 参考

### `larry-server`

```
--addr <addr>           监听地址 (默认 :80)
--registry <path>       agents.json 路径 (默认 agents.json)
--broadcast <dur>       浏览器快照推送间隔 (默认 2s)
--mesh-interval <dur>   延迟网格探测间隔 (默认 15s)
--offline-ttl <dur>     多久无上报视为离线 (默认 30s)
```

子命令:`token add|list|remove`、`version`

### `larry-agent`

```
--server <url>          Dashboard 地址 ws://host:port (必填)
--token <tok>           Agent token (必填,来自 token add)
--probe-addr <addr>     探测端口监听地址 (默认 :36510)
--probe-host <host>     其他 Agent 拨号本机时用的可达主机 (NAT 后用)
--interval <sec>        上报间隔秒 (默认 5,服务端可覆盖)
--name <name>           覆盖上报的主机名
--once                  单次会话后退出 (自测用)
```

## 配置与运维

- **Agent 注册表**:`agents.json`(JSON 数组,可手编)记录 `{id,name,token}`。Dashboard 启动时加载,token 增删通过 CLI 完成并即时落盘。
- **历史数据**:当前指标 + 最近 64 个采样点存于内存,进程重启即清空。Larry 定位为实时运维探针而非长期指标仓库;如需长期留存,定时拉取 `/api/snapshot` 写入你自己的 TSDB。
- **反向代理**:Dashboard 放在 Nginx/Caddy 后面时,务必转发 `Upgrade` / `Connection` 头给 `/ws` 与 `/api/agent`(否则 WebSocket 升级失败)。

## 开发

```bash
make fmt      # 格式化并检查
make vet      # go vet
make test     # 跑测试(含 mesh 探测握手自检)
make          # 构建
```

项目结构:

```
larry/
├── cmd/
│   ├── larry-server/    # Dashboard 入口 + token CLI
│   └── larry-agent/     # Agent 入口
├── internal/
│   ├── protocol/        # WebSocket 消息定义(共享)
│   ├── server/          # hub / store / mesh / registry / HTTP
│   └── agent/           # 采集器 / 探测端口 / 会话循环
└── web/                 # 内嵌仪表盘(HTML + 原生 JS,无构建)
```

## 路线图

- [ ] 告警闭环:CPU/内存/离线/延迟阈值 → 邮件/Telegram/飞书/钉钉
- [ ] 服务监控:HTTP / TCP / ICMP / TLS 证书
- [ ] 指标历史持久化(可选 SQLite,默认关闭)
- [ ] 多用户与分组
- [ ] Web 终端与文件管理(可选,默认关闭)

## 致谢

设计参考了 [哪吒探针 Nezha](https://github.com/nezhahq/nezha) 与 [Komari](https://github.com/komari-monitor/komari) 的 Dashboard+Agent 范式。Larry 在此之上做了**减法**(单二进制、无外部 DB、内嵌 UI)和**加法**(节点互联延迟网格)。

## License

MIT — 见 [LICENSE](LICENSE)。

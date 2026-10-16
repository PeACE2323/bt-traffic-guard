# BT Traffic Guard

一个专为 **Unraid / NAS** 设计的 BT 上传流量监控与自动限速工具。

它持续采集 **qBittorrent** 和 **Transmission** 的上传速度与累计流量（**区分 IPv4 / IPv6**），
当在自定义周期（每天 / 每周 / 每月 / 滚动窗口）内上传总量达到你设定的阈值时，
**自动下发上传限速**；进入新周期后**自动解除**。

> 典型场景：PT 站有每日上传量考核，或者你的宽带/机场有月流量上限。
> 希望「白天放开跑、跑够了就自动踩刹车」，而不是整天盯着后台手动调速。

---

## 功能特性

| 功能 | 说明 |
|---|---|
| 多客户端 | 同时监控任意数量的 qBittorrent / Transmission 实例 |
| IPv4 / IPv6 分开统计 | 两条曲线独立展示，可分别设置阈值与限速 |
| 自定义周期 | 自然日 / 自然周 / 自然月 / 自然小时 / 滚动窗口 |
| 自动限速与恢复 | 达阈值即限速，新周期自动恢复；退出容器时也会恢复，绝不把下载器留在限速状态 |
| 防抖动 | 支持冷却期，避免在阈值附近反复触发/解除 |
| 热重载 | 改配置即时生效，**不会**丢失当前周期进度与限速状态 |
| Web 面板 | 实时曲线、客户端明细、策略进度、事件日志、在线编辑配置 |
| 通知 | 支持 ntfy、通用 Webhook、Discord、Slack |
| 轻量 | 单二进制，约 **7.5 MB** 静态编译，无运行时依赖 |
| 分族限速 | qBittorrent 4.6+ 支持 IPv4/IPv6 分别限速 |

---

## 快速开始（Unraid）

### 方式一：Unraid Docker 模板（推荐）

1. 把本仓库的 `unraid-template.xml` 上传到 Unraid 的
   `/boot/config/plugins/dockerMan/templates-user/`，并重命名为
   `my-bt-traffic-guard.xml`。
   > 也可以直接在 Unraid 网页 → **Docker** → **Add Container** →
   > 右上角切到 **XML** 视图，把模板内容粘贴进去。

2. 在 **Add Container** 页面的 Template 下拉框中选择 `my-bt-traffic-guard`。

3. 按需填写这几项（其余保持默认即可）：

   | 字段 | 说明 |
   |---|---|
   | **WebUI 端口** | 默认 `8080`，被占用就换一个 |
   | **配置目录** | 默认 `/mnt/user/appdata/bt-traffic-guard/config` |
   | **数据目录** | 默认 `/mnt/user/appdata/bt-traffic-guard/data` |
   | **时区** | 默认 `Asia/Shanghai`。**务必设置正确**，它决定「自然日」从几点开始算 |
   | **qBittorrent 地址** | 例如 `http://192.168.1.10:8080` |
   | **qBittorrent 用户名 / 密码** | 你的 WebUI 登录凭据 |
   | **Transmission 地址** | 例如 `http://192.168.1.10:9091` |
   | **面板访问密码** | 可选，设置后开启 Web 认证 |

4. 点击 **Apply** 启动容器。

5. 浏览器打开 `http://<你的Unraid IP>:8080` 即可看到面板。

> ⚠️ **关于地址填写**：如果容器使用 bridge 网络（Unraid 默认），
> qBittorrent 地址**不能填 `127.0.0.1`**，因为那是容器自己的回环地址。
> 请填 Unraid 的局域网 IP，例如 `http://192.168.1.10:8080`。
> 若希望用 `127.0.0.1`，请把容器的网络模式改成 **Host**。

### 方式二：docker-compose

```bash
git clone https://github.com/PeACE2323/bt-traffic-guard.git
cd bt-traffic-guard
docker compose up -d --build
```

默认把配置放在 `./config`、数据放在 `./data`，并映射 `8080` 端口。

### 方式三：docker run

先克隆并构建镜像：

```bash
git clone https://github.com/PeACE2323/bt-traffic-guard.git
cd bt-traffic-guard
docker build -t bt-traffic-guard:1.0.0 .
```

然后启动容器：

```bash
docker run -d \
  --name bt-traffic-guard \
  --restart unless-stopped \
  -p 8080:8080 \
  -e TZ=Asia/Shanghai \
  -e BTG_QBITTORRENT_URL=http://192.168.1.10:8080 \
  -e BTG_QBITTORRENT_USERNAME=admin \
  -e BTG_QBITTORRENT_PASSWORD=adminadmin \
  -e BTG_TRANSMISSION_URL=http://192.168.1.10:9091 \
  -v /mnt/user/appdata/bt-traffic-guard/config:/config \
  -v /mnt/user/appdata/bt-traffic-guard/data:/data \
  bt-traffic-guard:1.0.0
```

> 国内网络构建慢的话，把构建命令换成：
> `docker build --build-arg GOPROXY=https://goproxy.cn,direct -t bt-traffic-guard:1.0.0 .`

---

## 配置说明

首次启动会在配置目录自动生成 `config.yaml`。
完整的带注释示例见 [`config.example.yaml`](config.example.yaml)。

编辑方式二选一：

- 直接编辑挂载出来的 `config.yaml`（**保存后 3 秒内自动热重载**）；
- 或者打开 Web 面板 → **配置** 标签页 → 在线编辑 → 点「保存并应用」。

### 单位约定

| 类型 | 单位 | 可用写法 |
|---|---|---|
| 阈值 `threshold_*` | 字节 | `500GiB`、`1.5TB`、`536870912000` |
| 限速 `limit_*` | **字节/秒** | `1048576`（=1 MiB/s）、`1MiB`、`512K`、`0`（不限速） |
| 周期 `window` | 时长 | `30m`、`24h`、`7d`、`30d`、`1w` |

> `GiB` 是 1024 进制，`GB` 是 1000 进制；限速写 `0` 表示完全不限速。

### 最小可用配置

```yaml
clients:
  - name: qbittorrent
    type: qbittorrent
    url: "http://192.168.1.10:8080"
    username: "admin"
    password: "adminadmin"

policies:
  - name: daily-500g
    clients: ["*"]
    window: 24h
    align: day            # 自然日，每天 00:00 重置
    threshold_total: 500GiB
    limit_total: 1048576  # 限速到 1 MiB/s
    resume_on_window_reset: true
    cooldown: 10m         # 触发后至少保持 10 分钟
```

### 周期对齐方式 `align`

| 取值 | 含义 |
|---|---|
| `day` | 自然日，每天 00:00 归零（**最常用**，契合「每日上传量考核」） |
| `week` | 自然周，每周一 00:00 归零 |
| `month` | 自然月，每月 1 日 00:00 归零 |
| `hour` | 自然小时 |
| `rolling` | 滚动窗口，从容器启动起算，每 `window` 时长归零一次 |

### IPv4 / IPv6 分开设置

```yaml
policies:
  # IPv6 走小水管：单独设更严的阈值与限速
  - name: ipv6-cap
    clients: ["qbittorrent"]
    window: 24h
    align: day
    threshold_ipv6: 100GiB
    limit_ipv6: 524288      # 仅限 IPv6 到 512 KiB/s，IPv4 不受影响
```

> 分族限速需要 **qBittorrent 4.6 或更高版本**。
> 低版本或 Transmission 会自动回落到全局限速（取分族值中较宽松的一个）。

### 阈值逻辑

`threshold_total`、`threshold_ipv4`、`threshold_ipv6` 之间是 **或** 的关系，
任意一个被满足就会触发限速。设为 `0` 表示不判断该项。

限速持续到 **进入新周期** 且 **冷却期已过**，然后自动恢复到触发前的限速值。

---

## 通知配置

### ntfy（推荐，手机推送最简单）

1. 手机安装 ntfy App，订阅一个 topic（比如 `my-bt-guard`）。
2. 配置：

```yaml
notify:
  enabled: true
  notify_on: ["speedup", "resume", "error"]
  ntfy:
    enabled: true
    url: "https://ntfy.sh"      # 自建填 http://192.168.1.10:8081
    topic: "my-bt-guard"
    priority: "high"
```

### 通用 Webhook / Discord / Slack

```yaml
notify:
  enabled: true
  webhook:
    enabled: true
    url: "https://discord.com/api/webhooks/xxx/yyy"
    format: discord             # generic / discord / slack
```

推送的 JSON 字段（`format: generic`）：
`event`、`title`、`policy`、`hostname`、`time`、`message`、`clients`、`window_bytes`、`threshold`、`old_limit`、`new_limit`。

---

## Web 面板

访问 `http://<NAS IP>:8080`：

- **总览** — 实时上传速度曲线（IPv4 / IPv6 分色）、周期累计进度、限速状态、客户端明细表
- **策略** — 每条策略的周期进度、阈值、当前累计量，可手动「解除限速」或「测试限速」
- **事件日志** — 每次触发/解除/失败都有时间戳与详情
- **配置** — 在线编辑 YAML，保存即热重载

设置环境变量 `BTG_WEB_PASSWORD` 即可开启 Basic 认证（用户名默认 `admin`，可用 `BTG_WEB_USERNAME` 修改）。

---

## 环境变量

所有配置项都可以用环境变量覆盖（优先级高于 YAML）：

| 变量 | 说明 |
|---|---|
| `BTG_CONFIG` | 配置文件路径，默认 `/config/config.yaml` |
| `BTG_LISTEN` | Web 监听地址，默认 `:8080` |
| `BTG_DATA_DIR` | 数据目录，默认 `/data` |
| `BTG_LOG_LEVEL` | `debug` / `info` / `warn` / `error` |
| `BTG_POLL_INTERVAL` | 采集间隔，如 `5s` |
| `BTG_TIMEZONE` | 时区，如 `Asia/Shanghai` |
| `BTG_QBITTORRENT_URL` / `_USERNAME` / `_PASSWORD` | qBittorrent 连接信息 |
| `BTG_TRANSMISSION_URL` / `_USERNAME` / `_PASSWORD` | Transmission 连接信息 |
| `BTG_WEB_PASSWORD` / `BTG_WEB_USERNAME` | 面板认证 |
| `TZ` | 容器时区 |

---

## 从源码构建

需要 Go 1.24+：

```bash
go build -trimpath -ldflags "-s -w" -o bt-traffic-guard ./cmd/bt-traffic-guard
./bt-traffic-guard -config ./config.yaml
```

交叉编译到 Linux（用于 Docker）：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bt-traffic-guard ./cmd/bt-traffic-guard
```

---

## 工作原理

```
   ┌──────────────┐   轮询 API     ┌──────────────────┐
   │ qBittorrent  │◀──────────────│                  │
   └──────────────┘               │  BT Traffic      │
   ┌──────────────┐   轮询 API     │  Guard           │
   │ Transmission │◀──────────────│                  │
   └──────────────┘               │  ┌────────────┐  │
          ▲                       │  │ 流量累计器 │  │
          │  下发限速              │  └────────────┘  │
          └───────────────────────│  ┌────────────┐  │
                                  │  │ 策略状态机 │  │
                                  │  └────────────┘  │
                                  └──────────────────┘
                                           │
                                           ▼
                                    Web 面板 / 通知
```

**流量累计**优先使用客户端自报的 `uploadedBytes` 差值（精确，不受采样抖动影响）；
当客户端重启导致该值归零时，自动退化为「速率 × 时间」的梯形积分，避免出现负差值把累计量冲掉。

**状态机**：`normal` —(累计量 ≥ 阈值)→ `limited` —(进入新周期 且 冷却期已过)→ `normal`。

---

## 关于 IPv4 / IPv6 统计的说明（请务必阅读）

这是本项目最需要说清楚的一点：

- **qBittorrent** 的 WebUI API 只提供**合计**上传速度与累计量，
  不直接区分地址族。本项目通过 `/api/v2/sync/maindata` 的 peer 明细，
  按 peer 的 IP 地址把流量归因到 IPv4 / IPv6，剩余无法归因的部分记为 `未分类`。

- **Transmission** 的 RPC 同样只给合计值。本实现通过 `torrent-get` 的 peer 列表，
  按**各地址族的连接数占比**来分摊速度和累计量，属于**估算**。

因此：

- 如果你的策略只需要「总量」控制，用 `threshold_total`，**结果完全准确**。
- 如果你需要**精确**的 IPv4 / IPv6 分族统计，客户端 API 无法提供，
  需要在网络层抓包（例如在 Unraid 上统计 qBittorrent 容器的 veth 流量，
  或使用 `nftables` / `iftop` 之类工具按协议族计数）——这超出了本容器单靠 API 能实现的范围。
- Web 面板中，分族数据会标注来源；当客户端不支持分族限速时，
  分族限速配置会自动回落到全局限速，并在事件日志中说明。

`未分类` 流量在判断 `threshold_total` 时会正常计入；
但在判断 `threshold_ipv4` / `threshold_ipv6` 时不会被算进去。

---

## 常见问题

**Q：容器日志报「登录 qBittorrent 失败：IP 已被临时封禁」？**
A：qBittorrent 默认在多次登录失败后封禁来源 IP。
先确认账号密码正确，然后在 qBittorrent 设置里解除封禁，或把
`WebUI → 安全 → 封禁前允许的失败次数` 调大。

**Q：上传速度一直是 0 / 客户端显示异常？**
A：点客户端那一行的「测试」按钮看具体报错。最常见的原因是
容器用了 bridge 网络却把地址填成了 `127.0.0.1`。
另外确认 qBittorrent 的 WebUI 允许来自局域网的连接（可能需要关闭「仅允许本地主机」）。

**Q：改了 `config.yaml` 但没生效？**
A：程序每 3 秒检查一次文件修改时间，稍等几秒即可；
也可以在面板点「配置」→「重新载入」。
如果配置有语法错误，程序会**保留原配置继续运行**并在日志中报错，不会崩溃。

**Q：为什么限速后过一会儿又自动解除了？**
A：这是设计行为——进入新周期后自动恢复。
如果希望触发后一直保持限速，把 `resume_on_window_reset` 设为 `false`。

**Q：周期还没结束就重置了累计量？**
A：检查 `align` 设置。`day` 是自然日（每天 00:00 重置），
不是「从触发时刻起 24 小时」。想要后者请用 `rolling`。
同时确认容器 `TZ` 时区设置正确。

**Q：能不能只监控不限制？**
A：把 `limit_total` 设为 `0`（不限速），此时策略仍会记录事件与推送通知，
但实际不会改变客户端限速（触发时会给出 1 MiB/s 的保守兜底值，
如需纯监控请把对应策略的 `enabled` 设为 `false` 并参考面板曲线）。

**Q：容器停止后下载器还被限速着？**
A：程序在收到 SIGTERM 时会尝试恢复所有处于限速状态的客户端
（若为 `docker stop`，请留出默认 10 秒的停止超时）。

---

## 项目结构

```
bt-traffic-guard/
├── cmd/bt-traffic-guard/main.go      # 程序入口、组件装配、热重载
├── internal/
│   ├── config/                       # 配置加载、校验、周期边界计算
│   ├── client/                       # qBittorrent / Transmission 客户端
│   ├── traffic/                      # 流量累计与实时曲线
│   ├── policy/                       # 限速状态机
│   ├── notify/                       # ntfy / Webhook 通知
│   └── web/                          # HTTP 服务与 Web 面板
├── Dockerfile
├── docker-compose.yml
├── unraid-template.xml               # Unraid Docker 模板
├── config.example.yaml               # 带注释的配置示例
└── README.md
```

---

## 许可证

MIT

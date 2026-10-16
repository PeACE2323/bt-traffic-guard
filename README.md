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
| qBittorrent API Key | 支持 5.2.0+ 的 `Bearer` API Key 认证，也兼容旧版用户名密码，自动选择 |
| 图形化配置 | 所有配置都能在网页上完成，无需预设环境变量或手写 YAML |
| IPv4 / IPv6 分开统计 | 两条曲线独立展示，可分别设置阈值与限速 |
| 自定义周期 | 自然日 / 自然周 / 自然月 / 自然小时 / 滚动窗口 |
| 自动限速与恢复 | 达阈值即限速，新周期自动恢复；退出容器时也会恢复，绝不把下载器留在限速状态 |
| 防抖动 | 支持冷却期，避免在阈值附近反复触发/解除 |
| 热重载 | 改配置即时生效，**不会**丢失当前周期进度与限速状态 |
| Web 面板 | 实时曲线、客户端明细、策略进度、事件日志、图形化配置 |
| 通知 | 支持 ntfy、通用 Webhook、Discord、Slack |
| 预构建镜像 | GitHub Actions 自动发布 amd64/arm64 双架构镜像到 GHCR，直接 `docker pull` |
| 轻量 | 单二进制，约 **7.5 MB** 静态编译，无运行时依赖 |
| 分族限速 | qBittorrent 4.6+ 支持 IPv4/IPv6 分别限速 |

---

## 快速开始（Unraid）

### 方式一：直接拉取预构建镜像（最省事，推荐）

GitHub Actions 会自动构建多架构镜像（amd64 / arm64）并发布到 GHCR，
**不需要在 NAS 上安装 Go 或 Docker 构建环境**：

```bash
docker run -d \
  --name bt-traffic-guard \
  --restart unless-stopped \
  -p 8080:8080 \
  -e TZ=Asia/Shanghai \
  -v /mnt/user/appdata/bt-traffic-guard/config:/config \
  -v /mnt/user/appdata/bt-traffic-guard/data:/data \
  ghcr.io/peace2323/bt-traffic-guard:latest
```

> **注意镜像地址必须全小写**：GitHub 网页上是 `PeACE2323`，
> 但 GHCR 的镜像路径只接受小写，所以是 `ghcr.io/peace2323/...`。
>
> 如果拉取时报 `denied` / `unauthorized`：GHCR 的包默认可能是私有可见性。
> 到 GitHub 的 **你的头像 → Packages → bt-traffic-guard → Package settings**
> 把可见性改成 **Public** 即可匿名拉取。
> 或者登录一次：`docker login ghcr.io -u PeACE2323`（密码填一个有 `read:packages` 权限的 PAT）。

**启动后所有配置都在网页上完成，不需要提前设置任何环境变量。**
打开 `http://<你的Unraid IP>:8080` → **配置** 标签页 →
填写 qBittorrent / Transmission 的地址与凭据 → 保存，立即生效。

### 方式二：Unraid Docker 模板

1. 把本仓库的 `unraid-template.xml` 上传到 Unraid 的
   `/boot/config/plugins/dockerMan/templates-user/`，并重命名为
   `my-bt-traffic-guard.xml`。
   > 也可以直接在 Unraid 网页 → **Docker** → **Add Container** →
   > 右上角切到 **XML** 视图，把模板内容粘贴进去。

2. 在 **Add Container** 页面的 Template 下拉框中选择 `my-bt-traffic-guard`。

3. 通常只需要填这四项，连接信息全部留空即可：

   | 字段 | 说明 |
   |---|---|
   | **WebUI 端口** | 默认 `8080`，被占用就换一个 |
   | **配置目录** | 默认 `/mnt/user/appdata/bt-traffic-guard/config` |
   | **数据目录** | 默认 `/mnt/user/appdata/bt-traffic-guard/data` |
   | **时区** | 默认 `Asia/Shanghai`。**务必设置正确**，它决定「自然日」从几点开始算 |

4. 点击 **Apply** 启动容器。

5. 浏览器打开 `http://<你的Unraid IP>:8080`，在 **配置** 页填写下载器信息。

> 模板里保留了几个可选的环境变量（如 `BTG_QBITTORRENT_URL`）。
> 它们只在你想「用环境变量强制覆盖配置文件」时才需要填，
> 平时留空即可——留空时以网页/`config.yaml` 里的设置为准。

> ⚠️ **关于地址填写**：如果容器使用 bridge 网络（Unraid 默认），
> qBittorrent 地址**不能填 `127.0.0.1`**，因为那是容器自己的回环地址。
> 请填 Unraid 的局域网 IP，例如 `http://192.168.1.10:8080`。
> 若希望用 `127.0.0.1`，请把容器的网络模式改成 **Host**。

### 方式三：docker-compose

```bash
git clone https://github.com/PeACE2323/bt-traffic-guard.git
cd bt-traffic-guard
docker compose up -d
```

默认拉取 GHCR 上的镜像；想从源码构建加 `--build`。

### 方式四：从源码构建镜像

```bash
git clone https://github.com/PeACE2323/bt-traffic-guard.git
cd bt-traffic-guard
docker build -t bt-traffic-guard:1.0.0 .
```

国内网络构建慢的话：

```bash
docker build --build-arg GOPROXY=https://goproxy.cn,direct -t bt-traffic-guard:1.0.0 .
```

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
    # 二选一：API Key（5.2.0+）或用户名密码
    api_key: "qbt_xxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    # username: "admin"
    # password: "adminadmin"

policies:
  - name: daily-500g
    clients: ["*"]
    window: 24h
    align: day            # 自然日，每天 00:00 重置
    threshold_total: 500GiB
    limit_total: 1MiB     # 限速到 1 MiB/s
    resume_on_window_reset: true
    cooldown: 10m         # 触发后至少保持 10 分钟
```

### qBittorrent 认证：API Key 与用户名密码

qBittorrent 从 **v5.2.0** 起引入了 WebUI API Key，本项目两种方式都支持，**填了 API Key 就优先用它**。

| | API Key | 用户名密码 |
|---|---|---|
| 最低版本 | 5.2.0 | 任意 |
| 机制 | `Authorization: Bearer qbt_xxx`，无状态 | `POST /api/v2/auth/login` 换 Cookie |
| 失败处理 | Key 无效直接报错 | 连续失败会**触发 IP 临时封禁** |
| 获取方式 | 设置 → WebUI → API Key → 生成 | 你设置的 WebUI 账号 |

API Key 的格式为 `qbt_` 加 28 位随机字符。生成后填到配置的 `api_key` 字段即可：

```yaml
clients:
  - name: qbittorrent
    type: qbittorrent
    url: "http://192.168.1.10:8080"
    api_key: "qbt_abcdefghijklmnopqrstuvwxyz1234"
```

> - 使用 API Key 时**不会**再调用登录/登出接口（官方明确 API Key 不能用于 `auth` 端点），
>   因此不会因为反复登录而被封 IP。
> - 如果填了 API Key 但 qBittorrent 版本低于 5.2.0，启动时日志会给出明确警告。
> - Key 被轮换后旧 Key 立即失效，此时日志会提示「API Key 无效或已被轮换」，
>   到面板里换一个新的即可。
> - **Transmission 不支持 API Key**，只能用用户名密码（RPC 基本认证）。

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
- **配置** — 分四个子页：

| 子页 | 能做什么 |
|---|---|
| **下载器** | 图形化增删改 qBittorrent / Transmission：名称、类型、地址、API Key 或用户名密码、超时、TLS 跳过校验。可实时看到每个下载器的连接状态与当前速度 |
| **限速策略** | 图形化增删改策略：周期、对齐方式、阈值（合计 / 仅 IPv4 / 仅 IPv6）、限速值、冷却期、作用对象 |
| **通用与通知** | 端口、采集间隔、日志级别、时区、面板认证、ntfy 与 Webhook 配置 |
| **高级（YAML）** | 直接编辑 `config.yaml` 原文，适合批量修改或使用表单未暴露的选项 |

**图形化表单与 YAML 是同一份配置**，两边随时可以互相切换，改动都会同步。

> 安全说明：表单读取接口**绝不回传明文密码或 API Key**，
> 只返回一个掩码占位与首尾可见的提示串（如 `qbt_abcd…1234`）。
> 前端原样回传掩码时服务端会沿用已保存的值；
> 只有你明确点击「清除」或填入新值才会改动。

设置环境变量 `BTG_WEB_PASSWORD` 即可开启 Basic 认证（用户名默认 `admin`，可用 `BTG_WEB_USERNAME` 修改），
也可以在面板的 **配置 → 通用与通知** 里开启。

---

## 环境变量

**所有配置都可以在网页上完成，环境变量完全是可选的。**
它们只在你想用环境变量强制覆盖配置文件时才有用（优先级高于 YAML）。

| 变量 | 说明 |
|---|---|
| `BTG_CONFIG` | 配置文件路径，默认 `/config/config.yaml` |
| `BTG_LISTEN` | Web 监听地址，默认 `:8080` |
| `BTG_DATA_DIR` | 数据目录，默认 `/data` |
| `BTG_LOG_LEVEL` | `debug` / `info` / `warn` / `error` |
| `BTG_POLL_INTERVAL` | 采集间隔，如 `5s` |
| `BTG_TIMEZONE` | 时区，如 `Asia/Shanghai` |
| `BTG_QBITTORRENT_URL` / `_USERNAME` / `_PASSWORD` / `_API_KEY` | qBittorrent 连接信息 |
| `BTG_TRANSMISSION_URL` / `_USERNAME` / `_PASSWORD` | Transmission 连接信息 |
| `BTG_WEB_PASSWORD` / `BTG_WEB_USERNAME` | 面板认证 |
| `TZ` | 容器时区 |

> ⚠️ 环境变量的优先级**高于** `config.yaml`。
> 如果你设置了 `BTG_QBITTORRENT_URL`，那么在网页上修改这个地址**不会生效**——
> 此时需要把环境变量删掉再改。这也是为什么容器默认不设置这些变量。

---

## 镜像构建（GitHub Actions）

仓库内置了 [`.github/workflows/build.yml`](.github/workflows/build.yml)：

- **推送到 `main`** → 跑测试 → 构建并推送 `:latest` 与 `:main`
- **打 `v*` 标签**（如 `v1.0.0`）→ 额外推送 `:1.0.0`、`:1.0`、`:1`
- **Pull Request** → 只跑测试与构建验证，不推送
- **手动触发** → 可自定义标签

每次发布都是 **linux/amd64 + linux/arm64** 双架构，覆盖普通 x86 服务器与 ARM 设备（如树莓派、部分群晖）。

**测试不通过就不会构建镜像**，避免推出去一个坏的版本。

发布一个正式版本的流程：

```bash
git tag v1.0.0
git push origin v1.0.0
```

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

跑测试：

```bash
go test ./...
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

**Q：`docker pull ghcr.io/peace2323/bt-traffic-guard:latest` 报 denied？**
A：GHCR 的包首次发布后可能仍是私有可见性。到 GitHub 的
**你的头像 → Packages → bt-traffic-guard → Package settings → Change visibility** 改成 Public。
或者先 `docker login ghcr.io -u PeACE2323`（密码用有 `read:packages` 权限的 PAT）。

**Q：日志报「API Key 无效或已被轮换」？**
A：在 qBittorrent 里重新生成一个 Key（设置 → WebUI → API Key），
然后到面板的 **配置 → 下载器** 里把新的 Key 粘贴进去保存。
注意生成新 Key 会立即使旧 Key 失效。

**Q：日志报「客户端 xxx 不支持 API Key 认证，该功能需要 5.2.0 及以上版本」？**
A：你的 qBittorrent 版本低于 5.2.0，没有 API Key 机制。
把 `api_key` 清空，改用用户名密码即可（或者在面板里清除 API Key 并填上账号密码）。

**Q：面板上改了地址，但好像没生效？**
A：检查是否设置了对应的环境变量（如 `BTG_QBITTORRENT_URL`）。
**环境变量优先级高于配置文件**，如果设置了，网页上的修改会被它覆盖。
把该环境变量删掉再改即可。容器默认不设置这些变量，就是为了避免这个坑。

**Q：在面板上能改密码吗？会不会把密码显示出来？**
A：能改，但**不会回显**。表单读取接口只返回掩码和首尾提示串（如 `qbt_abcd…1234`）。
只要不动那个输入框，保存时就会沿用已保存的值；
想换新值就直接输入，想彻底清除就点旁边的「清除」按钮。

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
│   └── web/                          # HTTP 服务、图形化表单与 Web 面板
├── .github/workflows/build.yml       # 自动测试并发布镜像到 GHCR
├── Dockerfile
├── docker-compose.yml
├── unraid-template.xml               # Unraid Docker 模板
├── config.example.yaml               # 带注释的配置示例
└── README.md
```

---

## 许可证

MIT

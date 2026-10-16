package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// qBittorrent 客户端
// ---------------------------------------------------------------------------
//
// 依赖 WebUI API v2：
//
//	POST /api/v2/auth/login                    登录，返回 SID Cookie
//	GET  /api/v2/transfer/info                 全局传输信息（含 up_info_speed / up_info_data）
//	POST /api/v2/transfer/setUploadLimit       设置全局上传限速（字节/秒，0 表示无限）
//	GET  /api/v2/torrents/info                  种子列表（用于统计连接数与地址族分布）
//	GET  /api/v2/app/version                    版本号
//
// 关于 IPv4 / IPv6 统计：
// qBittorrent 的 WebUI API 只暴露「合计」上传速度与累计量，不区分地址族。
// 因此本实现通过 /api/v2/torrents/info 的 peer 分布做族归因估算：
// 读取每个种子的连接数与已连接 peer 信息，按 peer 地址推断族。
// 若接口未提供足够信息（多数版本如此），则退化为：
//   - 若配置中只关心一个族，把全部流量归到该族；
//   - 否则归入 unknown，并在 stats 中标记 SupportsFamilyLimit=false。
//
// 同时，qBittorrent 4.6+ 支持 preferences 中的分族限速（ipv4/ipv6 上传限速），
// 本实现会在可用时通过 /api/v2/app/setPreferences 下发分族限速。

// QBittorrent 实现 Client 接口。
type QBittorrent struct {
	name     string
	doer     *httpDoer
	prefsMtx sync.Mutex

	// 登录状态
	authMtx  sync.Mutex
	loggedIn bool
	lastAuth time.Time

	// 连接信息缓存
	verMtx      sync.Mutex
	version     string
	versionAt   time.Time
	hasFamLimit bool

	// peer 族分布缓存（由 /api/v2/sync/maindata 或 torrents/info 推断）
	peerFamilyMtx sync.Mutex
	peerSamples   map[Family]int64

	tracker limitTracker
}

// NewQBittorrent 构造 qBittorrent 客户端。
func NewQBittorrent(opt Options) *QBittorrent {
	return &QBittorrent{
		name:        opt.Name,
		doer:        newHTTPDoer(opt.URL, opt.Username, opt.Password, opt.Timeout, opt.InsecureSkipVerify, false),
		peerSamples: map[Family]int64{},
	}
}

// Name 返回客户端名。
func (q *QBittorrent) Name() string { return q.name }

// Type 返回客户端类型。
func (q *QBittorrent) Type() string { return "qbittorrent" }

// login 执行登录，返回 SID cookie。已登录时直接复用。
func (q *QBittorrent) login(ctx context.Context) error {
	q.authMtx.Lock()
	defer q.authMtx.Unlock()

	if q.loggedIn && time.Since(q.lastAuth) < 20*time.Minute {
		return nil
	}

	form := url.Values{}
	form.Set("username", q.doer.user)
	form.Set("password", q.doer.pass)
	// 允许 qBittorrent 的「对本地主机跳过认证」以及 IP 封禁场景下的正常流程。
	form.Set("referer", q.doer.base)

	data, code, err := q.doer.postForm(ctx, "/api/v2/auth/login", form, nil)
	if err != nil {
		return fmt.Errorf("登录 qBittorrent 失败: %w", err)
	}
	if code == 403 {
		return fmt.Errorf("登录 qBittorrent 被拒绝（HTTP 403）：连续失败次数过多，请在 WebUI 中解除 IP 封禁，或检查账号密码")
	}
	if code != 200 {
		return statusError("登录 qBittorrent", code, data)
	}

	body := strings.TrimSpace(strings.ToLower(string(data)))
	if body != "ok." && body != "ok" {
		if strings.Contains(body, "banned") {
			return fmt.Errorf("登录 qBittorrent 失败：IP 已被临时封禁，请稍后再试或在 WebUI 中解除")
		}
		if strings.Contains(body, "fail") {
			return fmt.Errorf("登录 qBittorrent 失败：用户名或密码错误")
		}
		return fmt.Errorf("登录 qBittorrent 返回未知结果: %q", strings.TrimSpace(string(data)))
	}

	q.loggedIn = true
	q.lastAuth = time.Now()
	return nil
}

// invalidateAuth 让下次请求重新登录（遇到 403 时调用）。
func (q *QBittorrent) invalidateAuth() {
	q.authMtx.Lock()
	q.loggedIn = false
	q.authMtx.Unlock()
}

// apiGet 带自动登录重试的 GET。
func (q *QBittorrent) apiGet(ctx context.Context, path string) ([]byte, error) {
	if err := q.login(ctx); err != nil {
		return nil, err
	}
	data, code, err := q.doer.get(ctx, path)
	if err != nil {
		return nil, err
	}
	if code == 403 || code == 401 {
		q.invalidateAuth()
		if err := q.login(ctx); err != nil {
			return nil, err
		}
		data, code, err = q.doer.get(ctx, path)
		if err != nil {
			return nil, err
		}
	}
	if code != 200 {
		return nil, statusError("qBittorrent GET "+path, code, data)
	}
	return data, nil
}

// apiPost 带自动登录重试的 POST（表单）。
func (q *QBittorrent) apiPost(ctx context.Context, path string, form url.Values) ([]byte, error) {
	if err := q.login(ctx); err != nil {
		return nil, err
	}
	data, code, err := q.doer.postForm(ctx, path, form, nil)
	if err != nil {
		return nil, err
	}
	if code == 403 || code == 401 {
		q.invalidateAuth()
		if err := q.login(ctx); err != nil {
			return nil, err
		}
		data, code, err = q.doer.postForm(ctx, path, form, nil)
		if err != nil {
			return nil, err
		}
	}
	if code != 200 {
		return nil, statusError("qBittorrent POST "+path, code, data)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// 版本与能力探测
// ---------------------------------------------------------------------------

// detectVersion 查询 qBittorrent 版本号并缓存 10 分钟。
func (q *QBittorrent) detectVersion(ctx context.Context) string {
	q.verMtx.Lock()
	if q.version != "" && time.Since(q.versionAt) < 10*time.Minute {
		v := q.version
		q.verMtx.Unlock()
		return v
	}
	q.verMtx.Unlock()

	data, err := q.apiGet(ctx, "/api/v2/app/version")
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(data))
	v = strings.TrimPrefix(v, "v")

	q.verMtx.Lock()
	q.version = v
	q.versionAt = time.Now()
	q.hasFamLimit = versionAtLeast(v, 4, 6, 0)
	q.verMtx.Unlock()
	return v
}

// supportsFamilyLimit 返回该版本是否支持分族上传限速。
func (q *QBittorrent) supportsFamilyLimit(ctx context.Context) bool {
	v := q.detectVersion(ctx)
	if v == "" {
		return false
	}
	return versionAtLeast(v, 4, 6, 0)
}

// versionAtLeast 比较语义化版本号，判断 v >= (maj,min,pat)。
func versionAtLeast(v string, maj, min, pat int) bool {
	// 去掉 "-beta1" 之类后缀
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	get := func(i int) int {
		if i >= len(parts) {
			return 0
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return 0
		}
		return n
	}
	a, b, c := get(0), get(1), get(2)
	if a != maj {
		return a > maj
	}
	if b != min {
		return b > min
	}
	return c >= pat
}

// ---------------------------------------------------------------------------
// 采样
// ---------------------------------------------------------------------------

// transferInfo 是 /api/v2/transfer/info 的响应子集。
type transferInfo struct {
	UpInfoSpeed      int64  `json:"up_info_speed"`
	UpInfoData       int64  `json:"up_info_data"`
	DlInfoSpeed      int64  `json:"dl_info_speed"`
	DlInfoData       int64  `json:"dl_info_data"`
	UpRateLimit      int64  `json:"up_rate_limit"`
	DlRateLimit      int64  `json:"dl_rate_limit"`
	ConnectionStatus string `json:"connection_status"`
	GlobalRatio      any    `json:"global_ratio"`
}

// mainData 是 /api/v2/sync/maindata 的响应子集。
type mainData struct {
	ServerState struct {
		UpInfoSpeed   int64 `json:"up_info_speed"`
		UpInfoData    int64 `json:"up_info_data"`
		DlInfoSpeed   int64 `json:"dl_info_speed"`
		UpRateLimit   int64 `json:"up_rate_limit"`
		AlltimeUL     int64 `json:"alltime_ul"`
		TotalPeerConn int   `json:"total_peer_connections"`
	} `json:"server_state"`
	Torrents map[string]struct {
		UpSpeed      int64 `json:"upspeed"`
		UpData       int64 `json:"uploaded"`
		NumLeechs    int   `json:"num_leechs"`
		NumSeeds     int   `json:"num_seeds"`
		NumComplete  int   `json:"num_complete"`
		NumIncomplet int   `json:"num_incomplete"`
	} `json:"torrents"`
	// 部分版本提供 peers 明细
	Peers map[string]map[string]struct {
		UpSpeed int64  `json:"up_speed"`
		Country string `json:"country"`
		IP      string `json:"ip"`
		Flags   string `json:"flags"`
	} `json:"peers"`
}

// Fetch 采集一次 qBittorrent 状态。
func (q *QBittorrent) Fetch(ctx context.Context) (Stats, error) {
	var info transferInfo
	var md mainData
	var infoOK, mdOK bool

	if err := retry(ctx, 2, 300*time.Millisecond, func() error {
		data, err := q.apiGet(ctx, "/api/v2/transfer/info")
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &info)
	}); err == nil {
		infoOK = true
	}

	// maindata 提供 peer 明细，用于地址族归因；失败不影响主流程。
	if data, err := q.apiGet(ctx, "/api/v2/sync/maindata"); err == nil {
		if err := json.Unmarshal(data, &md); err == nil {
			mdOK = true
		}
	}

	if !infoOK && !mdOK {
		return Stats{}, fmt.Errorf("无法获取 qBittorrent 传输信息（transfer/info 与 sync/maindata 均失败）")
	}

	upSpeed := info.UpInfoSpeed
	upData := info.UpInfoData
	dlSpeed := info.DlInfoSpeed
	upLimit := info.UpRateLimit
	if upSpeed == 0 && mdOK {
		upSpeed = md.ServerState.UpInfoSpeed
		upData = md.ServerState.UpInfoData
		dlSpeed = md.ServerState.DlInfoSpeed
		upLimit = md.ServerState.UpRateLimit
	}

	st := Stats{
		Timestamp:           time.Now(),
		UpSpeed:             map[Family]int64{},
		UpTotal:             map[Family]int64{},
		DlSpeed:             dlSpeed,
		PeerCount:           md.ServerState.TotalPeerConn,
		TorrentCount:        len(md.Torrents),
		SpeedLimited:        upLimit > 0,
		CurrentUpLimit:      upLimit,
		SupportsFamilyLimit: q.supportsFamilyLimit(ctx),
		Raw:                 map[string]any{"version": q.detectVersion(ctx)},
	}

	// 按地址族拆分。
	v4, v6, unknown := q.splitByFamily(mdOK, &md, upSpeed, upData)
	st.UpSpeed[FamilyIPv4] = v4.speed
	st.UpTotal[FamilyIPv4] = v4.total
	st.UpSpeed[FamilyIPv6] = v6.speed
	st.UpTotal[FamilyIPv6] = v6.total
	if unknown.speed > 0 || unknown.total > 0 {
		st.UpSpeed[FamilyUnknown] = unknown.speed
		st.UpTotal[FamilyUnknown] = unknown.total
	}

	// 兜底：若三者全为 0 但总速度不为 0，说明归因失败，全部计入 unknown。
	if st.UpSpeed[FamilyIPv4]+st.UpSpeed[FamilyIPv6]+st.UpSpeed[FamilyUnknown] == 0 && upSpeed > 0 {
		st.UpSpeed[FamilyUnknown] = upSpeed
		st.UpTotal[FamilyUnknown] = upData
	}
	// 累计量兜底
	if st.UpTotal[FamilyIPv4]+st.UpTotal[FamilyIPv6]+st.UpTotal[FamilyUnknown] == 0 && upData > 0 {
		st.UpTotal[FamilyUnknown] = upData
	}

	return st, nil
}

// familySplit 是某一地址族的流量。
type familySplit struct {
	speed int64
	total int64
}

// splitByFamily 尝试按 peer 的 IP 地址把上传速度拆分到 v4 / v6。
//
// 说明：qBittorrent 的 maindata.peers 只在部分版本与配置下返回，
// 且提供的是瞬时速率而非累计量。因此：
//   - 速度：按 peer 的 up_speed 直接归属地址族，剩余部分计入 unknown；
//   - 累计量：API 不提供分族累计，按当前速度占比对 up_data 做比例分摊。
func (q *QBittorrent) splitByFamily(mdOK bool, md *mainData, totalSpeed, totalData int64) (familySplit, familySplit, familySplit) {
	var v4, v6 familySplit
	if !mdOK || len(md.Peers) == 0 {
		// 无法归因：全部记为 unknown，由上层按单族配置决定如何处置。
		return v4, v6, familySplit{speed: totalSpeed, total: totalData}
	}

	var sumV4, sumV6 int64
	for _, peerSet := range md.Peers {
		for ip, p := range peerSet {
			addr := p.IP
			if addr == "" {
				addr = ip
			}
			sp := p.UpSpeed
			if sp <= 0 {
				continue
			}
			switch classifyIP(addr) {
			case FamilyIPv4:
				sumV4 += sp
			case FamilyIPv6:
				sumV6 += sp
			}
		}
	}

	total := sumV4 + sumV6
	if total <= 0 {
		return v4, v6, familySplit{speed: totalSpeed, total: totalData}
	}

	// 速度：以 peer 明细为准，剩余部分（tracker/DHT 开销等）按比例分摊。
	v4.speed = sumV4
	v6.speed = sumV6
	if total < totalSpeed {
		rest := totalSpeed - total
		v4.speed += rest * sumV4 / total
		v6.speed += rest * sumV6 / total
	}
	// 累计量：按速度占比分摊总量。
	v4.total = totalData * sumV4 / total
	v6.total = totalData * sumV6 / total

	return v4, v6, familySplit{}
}

// ---------------------------------------------------------------------------
// 限速
// ---------------------------------------------------------------------------

// SetUploadLimit 下发上传限速。
//
// 优先使用 qBittorrent 4.6+ 的分族限速（app/setPreferences 的
// up_limit_ipv4 / up_limit_ipv6），否则回落到全局 transfer/setUploadLimit。
func (q *QBittorrent) SetUploadLimit(ctx context.Context, limit SpeedLimit) error {
	useFamily := limit.UseFamily && q.supportsFamilyLimit(ctx) &&
		(limit.IPv4 >= 0 || limit.IPv6 >= 0)

	if useFamily {
		// 构造 preferences JSON，只下发分族上传限速。
		prefs := map[string]any{}
		if limit.IPv4 >= 0 {
			prefs["up_limit_ipv4"] = limit.IPv4
		}
		if limit.IPv6 >= 0 {
			prefs["up_limit_ipv6"] = limit.IPv6
		}
		if len(prefs) > 0 {
			blob, err := json.Marshal(prefs)
			if err != nil {
				return fmt.Errorf("序列化 qBittorrent preferences 失败: %w", err)
			}
			form := url.Values{}
			form.Set("json", string(blob))
			if _, err := q.apiPost(ctx, "/api/v2/app/setPreferences", form); err == nil {
				q.tracker.record(limit)
				return nil
			}
			// 失败则回落全局限速，不让策略静默失效。
		}
	}

	// 全局限速：需要一个单一数值。
	value := limit.Total
	if value < 0 {
		// 只给了分族值但客户端不支持分族：取最宽松（最大）的一个作为全局值，
		// 以免误伤；若都未设置则不改动。
		switch {
		case limit.IPv4 >= 0 && limit.IPv6 >= 0:
			value = maxInt64(limit.IPv4, limit.IPv6)
		case limit.IPv4 >= 0:
			value = limit.IPv4
		case limit.IPv6 >= 0:
			value = limit.IPv6
		default:
			return nil
		}
	}

	form := url.Values{}
	form.Set("limit", strconv.FormatInt(value, 10))
	if _, err := q.apiPost(ctx, "/api/v2/transfer/setUploadLimit", form); err != nil {
		return fmt.Errorf("设置 qBittorrent 上传限速失败: %w", err)
	}

	q.tracker.record(SpeedLimit{Total: value, IPv4: limit.IPv4, IPv6: limit.IPv6})
	return nil
}

// maxInt64 返回较大值。
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// 地址族判定
// ---------------------------------------------------------------------------

// classifyIP 根据地址字符串判断其地址族。
func classifyIP(s string) Family {
	s = strings.TrimSpace(s)
	if s == "" {
		return FamilyUnknown
	}
	// 去掉可能存在的端口后缀（形如 [::1]:51413 或 1.2.3.4:6881）
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			s = s[1:i]
		}
	} else if strings.Count(s, ":") == 1 {
		if i := strings.LastIndex(s, ":"); i > 0 {
			s = s[:i]
		}
	}
	if strings.Contains(s, ":") {
		return FamilyIPv6
	}
	if strings.Contains(s, ".") {
		return FamilyIPv4
	}
	return FamilyUnknown
}

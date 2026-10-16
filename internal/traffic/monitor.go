// Package traffic 负责把客户端采样转换成「周期内的累计上传流量」，
// 并驱动限速状态机。
//
// 设计要点：
//
//  1. 累计量优先使用客户端自报的 uploadedBytes 差值，
//     而不是「速率 × 采样间隔」的累加值——后者在客户端重启或采样丢失时会漂移。
//  2. 客户端重启会导致 uploadedBytes 归零，此时自动退化为速率积分，
//     避免出现巨大的负差值把累计量冲掉。
//  3. 每个 (客户端, 地址族) 维护独立的基线，周期滚动时重置基线。
package traffic

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/btguard/bt-traffic-guard/internal/client"
)

// ---------------------------------------------------------------------------
// 计数器
// ---------------------------------------------------------------------------

// counter 跟踪单个 (客户端, 地址族) 的周期累计上传量。
type counter struct {
	// lastTotal 是上一次看到的客户端自报累计值。
	lastTotal int64
	// lastSpeed 是上一次采样到的速率，用于积分兜底。
	lastSpeed int64
	// lastSeen 是上一次采样时刻。
	lastSeen time.Time
	// hasBaseline 表示是否已经建立基线（首次采样只建立基线，不计入周期）。
	hasBaseline bool
	// windowBytes 是本周期内累计的字节数。
	windowBytes int64
	// totalBytes 是进程启动以来的累计字节数（用于展示）。
	totalBytes int64
	// resets 记录检测到客户端重启的次数。
	resets int
}

// reset 开始一个新周期，保留基线以免丢掉跨周期的差值。
func (c *counter) reset() {
	c.windowBytes = 0
}

// ---------------------------------------------------------------------------
// 采样快照
// ---------------------------------------------------------------------------

// Sample 是一条用于绘制曲线的采样点。
type Sample struct {
	Timestamp time.Time `json:"t"`
	// UpSpeed 是各地址族的上传速度（字节/秒）。
	UpSpeed map[client.Family]int64 `json:"up"`
	// DlSpeed 是下载速度（字节/秒）。
	DlSpeed int64 `json:"dl"`
	// WindowBytes 是该时刻所在周期的累计上传量。
	WindowBytes int64 `json:"win"`
	// Limited 表示该时刻是否处于限速状态。
	Limited bool `json:"lim"`
}

// ClientSnapshot 是单个客户端在某一时刻的完整视图。
type ClientSnapshot struct {
	Name                string                  `json:"name"`
	Type                string                  `json:"type"`
	URL                 string                  `json:"url"`
	Healthy             bool                    `json:"healthy"`
	LastError           string                  `json:"last_error,omitempty"`
	LastSeen            time.Time               `json:"last_seen"`
	UpSpeed             map[client.Family]int64 `json:"up_speed"`
	UpTotal             map[client.Family]int64 `json:"up_total"`
	WindowBytes         map[client.Family]int64 `json:"window_bytes"`
	TotalBytes          map[client.Family]int64 `json:"total_bytes"`
	DlSpeed             int64                   `json:"dl_speed"`
	PeerCount           int                     `json:"peers"`
	TorrentCount        int                     `json:"torrents"`
	SupportsFamilyLimit bool                    `json:"supports_family_limit"`
	CurrentUpLimit      int64                   `json:"current_up_limit"`
}

// TotalSpeed 返回该客户端所有地址族的上传速度合计。
func (s ClientSnapshot) TotalSpeed() int64 {
	var n int64
	for _, v := range s.UpSpeed {
		n += v
	}
	return n
}

// TotalWindow 返回该客户端周期累计上传量合计。
func (s ClientSnapshot) TotalWindow() int64 {
	var n int64
	for _, v := range s.WindowBytes {
		n += v
	}
	return n
}

// ---------------------------------------------------------------------------
// 监控器
// ---------------------------------------------------------------------------

// Monitor 周期性采集所有客户端并维护累计量。
type Monitor struct {
	registry *client.Registry
	interval time.Duration

	mu sync.RWMutex
	// counters[clientName][family]
	counters map[string]map[client.Family]*counter
	// snapshots 是各客户端最近一次的完整快照。
	snapshots map[string]ClientSnapshot
	// history 是各客户端的实时曲线（环形缓冲）。
	history     map[string][]Sample
	historyCap  int
	lastPollEnd time.Time
	pollCount   int64
	pollErrors  int64
}

// NewMonitor 创建监控器。
func NewMonitor(reg *client.Registry, interval time.Duration, historyCap int) *Monitor {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if historyCap <= 0 {
		historyCap = 720
	}
	m := &Monitor{
		registry:   reg,
		interval:   interval,
		counters:   map[string]map[client.Family]*counter{},
		snapshots:  map[string]ClientSnapshot{},
		history:    map[string][]Sample{},
		historyCap: historyCap,
	}
	// 为每个已知客户端预建计数器。
	for _, c := range reg.All() {
		m.ensureCounters(c.Name())
	}
	return m
}

// ensureCounters 为指定客户端准备好各地址族的计数器。调用方需持有写锁。
func (m *Monitor) ensureCounters(name string) map[client.Family]*counter {
	fams, ok := m.counters[name]
	if !ok {
		fams = map[client.Family]*counter{}
		for _, f := range client.Families() {
			fams[f] = &counter{}
		}
		m.counters[name] = fams
	}
	return fams
}

// Interval 返回采样间隔。
func (m *Monitor) Interval() time.Duration { return m.interval }

// PollOnce 对所有客户端采样一次并更新累计量。
// now 是本次采样的时间戳。
func (m *Monitor) PollOnce(ctx context.Context, now time.Time) {
	clients := m.registry.All()

	// 并发采集，避免某个客户端超时拖慢整体节奏。
	type result struct {
		client *client.Managed
		stats  client.Stats
		err    error
	}
	results := make([]result, len(clients))
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *client.Managed) {
			defer wg.Done()
			st, err := c.Fetch(ctx)
			results[i] = result{client: c, stats: st, err: err}
		}(i, c)
	}
	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range results {
		name := r.client.Name()
		fams := m.ensureCounters(name)

		if r.err != nil {
			m.pollErrors++
			// 采集失败：保留上一次快照，仅更新健康状态，不污染累计量。
			snap := m.snapshots[name]
			snap.Name = name
			snap.Type = r.client.Type()
			snap.Healthy = false
			snap.LastError = r.err.Error()
			snap.LastSeen = r.client.LastSeen()
			m.snapshots[name] = snap
			m.appendHistoryLocked(name, Sample{
				Timestamp:   now,
				UpSpeed:     copyFamilyMap(snap.UpSpeed),
				DlSpeed:     snap.DlSpeed,
				WindowBytes: snap.TotalWindow(),
			})
			continue
		}

		st := r.stats
		st.Timestamp = now

		// 更新每个地址族的累计量。
		window := map[client.Family]int64{}
		total := map[client.Family]int64{}
		for _, fam := range client.Families() {
			cnt := fams[fam]
			speed := st.UpSpeed[fam]
			cum := st.UpTotal[fam]

			if !cnt.hasBaseline {
				// 首次采样：只建立基线，不计入本周期。
				cnt.hasBaseline = true
				cnt.lastTotal = cum
				cnt.lastSpeed = speed
				cnt.lastSeen = now
				window[fam] = cnt.windowBytes
				total[fam] = cnt.totalBytes
				continue
			}

			delta := int64(0)
			if cum > 0 {
				if cum >= cnt.lastTotal {
					// 正常情况：用自报累计量的差值，精确且不受采样抖动影响。
					delta = cum - cnt.lastTotal
				} else {
					// 客户端重启：累计量归零，改用速率积分补上这一段。
					delta = integrate(cnt.lastSpeed, speed, now.Sub(cnt.lastSeen))
					cnt.resets++
				}
				cnt.lastTotal = cum
			} else {
				// 客户端不提供累计量：只能速率积分。
				delta = integrate(cnt.lastSpeed, speed, now.Sub(cnt.lastSeen))
			}

			// 防御：单次采样增量不可能超过「峰值速率 × 间隔」的合理上限。
			if maxDelta := maxInt64(cnt.lastSpeed, speed) * int64(now.Sub(cnt.lastSeen).Seconds()+1) * 2; maxDelta > 0 && delta > maxDelta {
				delta = maxDelta
			}
			if delta < 0 {
				delta = 0
			}

			cnt.windowBytes += delta
			cnt.totalBytes += delta
			cnt.lastSpeed = speed
			cnt.lastSeen = now

			window[fam] = cnt.windowBytes
			total[fam] = cnt.totalBytes
		}

		var totalWindow int64
		for _, v := range window {
			totalWindow += v
		}

		m.snapshots[name] = ClientSnapshot{
			Name:                name,
			Type:                r.client.Type(),
			URL:                 clientURL(r.client),
			Healthy:             true,
			LastSeen:            now,
			UpSpeed:             copyFamilyMap(st.UpSpeed),
			UpTotal:             copyFamilyMap(st.UpTotal),
			WindowBytes:         window,
			TotalBytes:          total,
			DlSpeed:             st.DlSpeed,
			PeerCount:           st.PeerCount,
			TorrentCount:        st.TorrentCount,
			SupportsFamilyLimit: st.SupportsFamilyLimit,
			CurrentUpLimit:      st.CurrentUpLimit,
		}

		m.appendHistoryLocked(name, Sample{
			Timestamp:   now,
			UpSpeed:     copyFamilyMap(st.UpSpeed),
			DlSpeed:     st.DlSpeed,
			WindowBytes: totalWindow,
		})
	}

	m.lastPollEnd = now
	m.pollCount++
}

// appendHistoryLocked 追加一条曲线点，超出容量时丢弃最旧的点。
func (m *Monitor) appendHistoryLocked(name string, s Sample) {
	h := append(m.history[name], s)
	if len(h) > m.historyCap {
		h = h[len(h)-m.historyCap:]
	}
	m.history[name] = h
}

// Aggregate 返回全部客户端汇总后的快照（用于总览页）。
func (m *Monitor) Aggregate() ClientSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agg := ClientSnapshot{
		Name:        "__all__",
		Type:        "aggregate",
		UpSpeed:     map[client.Family]int64{},
		UpTotal:     map[client.Family]int64{},
		WindowBytes: map[client.Family]int64{},
		TotalBytes:  map[client.Family]int64{},
		Healthy:     true,
	}
	for _, snap := range m.snapshots {
		if !snap.Healthy {
			agg.Healthy = false
		}
		for _, f := range client.Families() {
			agg.UpSpeed[f] += snap.UpSpeed[f]
			agg.UpTotal[f] += snap.UpTotal[f]
			agg.WindowBytes[f] += snap.WindowBytes[f]
			agg.TotalBytes[f] += snap.TotalBytes[f]
		}
		agg.DlSpeed += snap.DlSpeed
		agg.PeerCount += snap.PeerCount
		agg.TorrentCount += snap.TorrentCount
	}
	return agg
}

// Snapshot 返回指定客户端的快照。
func (m *Monitor) Snapshot(name string) (ClientSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.snapshots[name]
	return s, ok
}

// AllSnapshots 返回全部客户端快照，顺序与注册表一致。
func (m *Monitor) AllSnapshots() []ClientSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ClientSnapshot, 0, len(m.snapshots))
	for _, c := range m.registry.All() {
		if s, ok := m.snapshots[c.Name()]; ok {
			out = append(out, s)
		} else {
			out = append(out, ClientSnapshot{
				Name:        c.Name(),
				Type:        c.Type(),
				Healthy:     false,
				UpSpeed:     map[client.Family]int64{},
				UpTotal:     map[client.Family]int64{},
				WindowBytes: map[client.Family]int64{},
				TotalBytes:  map[client.Family]int64{},
			})
		}
	}
	return out
}

// History 返回指定客户端的曲线数据。为空时返回聚合曲线。
func (m *Monitor) History(name string) []Sample {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if name != "" && name != "__all__" {
		return append([]Sample(nil), m.history[name]...)
	}
	// 聚合：把各客户端按时间戳对齐求和。
	type acc struct {
		up   int64
		dl   int64
		win  int64
		seen bool
	}
	merged := map[int64]*acc{}
	order := []int64{}
	for _, h := range m.history {
		for _, s := range h {
			key := s.Timestamp.Unix()
			a, ok := merged[key]
			if !ok {
				a = &acc{}
				merged[key] = a
				order = append(order, key)
			}
			var up int64
			for _, v := range s.UpSpeed {
				up += v
			}
			a.up += up
			a.dl += s.DlSpeed
			a.win += s.WindowBytes
		}
	}
	// 按时间排序
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && order[j] < order[j-1]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	out := make([]Sample, 0, len(order))
	for _, k := range order {
		a := merged[k]
		out = append(out, Sample{
			Timestamp:   time.Unix(k, 0),
			UpSpeed:     map[client.Family]int64{client.FamilyUnknown: a.up},
			DlSpeed:     a.dl,
			WindowBytes: a.win,
		})
	}
	return out
}

// AdoptStateFrom 把旧监控器的累计状态迁移过来，
// 用于配置热重载时避免周期累计量被清零。
func (m *Monitor) AdoptStateFrom(old *Monitor) {
	if old == nil || old == m {
		return
	}
	old.mu.RLock()
	oldCounters := old.counters
	oldHistory := old.history
	oldSnapshots := old.snapshots
	oldPollCount := old.pollCount
	oldPollErrors := old.pollErrors
	old.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	for name, fams := range oldCounters {
		dst := m.ensureCounters(name)
		for fam, c := range fams {
			if d, ok := dst[fam]; ok {
				// 复制值而非指针，避免两个监控器共享同一计数器。
				*d = *c
			}
		}
	}
	// 保留已有快照（视图连续性），但用旧数据补齐本次没有采到的客户端。
	for name, snap := range oldSnapshots {
		if _, exists := m.snapshots[name]; !exists {
			m.snapshots[name] = snap
		}
	}
	for name, h := range oldHistory {
		if len(m.history[name]) == 0 {
			m.history[name] = append([]Sample(nil), h...)
		}
	}
	m.pollCount = oldPollCount
	m.pollErrors = oldPollErrors
}

// ResetWindow 把指定客户端的周期累计清零（进入新周期时调用）。
func (m *Monitor) ResetWindow(names []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(names) == 0 {
		for _, fams := range m.counters {
			for _, c := range fams {
				c.reset()
			}
		}
		return
	}
	for _, n := range names {
		if fams, ok := m.counters[n]; ok {
			for _, c := range fams {
				c.reset()
			}
		}
	}
}

// ResetWindowFamilies 只重置指定地址族的周期累计。
func (m *Monitor) ResetWindowFamilies(name string, fams []client.Family) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counters, ok := m.counters[name]
	if !ok {
		return
	}
	for _, f := range fams {
		if c, ok := counters[f]; ok {
			c.reset()
		}
	}
}

// Stats 返回监控器自身的运行指标。
func (m *Monitor) Stats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return map[string]any{
		"interval_seconds": m.interval.Seconds(),
		"poll_count":       m.pollCount,
		"poll_errors":      m.pollErrors,
		"last_poll":        m.lastPollEnd,
		"clients":          len(m.snapshots),
	}
}

// Run 启动周期采样循环，直到 ctx 取消。
func (m *Monitor) Run(ctx context.Context) {
	// 立即采样一次以建立基线。
	m.PollOnce(ctx, time.Now())

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.PollOnce(ctx, now)
		}
	}
}

// ---------------------------------------------------------------------------
// 助手
// ---------------------------------------------------------------------------

// integrate 用梯形法估算两次采样之间产生的字节数。
func integrate(prevSpeed, curSpeed int64, dt time.Duration) int64 {
	if dt <= 0 {
		return 0
	}
	secs := dt.Seconds()
	// 梯形法：平均速率 × 时间
	avg := (float64(prevSpeed) + float64(curSpeed)) / 2
	return int64(math.Round(avg * secs))
}

// maxInt64 返回较大者（本包内部使用，避免与 client 包的实现冲突）。
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// copyFamilyMap 复制一个地址族映射，避免共享底层 map。
func copyFamilyMap(in map[client.Family]int64) map[client.Family]int64 {
	out := make(map[client.Family]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// clientURL 返回客户端的地址（从 Managed 中取不到时返回空串）。
func clientURL(c *client.Managed) string {
	type urlProvider interface{ URL() string }
	if u, ok := c.Client.(urlProvider); ok {
		return u.URL()
	}
	return ""
}

// FormatBytes 把字节数格式化为人类可读字符串。
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	val := float64(n)
	i := -1
	for val >= unit && i < len(units)-1 {
		val /= unit
		i++
	}
	return fmt.Sprintf("%.2f %s", val, units[i])
}

// FormatSpeed 把字节/秒格式化为人类可读字符串。
func FormatSpeed(n int64) string {
	if n <= 0 {
		return "不限速"
	}
	return FormatBytes(n) + "/s"
}

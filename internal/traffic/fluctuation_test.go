package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/btguard/bt-traffic-guard/internal/client"
)

// fluctuatingClient 模拟 Transmission 的「按 peer 占比分摊累计量」行为。
//
// 关键特征：totalData 单调增长，但上报给某个地址族的值会随 peer 数波动
// 而【上下跳动】—— 因为它是 totalData × 占比，占比每轮都变。
type fluctuatingClient struct {
	name string

	totalData int64 // 历史累计（单调增长）
	v4Peers   int64
	v6Peers   int64
	fam       client.Family // 当前把量分摊到哪个族
}

func (c *fluctuatingClient) Name() string { return c.name }
func (c *fluctuatingClient) Type() string { return "transmission" }
func (c *fluctuatingClient) URL() string  { return "http://fake" }

func (c *fluctuatingClient) Fetch(context.Context) (client.Stats, error) {
	st := client.Stats{
		Timestamp: time.Now(),
		UpSpeed:   map[client.Family]int64{},
		UpTotal:   map[client.Family]int64{},
		// 关键：与 Transmission 的真实行为一致 ——
		// 分族累计量是按 peer 占比估算的，不是单调计数器。
		FamilyTotalEstimated: true,
	}
	sum := c.v4Peers + c.v6Peers
	if sum > 0 {
		st.UpTotal[client.FamilyIPv4] = c.totalData * c.v4Peers / sum
		st.UpTotal[client.FamilyIPv6] = c.totalData * c.v6Peers / sum
	}
	return st, nil
}

func (c *fluctuatingClient) SetUploadLimit(context.Context, client.SpeedLimit) error {
	return nil
}

// TestPeerRatioFluctuationDoesNotInflate 验证 peer 占比波动不会污染周期累计。
//
// 这是用户实际遇到的问题：
//
//	Transmission 的 UpTotal 是按「当前 peer 地址族占比」分摊出来的估算值，
//	并非单调递增的计数器。当 IPv6 peer 断开时，IPv4 那一份会【变小】；
//	monitor 把「变小」误判为「客户端重启」，随后数值回升时
//	又把整段差值当成新增流量，于是每波动一次就灌入几百 GB 虚假流量。
func TestPeerRatioFluctuationDoesNotInflate(t *testing.T) {
	fc := &fluctuatingClient{name: "tr", totalData: 2 << 40, v4Peers: 10}
	reg := client.NewRegistry()
	reg.Add(client.NewManaged(fc))
	m := NewMonitor(reg, time.Second, 100)

	ctx := context.Background()
	t0 := time.Now()

	// 第 1 轮：建立基线（10 个 IPv4 peer，无 IPv6）
	m.PollOnce(ctx, t0)

	// 第 2 轮：IPv6 peer 连上，IPv4 份额被稀释 -> 数值【下降】
	fc.v6Peers = 2
	m.PollOnce(ctx, t0.Add(time.Second))

	// 第 3 轮：IPv6 peer 断开 -> IPv4 份额回升 -> 数值【上升】
	fc.v6Peers = 0
	m.PollOnce(ctx, t0.Add(2*time.Second))

	// 第 4 轮：再来一次波动
	fc.v6Peers = 3
	m.PollOnce(ctx, t0.Add(3*time.Second))
	fc.v6Peers = 0
	m.PollOnce(ctx, t0.Add(4*time.Second))

	got, ok := m.Snapshot("tr")
	if !ok {
		t.Fatal("应能取到快照")
	}
	win := got.TotalWindow()

	// 真实新增流量应当很小（totalData 一直是 2 TiB，只做份额腾挪）。
	// 波动造成的虚假增量可能达到数百 GB —— 这里给一个宽松但明确的上限。
	const maxReasonable = int64(50) << 20 // 50 MiB
	if win > maxReasonable {
		t.Errorf("周期累计被 peer 波动污染: %d 字节 (%.2f GiB)，"+
			"实际新增应接近 0", win, float64(win)/(1<<30))
	}
}

// TestTotalBytesNotInflatedByFluctuation 验证总累计量同样不受影响。
func TestTotalBytesNotInflatedByFluctuation(t *testing.T) {
	fc := &fluctuatingClient{name: "tr", totalData: 1 << 40, v4Peers: 8}
	reg := client.NewRegistry()
	reg.Add(client.NewManaged(fc))
	m := NewMonitor(reg, time.Second, 100)
	ctx := context.Background()
	t0 := time.Now()

	m.PollOnce(ctx, t0)
	for i := 1; i <= 10; i++ {
		fc.v6Peers = int64(i % 3)
		m.PollOnce(ctx, t0.Add(time.Duration(i)*time.Second))
	}

	snap, _ := m.Snapshot("tr")
	const maxReasonable = int64(50) << 20
	if snap.TotalWindow() > maxReasonable {
		t.Errorf("累计量被污染: %d 字节", snap.TotalWindow())
	}
}

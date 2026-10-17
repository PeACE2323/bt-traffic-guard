package client

import "testing"

// TestQBSplitByFamilyWhenPeersIdle 验证 peer 瞬时速率为 0 时仍能按地址族归类。
//
// 真实场景：PT 保种时上传经常是 0，此时所有 peer 的 up_speed 都是 0。
// 若把它们全部跳过，就会退回「全部 unknown」——
// 用户看到的现象是「永远是未分类」，即使确实有 IPv4/IPv6 peer 在线。
func TestQBSplitByFamilyWhenPeersIdle(t *testing.T) {
	md := &mainData{
		Peers: map[string]map[string]struct {
			UpSpeed int64  `json:"up_speed"`
			Country string `json:"country"`
			IP      string `json:"ip"`
			Flags   string `json:"flags"`
		}{
			"h1": {
				"1.2.3.4":     {IP: "1.2.3.4", UpSpeed: 0},
				"2001:db8::1": {IP: "2001:db8::1", UpSpeed: 0},
			},
		},
	}
	q := &QBittorrent{}

	// 总速度 1000，按 peer 数（1:1）分摊 -> 各 500
	v4, v6, unk := q.splitByFamily(true, md, 1000, 100000)
	t.Logf("v4=%+v v6=%+v unk=%+v", v4, v6, unk)

	if v4.speed == 0 && v6.speed == 0 {
		t.Errorf("peer 速率为 0 时仍应按地址族归类，实际全部落入 unknown")
	}
}

// TestQBSplitByFamilyWithSpeed 验证有速率时的正常归类。
func TestQBSplitByFamilyWithSpeed(t *testing.T) {
	md := &mainData{
		Peers: map[string]map[string]struct {
			UpSpeed int64  `json:"up_speed"`
			Country string `json:"country"`
			IP      string `json:"ip"`
			Flags   string `json:"flags"`
		}{
			"h1": {
				"1.2.3.4":     {IP: "1.2.3.4", UpSpeed: 600},
				"2001:db8::1": {IP: "2001:db8::1", UpSpeed: 400},
			},
		},
	}
	q := &QBittorrent{}
	v4, v6, _ := q.splitByFamily(true, md, 1000, 100000)
	t.Logf("v4.speed=%d v6.speed=%d", v4.speed, v6.speed)
	if v4.speed == 0 || v6.speed == 0 {
		t.Errorf("有速率时应按地址族正确归类")
	}
}

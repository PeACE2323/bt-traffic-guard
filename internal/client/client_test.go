package client

import "testing"

func TestClassifyIP(t *testing.T) {
	cases := []struct {
		in   string
		want Family
	}{
		{"1.2.3.4", FamilyIPv4},
		{"1.2.3.4:6881", FamilyIPv4},
		{"255.255.255.255", FamilyIPv4},
		{"2001:db8::1", FamilyIPv6},
		{"[2001:db8::1]:51413", FamilyIPv6},
		{"::1", FamilyIPv6},
		{"fe80::1%eth0", FamilyIPv6},
		{"", FamilyUnknown},
		{"not-an-ip", FamilyUnknown},
	}
	for _, c := range cases {
		if got := classifyIP(c.in); got != c.want {
			t.Errorf("classifyIP(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestCoerceInt64(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{nil, 0},
		{float64(42), 42},
		{int(7), 7},
		{int64(99), 99},
		{"123", 123},
		{true, 1},
		{false, 0},
		{"abc", 0},
	}
	for _, c := range cases {
		if got := coerceInt64(c.in); got != c.want {
			t.Errorf("coerceInt64(%v) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

func TestCoerceBool(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{nil, false},
		{true, true},
		{false, false},
		{float64(1), true},
		{float64(0), false},
		{"true", true},
		{"yes", true},
		{"no", false},
	}
	for _, c := range cases {
		if got := coerceBool(c.in); got != c.want {
			t.Errorf("coerceBool(%v) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

// TestVersionAtLeast 验证 qBittorrent 版本比较，
// 用于判断是否支持分族限速（4.6.0+）。
func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		v    string
		maj  int
		min  int
		pat  int
		want bool
	}{
		{"4.6.0", 4, 6, 0, true},
		{"4.6.5", 4, 6, 0, true},
		{"4.6.0-beta1", 4, 6, 0, true},
		{"4.5.5", 4, 6, 0, false},
		{"5.0.0", 4, 6, 0, true},
		{"4.6", 4, 6, 0, true},
		{"4", 4, 6, 0, false},
		{"v4.6.2", 4, 6, 0, false}, // 未去 v 前缀时按 0 处理
		{"", 4, 6, 0, false},
	}
	for _, c := range cases {
		if got := versionAtLeast(c.v, c.maj, c.min, c.pat); got != c.want {
			t.Errorf("versionAtLeast(%q, %d, %d, %d) = %v, 期望 %v",
				c.v, c.maj, c.min, c.pat, got, c.want)
		}
	}
}

func TestSpeedLimitHelpers(t *testing.T) {
	if l := NewNoopLimit(); l.Total != -1 {
		t.Errorf("NewNoopLimit().Total = %d, 期望 -1", l.Total)
	}
	if l := NewUnlimited(); l.Total != 0 {
		t.Errorf("NewUnlimited().Total = %d, 期望 0", l.Total)
	}
}

func TestStatsTotals(t *testing.T) {
	s := Stats{
		UpSpeed: map[Family]int64{FamilyIPv4: 100, FamilyIPv6: 200},
		UpTotal: map[Family]int64{FamilyIPv4: 1000, FamilyIPv6: 2000},
	}
	if got := s.TotalUpSpeed(); got != 300 {
		t.Errorf("TotalUpSpeed() = %d, 期望 300", got)
	}
	if got := s.TotalUpBytes(); got != 3000 {
		t.Errorf("TotalUpBytes() = %d, 期望 3000", got)
	}
	if got := s.SpeedOf(FamilyIPv6); got != 200 {
		t.Errorf("SpeedOf(ipv6) = %d, 期望 200", got)
	}
}

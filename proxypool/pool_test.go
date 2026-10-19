package proxypool

import (
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		raw, scheme, want string
		wantErr           bool
	}{
		{raw: "http://Default:pass@15.204.80.28:2260", scheme: "http", want: "http://Default:pass@15.204.80.28:2260"},
		{raw: "socks5://uvOK:pw@38.248.238.174:443", scheme: "socks5", want: "socks5://uvOK:pw@38.248.238.174:443"},
		{raw: "38.248.238.174:443:uvOK:pw", scheme: "socks5", want: "socks5://uvOK:pw@38.248.238.174:443"},
		{raw: "38.248.238.174:443:uvOK:pw", scheme: "http", want: "http://uvOK:pw@38.248.238.174:443"},
		{raw: "1.2.3.4:8080", scheme: "http", want: "http://1.2.3.4:8080"},
		{raw: "1.2.3.4:8080", scheme: "", want: "http://1.2.3.4:8080"}, // 默认补 http
		{raw: "ftp://1.2.3.4:21", scheme: "http", wantErr: true},
		{raw: "1.2.3.4", scheme: "http", wantErr: true},        // 缺端口
		{raw: "1.2.3.4:99999", scheme: "http", wantErr: true},  // 端口越界
		{raw: "1.2.3.4:abc", scheme: "http", wantErr: true},    // 端口非数字
		{raw: "1.2.3.4:80:u", scheme: "http", wantErr: true},   // 三段
		{raw: "  ", scheme: "http", wantErr: true},             // 空行
	}
	for _, c := range cases {
		got, err := ParseLine(c.raw, c.scheme)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseLine(%q, %q) = %q, 期望报错", c.raw, c.scheme, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLine(%q, %q) 报错: %v", c.raw, c.scheme, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLine(%q, %q) = %q, 期望 %q", c.raw, c.scheme, got, c.want)
		}
	}
}

func entry(url string) Entry { return Entry{URL: url, Enabled: true} }

func TestNextRoundRobin(t *testing.T) {
	p := New([]Entry{entry("http://a:1"), entry("http://b:2"), entry("http://c:3")})
	// 连续取号按顺序轮转一圈回到开头。
	want := []string{"http://a:1", "http://b:2", "http://c:3", "http://a:1"}
	for i, w := range want {
		u, _, ok := p.Next()
		if !ok || u.String() != w {
			t.Errorf("第 %d 次取号 = %v(ok=%v), 期望 %s", i+1, u, ok, w)
		}
	}
}

func TestNextSkipsDisabledAndCooling(t *testing.T) {
	p := New([]Entry{entry("http://a:1"), entry("http://b:2"), entry("http://c:3")})
	// 禁用 a。
	entries := p.Status()
	entries[0].Enabled = false
	p.Update(entriesFrom(entries))
	// 冷却 b。
	p.Fail(2, FailReasonNet)
	u, _, _ := p.Next()
	if u.String() != "http://c:3" {
		t.Errorf("跳过禁用+冷却后应取 c,实际 %s", u)
	}
}

func TestNextDegradesToEarliestCooling(t *testing.T) {
	p := New([]Entry{entry("http://a:1"), entry("http://b:2")})
	p.Fail(1, FailReasonNet)      // 60s 后到期
	p.Fail(2, FailReasonForbidden) // 5min 后到期,晚于 a
	// 全冷却 → 降级取最早到期的 a,而不是失败。
	u, id, ok := p.Next()
	if !ok || id != 1 || u.String() != "http://a:1" {
		t.Errorf("全冷却降级应取最早到期的 a(id=1),实际 id=%d %v ok=%v", id, u, ok)
	}
}

func TestNextEmptyAndAllDisabled(t *testing.T) {
	if _, _, ok := (&Pool{}).Next(); ok {
		t.Error("空池应返回 ok=false")
	}
	p := New([]Entry{{URL: "http://a:1", Enabled: false}})
	if _, _, ok := p.Next(); ok {
		t.Error("全部禁用应返回 ok=false")
	}
}

func TestOKClearsCooldown(t *testing.T) {
	p := New([]Entry{entry("http://a:1"), entry("http://b:2")})
	p.Fail(1, FailReasonCheck)
	p.OK(1)
	// 游标从 0 开始,a 已解冻,应取到 a。
	u, _, _ := p.Next()
	if u.String() != "http://a:1" {
		t.Errorf("OK 解冻后应取 a,实际 %s", u)
	}
}

func TestFailUnknownReasonFallsBackToNet(t *testing.T) {
	p := New([]Entry{entry("http://a:1")})
	p.Fail(1, "whatever")
	st := p.Status()
	if !st[0].Cooling || st[0].CoolReason != FailReasonNet {
		t.Errorf("未知 reason 应按 net 冷却,实际 %+v", st[0])
	}
	if d := time.Until(st[0].CoolUntil); d > 60*time.Second+2*time.Second {
		t.Errorf("未知 reason 冷却时长应封顶 60s,实际 %v", d)
	}
}

func TestUpdateRenumbersAndDedupes(t *testing.T) {
	p := New([]Entry{entry("http://a:1"), entry("http://a:1"), {URL: "  ", Enabled: true}, entry("http://b:2")})
	if p.Len() != 2 {
		t.Fatalf("去重+过滤空行后应剩 2 条,实际 %d", p.Len())
	}
	st := p.Status()
	if st[0].ID != 1 || st[1].ID != 2 {
		t.Errorf("Update 后应重排 ID 为 1..N,实际 %d/%d", st[0].ID, st[1].ID)
	}
	// Update 清空冷却。
	p.Fail(1, FailReasonNet)
	p.Update([]Entry{entry("http://a:1")})
	if st := p.Status(); st[0].Cooling {
		t.Error("Update 后冷却表应清空")
	}
}

func entriesFrom(st []EntryStatus) []Entry {
	out := make([]Entry, 0, len(st))
	for _, s := range st {
		out = append(out, s.Entry)
	}
	return out
}

// 高优先级梯队优先；该梯队全部冷却后才降级到下一档。
func TestNextPrefersHigherPriorityTier(t *testing.T) {
	p := New([]Entry{
		{URL: "http://backup-a:1", Enabled: true, Priority: 0},
		{URL: "http://primary:1", Enabled: true, Priority: 10},
		{URL: "http://backup-b:1", Enabled: true, Priority: 0},
	})
	for i := 0; i < 5; i++ {
		u, _, ok := p.Next()
		if !ok || u.Host != "primary:1" {
			t.Fatalf("第 %d 次取号应为 primary，实际 %v", i, u)
		}
	}
	// primary 冷却后应降级到静态梯队，且不再返回 primary。
	p.Fail(2, FailReasonNet) // ID 2 = primary
	for i := 0; i < 4; i++ {
		u, _, ok := p.Next()
		if !ok || u.Host == "primary:1" {
			t.Fatalf("primary 冷却后不应再被选中，实际 %v", u)
		}
	}
}

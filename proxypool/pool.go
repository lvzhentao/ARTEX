// Package proxypool 提供进程内出口代理池:条目平铺 + round-robin 轮询 + 失败冷却。
// 三处消费:MITM 上游(逐请求取号,见 traffic.SetUpstreamPicker)、LLM transport
// (逐请求取号 + 失败上报,见 agent.EgressPool)、agent env 注入(每 run 取一个,
// 见 Manager.ProxyAddr)。池实例进程内单例,Update 原地换内容,引用方永远拿到最新条目。
// 游标与冷却表只在内存——冷却本来就是分钟级短时状态,重启清零无副作用。
package proxypool

import (
	"net/url"
	"strings"
	"sync"
	"time"
)

// 失败原因与对应冷却时长(语义对齐 easyclaw-adapter 的 ProxyPoolService:
// 网络级失败短冷却,健康检查失败中冷却)。
const (
	FailReasonNet       = "net"       // 建连/读写失败(代理大概率挂了)
	FailReasonForbidden = "forbidden" // 上游明确拒绝(403 类,预留)
	FailReasonCheck     = "check"     // 面板连通性测试失败
)

var failTTL = map[string]time.Duration{
	FailReasonNet:       60 * time.Second,
	FailReasonForbidden: 5 * time.Minute,
	FailReasonCheck:     5 * time.Minute,
}

// Entry 是池里的一个代理。URL 必须带 scheme(http/https/socks5),认证写进 URL。
type Entry struct {
	ID      int64  `json:"id"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Note    string `json:"note"`
}

// EntryStatus 是给面板的快照条目,附当前冷却状态。
type EntryStatus struct {
	Entry
	Cooling    bool      `json:"cooling"`
	CoolUntil  time.Time `json:"cool_until"`
	CoolReason string    `json:"cool_reason"`
}

type coolState struct {
	until  time.Time
	reason string
}

// Pool 是并发安全的轮询池。
type Pool struct {
	mu      sync.Mutex
	entries []Entry
	cursor  int
	cool    map[int64]coolState
}

// New 建池并重排 ID 为 1..N(过滤空 URL 与重复项),保持传入顺序。
func New(entries []Entry) *Pool {
	p := &Pool{cool: make(map[int64]coolState)}
	p.Update(entries)
	return p
}

// Update 原地替换全部条目(重新编号、清空冷却、游标归零)。池实例不变,
// 已注入各处(LLM transport/MITM picker)的引用立即看到新内容。
func (p *Pool) Update(entries []Entry) {
	seen := make(map[string]bool, len(entries))
	normalized := make([]Entry, 0, len(entries))
	var id int64
	for _, e := range entries {
		e.URL = normalizeSpace(e.URL)
		if e.URL == "" || seen[e.URL] {
			continue
		}
		seen[e.URL] = true
		id++
		e.ID = id
		e.Note = normalizeSpace(e.Note)
		normalized = append(normalized, e)
	}
	p.mu.Lock()
	p.entries = normalized
	p.cursor = 0
	p.cool = make(map[int64]coolState)
	p.mu.Unlock()
}

// Next 轮询取号:按固定顺序跳过禁用与冷却中的条目,返回第一个可用的。
// 全部条目都在冷却时降级返回最早到期的那个(宁可重试也不让上层直连泄露
// 真实出口 IP);没有任何条目(或全部禁用)时 ok=false。
func (p *Pool) Next() (proxyURL *url.URL, id int64, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.entries)
	if n == 0 {
		return nil, 0, false
	}
	now := time.Now()
	// 第一轮:找未冷却且启用的。
	for i := 0; i < n; i++ {
		idx := (p.cursor + i) % n
		e := p.entries[idx]
		if !e.Enabled {
			continue
		}
		if c, cooling := p.cool[e.ID]; cooling && c.until.After(now) {
			continue
		}
		p.cursor = (idx + 1) % n
		return parseURL(e.URL), e.ID, true
	}
	// 第二轮(降级):全部冷却,取到期最早的启用条目。
	var best *Entry
	var bestUntil time.Time
	for i := range p.entries {
		e := &p.entries[i]
		if !e.Enabled {
			continue
		}
		c := p.cool[e.ID]
		if best == nil || c.until.Before(bestUntil) {
			best, bestUntil = e, c.until
		}
	}
	if best == nil {
		return nil, 0, false
	}
	p.cursor = 0
	return parseURL(best.URL), best.ID, true
}

// NextURL 是 traffic picker 回调的适配形态(func() *url.URL):取号失败返回 nil,
// 上层据此让该请求失败而不是直连。
func (p *Pool) NextURL() *url.URL {
	u, _, _ := p.Next()
	return u
}

// Fail 把条目置入冷却。reason 见 FailReason* 常量,未知 reason 按 net 处理。
func (p *Pool) Fail(id int64, reason string) {
	ttl, known := failTTL[reason]
	if !known {
		reason, ttl = FailReasonNet, failTTL[FailReasonNet]
	}
	p.mu.Lock()
	p.cool[id] = coolState{until: time.Now().Add(ttl), reason: reason}
	p.mu.Unlock()
}

// OK 上报成功,解除该条目的冷却(若在冷却中)。
func (p *Pool) OK(id int64) {
	p.mu.Lock()
	delete(p.cool, id)
	p.mu.Unlock()
}

// Status 返回全部条目的快照(含冷却信息),按池内顺序。
func (p *Pool) Status() []EntryStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]EntryStatus, 0, len(p.entries))
	for _, e := range p.entries {
		st := EntryStatus{Entry: e}
		if c, hit := p.cool[e.ID]; hit && c.until.After(now) {
			st.Cooling, st.CoolUntil, st.CoolReason = true, c.until, c.reason
		}
		out = append(out, st)
	}
	return out
}

// Len 返回条目总数(含禁用)。
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// parseURL 忽略错误返回 *url.URL(条目入库前已经 ParseLine 校验过,这里只为
// 拿到解析结果;意外失败返回 nil,上层按"取号失败"处理)。
func parseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return u
}

func normalizeSpace(s string) string { return strings.TrimSpace(s) }

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/proxypool"
)

// 出口代理池面板 API。条目列表全量提交(前端管理整个列表,一次 PUT 落库+原地
// 生效);连通性测试逐条经代理访问出口回显服务,回传延迟与出口 IP,失败条目
// 自动进入冷却。

// egressTestURL 是连通性测试的目标:响应体就是出口 IP(纯文本)。
const egressTestURL = "https://api.ipify.org"

// proxyPoolTestTimeout 单条测试的总预算(建连+TLS+响应)。
const proxyPoolTestTimeout = 10 * time.Second

// proxyPoolTestConcurrency 并发测试条数上限,避免一次"全部测试"打出 22 条并发
// 建连把小水管代理或出口服务商打满。
const proxyPoolTestConcurrency = 5

// getProxyPool serves the pool toggle + entry snapshot (cooldown included).
func (s *Server) getProxyPool(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"enabled": s.m.ProxyPoolEnabled(),
		"entries": s.m.ProxyPoolStatus(),
	})
}

// putProxyPool applies a full-list update and/or the toggle. Field semantics:
// enabled nil = leave; entries nil = leave (otherwise the list REPLACES the
// pool, empty array clears it). default_scheme补全无 scheme 行的协议(默认 http)。
func (s *Server) putProxyPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled       *bool              `json:"enabled"`
		Entries       *[]proxyPoolEntry  `json:"entries"`
		DefaultScheme string             `json:"default_scheme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.DefaultScheme == "" {
		req.DefaultScheme = "http"
	}
	enabledChanged := false
	if req.Enabled != nil {
		if err := s.m.SetProxyPoolEnabled(*req.Enabled); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		enabledChanged = true
	}
	if req.Entries != nil {
		// 面板暂不展示优先级：客户端未带 priority(0) 时沿用同 URL 旧条目的值，
		// 避免在界面保存一次就把「动态为主」的梯队配置清掉。
		prev := map[string]int{}
		for _, st := range s.m.ProxyPoolStatus() {
			prev[st.URL] = st.Priority
		}
		entries := make([]proxypool.Entry, 0, len(*req.Entries))
		for _, e := range *req.Entries {
			p := e.Priority
			if p == 0 {
				p = prev[e.URL]
			}
			entries = append(entries, proxypool.Entry{URL: e.URL, Enabled: e.Enabled, Note: e.Note, Priority: p})
		}
		if _, err := s.m.UpdateProxyPool(entries, req.DefaultScheme); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if enabledChanged {
		// 开关切换改写 LLM 出口形态(池↔直连/单代理),必须重建 provider 链。
		s.rebuildAgentsForProxyPool()
	}
	writeJSON(w, 200, map[string]any{
		"enabled": s.m.ProxyPoolEnabled(),
		"entries": s.m.ProxyPoolStatus(),
	})
}

type proxyPoolEntry struct {
	URL      string `json:"url"`
	Enabled  bool   `json:"enabled"`
	Note     string `json:"note"`
	Priority int    `json:"priority"` // 越大越优先，高优先级全部冷却才降级
}

// rebuildAgentsForProxyPool reruns applyLLM so providers pick up (or drop) the
// pool egress. Mirrors putSettings' changed-path guard.
func (s *Server) rebuildAgentsForProxyPool() {
	s.cfgMu.Lock()
	cfg, on := s.llmCfg, s.llmOn
	s.cfgMu.Unlock()
	if on {
		if err := s.applyLLM(cfg); err != nil {
			log.Printf("[proxy-pool] 重建 agent 失败(新的出口配置对已运行的会话未生效): %v", err)
		}
	}
}

// testProxyPool runs a real egress check per URL: GET the echo endpoint through
// that proxy and report latency + egress IP. Empty urls = test every pool entry.
// Failed entries are cooled down (FailReasonCheck) so live rotation skips them.
func (s *Server) testProxyPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URLs []string `json:"urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	targets := req.URLs
	if len(targets) == 0 {
		for _, st := range s.m.ProxyPoolStatus() {
			targets = append(targets, st.URL)
		}
	}
	results := make([]proxyPoolTestResult, len(targets))
	sem := make(chan struct{}, proxyPoolTestConcurrency)
	var wg sync.WaitGroup
	for i, raw := range targets {
		wg.Add(1)
		go func(i int, raw string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = checkEgress(raw)
		}(i, raw)
	}
	wg.Wait()
	// 失败条目回写池冷却(按 URL 匹配池内条目;不在池里的临时测试串不回写)。
	pool := s.m.ProxyPoolRef()
	if pool != nil {
		for i, res := range results {
			if !res.OK {
				for _, st := range pool.Status() {
					if st.URL == targets[i] {
						pool.Fail(st.ID, proxypool.FailReasonCheck)
					}
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{"results": results})
}

type proxyPoolTestResult struct {
	URL       string `json:"url"`
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	EgressIP  string `json:"egress_ip,omitempty"`
	Error     string `json:"error,omitempty"`
}

// checkEgress dials egressTestURL through one proxy and reports the outcome.
func checkEgress(raw string) proxyPoolTestResult {
	res := proxyPoolTestResult{URL: raw}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		res.Error = "无效的代理地址"
		return res
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(u)
	client := &http.Client{Transport: tr, Timeout: proxyPoolTestTimeout}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), proxyPoolTestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, egressTestURL, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := client.Do(req)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return res
	}
	res.OK = true
	res.EgressIP = strings.TrimSpace(string(body))
	return res
}

// poolifyLLMProxy 在「代理池开启」且 profile 未显式指定单代理(空)或显式要求走池
// (填 "pool")时,把进程级代理池注入 LLM 配置,使该 profile 的请求逐条轮询出口。
// 显式 URL 优先——个别 profile 想固定走某个代理时不受池影响。
func (s *Server) poolifyLLMProxy(cfg *agent.Config, explicitProxy string) {
	p := strings.TrimSpace(explicitProxy)
	if p == "" || strings.EqualFold(p, "pool") {
		if pool := s.m.ProxyPoolRef(); pool != nil {
			cfg.EgressPool = pool
		}
	}
}

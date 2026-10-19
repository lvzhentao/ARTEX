package agent

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakePool 记录上报表,用于验证 pooledTransport 的取号与结果回传。
type fakePool struct {
	u      *url.URL
	failed bool
	okd    bool
}

func (f *fakePool) Next() (*url.URL, int64, bool) { return f.u, 1, f.u != nil }
func (f *fakePool) Fail(int64, string)            { f.failed = true }
func (f *fakePool) OK(int64)                      { f.okd = true }

func TestPooledTransportReportsNetFailure(t *testing.T) {
	pool := &fakePool{u: &url.URL{Scheme: "http", Host: "127.0.0.1:1"}} // 不可达端口
	tr := &pooledTransport{pool: pool}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("经不可达代理的请求应失败")
	}
	if !pool.failed {
		t.Error("网络层失败应上报 Fail(触发池冷却)")
	}
}

func TestPooledTransportEmptyPoolErrors(t *testing.T) {
	tr := &pooledTransport{pool: &fakePool{}}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	_, err := tr.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "no enabled proxy entry") {
		t.Fatalf("池空应报 no enabled 错误,实际 %v", err)
	}
}

func TestPooledTransportReportsOK(t *testing.T) {
	// 一个极简 http 正向代理:收到绝对 URI 的请求直接原样转发(仅 http 目标,
	// 对测试足够),让请求经它成功往返。
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("target"))
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, _ := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		resp, err := http.DefaultClient.Do(out)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		w.Write(buf[:n])
	}))
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)

	pool := &fakePool{u: pu}
	tr := &pooledTransport{pool: pool}
	req, _ := http.NewRequest(http.MethodGet, target.URL, nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("经代理请求应成功: %v", err)
	}
	resp.Body.Close()
	if !pool.okd {
		t.Error("成功请求应上报 OK")
	}
	if pool.failed {
		t.Error("成功请求不应触发 Fail")
	}
}

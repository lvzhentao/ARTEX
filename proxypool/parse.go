package proxypool

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseLine 把一行代理配置解析成规范化的完整 URL(scheme://user:pass@host:port)。
// 支持三种写法:
//   - scheme://user:pass@host:port   (完整 URL,原样校验)
//   - host:port:user:pass            (批量导入常见格式,补 defaultScheme)
//   - host:port                      (无认证,补 defaultScheme)
//
// scheme 仅接受 http/https/socks5(与 traffic.ValidateProxyURL 的口径一致,
// net/http 与 go-mitmproxy 都原生支持这三种)。
func ParseLine(raw, defaultScheme string) (string, error) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return "", fmt.Errorf("代理行不能为空")
	}
	if defaultScheme == "" {
		defaultScheme = "http"
	}

	// 带 scheme 的标准 URL 形态。
	if i := strings.Index(line, "://"); i > 0 {
		scheme := strings.ToLower(line[:i])
		rest := line[i+3:]
		u, err := url.Parse(line)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("代理 %q 不是合法的 URL", raw)
		}
		if err := checkScheme(scheme); err != nil {
			return "", err
		}
		if _, _, err := splitHostPort(u.Host); err != nil {
			return "", fmt.Errorf("代理 %q: %v", raw, err)
		}
		// 规范化小写 scheme + 去掉尾部杂物,保留 userinfo 原样。
		return scheme + "://" + rest, nil
	}

	// 无 scheme:按冒号段数区分 host:port:user:pass 与 host:port。
	parts := strings.Split(line, ":")
	var host, port, user, pass string
	switch len(parts) {
	case 2:
		host, port = parts[0], parts[1]
	case 4:
		host, port, user, pass = parts[0], parts[1], parts[2], parts[3]
	default:
		return "", fmt.Errorf("代理 %q 格式不符(需 scheme://user:pass@host:port、host:port:user:pass 或 host:port)", raw)
	}
	if err := checkScheme(defaultScheme); err != nil {
		return "", err
	}
	if _, _, err := splitHostPort(host + ":" + port); err != nil {
		return "", fmt.Errorf("代理 %q: %v", raw, err)
	}
	u := &url.URL{Scheme: defaultScheme, Host: host + ":" + port}
	if user != "" {
		u.User = url.UserPassword(user, pass)
	}
	return u.String(), nil
}

func checkScheme(scheme string) error {
	switch scheme {
	case "http", "https", "socks5":
		return nil
	}
	return fmt.Errorf("不支持的代理协议 %q(用 http、https 或 socks5)", scheme)
}

// splitHostPort 校验 host:port 且端口为 1..65535 的数字。
func splitHostPort(hostport string) (string, int, error) {
	i := strings.LastIndex(hostport, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("缺少端口")
	}
	host, portStr := hostport[:i], hostport[i+1:]
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("端口 %q 不合法", portStr)
	}
	if host == "" {
		return "", 0, fmt.Errorf("缺少主机地址")
	}
	return host, port, nil
}

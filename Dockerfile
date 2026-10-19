# syntax=docker/dockerfile:1
#
# 运行镜像（不在镜像里编译）：只装常用工具，放入**预编译好的 Linux 单二进制**。
# 二进制由 CI 的 binaries job 交叉编译（纯 Go、无 QEMU），按目标架构放在
# 构建上下文的 dist/<TARGETARCH>/artex。这样多架构构建时 arm64 只需模拟 apt 层，
# 不再模拟 Next/Go 编译，速度快得多。
#
# 本地手动构建镜像时，先自行准备二进制：
#   cd web && npm run build:static && cd ..
#   cp -r web/out server/webui/dist
#   CGO_ENABLED=0 GOARCH=amd64 go build -tags embedui -o dist/amd64/artex ./cmd/artex
#   docker build -t artex:local .
FROM python:3.12-slim-bookworm
ARG TARGETARCH
# 常用工具：ripgrep / curl / vim，加一批 recon 常备件（按需增删）。
# Node 从 NodeSource 装 20.x：bookworm 自带的 apt nodejs 是 18，Playwright 要求 >=20。
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap proxychains4 \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# 预装 Playwright MCP 与 CLI（全局），运行时不再 npx 联网下载。
# @playwright/mcp：browser MCP 直接 `npx @playwright/mcp`（已全局装好，无需 -y/@latest）。
# @playwright/cli：提供 playwright-cli，装完顺带 --help 验证可执行。
# 再装 playwright（提供浏览器管理），装完用 --with-deps 预置 chromium 及其系统依赖，
# 这样容器内 MCP/CLI 首次启动即可用，不再联网下载浏览器。
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
# proxychains: LD_PRELOAD 强制代理原始 TCP 工具(nmap/nc 等)。so 链到统一路径(Go 代码写死该路径,
# 与架构无关);配置指向容器内常驻的 MITM 记录代理 :8788 —— ARTEX 在池开启时把它的上游换成
# 代理池逐请求轮换,因此经它出站的每个连接都是代理出口,服务器真实 IP 不接触目标。
RUN ln -sf "$(dpkg -L libproxychains4 | grep "libproxychains.so")" /usr/local/lib/libproxychains4.so \
    && printf "strict_chain\nproxy_dns\ntcp_read_time_out 15000\ntcp_connect_time_out 10000\n[ProxyList]\nhttp 127.0.0.1 8788\n" > /etc/proxychains4.conf

# nmap wrapper: 末置 -sT 强制 connect 扫描(后置 flag 覆盖用户给的 -sS/-sF 等 raw-socket
# 扫描类型)。root 下 nmap 默认 -sS 用 raw socket 发包,绕过 LD_PRELOAD 代理钩子,会从
# 服务器真实 IP 直连目标;-sT 的每次连接都经 libc connect(),被强制走代理出口。
RUN printf '#!/bin/sh\nexec /usr/bin/nmap "$@" -sT\n' > /usr/local/bin/nmap && chmod +x /usr/local/bin/nmap

WORKDIR /app
# 预编译好的对应架构二进制（dist/amd64/artex 或 dist/arm64/artex）
COPY dist/${TARGETARCH}/artex /app/artex
# 守护启动脚本：进程退出后按退出码决定是否重新拉起，页面一键更新靠它完成换装。
# 它同时负责把 SIGTERM 转发给 artex —— docker stop 只把信号发给 PID 1，
# 不转发的话 artex 收不到、做不了优雅关闭，10 秒后被 SIGKILL 硬杀。
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# data/（SQLite + jwt.key）持久化点
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]

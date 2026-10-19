# ARTEX2 → 本地 english-ui 合并操作手册

> 生成时间：2026-10-19
> 本地仓库：`/Users/lvzhentao/gitee-learn/ARTEX`（分支 `english-ui`）
> 线上仓库：`https://github.com/lvzhentao/ARTEX2.git`（分支 `main`，HEAD `eac1e74`）
> 本文档供另一个会话直接执行，所有路径、提交哈希、预期结果均已核对。

---

## 1. 背景

两个仓库在 `b55ceb1` 分叉：

- **线上 ARTEX2**：上游安全/正确性修复 + norma 源码内联 + Docker 多架构构建。
- **本地 english-ui**：英文 UI/i18n + 代理池（proxypool）+ `X-ARTEx-Token` 反代认证头。

两边各有对方没有的改动，需要双向同步。本手册先做 **ARTEX2 → 本地**。

### 本地未提交改动（合并前必须先提交）

```text
 M Dockerfile
 M agent/provider.go
 M agent/provider_capture_test.go
 M agent/worker.go
 M server/auth.go
 M server/manager.go
 M server/server.go
 M traffic/traffic.go
 M web/src/app/(main)/system/settings/page.tsx
 M web/src/lib/api.ts
 M web/src/lib/types.ts
?? agent/pooled_transport_test.go
?? proxypool/
?? server/proxy_pool.go
?? web/src/app/(main)/system/settings/_components/proxy-pool-card.tsx
```

这些改动属于本地代理池功能，ARTEX2 中没有对应实现，**不要丢弃**。

### 已实测的合并冲突

在临时克隆中试合并 `artex2/main → english-ui`（不含上述未提交改动），冲突文件只有 3 个：

```text
README.md
web/src/app/(auth)/setup/page.tsx
web/src/app/(main)/system/intercept/page.tsx
```

加上本地未提交的 `Dockerfile` 后，实际还需手工处理 `Dockerfile`。

---

## 2. 合并策略

推荐 **分段 cherry-pick**，不要直接 `git merge artex2/main`。原因：

- README 冲突面太大，直接 merge 会带进一整版中文 README。
- Dockerfile 需要以 ARTEX2 新版为基底，再补回本地代理池层。
- 分段合入便于逐段测试和回滚。

**提交顺序不能乱**：`norma` 必须在 Docker 构建链之前，因为新 Dockerfile 的 `RUN go mod download` 前会 `COPY norma/go.mod`。

---

## 3. 执行步骤

### 步骤 0：确认工作区并保存本地改动

```bash
cd /Users/lvzhentao/gitee-learn/ARTEX
git status --short
git branch --show-current   # 期望：english-ui
```

如果工作区还有未提交改动：

```bash
git add -A
git commit -m "feat: proxy pool + X-ARTEx-Token header"
```

如果暂不想提交：

```bash
git stash push -u -m "local proxy pool before artex2 merge"
```

> 注意：`git stash -u` 会包含未跟踪文件；合并完成后用 `git stash pop` 恢复。推荐直接提交，避免冲突解决期间 stash 丢失。

### 步骤 1：添加线上仓库并 fetch

```bash
git remote add artex2 https://github.com/lvzhentao/ARTEX2.git
git fetch artex2
git log --oneline -3 artex2/main
# 期望看到 eac1e74、f3baec9、7c218ea
```

### 步骤 2：合入安全/正确性修复（4 个提交）

```bash
git cherry-pick a951e4a a71da96 7c218ea f3baec9
```

各提交内容：

| 提交 | 内容 | 预期冲突 |
|---|---|---|
| `a951e4a` | 认证 fail-open 修复、密码长度后端校验、`INSERT ... ON CONFLICT DO NOTHING`、DB 连接池上限 32 | `web/src/app/(auth)/setup/page.tsx` |
| `a71da96` | `parseTarget` 拒绝非法 IPv4 四段组与越界端口 | 无 |
| `7c218ea` | 报告证据/PoC 动态围栏，防 Markdown 注入 | 无 |
| `f3baec9` | findings 导出排序/UTF-8 文件名修复、有界审批重试、intercept 配置 UI | `web/src/app/(main)/system/intercept/page.tsx`、`web/src/lib/types.ts` 可能自动合并 |

#### 冲突处理 A：`setup/page.tsx`

本地版本已把全部文案翻成英文，线上版本新增 `unavailable` 状态。解决原则：

1. **保留本地的英文文案与现有结构。**
2. 合入线上的 `unavailable` state、`authStatus` 失败时的 `setUnavailable(true)`、以及“无法确认初始化状态”的替代 UI。
3. 新增文案写成英文，并同步补充 `web/src/lib/i18n/locale-zh.json`。

参考英文文案：

```text
无法确认初始化状态
→ Unable to confirm setup status

后端或数据库暂时不可用。为避免覆盖实例上已有的密码，初始化入口已临时关闭——请恢复服务后重试。
→ Backend or database is temporarily unavailable. To avoid overwriting an existing password, setup has been disabled until the service recovers.

重试
→ Retry
```

`locale-zh.json` 是 `"中文原文": "English"` 映射，需要补：

```json
{
  "无法确认初始化状态": "Unable to confirm setup status",
  "后端或数据库暂时不可用。为避免覆盖实例上已有的密码，初始化入口已临时关闭——请恢复服务后重试。": "Backend or database is temporarily unavailable. To avoid overwriting an existing password, setup has been disabled until the service recovers.",
  "重试": "Retry"
}
```

#### 冲突处理 B：`intercept/page.tsx`

本地版本已整体英文化，线上版本新增两个字段和对应控件。解决原则：

1. 保留本地英文结构。
2. 在 `defaultJudge()` 中加入 `retry_count: 0`、`retry_interval_ms: 1000`。
3. 合入两个 number input 与说明文字。
4. 新文案写成英文，并补 `locale-zh.json`。

参考英文文案：

```text
每次模型判定超时（秒）
→ Per-attempt model review timeout (seconds)

审核失败重试次数（0 为不重试）
→ Review retry count (0 = no retry)

审核重试间隔（毫秒）
→ Review retry interval (ms)

仅在审核请求出错、超时或结果无法解析时重试，不会重复执行命令；已返回的放行、拦截或人工审批决定不会重试。
→ Retries only when the review request errors, times out, or returns an unparseable result. The tool command is never re-executed, and an already-returned allow/deny/ask verdict is not retried.

重试仍失败时（出错 / 超时 / 无法解析）
→ After retries still fail (error / timeout / unparseable)
```

#### 冲突处理 C：`types.ts`

本地工作区新增了代理池类型，线上新增：

```ts
retry_count: number;
retry_interval_ms: number;
```

两者位置不同，通常自动合并；若冲突，**两边都保留**。

完成后验证：

```bash
git status --short
go build ./...
go test ./db ./server ./report ./intercept
```

### 步骤 3：合入 norma 源码内联

```bash
git cherry-pick 69ad7b0
```

内容：

- 新增 `norma/` 目录（约 4 万行，v0.4.3 源码）。
- `go.mod` 增加 `replace github.com/Autumn-27/norma => ./norma`。
- `go.sum` 删除对远程 norma 模块的校验行。

这一步通常无冲突。完成后必须验证：

```bash
go build ./...
```

> 如果 Go 提示需要下载 `go1.26.3` toolchain，属正常现象；首次构建会拉取工具链。

### 步骤 4：合入 Docker 构建链

`031dff5`、`82b6e76`、`c764649`、`eac1e74` 都改同一个 `Dockerfile`，逐个 cherry-pick 会产生多轮冲突。推荐：

1. 先 cherry-pick 不涉及 Dockerfile 的发布工作流与忽略规则：

```bash
git cherry-pick eac1e74
```

预期冲突：

```text
Dockerfile
.github/workflows/release.yml
```

2. 对 `Dockerfile` **采用线上版本作为基底**：

```bash
git checkout --theirs Dockerfile
git add Dockerfile
```

3. 再把本地代理池需要的两段加回 `Dockerfile` 的 runtime 阶段（见下方“Dockerfile 融合模板”）。

4. `.dockerignore` 与 `docker-compose.yml` 采用线上版本：

```bash
git checkout --theirs .dockerignore docker-compose.yml
git add .dockerignore docker-compose.yml
```

5. `release.yml` 若冲突，采用线上版本：

```bash
git checkout --theirs .github/workflows/release.yml
git add .github/workflows/release.yml
```

6. 继续 cherry-pick 解决：

```bash
git cherry-pick --continue
```

> 如果 `eac1e74` 因依赖前三个 Docker 提交而无法直接应用，改用：
>
> ```bash
> git checkout artex2/main -- Dockerfile .dockerignore docker-compose.yml .github/workflows/release.yml .github/workflows/docker-release.yml scripts/smoke-docker.py
> git add ...
> git commit -m "build: merge artex2 docker build/release pipeline"
> ```
>
> 然后再手工补回代理池层。这样比逐个 cherry-pick 更省事。

#### Dockerfile 融合模板

线上新 Dockerfile 的 runtime 阶段（`FROM python:3.12-slim-bookworm AS runtime`）里，在 `apt-get install` 列表中补：

```dockerfile
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap proxychains4 \
```

并在该 `RUN` 之后加入本地代理池的两段：

```dockerfile
# proxychains: LD_PRELOAD 强制代理原始 TCP 工具(nmap/nc 等)。
# so 链到统一路径(Go 代码写死该路径,与架构无关);配置指向容器内常驻的
# MITM 记录代理 :8788 —— ARTEX 在池开启时把它的上游换成代理池逐请求轮换。
RUN ln -sf "$(dpkg -L libproxychains4 | grep "libproxychains.so")" /usr/local/lib/libproxychains4.so \
    && printf "strict_chain\nproxy_dns\ntcp_read_time_out 15000\ntcp_connect_time_out 10000\n[ProxyList]\nhttp 127.0.0.1 8788\n" > /etc/proxychains4.conf

# nmap wrapper: 末置 -sT 强制 connect 扫描，让每次连接都经 libc connect() 走代理出口。
RUN printf '#!/bin/sh\nexec /usr/bin/nmap "$@" -sT\n' > /usr/local/bin/nmap && chmod +x /usr/local/bin/nmap
```

注意：

- 线上 Dockerfile 里 `nmap` 由 apt 安装，路径是 `/usr/bin/nmap`；wrapper 写入 `/usr/local/bin/nmap` 会优先命中。
- 不要删除线上的 `PLAYWRIGHT_VERSION`、`PLAYWRIGHT_DOWNLOAD_HOST`、`artex-chromium` 软链和 `/app/node_modules` 软链。
- 本地若继续复用 CI 预编译二进制，构建时传 `--build-arg ARTEX_BINARY_SOURCE=prebuilt`，并把 `dist/<arch>/artex` 放在构建上下文；默认源码构建也保留。

完成后验证：

```bash
docker build -t artex:merge-test .
docker run --rm artex:merge-test ls -l /usr/local/lib/libproxychains4.so
docker run --rm --entrypoint /usr/local/bin/nmap artex:merge-test --version
docker run --rm artex:merge-test cat /etc/proxychains4.conf
```

### 步骤 5（可选）：CHANGELOG 与文档

```bash
git cherry-pick e6ec569 7ce858f
```

- `e6ec569` 只改 `CHANGELOG.md`，可能因本地 README 翻译产生少量冲突，按需保留。
- `7ce858f` 新增 9 篇中文设计文档，通常无冲突。

**不要直接合 `703d11c`、`79f18c8`、`05f3e82`、`912e57d`、`4e5cd88` 的 README 部分**：本地 README 已翻成英文，直接合会变回中文。只单独取 `docker-compose.yml` 的镜像源更新：

```bash
git checkout artex2/main -- docker-compose.yml .dockerignore
git add docker-compose.yml .dockerignore
git commit -m "build: sync compose image source and dockerignore from artex2"
```

> 注意：线上 compose 的镜像名是 `mianhuatang11/artex`。本地若用自己的镜像仓库，需要改成对应名字。

---

## 4. 合并后验证清单

### 后端

```bash
cd /Users/lvzhentao/gitee-learn/ARTEX
go build ./...
go test ./db ./server ./report ./intercept ./agent ./traffic ./proxypool
```

重点确认：

- `parseTarget` 拒绝 `1.2.3.4.5`、`1.2.3.4444`、`999.999.999.999`、`1.2.3.4:70000`。
- 未知严重等级的 finding 导出排序不 panic。
- 报告中的证据含 ``` 时，围栏长度自动加长。
- 审批重试只重试模型审核，不重复执行工具命令。

### 认证行为

1. 停掉 PostgreSQL，访问 `GET /api/auth/status`：

```bash
curl -i http://127.0.0.1:8787/api/auth/status
# 期望：HTTP 503，body 为 "数据源暂时不可用，请稍后重试"
# 不允许：HTTP 200 {"initialized":false}
```

2. 打开 `/setup`：数据库不可用时应显示“无法确认初始化状态”并隐藏表单，而不是展示初始化密码表单。

3. 密码校验：8 位以下、72 字节以上均应被后端拒绝。

### 前端

```bash
cd web
npm run build:static
```

人工检查：

- 英文模式下 setup 页与 intercept 页无中文残留。
- 中文模式下新增文案能通过 `locale-zh.json` 正确回译。
- intercept 页 `retry_count`、`retry_interval_ms` 能保存并在刷新后回显。

### Docker

```bash
docker build -t artex:merge-test .
docker run --rm artex:merge-test ls -l /usr/local/lib/libproxychains4.so
docker run --rm --entrypoint /usr/local/bin/nmap artex:merge-test --version
```

---

## 5. 回滚

合并前记录当前提交：

```bash
git rev-parse HEAD
```

回滚到合并前：

```bash
git reset --hard <合并前提交>
```

若已推送，不要 `reset --hard` 后强推 `english-ui`；改用 `git revert <merge/commit>` 生成反向提交。

---

## 6. 风险与注意事项

1. **DB 连接池上限 32**：线上 PostgreSQL 的 `max_connections` 若低于 100，需要同步评估；不要把 `maxOpenConns` 调到接近 `max_connections`。
2. **norma 体积**：新增约 4 万行源码，首次 `go build` 会变慢；但解决了上游闭源后依赖拉不到的问题。
3. **Dockerfile 融合**：最容易出问题的一步。以线上新版为基底，只追加代理池两段；不要反过来把线上新特性逐行搬进本地旧 Dockerfile。
4. **i18n 遗漏**：任何合并进来的中文新文案都必须配英文硬编码 + `locale-zh.json` 条目，否则英文界面会中英混杂。
5. **不要使用 `git checkout --ours` 整体覆盖冲突页面**：setup 与 intercept 页面需要“本地英文 + 线上新增逻辑”手工合并。
6. **合并方向**：ARTEX2 没有本地代理池、`X-ARTEx-Token`、英文 UI。完成本地合并后，还需要把这些本地改动反向同步到线上分支/部署，否则线上功能仍缺失。

---

## 7. 提交速查表

| 提交 | 主题 | 是否建议合入 |
|---|---|---|
| `a951e4a` | auth fail-open + DB 连接池 + setup 防护 | 必须 |
| `a71da96` | parseTarget 校验 | 必须 |
| `7c218ea` | 报告围栏注入修复 | 必须 |
| `f3baec9` | findings 导出 + 有界审批重试 | 必须 |
| `69ad7b0` | norma v0.4.3 源码内联 | 建议 |
| `031dff5` | Docker 多阶段源码构建 | 建议（与本地 Dockerfile 融合） |
| `82b6e76` | 运行层 npm/playwright 国内源 | 建议 |
| `c764649` | 前端产物 COPY --from 修复 | 建议 |
| `eac1e74` | 多架构 Docker 发布 + .dockerignore 安全排除 | 建议 |
| `e6ec569` | CHANGELOG 0.3.15 | 可选 |
| `7ce858f` | 9 篇设计文档 | 可选 |
| `703d11c` / `79f18c8` / `05f3e82` | README/截图改版 | 不建议直接合（与英文 README 冲突） |
| `4e5cd88` | compose 镜像源 | 只取 compose 部分 |
| `fd86f3c` / `a9d11cb` | PR merge commit | 跳过（内容已包含在对应提交） |

---

## 8. 一键执行参考（人工确认冲突后使用）

```bash
cd /Users/lvzhentao/gitee-learn/ARTEX
git branch --show-current          # english-ui
git add -A && git commit -m "chore: checkpoint local proxy pool work"
git remote add artex2 https://github.com/lvzhentao/ARTEX2.git 2>/dev/null || true
git fetch artex2

# 安全修复
git cherry-pick a951e4a a71da96 7c218ea f3baec9
# 解决 setup/intercept 页冲突后
git add -A && git cherry-pick --continue

# norma
git cherry-pick 69ad7b0
go build ./...

# Docker/发布链（Dockerfile 建议手动取线上版再补代理层）
git checkout artex2/main -- Dockerfile .dockerignore docker-compose.yml \
  .github/workflows/release.yml .github/workflows/docker-release.yml scripts/smoke-docker.py
# 手工把 proxychains/nmap 两段加回 Dockerfile
git add -A && git commit -m "build: adopt artex2 docker pipeline with proxy pool support"

# 文档（可选）
git cherry-pick e6ec569 7ce858f
```

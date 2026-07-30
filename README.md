# SMTP 发信测试器

一个开源的中文 SMTP 诊断工具，提供两个使用方式：

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/y08lin4/Main-SMTP-test/tree/main/worker)

- **Windows 本地客户端**：单文件 Go EXE，从当前电脑发起连接，适合诊断本机 DNS、防火墙、证书和自定义端口。
- **Cloudflare 在线版**：从 Cloudflare 网络发起受限连接，适合快速验证公网 SMTP 服务的标准提交端口。

两端共用同一套前端与 `/api/send` 响应结构，界面会明确标注测试连接的来源。

## 功能

- STARTTLS、SSL/TLS 和本地明文 SMTP 诊断
- AUTH PLAIN 与 AUTH LOGIN
- 逐阶段识别连接、TLS、认证、发件人、收件人和 DATA 错误
- 针对 `Relay access denied`、认证失败、证书失败和超时提供中文说明
- UTF-8 邮件主题与正文
- 密码仅在当前请求内存中使用，不写入文件或日志
- 在线版限制公网域名、标准端口并进行按来源限流

## Windows 客户端

### 下载

在 GitHub 仓库的 **Releases** 页面下载：

- `SMTP-Tester-Windows-x64.exe`：大多数 Windows 10/11 电脑
- `SMTP-Tester-Windows-arm64.exe`：Windows on ARM 设备

双击 EXE 后会打开一个命令窗口并自动启动浏览器。关闭命令窗口即可停止本地服务。

### 本地构建

需要 Go 1.23 或更高版本：

```powershell
go test ./...
go build -trimpath -o SMTP-Tester-Windows-x64.exe .
```

构建出的程序将 `web/index.html` 嵌入 EXE，不需要额外文件或 Node.js。

如需让客户端显示 GitHub 和下载链接：

```powershell
.\scripts\build-windows.ps1 -Version v0.1.0 -GitHubUrl https://github.com/y08lin4/Main-SMTP-test
```

## Cloudflare 在线版

在线版使用 Workers TCP Sockets 连接目标 SMTP 服务，静态页面由 Workers Assets 提供。

### 安全边界

公开服务不能等同于本地诊断工具，代码强制执行以下限制：

- 仅接受公网域名，不接受 IP、`localhost` 或私网目标
- DNS A/AAAA 记录中出现私网、保留或文档地址时拒绝连接
- STARTTLS 固定使用 `587`，SSL/TLS 固定使用 `465`
- 不支持端口 `25`、任意端口、明文认证或忽略证书错误
- 每个来源 IP 每分钟最多 5 次测试
- 限制请求体、主题、正文和凭据长度
- 不记录 `/api/send` 请求体、SMTP 密码或认证命令
- 同源请求检查与严格响应安全头

Cloudflare 的速率限制不是精确计数器。正式公开后还应在 Cloudflare 控制台配置 WAF、自定义速率限制规则和用量告警。

### 手动部署

需要 Node.js 20+ 和 Cloudflare 账号：

```powershell
cd worker
npm install
npx wrangler login
npx wrangler deploy
```

也可使用 README 顶部的 **Deploy to Cloudflare** 按钮：Cloudflare 会 fork/连接本仓库并引导完成首次 Worker 部署。首次部署完成后，仍需在 Worker 的 Settings > Variables and Secrets 中设置生产配置。

部署前修改 `worker/wrangler.jsonc` 中的变量：

```json
"vars": {
  "APP_VERSION": "v0.1.0",
  "GITHUB_URL": "https://github.com/y08lin4/Main-SMTP-test",
  "DOWNLOAD_URL": "",
  "PUBLIC_BASE_URL": "https://smtp.example.com"
}
```

`DOWNLOAD_URL` 留空时，页面会根据 `GITHUB_URL` 自动使用最新 Release 的 x64 EXE 地址。

`PUBLIC_BASE_URL` 建议设置为正式自定义域名。未设置时，canonical 与 sitemap 会使用当前请求域名。在线版动态提供 `robots.txt`、`sitemap.xml` 和绝对 canonical；Windows 本地版发送 `noindex` 响应头，不参与搜索引擎收录。

### mail-tester 官方 API

投递质量页面使用 [mail-tester 官方 API](https://www.mail-tester.com/api-documentation)，不会抓取报告网页或规避上游限制。使用前先创建 mail-tester 账号，并在 `worker/wrangler.jsonc` 配置账号用户名及该账号实际显示的收件域：

```json
"MAIL_TESTER_USERNAME": "your-account-name",
"MAIL_TESTER_INBOX_DOMAIN": "your-assigned-mail-tester-domain"
```

收件域必须以 mail-tester 账号页面实际分配的信息为准，不要根据示例猜测。然后设置至少 32 字节的报告令牌签名 Secret：

```powershell
cd worker
npx wrangler secret put MAIL_TESTER_TOKEN_SECRET
```

配置完整后，主页会显示“投递质量检测”入口，并加入 sitemap。Worker 为每次测试生成高熵地址后缀和 30 分钟 HMAC 签名令牌；浏览器无法查询其他地址的报告。报告轮询单独限流，完成结果缓存 5 分钟。上游返回 `403/429` 时服务会停止调用并显示错误，不进行来源伪装或代理绕过。

### GitHub Actions 自动部署

仓库包含 `.github/workflows/deploy-worker.yml`。在 GitHub 仓库配置以下 Actions secrets：

- `CLOUDFLARE_API_TOKEN`
- `CLOUDFLARE_ACCOUNT_ID`

推送到 `main` 后会先执行 Go 测试和 Worker 类型检查，再部署在线版。打 `v*` 标签会构建 Windows x64/arm64 客户端并发布到 GitHub Releases。

## 本地开发

运行 Go 客户端：

```powershell
go run .
```

浏览器会打开 `http://127.0.0.1:8765`。如果端口已占用，程序会自动选择一个本机空闲端口。

运行 Worker：

```powershell
cd worker
npm install
npm run dev
```

## 测试结果的含义

“发送成功”表示目标 SMTP 服务器已经返回 `250` 并接受邮件内容，不保证邮件最终进入收件箱。后续仍可能受到队列、反垃圾规则、SPF、DKIM、DMARC 或收件方策略影响。

在线版反映 Cloudflare 到 SMTP 服务器的路径；本地版反映当前 Windows 电脑到 SMTP 服务器的路径。两者结果不同通常是有价值的网络诊断信号。

## 投递质量检测规划

本项目计划加入类似 mail-tester.com 的临时收件地址与邮件质量报告，但它需要独立的入站邮件和分析基础设施，不属于 `/api/send` 的连接测试范围。架构比较、报告模型、安全边界和分阶段实施方案见 [投递质量检测路线图](docs/DELIVERABILITY_ROADMAP.md)。

## 许可证

[MIT](LICENSE)

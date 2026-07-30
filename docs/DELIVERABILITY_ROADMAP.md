# 投递质量检测路线图

## 与当前工具的边界

当前 SMTP 发信测试器解决的是“客户端能否连接、认证并把邮件提交给 SMTP 服务器”。类似 mail-tester.com 的系统解决的是“邮件到达一个受控收件箱后，其身份认证、内容和发送基础设施是否有利于投递”。

两者应共享产品入口和报告视觉语言，但保持独立后端：

```text
SMTP 连接测试
浏览器/Windows 客户端 -> /api/send -> 用户指定的 SMTP 服务器

投递质量检测
分配临时地址 -> 用户发送邮件 -> 入站 MX -> 分析队列 -> 评分报告
```

## 参考系统能力

mail-tester.com 的公开流程是：分配临时收件地址、等待用户发送邮件、生成评分报告。公开 API 报告包含：

1. `messageInfo`：主题、接收时间、退信地址等邮件信息。
2. `spamAssassin`：SpamAssassin 规则、扣分和建议。
3. `signature`：SPF、DKIM、rDNS 等身份认证结果。
4. `body`：HTML/纯文本版本、比例、标签和图片替代文本。
5. `blacklists`：发送 IP/域名的黑名单查询结果。
6. `links`：邮件中的失效链接。

每项报告统一包含标题、分数、状态、描述和修复建议。这一数据模型适合本项目后续复用。

## 路线 A：接入第三方 API

优点：开发快，不需要维护入站邮件和反垃圾分析基础设施。

限制：需要付费账号或 API 授权；邮件正文和发送基础设施数据会交给第三方；可用性、额度、报告结构和商业条款受第三方控制。

建议：只作为早期需求验证或可选提供商，不把核心产品绑定到单一第三方。接入前必须确认 API 商业使用、缓存、再展示和隐私条款。

### 合规接入约束

- 只使用 mail-tester 官方账号和其允许的 API URL，不抓取结果页面或伪装浏览器来源。
- API 账号或令牌仅保存在 Worker Secret/服务端密钥管理中，绝不返回给浏览器。
- 服务端设置明确、稳定的产品 `User-Agent`；不伪造 `Referer`、轮换来源或尝试绕过 `403/429`。
- 对报告做短期缓存并按用户和全局额度限流，避免重复触发同一分析。
- 遇到配额、拒绝或服务不可用时停止调用并向用户显示降级状态，不自动切换隐蔽代理。
- 页面明确标注报告由 mail-tester 提供，并保留其条款、隐私政策和来源链接。
- 使用 provider 接口隔离第三方结构，例如 `CreateInbox`、`GetReport` 和 `NormalizeReport`，以便未来切换自建分析器。

## 路线 B：自建服务

### 推荐架构

```text
Cloudflare Pages/Worker
  |-- POST /api/inboxes        分配短期测试地址
  |-- GET  /api/reports/:token 查询状态与报告
  |
Cloudflare Email Routing（测试域 Catch-all）
  |-- Email Worker 校验收件 token
  |-- 原始 EML 写入 R2（短 TTL）
  |-- D1 写入任务状态
  `-- Queue 投递分析任务

隔离分析服务（容器/VPS）
  |-- MIME/HTML/链接静态分析
  |-- SPF、DKIM、DMARC、rDNS
  |-- Rspamd 或 SpamAssassin
  `-- 受控 DNSBL 查询
```

Cloudflare Worker 不能作为通用公网 SMTP 服务器监听 TCP 25，但 Email Routing 可以把发往自有测试域的邮件交给 Email Worker。Catch-all 路由配合高熵 token 可生成临时地址。SpamAssassin/Rspamd 等重型或需要本地规则库的分析不适合直接运行在 Worker 中，应由隔离容器处理。

### 数据模型

- `inbox`: token、临时地址、创建时间、过期时间、状态。
- `message`: inbox token、R2 对象键、接收时间、Envelope From、源 IP 摘要。
- `report`: 总分、状态、分析器版本、完成时间。
- `checks`: `authentication`、`content`、`reputation`、`links`、`headers` 分类下的独立检查项。
- `check`: key、title、status、score、summary、suggestions、evidence。

### 安全与隐私

- 临时地址使用至少 128 位随机 token，不允许枚举。
- 原始邮件默认 24 小时自动删除，报告默认 7 天删除；页面明确展示保留期。
- R2 原始邮件与报告分桶或分前缀授权，分析服务只取单个任务。
- HTML 不直接渲染；预览必须消毒并禁用远程资源。
- 链接检测默认只做解析和 DNS/HEAD 受控探测，防止 SSRF 和追踪像素回连。
- DNSBL 查询遵守各列表使用条款和速率限制，不能把公共 DNS 解析器当作批量黑名单代理。
- 报告 URL 使用不可猜 token，可选一次性访问或用户账号绑定。

## 推荐实施阶段

### 阶段 1：静态邮件报告

实现临时地址、Cloudflare Email Routing 入站、R2 短期存储、MIME/头部解析、SPF/DKIM/DMARC 结果展示。先不做总分，使用通过/警告/失败，避免伪精确评分。

### 阶段 2：内容与信誉

增加 HTML/纯文本质量、危险标签、缺失 `alt`、链接语法、rDNS 和有限 DNSBL 查询。引入隔离分析容器和队列重试。

### 阶段 3：规则评分与历史

接入 Rspamd/SpamAssassin，建立透明的评分权重、规则版本和变更记录。再考虑账号、历史报告、API 和团队配额。

## 当前仓库预留

当前 `/api/send` 保持专注于 SMTP 提交测试。未来新增接口统一放在 `/api/inboxes` 与 `/api/reports`，不会让 Windows 客户端保存入站邮件。共享前端可在二期增加“连接测试 / 投递质量”两个顶层标签页。

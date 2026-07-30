# 安全政策

## 报告漏洞

请通过 GitHub Security Advisory 私下报告漏洞：

https://github.com/y08lin4/Main-SMTP-test/security/advisories/new

不要在公开 Issue 中提交 SMTP 密码、授权码、完整邮件内容、Worker Secret 或可用的 mail-tester 报告令牌。

## 支持范围

安全更新以最新 GitHub Release 和 `main` 分支为准。第三方 SMTP 服务、mail-tester、Cloudflare 和用户自行部署环境的可用性或安全策略不属于本项目控制范围。

## 部署者责任

- 使用受限 Cloudflare API Token，并保护 GitHub Actions secrets。
- 为公开 Worker 配置 WAF、速率限制、用量告警和自定义域名。
- 使用至少 32 字节随机值作为 `MAIL_TESTER_TOKEN_SECRET`。
- 不在日志、Issue 或监控事件中记录 SMTP 密码、认证命令和原始邮件内容。

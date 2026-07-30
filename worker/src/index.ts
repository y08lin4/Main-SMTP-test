import { connect } from "cloudflare:sockets";

type CloudflareSocket = ReturnType<typeof connect>;

interface Env {
  ASSETS: Fetcher;
  RATE_LIMITER: RateLimit;
  APP_VERSION?: string;
  GITHUB_URL?: string;
  PUBLIC_BASE_URL?: string;
}

interface SmtpInput {
  host: string;
  port: number;
  security: "starttls" | "ssl";
  username: string;
  password: string;
  from: string;
  fromName?: string;
  unsubscribe?: string;
  to: string;
  subject: string;
  message: string;
  allowInvalidCert?: boolean;
}

interface SmtpReply {
  code: number;
  text: string;
}

const MAX_BODY_BYTES = 128 * 1024;
const SESSION_TIMEOUT_MS = 20_000;
const CONNECT_TIMEOUT_MS = 10_000;
const encoder = new TextEncoder();

class SmtpFailure extends Error {
  constructor(
    readonly stage: string,
    message: string,
    readonly response = "",
  ) {
    super(message);
    this.name = "SmtpFailure";
  }
}

class SmtpSession {
  private reader: ReadableStreamDefaultReader<Uint8Array>;
  private writer: WritableStreamDefaultWriter<Uint8Array>;
  private readonly decoder = new TextDecoder();
  private buffer = "";

  constructor(private socket: CloudflareSocket) {
    this.reader = socket.readable.getReader();
    this.writer = socket.writable.getWriter();
  }

  static async open(input: SmtpInput): Promise<SmtpSession> {
    const socket = connect(
      { hostname: input.host, port: input.port },
      { secureTransport: input.security === "ssl" ? "on" : "starttls", allowHalfOpen: false },
    );
    await withTimeout(socket.opened, CONNECT_TIMEOUT_MS, "连接 SMTP 服务器超时");
    return new SmtpSession(socket);
  }

  async startTLS(): Promise<void> {
    this.reader.releaseLock();
    this.writer.releaseLock();
    this.socket = this.socket.startTls();
    await withTimeout(this.socket.opened, CONNECT_TIMEOUT_MS, "TLS 握手超时");
    this.reader = this.socket.readable.getReader();
    this.writer = this.socket.writable.getWriter();
    this.buffer = "";
  }

  async readReply(): Promise<SmtpReply> {
    const lines: string[] = [];
    let replyCode = 0;
    for (;;) {
      const line = await this.readLine();
      if (!/^\d{3}[ -]/.test(line)) {
        throw new Error("SMTP 服务器返回了无法识别的响应");
      }
      const code = Number(line.slice(0, 3));
      if (replyCode === 0) replyCode = code;
      if (code !== replyCode) throw new Error("SMTP 多行响应代码不一致");
      lines.push(line);
      if (line[3] === " ") return { code, text: lines.join("\n") };
    }
  }

  async command(value: string): Promise<SmtpReply> {
    await this.writer.write(encoder.encode(value + "\r\n"));
    return this.readReply();
  }

  async writeMessage(value: string): Promise<SmtpReply> {
    await this.writer.write(encoder.encode(value + "\r\n.\r\n"));
    return this.readReply();
  }

  close(): void {
    try { this.reader.releaseLock(); } catch { /* already released */ }
    try { this.writer.releaseLock(); } catch { /* already released */ }
    void this.socket.close();
  }

  private async readLine(): Promise<string> {
    for (;;) {
      const newline = this.buffer.indexOf("\n");
      if (newline >= 0) {
        const line = this.buffer.slice(0, newline).replace(/\r$/, "");
        this.buffer = this.buffer.slice(newline + 1);
        return line;
      }
      const result = await this.reader.read();
      if (result.done) throw new Error("SMTP 服务器提前关闭了连接");
      this.buffer += this.decoder.decode(result.value, { stream: true });
      if (this.buffer.length > 256 * 1024) throw new Error("SMTP 服务器响应异常过大");
    }
  }
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const publicBase = (env.PUBLIC_BASE_URL || url.origin).replace(/\/$/, "");
    if (url.pathname === "/robots.txt" && request.method === "GET") {
      return text(`User-agent: *\nAllow: /\nDisallow: /api/\nSitemap: ${publicBase}/sitemap.xml\n`, "text/plain; charset=utf-8");
    }
    if (url.pathname === "/sitemap.xml" && request.method === "GET") {
      return text(`<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>${escapeXML(publicBase)}/</loc></url><url><loc>${escapeXML(publicBase)}/privacy.html</loc></url></urlset>\n`, "application/xml; charset=utf-8");
    }
    if (url.pathname === "/api/meta" && request.method === "GET") {
      const githubUrl = (env.GITHUB_URL || "").replace(/\/$/, "");
      return json({
        mode: "online",
        version: env.APP_VERSION || "dev",
        githubUrl,
        allowPlain: false,
        allowCustomPort: false,
      });
    }
    if (url.pathname === "/api/send" && request.method === "POST") {
      return handleSend(request, env);
    }
    if (url.pathname.startsWith("/api/")) return json({ success: false, message: "接口不存在" }, 404);
    if (url.pathname === "/deliverability.html") return withSecurityHeaders(new Response("页面已迁移到独立实验项目", { status: 410, headers: { "Content-Type": "text/plain; charset=utf-8", "X-Robots-Tag": "noindex, nofollow" } }));
    if (url.pathname === "/index.html") return Response.redirect(`${url.origin}/`, 301);

    const response = await env.ASSETS.fetch(request);
    const secured = withSecurityHeaders(response);
    if (secured.headers.get("content-type")?.includes("text/html")) {
      const canonicalPath = url.pathname === "/index.html" ? "/" : url.pathname;
      const canonical = publicBase + canonicalPath;
      return new HTMLRewriter().on("head", {
        element(element) {
          element.append(`<link rel="canonical" href="${escapeHTML(canonical)}"><meta property="og:url" content="${escapeHTML(canonical)}">`, { html: true });
        },
      }).transform(secured);
    }
    return secured;
  },
} satisfies ExportedHandler<Env>;

async function handleSend(request: Request, env: Env): Promise<Response> {
  if (!validOrigin(request)) return json({ success: false, stage: "policy", message: "请求来源不受信任" }, 403);
  if (!request.headers.get("content-type")?.toLowerCase().startsWith("application/json")) {
    return json({ success: false, stage: "input", message: "请求格式必须是 JSON" }, 415);
  }
  const contentLength = Number(request.headers.get("content-length") || 0);
  if (contentLength > MAX_BODY_BYTES) return json({ success: false, stage: "input", message: "请求内容过大" }, 413);

  const clientKey = request.headers.get("CF-Connecting-IP") || "anonymous";
  const rate = await env.RATE_LIMITER.limit({ key: clientKey });
  if (!rate.success) {
    return json({ success: false, stage: "rate", message: "测试请求过于频繁", detail: "每个来源每分钟最多进行 5 次测试，请稍后再试。" }, 429);
  }

  let raw: unknown;
  try {
    const body = await request.text();
    if (encoder.encode(body).byteLength > MAX_BODY_BYTES) throw new Error("请求内容过大");
    raw = JSON.parse(body);
  } catch (error) {
    return json({ success: false, stage: "input", message: "请求内容不是有效 JSON", detail: safeError(error) }, 400);
  }

  const started = Date.now();
  let input: SmtpInput;
  try {
    input = validateInput(raw);
    await assertPublicHost(input.host);
    const result = await withTimeout(sendMail(input), SESSION_TIMEOUT_MS, "SMTP 测试超时");
    return json({ ...result, durationMs: Date.now() - started });
  } catch (error) {
    return json({ success: false, ...explainError(error), durationMs: Date.now() - started }, 400);
  }
}

function validateInput(raw: unknown): SmtpInput {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new SmtpFailure("input", "请求内容不正确");
  const value = raw as Record<string, unknown>;
  const clean = (field: string, max: number): string => {
    if (typeof value[field] !== "string") throw new SmtpFailure("input", `${field} 不能为空`);
    const result = String(value[field]).replace(/[\r\n]+/g, " ").trim();
    if (!result || result.length > max) throw new SmtpFailure("input", `${field} 格式不正确`);
    return result;
  };
  const host = clean("host", 253).toLowerCase();
  if (!isDomain(host)) throw new SmtpFailure("policy", "云端服务只允许公网 SMTP 域名", "不能使用 IP 地址、localhost 或格式不正确的主机名。");
  const security = value.security;
  const port = Number(value.port);
  if (security !== "starttls" && security !== "ssl") throw new SmtpFailure("policy", "云端服务只支持加密 SMTP");
  if ((security === "starttls" && port !== 587) || (security === "ssl" && port !== 465)) {
    throw new SmtpFailure("policy", "云端服务仅允许标准 SMTP 提交端口", "STARTTLS 使用 587，SSL/TLS 使用 465。");
  }
  const from = clean("from", 320);
  const fromName = cleanOptionalDisplayName(value.fromName);
  const unsubscribe = cleanOptionalUnsubscribe(value.unsubscribe);
  const to = clean("to", 320);
  if (!isMailbox(from)) throw new SmtpFailure("input", "发件人格式不正确");
  if (!isMailbox(to)) throw new SmtpFailure("input", "收件人格式不正确");
  const message = typeof value.message === "string" ? value.message : "";
  if (!message.trim() || message.length > 65_536) throw new SmtpFailure("input", "邮件内容不能为空或过长");
  const password = typeof value.password === "string" ? value.password : "";
  if (!password || password.length > 1024) throw new SmtpFailure("input", "密码或授权码不能为空或过长");
  return {
    host, port, security,
    username: clean("username", 320),
    password,
    from, fromName, unsubscribe, to,
    subject: clean("subject", 998),
    message,
    allowInvalidCert: false,
  };
}

function cleanOptionalDisplayName(value: unknown): string {
  if (value === undefined || value === null || value === "") return "";
  if (typeof value !== "string") throw new SmtpFailure("input", "发件人显示名格式不正确");
  const name = value.trim();
  if (name.length > 128 || encoder.encode(name).byteLength > 256 || /[\u0000-\u001F\u007F-\u009F\u2028\u2029]/.test(name)) {
    throw new SmtpFailure("input", "发件人显示名格式不正确");
  }
  return name;
}

function cleanOptionalUnsubscribe(value: unknown): string {
  if (value === undefined || value === null || value === "") return "";
  if (typeof value !== "string" || value.length > 512 || /[\u0000-\u001F\u007F-\u009F]/.test(value)) {
    throw new SmtpFailure("input", "退订地址格式不正确");
  }
  let url: URL;
  try { url = new URL(value.trim()); } catch { throw new SmtpFailure("input", "退订地址格式不正确"); }
  if (url.protocol === "mailto:" && isMailbox(decodeURIComponent(url.pathname))) return `mailto:${decodeURIComponent(url.pathname)}`;
  if (url.protocol === "https:" && isDomain(url.hostname)) return url.toString();
  throw new SmtpFailure("input", "退订地址仅支持有效的 HTTPS 地址或 mailto 邮箱");
}

async function assertPublicHost(host: string): Promise<void> {
  const answers = await Promise.all([resolveDNS(host, "A"), resolveDNS(host, "AAAA")]);
  const addresses = answers.flat();
  if (addresses.length === 0) throw new SmtpFailure("connect", "找不到 SMTP 服务器", "公网 DNS 未返回 A 或 AAAA 记录。");
  if (addresses.some((address) => !isPublicAddress(address))) {
    throw new SmtpFailure("policy", "云端服务不能连接私网或保留地址", "请使用可从公网访问的 SMTP 服务器。");
  }
}

async function resolveDNS(host: string, type: "A" | "AAAA"): Promise<string[]> {
  const response = await fetch(`https://cloudflare-dns.com/dns-query?name=${encodeURIComponent(host)}&type=${type}`, {
    headers: { Accept: "application/dns-json" },
    signal: AbortSignal.timeout(5_000),
  });
  if (!response.ok) throw new SmtpFailure("connect", "DNS 查询失败");
  const data = await response.json() as { Answer?: Array<{ type: number; data: string }> };
  const expected = type === "A" ? 1 : 28;
  return (data.Answer || []).filter((answer) => answer.type === expected).map((answer) => answer.data);
}

function isDomain(host: string): boolean {
  if (host === "localhost" || host.length > 253 || /^\d+(?:\.\d+){3}$/.test(host) || host.includes(":")) return false;
  return host.split(".").length >= 2 && host.split(".").every((label) => /^(?!-)[a-z0-9-]{1,63}(?<!-)$/.test(label));
}

function isMailbox(value: string): boolean {
  return /^[^\s<>@]+@[^\s<>@]+\.[^\s<>@]+$/.test(value);
}

function isPublicAddress(address: string): boolean {
  if (address.includes(":")) {
    const lower = address.toLowerCase();
    if (lower === "::" || lower === "::1" || lower.startsWith("fc") || lower.startsWith("fd") || /^fe[89ab]/.test(lower) || lower.startsWith("ff") || lower.startsWith("2001:db8:")) return false;
    if (lower.startsWith("::ffff:")) return isPublicAddress(lower.slice(7));
    return true;
  }
  const parts = address.split(".").map(Number);
  if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) return false;
  const [a, b, c] = parts;
  return !(
    a === 0 || a === 10 || a === 127 || a >= 224 ||
    (a === 100 && b >= 64 && b <= 127) ||
    (a === 169 && b === 254) ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) ||
    (a === 192 && b === 0 && c === 0) ||
    (a === 192 && b === 0 && c === 2) ||
    (a === 198 && (b === 18 || b === 19)) ||
    (a === 198 && b === 51 && c === 100) ||
    (a === 203 && b === 0 && c === 113)
  );
}

async function sendMail(input: SmtpInput): Promise<Record<string, unknown>> {
  let session: SmtpSession | undefined;
  try {
    session = await SmtpSession.open(input);
    let response = await session.readReply();
    expectReply(response, [220], "connect", "SMTP 服务器没有正常响应");
    response = await session.command("EHLO smtp-tester.online");
    expectReply(response, [250], "connect", "SMTP 服务器拒绝 EHLO");

    if (input.security === "starttls") {
      if (!response.text.toUpperCase().includes("STARTTLS")) throw new SmtpFailure("tls", "服务器没有提供 STARTTLS 加密", response.text);
      const startTLS = await session.command("STARTTLS");
      expectReply(startTLS, [220], "tls", "服务器拒绝启动 TLS");
      await session.startTLS();
      response = await session.command("EHLO smtp-tester.online");
      expectReply(response, [250], "tls", "TLS 建立后服务器拒绝 EHLO");
    }

    await authenticate(session, response.text, input.username, input.password);
    response = await session.command(`MAIL FROM:<${input.from}>`);
    expectReply(response, [250], "sender", "服务器拒绝发件人地址");
    response = await session.command(`RCPT TO:<${input.to}>`);
    expectReply(response, [250, 251, 252], "recipient", "服务器拒绝收件人或不允许向外部域中继");
    response = await session.command("DATA");
    expectReply(response, [354], "data", "服务器不接受邮件内容");
    response = await session.writeMessage(buildMessage(input));
    expectReply(response, [250], "data", "服务器未接受这封邮件");
    const detail = response.text;
    try { await session.command("QUIT"); } catch { /* message was already accepted */ }
    return {
      success: true,
      stage: "done",
      message: "测试邮件已被 SMTP 服务器接受",
      detail,
      server: `${input.host}:${input.port}`,
      accepted: input.to,
    };
  } finally {
    session?.close();
  }
}

async function authenticate(session: SmtpSession, capabilities: string, username: string, password: string): Promise<void> {
  const upper = capabilities.toUpperCase();
  let response: SmtpReply;
  if (upper.includes("AUTH") && upper.includes("PLAIN")) {
    response = await session.command("AUTH PLAIN " + encodeBase64(`\0${username}\0${password}`));
    expectReply(response, [235], "auth", "SMTP 认证失败");
    return;
  }
  response = await session.command("AUTH LOGIN");
  expectReply(response, [334], "auth", "服务器不支持 LOGIN 认证");
  response = await session.command(encodeBase64(username));
  expectReply(response, [334], "auth", "SMTP 账号未被服务器接受");
  response = await session.command(encodeBase64(password));
  expectReply(response, [235], "auth", "SMTP 认证失败");
}

function buildMessage(input: SmtpInput): string {
  const subject = encodeHeaderText(input.subject);
  const from = input.fromName ? `${encodeHeaderPhrase(input.fromName)} <${input.from}>` : `<${input.from}>`;
  const plain = input.message.replace(/\r\n/g, "\n").replace(/\r/g, "\n").replace(/\n/g, "\r\n");
  const html = escapeHTML(input.message).replace(/\r\n|\r|\n/g, "<br>\r\n");
  const boundary = `=_smtp_tester_${crypto.randomUUID().replace(/-/g, "")}`;
  const headers = [
    `Date: ${new Date().toUTCString()}`,
    `Message-ID: <${crypto.randomUUID()}@${input.host}>`,
    `From: ${from}`, `To: <${input.to}>`, `Subject: ${subject}`,
    ...(input.unsubscribe ? [`List-Unsubscribe: <${input.unsubscribe}>`] : []),
    "MIME-Version: 1.0", `Content-Type: multipart/alternative; boundary="${boundary}"`,
    "X-Mailer: smtp tester online", "",
  ];
  const body = [
    `--${boundary}`, "Content-Type: text/plain; charset=UTF-8", "Content-Transfer-Encoding: 8bit", "", plain,
    `--${boundary}`, "Content-Type: text/html; charset=UTF-8", "Content-Transfer-Encoding: 8bit", "",
    "<!doctype html>", `<html lang="zh-CN"><body>${html}</body></html>`, `--${boundary}--`, "",
  ];
  return [...headers, ...body].join("\r\n").replace(/(^|\r\n)\./g, "$1..");
}

function encodeHeaderText(value: string): string {
  return /^[\x20-\x7e]*$/.test(value) ? value : `=?UTF-8?B?${encodeBase64(value)}?=`;
}

function encodeHeaderPhrase(value: string): string {
  if (/^[\x20-\x7e]*$/.test(value)) return `"${value.replace(/([\\"])/g, "\\$1")}"`;
  return encodeRFC2047Words(value);
}

function encodeRFC2047Words(value: string): string {
  const chunks: string[] = [];
  let chunk = "";
  let byteLength = 0;
  for (const character of value) {
    const characterBytes = encoder.encode(character).byteLength;
    if (byteLength + characterBytes > 45 && chunk) {
      chunks.push(`=?UTF-8?B?${encodeBase64(chunk)}?=`);
      chunk = "";
      byteLength = 0;
    }
    chunk += character;
    byteLength += characterBytes;
  }
  if (chunk) chunks.push(`=?UTF-8?B?${encodeBase64(chunk)}?=`);
  return chunks.join(" ");
}

function encodeBase64(value: string): string {
  const bytes = encoder.encode(value);
  let binary = "";
  for (let index = 0; index < bytes.length; index += 0x8000) binary += String.fromCharCode(...bytes.subarray(index, index + 0x8000));
  return btoa(binary);
}

function expectReply(response: SmtpReply, accepted: number[], stage: string, message: string): void {
  if (!accepted.includes(response.code)) throw new SmtpFailure(stage, message, response.text);
}

function explainError(error: unknown): { stage: string; message: string; detail: string } {
  let stage = error instanceof SmtpFailure ? error.stage : "connect";
  let message = error instanceof Error ? error.message : "SMTP 测试失败";
  let detail = error instanceof SmtpFailure && error.response ? error.response : "请检查 SMTP 配置后重试。";
  const lower = `${message} ${detail}`.toLowerCase();
  if (lower.includes("relay access denied") || lower.includes("relay denied")) {
    stage = "recipient"; message = "服务器拒绝向外部域中继（Relay access denied）"; detail = "账号可能已经认证成功，但没有给外部邮箱发信的权限。请联系邮件管理员开放外发权限。";
  } else if (/\b535\b/.test(lower) || lower.includes("authentication") || lower.includes("auth failed")) {
    stage = "auth"; message = "SMTP 账号认证失败"; detail = "请确认账号、密码或授权码是否正确，并检查服务器是否允许 SMTP AUTH。";
  } else if (lower.includes("certificate") || lower.includes("tls")) {
    stage = "tls"; message = "TLS 加密协商或证书校验失败"; detail = "云端服务始终严格校验证书；请使用受信任证书后重试。";
  } else if (lower.includes("timeout") || lower.includes("超时")) {
    message = "连接或等待 SMTP 响应超时"; detail = "请检查 SMTP 服务是否允许来自 Cloudflare 网络的 465/587 端口连接。";
  }
  return { stage, message, detail };
}

function validOrigin(request: Request): boolean {
  const origin = request.headers.get("origin");
  return !origin || origin === new URL(request.url).origin;
}

function json(payload: unknown, status = 200): Response {
  return withSecurityHeaders(new Response(JSON.stringify(payload), { status, headers: { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store", "X-Robots-Tag": "noindex, nofollow, noarchive" } }));
}

function text(body: string, contentType: string): Response {
  return withSecurityHeaders(new Response(body, { headers: { "Content-Type": contentType, "Cache-Control": "public, max-age=3600" } }));
}

function escapeXML(value: string): string {
  return value.replace(/[<>&'\"]/g, (character) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", "'": "&apos;", "\"": "&quot;" })[character] || character);
}

function escapeHTML(value: string): string {
  return value.replace(/[&<>\"]/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;" })[character] || character);
}

function withSecurityHeaders(response: Response): Response {
  const headers = new Headers(response.headers);
  headers.set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'");
  headers.set("Referrer-Policy", "no-referrer");
  headers.set("X-Content-Type-Options", "nosniff");
  headers.set("X-Frame-Options", "DENY");
  headers.set("Permissions-Policy", "camera=(), microphone=(), geolocation=()");
  headers.set("Strict-Transport-Security", "max-age=31536000; includeSubDomains");
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
}

function safeError(error: unknown): string {
  return error instanceof Error ? error.message : "未知错误";
}

async function withTimeout<T>(promise: Promise<T>, milliseconds: number, message: string): Promise<T> {
  let timer = 0;
  const timeout = new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error(message)), milliseconds) as unknown as number; });
  try { return await Promise.race([promise, timeout]); }
  finally { clearTimeout(timer); }
}

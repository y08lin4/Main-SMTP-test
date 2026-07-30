package smtpclient

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

const defaultTimeout = 20 * time.Second

type Security string

const (
	SecurityStartTLS Security = "starttls"
	SecurityTLS      Security = "ssl"
	SecurityPlain    Security = "plain"
)

type Options struct {
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	Security         Security `json:"security"`
	Username         string   `json:"username"`
	Password         string   `json:"password"`
	From             string   `json:"from"`
	To               string   `json:"to"`
	Subject          string   `json:"subject"`
	Message          string   `json:"message"`
	AllowInvalidCert bool     `json:"allowInvalidCert"`
}

type Result struct {
	Success    bool   `json:"success"`
	Stage      string `json:"stage"`
	Message    string `json:"message"`
	Detail     string `json:"detail"`
	Server     string `json:"server"`
	Accepted   string `json:"accepted"`
	DurationMS int64  `json:"durationMs"`
}

type Failure struct {
	Stage    string
	Message  string
	Response string
	Err      error
}

func (f *Failure) Error() string {
	if f.Err != nil {
		return f.Message + ": " + f.Err.Error()
	}
	return f.Message
}

func (f *Failure) Unwrap() error { return f.Err }

type session struct {
	conn net.Conn
	tp   *textproto.Conn
}

func newSession(conn net.Conn) *session {
	return &session{conn: conn, tp: textproto.NewConn(conn)}
}

func (s *session) close() error { return s.tp.Close() }

func (s *session) read() (int, string, error) {
	code, message, err := s.tp.ReadResponse(0)
	return code, strings.TrimSpace(message), err
}

func (s *session) command(command string) (int, string, error) {
	if err := s.tp.PrintfLine("%s", command); err != nil {
		return 0, "", err
	}
	return s.read()
}

func (s *session) startTLS(ctx context.Context, config *tls.Config) error {
	tlsConn := tls.Client(s.conn, config)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return err
	}
	s.conn = tlsConn
	s.tp = textproto.NewConn(tlsConn)
	return nil
}

func validate(input Options) (Options, error) {
	input.Host = cleanHeader(input.Host)
	input.Username = cleanHeader(input.Username)
	input.From = cleanHeader(input.From)
	input.To = cleanHeader(input.To)
	input.Subject = cleanHeader(input.Subject)

	if input.Host == "" || len(input.Host) > 253 || strings.ContainsAny(input.Host, ":/ \\t\\r\\n") {
		return input, fail("input", "SMTP 服务器地址不正确", "", nil)
	}
	if input.Port < 1 || input.Port > 65535 {
		return input, fail("input", "SMTP 端口必须是 1-65535", "", nil)
	}
	if input.Security != SecurityStartTLS && input.Security != SecurityTLS && input.Security != SecurityPlain {
		return input, fail("input", "不支持的加密方式", "", nil)
	}
	if input.Username == "" || input.Password == "" {
		return input, fail("input", "登录账号和密码不能为空", "", nil)
	}
	from, err := parseMailbox(input.From)
	if err != nil {
		return input, fail("input", "发件人格式不正确", "", err)
	}
	to, err := parseMailbox(input.To)
	if err != nil {
		return input, fail("input", "收件人格式不正确", "", err)
	}
	input.From, input.To = from, to
	if input.Subject == "" || strings.TrimSpace(input.Message) == "" {
		return input, fail("input", "邮件主题和内容不能为空", "", nil)
	}
	if len(input.Subject) > 998 || len(input.Message) > 64*1024 {
		return input, fail("input", "邮件主题或内容过长", "", nil)
	}
	return input, nil
}

func parseMailbox(value string) (string, error) {
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || !strings.Contains(address.Address, "@") {
		return "", errors.New("invalid mailbox")
	}
	return address.Address, nil
}

func cleanHeader(value string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(value))
}

func fail(stage, message, response string, err error) *Failure {
	return &Failure{Stage: stage, Message: message, Response: response, Err: err}
}

func expect(code int, response string, accepted []int, stage, message string) error {
	for _, candidate := range accepted {
		if code == candidate {
			return nil
		}
	}
	return fail(stage, message, reply(code, response), nil)
}

func reply(code int, message string) string {
	if code == 0 {
		return strings.TrimSpace(message)
	}
	return strconv.Itoa(code) + " " + strings.TrimSpace(message)
}

func tlsConfig(options Options) *tls.Config {
	return &tls.Config{
		ServerName:         options.Host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: options.AllowInvalidCert, // Explicit local diagnostic option.
	}
}

func authenticate(s *session, capabilities, username, password string) error {
	upper := strings.ToUpper(capabilities)
	if strings.Contains(upper, "AUTH") && strings.Contains(upper, "PLAIN") {
		token := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
		code, message, err := s.command("AUTH PLAIN " + token)
		if err != nil {
			return fail("auth", "SMTP 认证失败", "", err)
		}
		return expect(code, message, []int{235}, "auth", "SMTP 认证失败")
	}

	code, message, err := s.command("AUTH LOGIN")
	if err != nil {
		return fail("auth", "SMTP 认证失败", "", err)
	}
	if err := expect(code, message, []int{334}, "auth", "服务器不支持 LOGIN 认证"); err != nil {
		return err
	}
	code, message, err = s.command(base64.StdEncoding.EncodeToString([]byte(username)))
	if err != nil {
		return fail("auth", "SMTP 账号未被服务器接受", "", err)
	}
	if err := expect(code, message, []int{334}, "auth", "SMTP 账号未被服务器接受"); err != nil {
		return err
	}
	code, message, err = s.command(base64.StdEncoding.EncodeToString([]byte(password)))
	if err != nil {
		return fail("auth", "SMTP 认证失败", "", err)
	}
	return expect(code, message, []int{235}, "auth", "SMTP 认证失败")
}

func Send(ctx context.Context, raw Options) (Result, error) {
	started := time.Now()
	options, err := validate(raw)
	if err != nil {
		return Result{}, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	address := net.JoinHostPort(options.Host, strconv.Itoa(options.Port))
	dialer := &net.Dialer{Timeout: defaultTimeout}
	var conn net.Conn
	if options.Security == SecurityTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig(options))
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return Result{}, fail("connect", "无法连接 SMTP 服务器", "", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(defaultTimeout)
	_ = conn.SetDeadline(deadline)

	s := newSession(conn)
	code, message, err := s.read()
	if err != nil {
		return Result{}, fail("connect", "SMTP 服务器没有正常响应", "", err)
	}
	if err := expect(code, message, []int{220}, "connect", "SMTP 服务器没有正常响应"); err != nil {
		return Result{}, err
	}

	code, capabilities, err := s.command("EHLO smtp-tester.local")
	if err != nil {
		return Result{}, fail("connect", "SMTP 服务器拒绝 EHLO", "", err)
	}
	if err := expect(code, capabilities, []int{250}, "connect", "SMTP 服务器拒绝 EHLO"); err != nil {
		return Result{}, err
	}

	if options.Security == SecurityStartTLS {
		if !strings.Contains(strings.ToUpper(capabilities), "STARTTLS") {
			return Result{}, fail("tls", "服务器没有提供 STARTTLS 加密", reply(code, capabilities), nil)
		}
		code, message, err = s.command("STARTTLS")
		if err != nil {
			return Result{}, fail("tls", "STARTTLS 加密协商失败", "", err)
		}
		if err := expect(code, message, []int{220}, "tls", "服务器拒绝启动 TLS"); err != nil {
			return Result{}, err
		}
		if err := s.startTLS(ctx, tlsConfig(options)); err != nil {
			return Result{}, fail("tls", "TLS 证书校验或握手失败", "", err)
		}
		code, capabilities, err = s.command("EHLO smtp-tester.local")
		if err != nil {
			return Result{}, fail("tls", "TLS 建立后服务器拒绝 EHLO", "", err)
		}
		if err := expect(code, capabilities, []int{250}, "tls", "TLS 建立后服务器拒绝 EHLO"); err != nil {
			return Result{}, err
		}
	}

	if err := authenticate(s, capabilities, options.Username, options.Password); err != nil {
		return Result{}, err
	}
	code, message, err = s.command("MAIL FROM:<" + options.From + ">")
	if err != nil {
		return Result{}, fail("sender", "服务器拒绝发件人地址", "", err)
	}
	if err := expect(code, message, []int{250}, "sender", "服务器拒绝发件人地址"); err != nil {
		return Result{}, err
	}
	code, message, err = s.command("RCPT TO:<" + options.To + ">")
	if err != nil {
		return Result{}, fail("recipient", "服务器拒绝收件人", "", err)
	}
	if err := expect(code, message, []int{250, 251, 252}, "recipient", "服务器拒绝收件人或不允许向外部域中继"); err != nil {
		return Result{}, err
	}
	code, message, err = s.command("DATA")
	if err != nil {
		return Result{}, fail("data", "服务器不接受邮件内容", "", err)
	}
	if err := expect(code, message, []int{354}, "data", "服务器不接受邮件内容"); err != nil {
		return Result{}, err
	}
	if err := writeMessage(s.tp.DotWriter(), options); err != nil {
		return Result{}, fail("data", "提交邮件内容失败", "", err)
	}
	code, message, err = s.read()
	if err != nil {
		return Result{}, fail("data", "服务器未接受这封邮件", "", err)
	}
	if err := expect(code, message, []int{250}, "data", "服务器未接受这封邮件"); err != nil {
		return Result{}, err
	}
	acceptedReply := reply(code, message)
	_, _, _ = s.command("QUIT")

	return Result{
		Success: true, Stage: "done", Message: "测试邮件已被 SMTP 服务器接受",
		Detail: acceptedReply, Server: address, Accepted: options.To,
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}

func writeMessage(writer io.WriteCloser, options Options) error {
	buffer := bufio.NewWriter(writer)
	randomID := make([]byte, 12)
	if _, err := rand.Read(randomID); err != nil {
		_ = writer.Close()
		return err
	}
	subject := options.Subject
	if !isASCII(subject) {
		subject = "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?="
	}
	fromDomain := options.From[strings.LastIndex(options.From, "@")+1:]
	boundary := fmt.Sprintf("=_smtp_tester_%x", randomID)
	headers := []string{
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		fmt.Sprintf("Message-ID: <%x@%s>", randomID, fromDomain),
		"From: <" + options.From + ">",
		"To: <" + options.To + ">",
		"Subject: " + subject,
		"MIME-Version: 1.0",
		fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"", boundary),
		"X-Mailer: SMTP Tester Go",
		"",
	}
	for _, header := range headers {
		if _, err := buffer.WriteString(header + "\r\n"); err != nil {
			_ = writer.Close()
			return err
		}
	}
	plain := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(options.Message)
	plain = strings.ReplaceAll(plain, "\n", "\r\n")
	htmlBody := strings.ReplaceAll(html.EscapeString(plain), "\r\n", "<br>\r\n")
	body := []string{
		"--" + boundary,
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		plain,
		"--" + boundary,
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		"<!doctype html>",
		"<html lang=\"zh-CN\"><body>" + htmlBody + "</body></html>",
		"--" + boundary + "--",
		"",
	}
	if _, err := buffer.WriteString(strings.Join(body, "\r\n")); err != nil {
		_ = writer.Close()
		return err
	}
	if err := buffer.Flush(); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

func isASCII(value string) bool {
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

type ExplainedError struct {
	Success bool   `json:"success"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

func Explain(err error) ExplainedError {
	result := ExplainedError{Success: false, Stage: "connect", Message: "SMTP 测试失败", Detail: "请检查 SMTP 配置后重试。"}
	var failure *Failure
	if errors.As(err, &failure) {
		result.Stage, result.Message = failure.Stage, failure.Message
		if failure.Response != "" {
			result.Detail = failure.Response
		} else if failure.Err != nil {
			result.Detail = failure.Err.Error()
		}
	}
	lower := strings.ToLower(result.Message + " " + result.Detail)
	switch {
	case strings.Contains(lower, "relay access denied") || strings.Contains(lower, "relay denied"):
		result.Stage, result.Message = "recipient", "服务器拒绝向外部域中继（Relay access denied）"
		result.Detail = "账号可能已经认证成功，但没有给外部邮箱发信的权限。请检查邮件服务器中继规则或联系管理员开放外发权限。"
	case strings.Contains(lower, "535") || strings.Contains(lower, "authentication") || strings.Contains(lower, "auth failed"):
		result.Stage, result.Message = "auth", "SMTP 账号认证失败"
		result.Detail = "请确认账号、密码或授权码是否正确，并检查服务器是否允许 SMTP AUTH。"
	case strings.Contains(lower, "certificate") || strings.Contains(lower, "x509"):
		result.Stage, result.Message = "tls", "TLS 证书校验失败"
		result.Detail = "请检查证书域名、有效期和证书链。本地客户端可在确认自签名证书后临时忽略校验。"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "timeout") || strings.Contains(lower, "超时"):
		result.Message = "连接或等待 SMTP 响应超时"
		result.Detail = "请检查防火墙、安全组、SMTP 端口和服务器网络是否可达。"
	}
	return result
}

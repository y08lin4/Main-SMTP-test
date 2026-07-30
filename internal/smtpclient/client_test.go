package smtpclient

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type closeBuffer struct{ bytes.Buffer }

func (b *closeBuffer) Close() error { return nil }

func validOptions() Options {
	return Options{
		Host: "smtp.example.com", Port: 587, Security: SecurityStartTLS,
		Username: "user@example.com", Password: "secret", From: "user@example.com",
		To: "test@example.net", Subject: "测试主题", Message: "first\n.second",
	}
}

func TestValidateRejectsHeaderInjection(t *testing.T) {
	options := validOptions()
	options.Host = "smtp.example.com\r\nRCPT TO:<bad@example.com>"
	if _, err := validate(options); err == nil {
		t.Fatal("expected injected host to be rejected")
	}
}

func TestWriteMessageEncodesSubject(t *testing.T) {
	options := validOptions()
	buffer := &closeBuffer{}
	if err := writeMessage(buffer, options); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	message, err := io.ReadAll(strings.NewReader(buffer.String()))
	if err != nil {
		t.Fatal(err)
	}
	text := string(message)
	if !strings.Contains(text, "Subject: =?UTF-8?B?") {
		t.Fatalf("subject was not MIME encoded: %s", text)
	}
	for _, expected := range []string{
		"Content-Type: multipart/alternative; boundary=",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Type: text/html; charset=UTF-8",
		"<html lang=\"zh-CN\"><body>first<br>\r\n.second</body></html>",
		"Message-ID: <",
		"@example.com>",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("message missing %q: %q", expected, text)
		}
	}
}

func TestExplainRelayDenied(t *testing.T) {
	explained := Explain(fail("recipient", "rejected", "554 5.7.1 Relay access denied", nil))
	if explained.Stage != "recipient" || !strings.Contains(explained.Message, "中继") {
		t.Fatalf("unexpected explanation: %+v", explained)
	}
}

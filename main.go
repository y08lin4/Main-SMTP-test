package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/y08lin4/Main-SMTP-test/internal/smtpclient"
)

const maxBodyBytes = 128 * 1024

var (
	version     = "dev"
	githubURL   = "https://github.com/y08lin4/Main-SMTP-test"
	downloadURL = "https://github.com/y08lin4/Main-SMTP-test/releases/latest/download/SMTP-Tester-Windows-x64.exe"
)

//go:embed web/*
var webFiles embed.FS

type metaResponse struct {
	Mode                  string `json:"mode"`
	Version               string `json:"version"`
	GitHubURL             string `json:"githubUrl"`
	DownloadURL           string `json:"downloadUrl"`
	AllowPlain            bool   `json:"allowPlain"`
	AllowCustomPort       bool   `json:"allowCustomPort"`
	DeliverabilityEnabled bool   `json:"deliverabilityEnabled"`
}

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	handler, err := appHandler(logger)
	if err != nil {
		logger.Fatalf("初始化失败：%v", err)
	}

	port := 8765
	if raw := os.Getenv("SMTP_TEST_PORT"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed >= 0 && parsed <= 65535 {
			port = parsed
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil && port == 8765 {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		logger.Fatalf("启动失败：%v", err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	address := "http://" + listener.Addr().String()
	fmt.Println()
	fmt.Println("  SMTP 发信测试器已启动")
	fmt.Println("  请打开：" + address)
	fmt.Println("  关闭此窗口即可停止服务。")
	fmt.Println()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("服务异常：%v", err)
		}
	}()
	openBrowser(address)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func appHandler(logger *log.Logger) (http.Handler, error) {
	assets, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, metaResponse{
			Mode: "local", Version: version, GitHubURL: githubURL,
			DownloadURL: downloadURL, AllowPlain: true, AllowCustomPort: true, DeliverabilityEnabled: false,
		})
	})
	mux.HandleFunc("POST /api/send", func(w http.ResponseWriter, r *http.Request) {
		if !validOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "请求来源不受信任"})
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"success": false, "message": "请求格式必须是 JSON"})
			return
		}
		body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		decoder := json.NewDecoder(body)
		decoder.DisallowUnknownFields()
		var options smtpclient.Options
		if err := decoder.Decode(&options); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "stage": "input", "message": "请求内容不正确", "detail": err.Error()})
			return
		}
		if err := ensureJSONEnd(decoder); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "stage": "input", "message": "请求只能包含一个 JSON 对象"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		result, err := smtpclient.Send(ctx, options)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, smtpclient.Explain(err))
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		if strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		file, err := assets.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		stat, err := file.Stat()
		if err != nil || stat.IsDir() {
			http.NotFound(w, r)
			return
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.Copy(w, file)
	})

	return securityHeaders(loggingMiddleware(logger, mux)), nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("extra JSON value")
		}
		return err
	}
	return nil
}

func validOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+r.Host
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/send" {
			logger.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

func openBrowser(url string) {
	if os.Getenv("SMTP_TEST_NO_OPEN") == "1" {
		return
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	_ = command.Start()
}

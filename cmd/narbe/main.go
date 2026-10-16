package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/app"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := env("NARBE_LISTEN", "127.0.0.1:8080")
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		if strings.HasPrefix(addr, "0.0.0.0:") {
			addr = strings.Replace(addr, "0.0.0.0:", "127.0.0.1:", 1)
		}
		c := http.Client{Timeout: 3 * time.Second}
		r, e := c.Get("http://" + addr + "/healthz")
		if e != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	level := slog.LevelInfo
	switch os.Getenv("NARBE_LOG_LEVEL") {
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "", "info":
	default:
		fmt.Fprintln(os.Stderr, "NARBE_LOG_LEVEL supports info/warn/error")
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
	s, e := store.Open(env("NARBE_DATA_DIR", "./data"))
	if e != nil {
		fmt.Fprintln(os.Stderr, "无法打开数据目录:", e)
		os.Exit(1)
	}
	defer s.Close()
	if e = s.InitPassword(os.Getenv("NARBE_ADMIN_PASSWORD_FILE")); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := app.New(s, os.Getenv("NARBE_ORIGIN"))
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	srv := http.Server{Addr: env("NARBE_LISTEN", "127.0.0.1:8080"), Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 10 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	slog.Info("Narbe 已启动")
	if e = srv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		slog.Error("HTTP 服务无法启动")
		stop()
	}
	<-done
}
func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

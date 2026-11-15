package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "egress" {
		e, err := probe.ObserveEgress(context.Background())
		_ = json.NewEncoder(os.Stdout).Encode(e)
		if err != nil {
			os.Exit(1)
		}
		return
	}
	data := env("NARBE_PROBE_DATA_DIR", "/data")
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if !probe.Healthy(data) {
			os.Exit(1)
		}
		return
	}
	var ports []int
	for _, raw := range strings.Split(env("NARBE_PROBE_ALLOWED_PORTS", "22,80,443,8443,8080"), ",") {
		p, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || p < 1 || p > 65535 {
			fmt.Fprintln(os.Stderr, "检测端口配置无效")
			os.Exit(1)
		}
		ports = append(ports, p)
	}
	token := os.Getenv("NARBE_PROBE_REGISTRATION_TOKEN")
	if path := os.Getenv("NARBE_PROBE_REGISTRATION_TOKEN_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil || len(b) > 512 {
			fmt.Fprintln(os.Stderr, "无法读取一次性探针注册凭据")
			os.Exit(1)
		}
		token = strings.TrimSpace(string(b))
	}
	agent, err := probe.NewAgent(probe.Config{
		ControlURL: os.Getenv("NARBE_PROBE_CONTROL_URL"), RegistrationToken: token,
		DataDir: data, AllowedPorts: ports,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Fprintln(os.Stdout, "Narbe Probe 已启动；实际出口和在线状态见管理端；不持有云账号凭据")
	_ = agent.Run(ctx)
}

func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}

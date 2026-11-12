// job-chat-copilot 本地服务入口：监听 127.0.0.1:7422，
// 提供 REST + 插件 WS（/ws/ext）+ 控制台 WS（/api/ws），内嵌控制台静态资源。
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/linuxsuren/job-chat-copilot/internal/api"
	"github.com/linuxsuren/job-chat-copilot/internal/browser"
	"github.com/linuxsuren/job-chat-copilot/internal/gateway"
	"github.com/linuxsuren/job-chat-copilot/internal/guard"
	"github.com/linuxsuren/job-chat-copilot/internal/orchestrator"
	"github.com/linuxsuren/job-chat-copilot/internal/review"
	"github.com/linuxsuren/job-chat-copilot/internal/store"
	"github.com/linuxsuren/job-chat-copilot/internal/webui"
)

func main() {
	var (
		addr     = flag.String("addr", "127.0.0.1:7422", "监听地址")
		dataDir  = flag.String("data", "data", "数据目录（SQLite 等）")
		extDir   = flag.String("extension", "../extension/dist", "插件构建产物目录（不存在则不加载）")
		chromeBin = flag.String("chrome", "", "Chrome 可执行文件路径（默认自动探测）")
		noBrowser = flag.Bool("no-browser", false, "不自动拉起浏览器")
	)
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		logger.Error("create data dir failed", "err", err)
		os.Exit(1)
	}
	st, err := store.Open(filepath.Join(*dataDir, "copilot.db"))
	if err != nil {
		logger.Error("open store failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	gw, err := gateway.New(st, nil, logger) // delegate 稍后注入
	if err != nil {
		logger.Error("init gateway failed", "err", err)
		os.Exit(1)
	}
	g := guard.New(guard.DefaultConfig(), func(reason string) {
		gw.AlertGuard(reason) // 控制台横幅告警，人工确认后消除
	})

	rev := review.NewService(st, gw, logger,
		func(draftID, convID int64) { // draft.ready
			gw.BroadcastConsole(gateway.EvDraftReady, map[string]any{"draft_id": draftID, "conv_id": convID})
		},
		func(pending int) { // review.stats → 插件
			gw.SendToPlugin(gateway.EvReviewStats, map[string]any{"pending_count": pending})
			gw.BroadcastConsole(gateway.EvReviewStats, map[string]any{"pending_count": pending})
		},
	)
	orch := orchestrator.New(st, gw, g, rev, logger)
	gw.SetDelegate(orch)

	brow := browser.New(*chromeBin, *extDir, filepath.Join(*dataDir, "chrome-profile"), logger)

	apiSrv := &api.Server{Store: st, GW: gw, Guard: g, Orch: orch, Rev: rev, Brow: brow, Log: logger}
	settings := apiSrv.LoadSettings()
	orch.SetLLMConfig(settings.LLM.Normalize())
	orch.SetAutoMode(settings.AutoMode)
	orch.SetGreeting(settings.Greeting.Templates, settings.Greeting.Strategy)
	g.UpdateConfig(settings.Guard)

	mux := http.NewServeMux()
	apiSrv.Register(mux)
	mux.Handle("/", webui.Handler())

	// chat.poll 兜底扫描定时器（60s，guard 暂停期间跳过），随服务生命周期
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()
	orch.StartPoller(pollCtx)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if !*noBrowser {
		if err := brow.Start(context.Background()); err != nil {
			logger.Warn("browser autostart failed (可用 --no-browser 跳过)", "err", err)
		}
	}

	go func() {
		logger.Info("job-chat-copilot listening", "addr", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	_ = brow.Stop()
}

// Package browser 用 rod 托管 Chrome：加载未打包插件（--load-extension）、
// 掉线自动重启、监督截图。rod 只做托管与监督，不做页面操作。
package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// ErrNotRunning 浏览器未启动/已掉线。
var ErrNotRunning = errors.New("browser not running")

// Manager 托管浏览器实例。
type Manager struct {
	mu       sync.Mutex
	logger   *slog.Logger
	bin      string        // Chrome 可执行文件路径（空则自动探测）
	extPath  string        // 插件目录（extension/dist）
	interval time.Duration // 掉线检查间隔
	userData string

	browser  *rod.Browser
	stopCh   chan struct{}
	started  bool
	lastShot time.Time
	shot     []byte
}

// New 创建托管器。extPath 为插件构建产物目录，不存在则不加载插件。
func New(bin, extPath, userDataDir string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if bin == "" {
		bin = detectChrome()
	}
	return &Manager{logger: logger, bin: bin, extPath: extPath, userData: userDataDir, interval: 5 * time.Second}
}

// detectChrome 探测常见 Chrome 路径；找不到返回空（Start 会报可读错误）。
func detectChrome() string {
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Start 拉起带插件的 Chrome 并启动掉线监督循环。
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return nil
	}
	if m.bin == "" {
		return fmt.Errorf("未找到 Chrome，请在设置中指定浏览器路径")
	}
	if err := os.MkdirAll(m.userData, 0o755); err != nil {
		return fmt.Errorf("创建浏览器数据目录失败: %w", err)
	}
	b, err := m.launch()
	if err != nil {
		return err
	}
	m.browser = b
	m.started = true
	m.stopCh = make(chan struct{})
	go m.monitor()
	m.logger.Info("browser started", "bin", m.bin, "extension", m.extPath)
	return nil
}

// launch 组装启动参数并拉起。调用方需持锁。
func (m *Manager) launch() (*rod.Browser, error) {
	l := launcher.New().
		Bin(m.bin).
		UserDataDir(m.userData).
		Set("remote-debugging-port", "0").
		Headless(false)
	if m.extPath != "" {
		if st, err := os.Stat(m.extPath); err == nil && st.IsDir() {
			// 未打包插件加载（--load-extension 需要 --disable-extensions-except 配合）
			l.Set("disable-extensions-except", m.extPath)
			l.Set("load-extension", m.extPath)
		} else {
			m.logger.Warn("extension dir missing, launching without extension", "path", m.extPath)
		}
	}
	url, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("浏览器启动失败: %w", err)
	}
	return rod.New().ControlURL(url), nil
}

// monitor 掉线检查：异常退出后自动重启（保留原节奏，最多每 interval 一次）。
func (m *Manager) monitor() {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-t.C:
			m.mu.Lock()
			if !m.started || m.browser == nil {
				m.mu.Unlock()
				continue
			}
			if err := m.browser.Connect(); err == nil {
				m.mu.Unlock()
				continue
			}
			m.logger.Warn("browser disconnected, restarting")
			old := m.browser
			b, err := m.launch()
			if err != nil {
				m.logger.Error("browser restart failed", "err", err)
				m.started = false
				m.mu.Unlock()
				old.Close()
				return
			}
			m.browser = b
			m.mu.Unlock()
			old.Close()
		}
	}
}

// Stop 停止托管并关闭浏览器。
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return nil
	}
	m.started = false
	close(m.stopCh)
	if m.browser != nil {
		m.browser.Close()
		m.browser = nil
	}
	return nil
}

// Running 返回是否在托管中。
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

// Screenshot 监督截图：返回第一个标签页的 PNG。15s 内的重复请求返回缓存。
func (m *Manager) Screenshot() ([]byte, error) {
	m.mu.Lock()
	if !m.started || m.browser == nil {
		m.mu.Unlock()
		return nil, ErrNotRunning
	}
	if time.Since(m.lastShot) < 15*time.Second && m.shot != nil {
		shot := m.shot
		m.mu.Unlock()
		return shot, nil
	}
	b := m.browser
	m.mu.Unlock()

	type result struct {
		img []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		pages, err := b.Pages()
		if err != nil {
			ch <- result{err: err}
			return
		}
		var target *rod.Page
		for _, p := range pages {
			info, err := p.Info()
			if err == nil && info != nil && info.URL != "" && info.URL != "about:blank" {
				target = p
				break
			}
		}
		if target == nil && len(pages) > 0 {
			target = pages[0]
		}
		if target == nil {
			ch <- result{err: errors.New("no page")}
			return
		}
		img, err := target.Screenshot(true, nil)
		ch <- result{img: img, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("截图失败: %w", r.err)
		}
		m.mu.Lock()
		m.shot, m.lastShot = bytes.Clone(r.img), time.Now()
		m.mu.Unlock()
		return r.img, nil
	case <-time.After(20 * time.Second):
		return nil, fmt.Errorf("截图超时")
	}
}

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/enetx/g"
	"github.com/enetx/surf"
)

// EgressPool 管理一组可故障转移的上游出口。
//
// 单一出口被 Cloudflare 拉黑后整条链路都会失败，而出口 IP 是否被针对只能通过
// 实际请求观测。这里用后台探测持续判定各出口健康度，并在当前出口连续失败时
// 切到下一个健康出口。
//
// 只服务于**未绑定代理**的账号：账号自己绑定了代理时出口由账号决定，
// 池子无权干预（cf_clearance 与签发 IP 强绑定，换出口等于作废凭证）。
type EgressPool struct {
	config EgressPoolConfig
	// probe 是出口连通性探测函数，测试可注入。返回 nil 表示该出口可用。
	probe func(ctx context.Context, proxyURL string, timeout time.Duration) error
	// onChange 在当前出口变化时回调，用于作废绑定旧出口的 HTTP client 与凭证。
	onChange func(previous, current string)

	mu              sync.RWMutex
	exits           []egressExit
	current         int
	consecutiveFail int
	stop            chan struct{}
	stopOnce        sync.Once
}

type egressExit struct {
	url     string
	healthy bool
	// failures 是连续失败次数；探测成功即清零，避免历史失败永久累积。
	failures int
}

// EgressPoolConfig 是出口池所需配置。
type EgressPoolConfig interface {
	UpstreamPool() []string
	UpstreamPoolProbeSeconds() int
}

// egressFailoverThreshold 是当前出口连续失败多少次后切换。
//
// 取 3 而非 1：单次超时可能只是瞬时抖动，过早切换会让出口频繁跳变，
// 而出口 IP 频繁变化本身就是风控信号。
const egressFailoverThreshold = 3

// NewEgressPool 创建出口池。池为空时 Current() 返回空串，
// BrowserHTTPClientForAccount 会回落到全局代理，行为与引入出口池之前一致。
func NewEgressPool(config EgressPoolConfig) *EgressPool {
	return &EgressPool{
		config:  config,
		probe:   probeEgress,
		exits:   nil,
		stop:    make(chan struct{}),
		current: 0,
	}
}

// SetProbe 注入探测函数，仅供测试使用。
func (p *EgressPool) SetProbe(probe func(ctx context.Context, proxyURL string, timeout time.Duration) error) {
	if p == nil || probe == nil {
		return
	}
	p.mu.Lock()
	p.probe = probe
	p.mu.Unlock()
}

// SetOnChange 设置出口切换回调。
//
// 回调在锁外执行：切换出口后要作废 client 与凭证，这些操作不应在持锁时进行，
// 否则所有读池状态的请求都会被阻塞。
func (p *EgressPool) SetOnChange(onChange func(previous, current string)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.onChange = onChange
	p.mu.Unlock()
}

// Start 启动后台探测循环；池为空时不启动任何 goroutine。
func (p *EgressPool) Start() {
	if p == nil || p.config == nil {
		return
	}
	exits := p.config.UpstreamPool()
	if len(exits) == 0 {
		return
	}
	p.mu.Lock()
	if p.exits != nil {
		p.mu.Unlock()
		return
	}
	p.exits = make([]egressExit, 0, len(exits))
	for _, exit := range exits {
		// 初始视为健康：尚未探测前不应把可用出口判死，否则首次请求会无谓地跳过它。
		p.exits = append(p.exits, egressExit{url: exit, healthy: true})
	}
	interval := time.Duration(p.config.UpstreamPoolProbeSeconds()) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	p.mu.Unlock()

	go p.loop(interval)
}

// Stop 停止后台探测循环，可重复调用。
func (p *EgressPool) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() { close(p.stop) })
}

func (p *EgressPool) loop(interval time.Duration) {
	// 先探一次再进入周期：启动时的出口可用性不能等到第一个间隔结束才知道。
	p.probeAll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.probeAll()
		}
	}
}

func (p *EgressPool) probeAll() {
	p.mu.RLock()
	exits := append([]egressExit(nil), p.exits...)
	probe := p.probe
	p.mu.RUnlock()
	if probe == nil {
		return
	}
	timeout := 15 * time.Second
	for _, exit := range exits {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := probe(ctx, exit.url, timeout)
		cancel()
		p.recordProbe(exit.url, err == nil)
	}
}

// recordProbe 记录一次探测结果，必要时切换出口。
func (p *EgressPool) recordProbe(proxyURL string, ok bool) {
	p.mu.Lock()
	index := -1
	for i := range p.exits {
		if p.exits[i].url == proxyURL {
			index = i
			break
		}
	}
	if index < 0 {
		p.mu.Unlock()
		return
	}
	p.exits[index].healthy = ok
	if ok {
		p.exits[index].failures = 0
	} else {
		p.exits[index].failures++
	}

	var previous, current string
	switched := false
	// 只有当前出口的失败才会计数切换：备胎失败与当前链路无关，
	// 把它计入会让主出口被误切。
	if index == p.current && !ok {
		p.consecutiveFail++
		if p.consecutiveFail >= egressFailoverThreshold {
			if next := p.nextHealthyLocked(); next >= 0 {
				previous = p.exits[p.current].url
				p.current = next
				current = p.exits[next].url
				p.consecutiveFail = 0
				switched = true
			}
		}
	} else if index == p.current && ok {
		p.consecutiveFail = 0
	}
	onChange := p.onChange
	p.mu.Unlock()

	if switched && onChange != nil {
		onChange(previous, current)
	}
}

// nextHealthyLocked 返回下一个健康出口的下标；没有可用备胎时返回 -1。
// 调用方必须持有 p.mu。
func (p *EgressPool) nextHealthyLocked() int {
	if len(p.exits) == 0 {
		return -1
	}
	// 环形遍历：单出口池下会回到自己，此时不切换（切了等于没切）。
	for offset := 1; offset < len(p.exits); offset++ {
		index := (p.current + offset) % len(p.exits)
		if p.exits[index].healthy {
			return index
		}
	}
	return -1
}

// Current 返回当前生效出口；池为空时返回空串（表示使用全局代理）。
func (p *EgressPool) Current() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.exits) == 0 {
		return ""
	}
	return p.exits[p.current].url
}

// Enabled 报告出口池是否已配置出口。
func (p *EgressPool) Enabled() bool {
	return p.Current() != ""
}

// EgressPoolStatus 是出口池对外暴露的脱敏状态。
type EgressPoolStatus struct {
	Current string           `json:"current"`
	Exits   []EgressExitInfo `json:"exits"`
}

// EgressExitInfo 是单个出口的脱敏状态。
type EgressExitInfo struct {
	URL      string `json:"url"`
	Active   bool   `json:"active"`
	Healthy  bool   `json:"healthy"`
	Failures int    `json:"failures"`
}

// Status 返回脱敏后的池状态，供管理接口与日志使用。
// URL 一律经 MaskProxyURL 处理，凭据不会外泄。
func (p *EgressPool) Status() EgressPoolStatus {
	if p == nil {
		return EgressPoolStatus{Exits: []EgressExitInfo{}}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	status := EgressPoolStatus{Exits: make([]EgressExitInfo, 0, len(p.exits))}
	if len(p.exits) > 0 {
		status.Current = MaskProxyURL(p.exits[p.current].url)
	}
	for i, exit := range p.exits {
		status.Exits = append(status.Exits, EgressExitInfo{
			URL:      MaskProxyURL(exit.url),
			Active:   i == p.current,
			Healthy:  exit.healthy,
			Failures: exit.failures,
		})
	}
	return status
}

// probeEgress 探测单个出口是否可用。
//
// 判定标准是「拿到了任意 HTTP 响应」而非状态码：出口被 Cloudflare 拦截时
// 返回的 403 挑战页恰恰说明链路是通的，把它判为不健康会导致所有出口
// 轮流被判死，池子彻底失效。只有传输层失败（EOF/RST/超时/DNS）才算不健康。
//
// 使用与业务一致的 surf TLS profile：用普通 http.Transport 探测会得到
// 不同的 TLS 指纹，探测结果无法代表真实业务链路。
func probeEgress(ctx context.Context, proxyURL string, timeout time.Duration) error {
	builder := surf.NewClient().Builder().
		SecureTLS().
		Impersonate().
		Chrome().
		Session().
		Timeout(timeout)
	if trimmed := strings.TrimSpace(proxyURL); trimmed != "" {
		builder = builder.Proxy(g.String(trimmed))
	}
	client, err := builder.Build().Result()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://chatgpt.com/", nil)
	if err != nil {
		return err
	}
	resp, err := client.Std().Do(req)
	if err != nil {
		return err
	}
	// 任意状态码都算连通，只需把少量响应体读掉后关闭，使连接可被复用。
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

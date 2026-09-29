package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testEgressConfig struct {
	pool    []string
	probeAt int
}

func (c testEgressConfig) UpstreamPool() []string        { return c.pool }
func (c testEgressConfig) UpstreamPoolProbeSeconds() int { return c.probeAt }

// 空池=不使用出口池，出口解析必须回落到全局代理，行为与引入前一致。
func TestEgressPoolEmptyKeepsPreviousBehaviour(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{})
	if pool.Enabled() {
		t.Fatal("Enabled() = true for an empty pool")
	}
	if got := pool.Current(); got != "" {
		t.Fatalf("Current() = %q, want empty", got)
	}
	status := pool.Status()
	if status.Current != "" || len(status.Exits) != 0 {
		t.Fatalf("Status() = %+v, want an empty pool", status)
	}
	// Start 不应为空池启动任何 goroutine；重复调用也必须安全。
	pool.Start()
	pool.Start()
	pool.Stop()
}

// 未探测前不能把出口判死，否则首次请求会无谓地跳过它。
func TestEgressPoolStartsOptimistic(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://a:1080", "socks5://b:1080"}})
	// 注入探测，避免 Start 的首次探测真的去连这些不存在的出口。
	pool.SetProbe(func(ctx context.Context, proxyURL string, timeout time.Duration) error { return nil })
	pool.Start()
	defer pool.Stop()

	if got := pool.Current(); got != "socks5://a:1080" {
		t.Fatalf("Current() = %q, want the first exit before any probe", got)
	}
	for _, exit := range pool.Status().Exits {
		if !exit.Healthy {
			t.Fatalf("exit %s starts unhealthy", exit.URL)
		}
	}
}

// 单次超时可能只是抖动，阈值以下不得切换；出口频繁跳变本身就是风控信号。
func TestEgressPoolSwitchesOnlyAfterThreshold(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://primary:1080", "socks5://backup:1080"}})
	pool.SetProbe(func(ctx context.Context, proxyURL string, timeout time.Duration) error {
		if strings.Contains(proxyURL, "primary") {
			return errors.New("dial timeout")
		}
		return nil
	})
	pool.mu.Lock()
	pool.exits = []egressExit{{url: "socks5://primary:1080", healthy: true}, {url: "socks5://backup:1080", healthy: true}}
	pool.mu.Unlock()

	for i := 1; i < egressFailoverThreshold; i++ {
		pool.probeAll()
		if got := pool.Current(); got != "socks5://primary:1080" {
			t.Fatalf("switched after %d failures, want to wait for %d", i, egressFailoverThreshold)
		}
	}
	pool.probeAll()
	if got := pool.Current(); got != "socks5://backup:1080" {
		t.Fatalf("Current() = %q, want failover to the healthy backup", got)
	}
}

// 备胎失败与当前链路无关，把它计入会让主出口被误切。
func TestEgressPoolIgnoresBackupFailures(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://primary:1080", "socks5://backup:1080"}})
	pool.SetProbe(func(ctx context.Context, proxyURL string, timeout time.Duration) error {
		if strings.Contains(proxyURL, "backup") {
			return errors.New("dial timeout")
		}
		return nil
	})
	pool.mu.Lock()
	pool.exits = []egressExit{{url: "socks5://primary:1080", healthy: true}, {url: "socks5://backup:1080", healthy: true}}
	pool.mu.Unlock()

	for i := 0; i < egressFailoverThreshold+2; i++ {
		pool.probeAll()
	}
	if got := pool.Current(); got != "socks5://primary:1080" {
		t.Fatalf("Current() = %q, backup failures must not evict a healthy primary", got)
	}
}

// 没有健康备胎时保持不动，不能切到已知不健康的出口。
func TestEgressPoolStaysWhenNoHealthyBackup(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://primary:1080", "socks5://backup:1080"}})
	pool.SetProbe(func(ctx context.Context, proxyURL string, timeout time.Duration) error {
		return errors.New("dial timeout")
	})
	pool.mu.Lock()
	pool.exits = []egressExit{{url: "socks5://primary:1080", healthy: true}, {url: "socks5://backup:1080", healthy: true}}
	pool.mu.Unlock()

	for i := 0; i < egressFailoverThreshold; i++ {
		pool.probeAll()
	}
	if got := pool.Current(); got != "socks5://primary:1080" {
		t.Fatalf("Current() = %q, must not switch to an unhealthy exit", got)
	}
}

// 切换后必须回调，且回调参数是 HTTP 编码前的原始出口：
// 调用方要用它去作废旧出口的 cf_clearance（凭证按原始 URL 作键）。
func TestEgressPoolNotifiesOnSwitch(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://primary:1080", "socks5://backup:1080"}})
	var mu sync.Mutex
	type change struct{ previous, current string }
	var changes []change
	pool.SetOnChange(func(previous, current string) {
		mu.Lock()
		changes = append(changes, change{previous, current})
		mu.Unlock()
	})
	pool.SetProbe(func(ctx context.Context, proxyURL string, timeout time.Duration) error {
		if strings.Contains(proxyURL, "primary") {
			return errors.New("dial timeout")
		}
		return nil
	})
	pool.mu.Lock()
	pool.exits = []egressExit{{url: "socks5://primary:1080", healthy: true}, {url: "socks5://backup:1080", healthy: true}}
	pool.mu.Unlock()

	for i := 0; i < egressFailoverThreshold; i++ {
		pool.probeAll()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 1 || changes[0].previous != "socks5://primary:1080" || changes[0].current != "socks5://backup:1080" {
		t.Fatalf("changes = %+v, want exactly one primary→backup switch", changes)
	}
}

// 恢复后的出口应重新可用：否则主出口一次抖动就被永久淘汰。
func TestEgressPoolFailuresResetOnSuccess(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://primary:1080", "socks5://backup:1080"}})
	var primaryDown atomic.Bool
	primaryDown.Store(true)
	pool.SetProbe(func(ctx context.Context, proxyURL string, timeout time.Duration) error {
		if strings.Contains(proxyURL, "primary") && primaryDown.Load() {
			return errors.New("dial timeout")
		}
		return nil
	})
	pool.mu.Lock()
	pool.exits = []egressExit{{url: "socks5://primary:1080", healthy: true}, {url: "socks5://backup:1080", healthy: true}}
	pool.mu.Unlock()

	for i := 0; i < egressFailoverThreshold; i++ {
		pool.probeAll()
	}
	if got := pool.Current(); got != "socks5://backup:1080" {
		t.Fatalf("Current() = %q, want failover first", got)
	}

	// 主出口恢复：只有它被标记健康后才会被再次选中。
	primaryDown.Store(false)
	pool.probeAll()
	pool.mu.Lock()
	primary := pool.exits[0]
	pool.mu.Unlock()
	if !primary.healthy || primary.failures != 0 {
		t.Fatalf("primary = %+v, want healthy with failures reset", primary)
	}
}

// 管理接口只能看到脱敏后的出口，凭据不得外泄。
func TestEgressPoolStatusMasksCredentials(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://user:secret@10.0.0.1:1080", "http://10.0.0.2:8080"}})
	pool.SetProbe(func(ctx context.Context, proxyURL string, timeout time.Duration) error { return nil })
	pool.Start()
	defer pool.Stop()

	status := pool.Status()
	if len(status.Exits) != 2 {
		t.Fatalf("exits = %+v", status.Exits)
	}
	if status.Current != "socks5://***@10.0.0.1:1080" {
		t.Fatalf("Current = %q, want the masked form", status.Current)
	}
	for _, exit := range status.Exits {
		if strings.Contains(exit.URL, "secret") || strings.Contains(exit.URL, "user:") {
			t.Fatalf("exit URL %q leaks credentials", exit.URL)
		}
	}
	if !status.Exits[0].Active || status.Exits[1].Active {
		t.Fatalf("active flags = %+v, want only the first exit active", status.Exits)
	}
}

// 出口解析优先级：账号绑定代理 > 出口池 > 全局代理。
func TestEgressProxyPriority(t *testing.T) {
	config := testProxyConfig{proxy: "http://global:8080"}
	svc := NewProxyService(config)

	if got := svc.EgressProxy(""); got != "http://global:8080" {
		t.Fatalf("EgressProxy() = %q, want the global proxy when no pool is set", got)
	}
	if got := svc.EgressProxy("socks5://account:1080"); got != "socks5://account:1080" {
		t.Fatalf("EgressProxy() = %q, want the account proxy to win", got)
	}

	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://pooled:1080"}})
	pool.SetProbe(func(ctx context.Context, proxyURL string, timeout time.Duration) error { return nil })
	pool.Start()
	defer pool.Stop()
	svc.SetEgressPool(pool)

	if got := svc.EgressProxy(""); got != "socks5://pooled:1080" {
		t.Fatalf("EgressProxy() = %q, want the pooled exit", got)
	}
	// 已绑定代理的账号不参与故障转移：换出口会让它的 cf_clearance 当场失效。
	if got := svc.EgressProxy("socks5://account:1080"); got != "socks5://account:1080" {
		t.Fatalf("EgressProxy() = %q, a bound account must keep its own egress", got)
	}
}

func TestEgressPoolStopIsIdempotent(t *testing.T) {
	pool := NewEgressPool(testEgressConfig{pool: []string{"socks5://a:1080"}, probeAt: 10})
	pool.Start()
	for i := 0; i < 3; i++ {
		pool.Stop()
	}
}

type testProxyConfig struct{ proxy string }

func (c testProxyConfig) Proxy() string { return c.proxy }

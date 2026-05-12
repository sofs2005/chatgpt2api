package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

type testBillingDefaults struct {
	billingType        string
	standardBalance    int
	subscriptionQuota  int
	subscriptionPeriod string
}

func (d testBillingDefaults) DefaultBillingType() string {
	return d.billingType
}

func (d testBillingDefaults) DefaultStandardBalance() int {
	return d.standardBalance
}

func (d testBillingDefaults) DefaultSubscriptionQuota() int {
	return d.subscriptionQuota
}

func (d testBillingDefaults) DefaultSubscriptionPeriod() string {
	return d.subscriptionPeriod
}

func newTestBillingService(t *testing.T, defaults testBillingDefaults) *BillingService {
	t.Helper()
	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	return NewBillingService(dir, backend, defaults)
}

func newTestBillingServiceAt(t *testing.T, dir string, defaults testBillingDefaults) *BillingService {
	t.Helper()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	return NewBillingService(dir, backend, defaults)
}

func billingTestUser(id string) Identity {
	return Identity{ID: id, Name: id, Role: AuthRoleUser, CredentialID: "cred-" + id}
}

func TestBillingServiceDefaults(t *testing.T) {
	standard := newTestBillingService(t, testBillingDefaults{})
	got := standard.Get("alice")
	if got["type"] != BillingTypeStandard || util.ToInt(got["available"], -1) != 0 {
		t.Fatalf("standard default = %#v", got)
	}

	subscription := newTestBillingService(t, testBillingDefaults{
		billingType:        BillingTypeSubscription,
		subscriptionQuota:  12,
		subscriptionPeriod: BillingPeriodWeekly,
	})
	got = subscription.Get("bob")
	if got["type"] != BillingTypeSubscription || util.ToInt(got["available"], 0) != 12 {
		t.Fatalf("subscription default = %#v", got)
	}
	sub := util.StringMap(got["subscription"])
	if sub["quota_period"] != BillingPeriodWeekly {
		t.Fatalf("quota_period = %#v in %#v", sub["quota_period"], got)
	}
}

func TestBillingServiceDefaultBoundaryNormalization(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{
		billingType:     "unsupported",
		standardBalance: -5,
	})
	got := svc.Get("alice")
	if got["type"] != BillingTypeStandard || util.ToInt(got["available"], -1) != 0 {
		t.Fatalf("normalized default = %#v", got)
	}

	svc = newTestBillingService(t, testBillingDefaults{
		billingType:        BillingTypeSubscription,
		subscriptionQuota:  -7,
		subscriptionPeriod: "yearly",
	})
	got = svc.Get("bob")
	sub := util.StringMap(got["subscription"])
	if got["type"] != BillingTypeSubscription || util.ToInt(got["available"], -1) != 0 || util.ToInt(sub["quota_limit"], -1) != 0 || sub["quota_period"] != BillingPeriodMonthly {
		t.Fatalf("normalized subscription defaults = %#v", got)
	}
}

func TestBillingServiceCheckAndChargeStandard(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{standardBalance: 4})
	user := billingTestUser("alice")

	if err := svc.CheckAvailable(user, 3); err != nil {
		t.Fatalf("CheckAvailable() error = %v", err)
	}
	got := svc.Get("alice")
	standard := util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], 0) != 4 || util.ToInt(got["available"], 0) != 4 {
		t.Fatalf("after check = %#v", got)
	}

	if err := svc.Charge(user, 2, BillingReference{Endpoint: "/v1/images/generations", Model: "gpt-image-2"}); err != nil {
		t.Fatalf("Charge() error = %v", err)
	}
	got = svc.Get("alice")
	standard = util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], 0) != 2 || util.ToInt(standard["lifetime_consumed"], 0) != 2 || util.ToInt(got["available"], 0) != 2 {
		t.Fatalf("after charge = %#v", got)
	}

	if err := svc.Charge(user, 0, BillingReference{}); err != nil {
		t.Fatalf("Charge(0) error = %v", err)
	}
	got = svc.Get("alice")
	standard = util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], 0) != 2 || util.ToInt(got["available"], 0) != 2 {
		t.Fatalf("after zero charge = %#v", got)
	}
}

func TestBillingServiceCheckAvailableBoundaryClasses(t *testing.T) {
	for _, tc := range []struct {
		name      string
		amount    int
		wantErr   bool
		wantAvail int
	}{
		{name: "zero is no-op", amount: 0, wantAvail: 2},
		{name: "negative is no-op", amount: -1, wantAvail: 2},
		{name: "below available", amount: 1, wantAvail: 2},
		{name: "exactly available", amount: 2, wantAvail: 2},
		{name: "one above available", amount: 3, wantErr: true, wantAvail: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestBillingService(t, testBillingDefaults{standardBalance: 2})
			err := svc.CheckAvailable(billingTestUser("alice"), tc.amount)
			if tc.wantErr {
				var limitErr BillingLimitError
				if !errors.As(err, &limitErr) || limitErr.Code != "user_balance_insufficient" {
					t.Fatalf("CheckAvailable(%d) error = %#v", tc.amount, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckAvailable(%d) error = %v", tc.amount, err)
			}
			got := svc.Get("alice")
			if util.ToInt(got["available"], -1) != tc.wantAvail {
				t.Fatalf("after CheckAvailable(%d) = %#v", tc.amount, got)
			}
		})
	}
}

func TestBillingServiceChargeAllowsPartialActualConsumption(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{standardBalance: 5})
	if err := svc.CheckAvailable(billingTestUser("alice"), 2); err != nil {
		t.Fatalf("CheckAvailable() error = %v", err)
	}
	if err := svc.Charge(billingTestUser("alice"), 1, BillingReference{}); err != nil {
		t.Fatalf("Charge() error = %v", err)
	}
	got := svc.Get("alice")
	standard := util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], -1) != 4 || util.ToInt(standard["lifetime_consumed"], -1) != 1 {
		t.Fatalf("partial charge = %#v", got)
	}

	if err := svc.Charge(billingTestUser("alice"), -5, BillingReference{}); err != nil {
		t.Fatalf("Charge(negative) error = %v", err)
	}
	got = svc.Get("alice")
	standard = util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], -1) != 4 || util.ToInt(standard["lifetime_consumed"], -1) != 1 {
		t.Fatalf("negative charge = %#v", got)
	}
}

func TestBillingServiceChargeIsAtomicAndIdempotent(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{standardBalance: 1})
	user := billingTestUser("alice")

	result, err := svc.ChargeUserID("alice", 1, BillingReference{ChargeKey: "task:alice:one:0"})
	if err != nil || !result.Charged || result.AlreadyCharged {
		t.Fatalf("first Charge() error = %v", err)
	}
	result, err = svc.ChargeUserID("alice", 1, BillingReference{ChargeKey: "task:alice:one:0"})
	if err != nil || !result.AlreadyCharged {
		t.Fatalf("duplicate Charge() error = %v", err)
	}
	got := svc.Get("alice")
	standard := util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], -1) != 0 || util.ToInt(standard["lifetime_consumed"], -1) != 1 {
		t.Fatalf("duplicate charge state = %#v", got)
	}

	var limitErr BillingLimitError
	if err := svc.Charge(user, 1, BillingReference{ChargeKey: "task:alice:one:1"}); !errors.As(err, &limitErr) || limitErr.Code != "user_balance_insufficient" {
		t.Fatalf("insufficient Charge() error = %#v", err)
	}
	got = svc.Get("alice")
	standard = util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], -1) != 0 || util.ToInt(standard["lifetime_consumed"], -1) != 1 {
		t.Fatalf("insufficient charge changed state = %#v", got)
	}

	bob := billingTestUser("bob")
	if err := svc.Charge(bob, 1, BillingReference{ChargeKey: "task:alice:one:0"}); err != nil {
		t.Fatalf("same charge key for different user Charge() error = %v", err)
	}
	got = svc.Get("bob")
	standard = util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], -1) != 0 || util.ToInt(standard["lifetime_consumed"], -1) != 1 {
		t.Fatalf("same charge key for different user state = %#v", got)
	}
}

func TestBillingServiceRefundsUnusedPrecharge(t *testing.T) {
	t.Run("standard", func(t *testing.T) {
		svc := newTestBillingService(t, testBillingDefaults{standardBalance: 4})
		user := billingTestUser("alice")
		chargeKey := "task:alice:image:precharge"
		if err := svc.Charge(user, 4, BillingReference{ChargeKey: chargeKey}); err != nil {
			t.Fatalf("Charge() error = %v", err)
		}
		if _, err := svc.RefundUserID("alice", 2, BillingReference{ChargeKey: "task:alice:image:refund", RefundForKey: chargeKey}); err != nil {
			t.Fatalf("RefundUserID() error = %v", err)
		}
		if _, err := svc.RefundUserID("alice", 2, BillingReference{ChargeKey: "task:alice:image:refund", RefundForKey: chargeKey}); err != nil {
			t.Fatalf("duplicate RefundUserID() error = %v", err)
		}
		got := svc.Get("alice")
		standard := util.StringMap(got["standard"])
		if util.ToInt(standard["balance"], -1) != 2 || util.ToInt(standard["lifetime_consumed"], -1) != 2 || util.ToInt(got["available"], -1) != 2 {
			t.Fatalf("after standard refund = %#v", got)
		}
	})

	t.Run("subscription", func(t *testing.T) {
		svc := newTestBillingService(t, testBillingDefaults{
			billingType:        BillingTypeSubscription,
			subscriptionQuota:  4,
			subscriptionPeriod: BillingPeriodMonthly,
		})
		user := billingTestUser("alice")
		chargeKey := "task:alice:image:precharge"
		if err := svc.Charge(user, 4, BillingReference{ChargeKey: chargeKey}); err != nil {
			t.Fatalf("Charge() error = %v", err)
		}
		if _, err := svc.RefundUserID("alice", 3, BillingReference{ChargeKey: "task:alice:image:refund", RefundForKey: chargeKey}); err != nil {
			t.Fatalf("RefundUserID() error = %v", err)
		}
		got := svc.Get("alice")
		sub := util.StringMap(got["subscription"])
		if util.ToInt(sub["quota_used"], -1) != 1 || util.ToInt(got["available"], -1) != 3 {
			t.Fatalf("after subscription refund = %#v", got)
		}
	})
}

func TestBillingServiceCheckAndChargeSubscription(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{
		billingType:        BillingTypeSubscription,
		subscriptionQuota:  4,
		subscriptionPeriod: BillingPeriodMonthly,
	})
	user := billingTestUser("alice")

	if err := svc.CheckAvailable(user, 3); err != nil {
		t.Fatalf("CheckAvailable() error = %v", err)
	}
	got := svc.Get("alice")
	sub := util.StringMap(got["subscription"])
	if util.ToInt(sub["quota_used"], 0) != 0 || util.ToInt(got["available"], 0) != 4 {
		t.Fatalf("after check = %#v", got)
	}

	if err := svc.Charge(user, 2, BillingReference{Endpoint: "/v1/images/generations"}); err != nil {
		t.Fatalf("Charge() error = %v", err)
	}
	got = svc.Get("alice")
	sub = util.StringMap(got["subscription"])
	if util.ToInt(sub["quota_used"], 0) != 2 || util.ToInt(got["available"], 0) != 2 {
		t.Fatalf("after charge = %#v", got)
	}

	if err := svc.Charge(user, 0, BillingReference{}); err != nil {
		t.Fatalf("Charge(0) error = %v", err)
	}
	got = svc.Get("alice")
	sub = util.StringMap(got["subscription"])
	if util.ToInt(sub["quota_used"], 0) != 2 || util.ToInt(got["available"], 0) != 2 {
		t.Fatalf("after zero charge = %#v", got)
	}
}

func TestBillingServiceSubscriptionManualDeltaBoundaries(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{
		billingType:        BillingTypeSubscription,
		subscriptionQuota:  5,
		subscriptionPeriod: BillingPeriodMonthly,
	})
	operator := Identity{ID: "admin", Name: "Admin", Role: AuthRoleAdmin}
	if _, err := svc.ApplyAdjustment("alice", operator, map[string]any{"type": "increase_quota", "amount": 2, "reason": "bonus"}); err != nil {
		t.Fatalf("increase_quota error = %v", err)
	}
	got := svc.Get("alice")
	if util.ToInt(got["available"], -1) != 7 {
		t.Fatalf("after increase_quota = %#v", got)
	}
	if _, err := svc.ApplyAdjustment("alice", operator, map[string]any{"type": "decrease_quota", "amount": 7, "reason": "use up"}); err != nil {
		t.Fatalf("decrease_quota exact remaining error = %v", err)
	}
	got = svc.Get("alice")
	sub := util.StringMap(got["subscription"])
	if util.ToInt(got["available"], -1) != 0 || util.ToInt(sub["manual_delta"], 0) != -5 {
		t.Fatalf("after exact decrease_quota = %#v", got)
	}
	if _, err := svc.ApplyAdjustment("alice", operator, map[string]any{"type": "decrease_quota", "amount": 1, "reason": "too much"}); err == nil {
		t.Fatal("decrease_quota beyond remaining error = nil")
	}
}

func TestBillingServiceInsufficientErrors(t *testing.T) {
	standard := newTestBillingService(t, testBillingDefaults{standardBalance: 1})
	err := standard.CheckAvailable(billingTestUser("alice"), 2)
	var limitErr BillingLimitError
	if !errors.As(err, &limitErr) || limitErr.Code != "user_balance_insufficient" || limitErr.Message != "user balance insufficient" {
		t.Fatalf("standard insufficient error = %#v", err)
	}

	subscription := newTestBillingService(t, testBillingDefaults{
		billingType:        BillingTypeSubscription,
		subscriptionQuota:  1,
		subscriptionPeriod: BillingPeriodDaily,
	})
	err = subscription.CheckAvailable(billingTestUser("bob"), 2)
	if !errors.As(err, &limitErr) || limitErr.Code != "user_quota_exceeded" || limitErr.Message != "user quota exceeded" {
		t.Fatalf("subscription insufficient error = %#v", err)
	}
}

func TestBillingServiceAdjustmentValidationBoundaries(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{standardBalance: 3})
	operator := Identity{ID: "admin", Name: "Admin", Role: AuthRoleAdmin}
	for _, body := range []map[string]any{
		{"reason": "missing type"},
		{"type": "increase_balance", "amount": 0, "reason": "zero"},
		{"type": "decrease_balance", "amount": 4, "reason": "negative balance"},
		{"type": "switch_to_subscription", "quota_limit": 1, "reason": "missing period"},
		{"type": "switch_to_subscription", "quota_period": BillingPeriodMonthly, "reason": "missing limit"},
		{"type": "switch_to_subscription", "quota_limit": -1, "quota_period": BillingPeriodMonthly, "reason": "negative limit"},
		{"type": "set_quota_period", "quota_period": "yearly", "reason": "bad period"},
	} {
		if _, err := svc.ApplyAdjustment("alice", operator, body); err == nil {
			t.Fatalf("ApplyAdjustment(%#v) error = nil", body)
		}
	}
	result, err := svc.ApplyAdjustment("alice", operator, map[string]any{"type": "increase_balance", "amount": 1})
	if err != nil {
		t.Fatalf("ApplyAdjustment() without reason error = %v", err)
	}
	adjustment := util.StringMap(result["adjustment"])
	if util.Clean(adjustment["reason"]) != "" {
		t.Fatalf("adjustment reason = %#v, want empty", adjustment["reason"])
	}
}

func TestBillingServiceAdminAndUnlimitedBypass(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{})
	admin := Identity{ID: "admin", Role: AuthRoleAdmin}
	if err := svc.CheckAvailable(admin, 99); err != nil {
		t.Fatalf("admin check = %v", err)
	}

	operator := Identity{ID: "admin", Name: "Admin", Role: AuthRoleAdmin}
	if _, err := svc.ApplyAdjustment("alice", operator, map[string]any{"type": "set_unlimited", "unlimited": true, "reason": "test"}); err != nil {
		t.Fatalf("set_unlimited error = %v", err)
	}
	if err := svc.CheckAvailable(billingTestUser("alice"), 99); err != nil {
		t.Fatalf("unlimited check = %v", err)
	}
}

func TestBillingServiceOwnerIDScopesBillingState(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{standardBalance: 2})
	oldKey := Identity{ID: "key-old", OwnerID: "linuxdo:123", Name: "Alice", Role: AuthRoleUser}
	newKey := Identity{ID: "key-new", OwnerID: "linuxdo:123", Name: "Alice", Role: AuthRoleUser}
	if err := svc.CheckAvailable(oldKey, 2); err != nil {
		t.Fatalf("CheckAvailable(old key) error = %v", err)
	}
	if err := svc.Charge(oldKey, 1, BillingReference{}); err != nil {
		t.Fatalf("Charge(old key) error = %v", err)
	}
	if err := svc.CheckAvailable(newKey, 2); err == nil {
		t.Fatal("CheckAvailable(new key same owner) error = nil, want shared owner balance checked")
	}
	got := svc.Get("linuxdo:123")
	standard := util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], -1) != 1 || util.ToInt(got["available"], -1) != 1 {
		t.Fatalf("owner scoped billing = %#v", got)
	}
}

func TestBillingServiceSubscriptionResetAndAdjustments(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{standardBalance: 5})
	operator := Identity{ID: "admin", Name: "Admin", Role: AuthRoleAdmin}
	if _, err := svc.ApplyAdjustment("alice", operator, map[string]any{"type": "switch_to_subscription", "quota_limit": 10, "quota_period": BillingPeriodDaily, "reason": "switch"}); err != nil {
		t.Fatalf("switch_to_subscription error = %v", err)
	}
	if _, err := svc.ApplyAdjustment("alice", operator, map[string]any{"type": "increase_quota", "amount": 3, "reason": "bonus"}); err != nil {
		t.Fatalf("increase_quota error = %v", err)
	}
	if err := svc.CheckAvailable(billingTestUser("alice"), 4); err != nil {
		t.Fatalf("CheckAvailable() error = %v", err)
	}
	if err := svc.Charge(billingTestUser("alice"), 4, BillingReference{}); err != nil {
		t.Fatalf("Charge() error = %v", err)
	}
	got := svc.Get("alice")
	sub := util.StringMap(got["subscription"])
	if util.ToInt(sub["quota_used"], 0) != 4 || util.ToInt(sub["manual_delta"], 0) != 3 {
		t.Fatalf("before reset = %#v", got)
	}

	svc.mu.Lock()
	state := svc.states["alice"]
	sub = billingSubscriptionState(state)
	sub["quota_period_ends_at"] = time.Now().Add(-time.Hour).Format(time.RFC3339)
	_ = svc.saveLocked()
	svc.mu.Unlock()

	got = svc.Get("alice")
	sub = util.StringMap(got["subscription"])
	if util.ToInt(sub["quota_used"], -1) != 0 || util.ToInt(sub["manual_delta"], -1) != 0 || util.ToInt(sub["quota_limit"], 0) != 10 {
		t.Fatalf("after reset = %#v", got)
	}
	if len(svc.ListAdjustments("alice", 10)) < 2 {
		t.Fatalf("adjustments missing")
	}
}

func TestBillingServiceSubscriptionPeriodBounds(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 5, 11, 15, 4, 5, 0, loc)
	tests := []struct {
		period string
		start  time.Time
		end    time.Time
	}{
		{
			period: BillingPeriodDaily,
			start:  time.Date(2026, 5, 11, 0, 0, 0, 0, loc),
			end:    time.Date(2026, 5, 12, 0, 0, 0, 0, loc),
		},
		{
			period: BillingPeriodWeekly,
			start:  time.Date(2026, 5, 11, 0, 0, 0, 0, loc),
			end:    time.Date(2026, 5, 18, 0, 0, 0, 0, loc),
		},
		{
			period: BillingPeriodMonthly,
			start:  time.Date(2026, 5, 1, 0, 0, 0, 0, loc),
			end:    time.Date(2026, 6, 1, 0, 0, 0, 0, loc),
		},
	}
	for _, tc := range tests {
		t.Run(tc.period, func(t *testing.T) {
			start, end := billingPeriodBounds(tc.period, now)
			if !start.Equal(tc.start) || !end.Equal(tc.end) {
				t.Fatalf("bounds = %s - %s, want %s - %s", start, end, tc.start, tc.end)
			}
		})
	}
}

func TestBillingServicePersistsCurrentMapShapeOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, billingDocumentName)
	if err := os.WriteFile(path, []byte(`{
		"states": [
			{
				"user_id": "legacy-array",
				"billing_type": "standard",
				"unit": "image",
				"unlimited": false,
				"standard": {"balance": 9, "balance_reserved": 0, "lifetime_consumed": 0},
				"subscription": {"quota_limit": 0, "quota_used": 0, "quota_reserved": 0, "manual_delta": 0, "quota_period": "monthly", "quota_period_started_at": "2026-05-01T00:00:00Z", "quota_period_ends_at": "2026-06-01T00:00:00Z"}
			}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write billing document: %v", err)
	}
	svc := newTestBillingServiceAt(t, dir, testBillingDefaults{})
	got := svc.Get("legacy-array")
	if got["type"] != BillingTypeStandard || util.ToInt(got["available"], -1) != 0 {
		t.Fatalf("array-shaped states should not be loaded: %#v", got)
	}
}

func TestBillingServiceConcurrentChargeDoesNotOversell(t *testing.T) {
	svc := newTestBillingService(t, testBillingDefaults{standardBalance: 5})
	user := billingTestUser("alice")
	var wg sync.WaitGroup
	successes := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if err := svc.Charge(user, 1, BillingReference{ChargeKey: fmt.Sprintf("task:alice:concurrent:%d", index)}); err == nil {
				successes <- struct{}{}
			}
		}(i)
	}
	wg.Wait()
	close(successes)
	count := 0
	for range successes {
		count++
	}
	if count != 5 {
		t.Fatalf("successful charges = %d, want 5", count)
	}
	got := svc.Get("alice")
	standard := util.StringMap(got["standard"])
	if util.ToInt(standard["balance"], -1) != 0 || util.ToInt(standard["lifetime_consumed"], -1) != 5 || util.ToInt(got["available"], -1) != 0 {
		t.Fatalf("after concurrent charge = %#v", got)
	}
}

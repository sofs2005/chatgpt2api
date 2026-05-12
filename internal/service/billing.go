package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const (
	BillingTypeStandard     = "standard"
	BillingTypeSubscription = "subscription"

	BillingUnitImage = "image"

	BillingPeriodDaily   = "daily"
	BillingPeriodWeekly  = "weekly"
	BillingPeriodMonthly = "monthly"

	billingDocumentName = "user_billing.json"
)

type BillingDefaults interface {
	DefaultBillingType() string
	DefaultStandardBalance() int
	DefaultSubscriptionQuota() int
	DefaultSubscriptionPeriod() string
}

type BillingReference struct {
	Endpoint       string
	Model          string
	TaskID         string
	RequestID      string
	CredentialID   string
	CredentialName string
	ChargeKey      string
	RefundForKey   string
	OutputIndex    int
}

type BillingChargeResult struct {
	Charged        bool
	AlreadyCharged bool
	Billing        map[string]any
}

type BillingRefundResult struct {
	Refunded        bool
	AlreadyRefunded bool
	Billing         map[string]any
}

type BillingLimitError struct {
	BillingType string
	Message     string
	Code        string
}

func (e BillingLimitError) Error() string {
	return e.Message
}

func (e BillingLimitError) OpenAIError() map[string]any {
	return map[string]any{
		"error": map[string]any{
			"message": e.Message,
			"type":    "insufficient_quota",
			"param":   nil,
			"code":    e.Code,
		},
	}
}

func NewBillingLimitError(billingType string) BillingLimitError {
	if normalizeBillingType(billingType) == BillingTypeSubscription {
		return BillingLimitError{
			BillingType: BillingTypeSubscription,
			Message:     "user quota exceeded",
			Code:        "user_quota_exceeded",
		}
	}
	return BillingLimitError{
		BillingType: BillingTypeStandard,
		Message:     "user balance insufficient",
		Code:        "user_balance_insufficient",
	}
}

type BillingService struct {
	mu       sync.Mutex
	path     string
	store    storage.JSONDocumentBackend
	defaults BillingDefaults

	states       map[string]map[string]any
	adjustments  []map[string]any
	transactions []map[string]any
}

func NewBillingService(dataDir string, backend storage.Backend, defaults BillingDefaults) *BillingService {
	path := filepath.Join(dataDir, billingDocumentName)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	s := &BillingService{
		path:     path,
		store:    jsonDocumentStoreFromBackend(backend),
		defaults: defaults,
		states:   map[string]map[string]any{},
	}
	s.mu.Lock()
	s.loadLocked()
	s.mu.Unlock()
	return s
}

func (s *BillingService) Get(userID string) map[string]any {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, changed := s.ensureStateLocked(userID)
	if s.resetSubscriptionIfDueLocked(state, time.Now()) {
		changed = true
	}
	if changed {
		_ = s.saveLocked()
	}
	return publicBillingState(state)
}

func (s *BillingService) GetMany(userIDs []string) map[string]map[string]any {
	out := map[string]map[string]any{}
	if len(userIDs) == 0 {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	now := time.Now()
	for _, userID := range userIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		state, stateChanged := s.ensureStateLocked(userID)
		if stateChanged {
			changed = true
		}
		if s.resetSubscriptionIfDueLocked(state, now) {
			changed = true
		}
		out[userID] = publicBillingState(state)
	}
	if changed {
		_ = s.saveLocked()
	}
	return out
}

func (s *BillingService) CheckAvailable(identity Identity, amount int) error {
	if s == nil || identity.Role != AuthRoleUser || amount <= 0 {
		return nil
	}
	userID := billingUserID(identity)
	if userID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, changed := s.ensureStateLocked(userID)
	if s.resetSubscriptionIfDueLocked(state, time.Now()) {
		changed = true
	}
	if util.ToBool(state["unlimited"]) {
		if changed {
			_ = s.saveLocked()
		}
		return nil
	}
	billingType := normalizeBillingType(util.Clean(state["billing_type"]))
	switch billingType {
	case BillingTypeStandard:
		standard := billingStandardState(state)
		if availableStandardBalance(standard) < amount {
			if changed {
				_ = s.saveLocked()
			}
			return NewBillingLimitError(BillingTypeStandard)
		}
	case BillingTypeSubscription:
		subscription := billingSubscriptionState(state)
		if availableSubscriptionQuota(subscription) < amount {
			if changed {
				_ = s.saveLocked()
			}
			return NewBillingLimitError(BillingTypeSubscription)
		}
	default:
		return fmt.Errorf("unsupported billing type: %s", billingType)
	}
	if changed {
		_ = s.saveLocked()
	}
	return nil
}

func (s *BillingService) Charge(identity Identity, amount int, ref BillingReference) error {
	if identity.Role != AuthRoleUser {
		return nil
	}
	_, err := s.ChargeUserID(billingUserID(identity), amount, ref)
	return err
}

func (s *BillingService) ChargeUserID(userID string, amount int, ref BillingReference) (BillingChargeResult, error) {
	return s.chargeUserID(strings.TrimSpace(userID), amount, ref)
}

func (s *BillingService) chargeUserID(userID string, amount int, ref BillingReference) (BillingChargeResult, error) {
	result := BillingChargeResult{}
	if s == nil || userID == "" || amount <= 0 {
		return result, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _ := s.ensureStateLocked(userID)
	s.resetSubscriptionIfDueLocked(state, time.Now())
	if util.ToBool(state["unlimited"]) {
		_ = s.saveLocked()
		result.Billing = publicBillingState(state)
		return result, nil
	}
	chargeKey := strings.TrimSpace(ref.ChargeKey)
	if chargeKey != "" && s.hasChargeKeyLocked(userID, chargeKey) {
		result.AlreadyCharged = true
		result.Billing = publicBillingState(state)
		return result, nil
	}
	billingType := normalizeBillingType(util.Clean(state["billing_type"]))
	switch billingType {
	case BillingTypeStandard:
		standard := billingStandardState(state)
		if availableStandardBalance(standard) < amount {
			return result, NewBillingLimitError(BillingTypeStandard)
		}
		standard["balance"] = intField(standard, "balance") - amount
		standard["lifetime_consumed"] = intField(standard, "lifetime_consumed") + amount
	case BillingTypeSubscription:
		subscription := billingSubscriptionState(state)
		if availableSubscriptionQuota(subscription) < amount {
			return result, NewBillingLimitError(BillingTypeSubscription)
		}
		subscription["quota_used"] = intField(subscription, "quota_used") + amount
	default:
		return result, fmt.Errorf("unsupported billing type: %s", billingType)
	}
	state["updated_at"] = util.NowISO()
	s.addTransactionLocked(map[string]any{
		"user_id":         userID,
		"billing_type":    billingType,
		"unit":            BillingUnitImage,
		"action":          "charge",
		"consumed_amount": amount,
		"charge_key":      chargeKey,
		"endpoint":        ref.Endpoint,
		"model":           ref.Model,
		"task_id":         ref.TaskID,
		"request_id":      ref.RequestID,
		"output_index":    ref.OutputIndex,
	})
	result.Charged = true
	result.Billing = publicBillingState(state)
	if err := s.saveLocked(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *BillingService) RefundUserID(userID string, amount int, ref BillingReference) (BillingRefundResult, error) {
	result := BillingRefundResult{}
	userID = strings.TrimSpace(userID)
	if s == nil || userID == "" || amount <= 0 {
		return result, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _ := s.ensureStateLocked(userID)
	s.resetSubscriptionIfDueLocked(state, time.Now())
	if util.ToBool(state["unlimited"]) {
		result.Billing = publicBillingState(state)
		return result, nil
	}
	refundKey := strings.TrimSpace(ref.ChargeKey)
	if refundKey != "" && s.hasRefundKeyLocked(userID, refundKey) {
		result.AlreadyRefunded = true
		result.Billing = publicBillingState(state)
		return result, nil
	}
	refundForKey := strings.TrimSpace(ref.RefundForKey)
	amount = s.refundableAmountLocked(userID, amount, refundForKey)
	if amount <= 0 {
		result.Billing = publicBillingState(state)
		return result, nil
	}
	billingType := normalizeBillingType(util.Clean(state["billing_type"]))
	switch billingType {
	case BillingTypeStandard:
		standard := billingStandardState(state)
		standard["balance"] = intField(standard, "balance") + amount
		standard["lifetime_consumed"] = max(0, intField(standard, "lifetime_consumed")-amount)
	case BillingTypeSubscription:
		subscription := billingSubscriptionState(state)
		subscription["quota_used"] = max(0, intField(subscription, "quota_used")-amount)
	default:
		return result, fmt.Errorf("unsupported billing type: %s", billingType)
	}
	state["updated_at"] = util.NowISO()
	s.addTransactionLocked(map[string]any{
		"user_id":               userID,
		"billing_type":          billingType,
		"unit":                  BillingUnitImage,
		"action":                "refund",
		"refunded_amount":       amount,
		"charge_key":            refundKey,
		"refund_for_charge_key": refundForKey,
		"endpoint":              ref.Endpoint,
		"model":                 ref.Model,
		"task_id":               ref.TaskID,
		"request_id":            ref.RequestID,
		"output_index":          ref.OutputIndex,
	})
	result.Refunded = true
	result.Billing = publicBillingState(state)
	if err := s.saveLocked(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *BillingService) ApplyAdjustment(userID string, operator Identity, body map[string]any) (map[string]any, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("user id is required")
	}
	adjustmentType := strings.TrimSpace(util.Clean(body["type"]))
	if adjustmentType == "" {
		return nil, errors.New("adjustment type is required")
	}
	reason := strings.TrimSpace(util.Clean(body["reason"]))

	s.mu.Lock()
	defer s.mu.Unlock()
	state, _ := s.ensureStateLocked(userID)
	s.resetSubscriptionIfDueLocked(state, time.Now())
	before := publicBillingState(state)
	amount := adjustmentAmount(body)

	switch adjustmentType {
	case "set_unlimited":
		state["unlimited"] = util.ToBool(body["unlimited"])
	case "switch_to_standard":
		state["billing_type"] = BillingTypeStandard
		if _, ok := body["balance"]; ok {
			if err := setStandardBalance(state, util.ToInt(body["balance"], 0)); err != nil {
				return nil, err
			}
		} else if _, ok := body["amount"]; ok {
			if err := setStandardBalance(state, amount); err != nil {
				return nil, err
			}
		}
	case "switch_to_subscription":
		rawQuotaLimit, ok := body["quota_limit"]
		if !ok {
			return nil, errors.New("quota limit is required")
		}
		quotaLimit := util.ToInt(rawQuotaLimit, 0)
		if quotaLimit < 0 {
			return nil, errors.New("quota limit cannot be negative")
		}
		period := normalizeBillingPeriod(util.Clean(body["quota_period"]))
		if period == "" {
			return nil, errors.New("quota period must be daily, weekly, or monthly")
		}
		state["billing_type"] = BillingTypeSubscription
		subscription := billingSubscriptionState(state)
		subscription["quota_limit"] = quotaLimit
		subscription["quota_period"] = period
		resetSubscriptionPeriod(subscription, time.Now())
	case "set_balance":
		if err := setStandardBalance(state, firstIntValue(body, "balance", "amount")); err != nil {
			return nil, err
		}
	case "increase_balance":
		if amount <= 0 {
			return nil, errors.New("amount must be greater than 0")
		}
		standard := billingStandardState(state)
		standard["balance"] = intField(standard, "balance") + amount
	case "decrease_balance":
		if amount <= 0 {
			return nil, errors.New("amount must be greater than 0")
		}
		standard := billingStandardState(state)
		if intField(standard, "balance")-amount < 0 {
			return nil, errors.New("balance cannot be negative")
		}
		standard["balance"] = intField(standard, "balance") - amount
	case "set_quota_limit":
		limit := firstIntValue(body, "quota_limit", "amount")
		if limit < 0 {
			return nil, errors.New("quota limit cannot be negative")
		}
		billingSubscriptionState(state)["quota_limit"] = limit
	case "set_quota_period":
		period := normalizeBillingPeriod(util.Clean(body["quota_period"]))
		if period == "" {
			return nil, errors.New("quota period must be daily, weekly, or monthly")
		}
		subscription := billingSubscriptionState(state)
		subscription["quota_period"] = period
		resetSubscriptionPeriod(subscription, time.Now())
	case "reset_quota":
		resetSubscriptionPeriod(billingSubscriptionState(state), time.Now())
	case "clear_quota_used":
		billingSubscriptionState(state)["quota_used"] = 0
	case "increase_quota":
		if amount <= 0 {
			return nil, errors.New("amount must be greater than 0")
		}
		subscription := billingSubscriptionState(state)
		subscription["manual_delta"] = intField(subscription, "manual_delta") + amount
	case "decrease_quota":
		if amount <= 0 {
			return nil, errors.New("amount must be greater than 0")
		}
		subscription := billingSubscriptionState(state)
		if availableSubscriptionQuota(subscription) < amount {
			return nil, errors.New("quota decrease cannot exceed remaining quota")
		}
		subscription["manual_delta"] = intField(subscription, "manual_delta") - amount
	default:
		return nil, fmt.Errorf("unsupported billing adjustment type: %s", adjustmentType)
	}

	state["billing_type"] = normalizeBillingType(util.Clean(state["billing_type"]))
	state["unit"] = BillingUnitImage
	state["updated_at"] = util.NowISO()
	after := publicBillingState(state)
	adjustment := map[string]any{
		"id":            "billing_adj_" + util.NewHex(18),
		"user_id":       userID,
		"operator_id":   billingUserID(operator),
		"operator_name": operator.Name,
		"billing_type":  state["billing_type"],
		"type":          adjustmentType,
		"amount":        amount,
		"reason":        reason,
		"before":        before,
		"after":         after,
		"created_at":    util.NowISO(),
	}
	s.adjustments = append(s.adjustments, adjustment)
	s.addTransactionLocked(map[string]any{
		"user_id":       userID,
		"billing_type":  state["billing_type"],
		"unit":          BillingUnitImage,
		"action":        "adjust",
		"adjustment_id": adjustment["id"],
		"adjustment":    adjustmentType,
		"amount":        amount,
	})
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return map[string]any{"billing": after, "adjustment": adjustment}, nil
}

func (s *BillingService) ListAdjustments(userID string, limit int) []map[string]any {
	userID = strings.TrimSpace(userID)
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, min(limit, len(s.adjustments)))
	for i := len(s.adjustments) - 1; i >= 0 && len(out) < limit; i-- {
		item := s.adjustments[i]
		if userID != "" && util.Clean(item["user_id"]) != userID {
			continue
		}
		out = append(out, copyBillingMap(item))
	}
	return out
}

func (s *BillingService) ensureStateLocked(userID string) (map[string]any, bool) {
	if s.states == nil {
		s.states = map[string]map[string]any{}
	}
	state := s.states[userID]
	if state == nil {
		state = defaultBillingState(userID, s.defaults)
		s.states[userID] = state
		return state, true
	}
	changed := normalizeBillingState(state, userID, s.defaults)
	return state, changed
}

func (s *BillingService) resetSubscriptionIfDueLocked(state map[string]any, now time.Time) bool {
	if normalizeBillingType(util.Clean(state["billing_type"])) != BillingTypeSubscription {
		return false
	}
	subscription := billingSubscriptionState(state)
	endsAt := parseBillingTime(util.Clean(subscription["quota_period_ends_at"]))
	if !endsAt.IsZero() && now.Before(endsAt) {
		return false
	}
	resetSubscriptionPeriod(subscription, now)
	state["updated_at"] = util.NowISO()
	s.addTransactionLocked(map[string]any{
		"user_id":      util.Clean(state["user_id"]),
		"billing_type": BillingTypeSubscription,
		"unit":         BillingUnitImage,
		"action":       "reset_subscription_period",
	})
	return true
}

func (s *BillingService) loadLocked() {
	raw := loadStoredJSON(s.store, billingDocumentName, s.path)
	doc, _ := raw.(map[string]any)
	s.states = map[string]map[string]any{}
	if states, ok := doc["states"].(map[string]any); ok {
		for userID, value := range states {
			if state, ok := value.(map[string]any); ok {
				normalizeBillingState(state, userID, s.defaults)
				s.states[userID] = state
			}
		}
	}
	s.adjustments = util.AsMapSlice(doc["adjustments"])
	s.transactions = util.AsMapSlice(doc["transactions"])
	if s.adjustments == nil {
		s.adjustments = []map[string]any{}
	}
	if s.transactions == nil {
		s.transactions = []map[string]any{}
	}
}

func (s *BillingService) saveLocked() error {
	states := map[string]any{}
	keys := make([]string, 0, len(s.states))
	for key := range s.states {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		states[key] = s.states[key]
	}
	doc := map[string]any{
		"states":       states,
		"adjustments":  s.adjustments,
		"transactions": s.transactions,
		"updated_at":   util.NowISO(),
	}
	return saveStoredJSON(s.store, billingDocumentName, s.path, doc)
}

func (s *BillingService) addTransactionLocked(item map[string]any) {
	if item == nil {
		return
	}
	item = copyBillingMap(item)
	if util.Clean(item["id"]) == "" {
		item["id"] = "billing_txn_" + util.NewHex(18)
	}
	if util.Clean(item["created_at"]) == "" {
		item["created_at"] = util.NowISO()
	}
	s.transactions = append(s.transactions, item)
	if len(s.transactions) > 5000 {
		s.transactions = append([]map[string]any(nil), s.transactions[len(s.transactions)-5000:]...)
	}
}

func (s *BillingService) hasChargeKeyLocked(userID, chargeKey string) bool {
	userID = strings.TrimSpace(userID)
	chargeKey = strings.TrimSpace(chargeKey)
	if userID == "" || chargeKey == "" {
		return false
	}
	for i := len(s.transactions) - 1; i >= 0; i-- {
		if util.Clean(s.transactions[i]["user_id"]) == userID && util.Clean(s.transactions[i]["charge_key"]) == chargeKey {
			return true
		}
	}
	return false
}

func (s *BillingService) hasRefundKeyLocked(userID, refundKey string) bool {
	userID = strings.TrimSpace(userID)
	refundKey = strings.TrimSpace(refundKey)
	if userID == "" || refundKey == "" {
		return false
	}
	for i := len(s.transactions) - 1; i >= 0; i-- {
		if util.Clean(s.transactions[i]["user_id"]) == userID && util.Clean(s.transactions[i]["charge_key"]) == refundKey && util.Clean(s.transactions[i]["action"]) == "refund" {
			return true
		}
	}
	return false
}

func (s *BillingService) refundableAmountLocked(userID string, amount int, chargeKey string) int {
	amount = max(0, amount)
	chargeKey = strings.TrimSpace(chargeKey)
	if amount <= 0 || chargeKey == "" {
		return amount
	}
	charged := 0
	refunded := 0
	for _, txn := range s.transactions {
		if util.Clean(txn["user_id"]) != userID {
			continue
		}
		switch util.Clean(txn["action"]) {
		case "charge":
			if util.Clean(txn["charge_key"]) == chargeKey {
				charged += util.ToInt(txn["consumed_amount"], 0)
			}
		case "refund":
			if util.Clean(txn["refund_for_charge_key"]) == chargeKey {
				refunded += util.ToInt(txn["refunded_amount"], 0)
			}
		}
	}
	return min(amount, max(0, charged-refunded))
}

func defaultBillingState(userID string, defaults BillingDefaults) map[string]any {
	now := time.Now()
	period := defaultBillingPeriod(defaults)
	started, ends := billingPeriodBounds(period, now)
	return map[string]any{
		"user_id":      userID,
		"billing_type": defaultBillingType(defaults),
		"unit":         BillingUnitImage,
		"unlimited":    false,
		"standard": map[string]any{
			"balance":           max(0, defaultStandardBalance(defaults)),
			"lifetime_consumed": 0,
		},
		"subscription": map[string]any{
			"quota_limit":             max(0, defaultSubscriptionQuota(defaults)),
			"quota_used":              0,
			"manual_delta":            0,
			"quota_period":            period,
			"quota_period_started_at": started.Format(time.RFC3339),
			"quota_period_ends_at":    ends.Format(time.RFC3339),
		},
		"updated_at": util.NowISO(),
	}
}

func normalizeBillingState(state map[string]any, userID string, defaults BillingDefaults) bool {
	changed := false
	if util.Clean(state["user_id"]) != userID {
		state["user_id"] = userID
		changed = true
	}
	billingType := normalizeBillingType(util.Clean(state["billing_type"]))
	if billingType == "" {
		billingType = defaultBillingType(defaults)
	}
	if state["billing_type"] != billingType {
		state["billing_type"] = billingType
		changed = true
	}
	if state["unit"] != BillingUnitImage {
		state["unit"] = BillingUnitImage
		changed = true
	}
	if _, ok := state["unlimited"]; !ok {
		state["unlimited"] = false
		changed = true
	}
	if _, ok := state["standard"].(map[string]any); !ok {
		state["standard"] = map[string]any{
			"balance":           max(0, defaultStandardBalance(defaults)),
			"lifetime_consumed": 0,
		}
		changed = true
	}
	standard := billingStandardState(state)
	for key := range map[string]struct{}{"balance": {}, "lifetime_consumed": {}} {
		value := max(0, intField(standard, key))
		if standard[key] != value {
			standard[key] = value
			changed = true
		}
	}
	if _, ok := standard["balance_reserved"]; ok {
		delete(standard, "balance_reserved")
		changed = true
	}
	if _, ok := state["subscription"].(map[string]any); !ok {
		period := defaultBillingPeriod(defaults)
		started, ends := billingPeriodBounds(period, time.Now())
		state["subscription"] = map[string]any{
			"quota_limit":             max(0, defaultSubscriptionQuota(defaults)),
			"quota_used":              0,
			"manual_delta":            0,
			"quota_period":            period,
			"quota_period_started_at": started.Format(time.RFC3339),
			"quota_period_ends_at":    ends.Format(time.RFC3339),
		}
		changed = true
	}
	subscription := billingSubscriptionState(state)
	for key := range map[string]struct{}{"quota_limit": {}, "quota_used": {}} {
		value := max(0, intField(subscription, key))
		if subscription[key] != value {
			subscription[key] = value
			changed = true
		}
	}
	if manualDelta := intField(subscription, "manual_delta"); subscription["manual_delta"] != manualDelta {
		subscription["manual_delta"] = manualDelta
		changed = true
	}
	if _, ok := subscription["quota_reserved"]; ok {
		delete(subscription, "quota_reserved")
		changed = true
	}
	period := normalizeBillingPeriod(util.Clean(subscription["quota_period"]))
	if period == "" {
		period = defaultBillingPeriod(defaults)
	}
	if subscription["quota_period"] != period {
		subscription["quota_period"] = period
		changed = true
	}
	if parseBillingTime(util.Clean(subscription["quota_period_started_at"])).IsZero() || parseBillingTime(util.Clean(subscription["quota_period_ends_at"])).IsZero() {
		resetSubscriptionPeriod(subscription, time.Now())
		changed = true
	}
	if util.Clean(state["updated_at"]) == "" {
		state["updated_at"] = util.NowISO()
		changed = true
	}
	return changed
}

func publicBillingState(state map[string]any) map[string]any {
	billingType := normalizeBillingType(util.Clean(state["billing_type"]))
	unlimited := util.ToBool(state["unlimited"])
	out := map[string]any{
		"type":         billingType,
		"unit":         BillingUnitImage,
		"unlimited":    unlimited,
		"available":    0,
		"standard":     nil,
		"subscription": nil,
		"updated_at":   state["updated_at"],
	}
	switch billingType {
	case BillingTypeStandard:
		standard := copyBillingMap(billingStandardState(state))
		available := availableStandardBalance(standard)
		out["available"] = available
		standard["available_balance"] = available
		out["standard"] = standard
	case BillingTypeSubscription:
		subscription := copyBillingMap(billingSubscriptionState(state))
		available := availableSubscriptionQuota(subscription)
		out["available"] = available
		subscription["remaining_quota"] = available
		out["subscription"] = subscription
	}
	if unlimited {
		out["limit_state"] = "unlimited"
	} else if util.ToInt(out["available"], 0) > 0 {
		out["limit_state"] = "ok"
	} else {
		out["limit_state"] = "insufficient"
	}
	return out
}

func billingStandardState(state map[string]any) map[string]any {
	standard, ok := state["standard"].(map[string]any)
	if !ok || standard == nil {
		standard = map[string]any{}
		state["standard"] = standard
	}
	return standard
}

func billingSubscriptionState(state map[string]any) map[string]any {
	subscription, ok := state["subscription"].(map[string]any)
	if !ok || subscription == nil {
		subscription = map[string]any{}
		state["subscription"] = subscription
	}
	return subscription
}

func availableStandardBalance(standard map[string]any) int {
	return max(0, intField(standard, "balance"))
}

func availableSubscriptionQuota(subscription map[string]any) int {
	return max(0, intField(subscription, "quota_limit")+intField(subscription, "manual_delta")-intField(subscription, "quota_used"))
}

func setStandardBalance(state map[string]any, balance int) error {
	if balance < 0 {
		return errors.New("balance cannot be negative")
	}
	standard := billingStandardState(state)
	standard["balance"] = balance
	return nil
}

func resetSubscriptionPeriod(subscription map[string]any, now time.Time) {
	period := normalizeBillingPeriod(util.Clean(subscription["quota_period"]))
	if period == "" {
		period = BillingPeriodMonthly
	}
	started, ends := billingPeriodBounds(period, now)
	subscription["quota_used"] = 0
	subscription["manual_delta"] = 0
	subscription["quota_period"] = period
	subscription["quota_period_started_at"] = started.Format(time.RFC3339)
	subscription["quota_period_ends_at"] = ends.Format(time.RFC3339)
}

func billingPeriodBounds(period string, now time.Time) (time.Time, time.Time) {
	loc := now.Location()
	switch normalizeBillingPeriod(period) {
	case BillingPeriodDaily:
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		return start, start.AddDate(0, 0, 1)
	case BillingPeriodWeekly:
		weekdayOffset := (int(now.Weekday()) + 6) % 7
		day := now.AddDate(0, 0, -weekdayOffset)
		start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
		return start, start.AddDate(0, 0, 7)
	default:
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		return start, start.AddDate(0, 1, 0)
	}
}

func parseBillingTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func billingUserID(identity Identity) string {
	if owner := util.Clean(identity.OwnerID); owner != "" {
		return owner
	}
	return util.Clean(identity.ID)
}

func normalizeBillingType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case BillingTypeSubscription:
		return BillingTypeSubscription
	case "", BillingTypeStandard:
		return BillingTypeStandard
	default:
		return ""
	}
}

func normalizeBillingPeriod(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case BillingPeriodDaily, BillingPeriodWeekly, BillingPeriodMonthly:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func defaultBillingType(defaults BillingDefaults) string {
	if defaults == nil {
		return BillingTypeStandard
	}
	if value := normalizeBillingType(defaults.DefaultBillingType()); value != "" {
		return value
	}
	return BillingTypeStandard
}

func defaultBillingPeriod(defaults BillingDefaults) string {
	if defaults == nil {
		return BillingPeriodMonthly
	}
	if value := normalizeBillingPeriod(defaults.DefaultSubscriptionPeriod()); value != "" {
		return value
	}
	return BillingPeriodMonthly
}

func defaultStandardBalance(defaults BillingDefaults) int {
	if defaults == nil {
		return 0
	}
	return defaults.DefaultStandardBalance()
}

func defaultSubscriptionQuota(defaults BillingDefaults) int {
	if defaults == nil {
		return 0
	}
	return defaults.DefaultSubscriptionQuota()
}

func intField(item map[string]any, key string) int {
	return util.ToInt(item[key], 0)
}

func firstIntValue(item map[string]any, keys ...string) int {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			return util.ToInt(value, 0)
		}
	}
	return 0
}

func adjustmentAmount(item map[string]any) int {
	return firstIntValue(item, "amount", "balance", "quota_limit")
}

func copyBillingMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if child, ok := value.(map[string]any); ok {
			out[key] = copyBillingMap(child)
		} else {
			out[key] = value
		}
	}
	return out
}

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
}

type BillingReservation struct {
	ID     string
	UserID string
	Amount int
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
	reservations map[string]map[string]any
	adjustments  []map[string]any
	transactions []map[string]any
}

func NewBillingService(dataDir string, backend storage.Backend, defaults BillingDefaults) *BillingService {
	path := filepath.Join(dataDir, billingDocumentName)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	s := &BillingService{
		path:         path,
		store:        jsonDocumentStoreFromBackend(backend),
		defaults:     defaults,
		states:       map[string]map[string]any{},
		reservations: map[string]map[string]any{},
	}
	s.mu.Lock()
	s.loadLocked()
	if s.releaseRecoveredReservationsLocked() {
		_ = s.saveLocked()
	}
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

func (s *BillingService) Reserve(identity Identity, amount int, ref BillingReference) (*BillingReservation, error) {
	if s == nil || identity.Role != AuthRoleUser || amount <= 0 {
		return nil, nil
	}
	userID := billingUserID(identity)
	if userID == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _ := s.ensureStateLocked(userID)
	s.resetSubscriptionIfDueLocked(state, time.Now())
	if util.ToBool(state["unlimited"]) {
		_ = s.saveLocked()
		return nil, nil
	}
	billingType := normalizeBillingType(util.Clean(state["billing_type"]))
	switch billingType {
	case BillingTypeStandard:
		standard := billingStandardState(state)
		if availableStandardBalance(standard) < amount {
			_ = s.saveLocked()
			return nil, NewBillingLimitError(BillingTypeStandard)
		}
		standard["balance_reserved"] = intField(standard, "balance_reserved") + amount
	case BillingTypeSubscription:
		subscription := billingSubscriptionState(state)
		if availableSubscriptionQuota(subscription) < amount {
			_ = s.saveLocked()
			return nil, NewBillingLimitError(BillingTypeSubscription)
		}
		subscription["quota_reserved"] = intField(subscription, "quota_reserved") + amount
	default:
		return nil, fmt.Errorf("unsupported billing type: %s", billingType)
	}
	now := util.NowISO()
	state["updated_at"] = now
	id := "billing_res_" + util.NewHex(18)
	reservation := map[string]any{
		"id":              id,
		"user_id":         userID,
		"billing_type":    billingType,
		"unit":            BillingUnitImage,
		"amount":          amount,
		"endpoint":        strings.TrimSpace(ref.Endpoint),
		"model":           strings.TrimSpace(ref.Model),
		"task_id":         strings.TrimSpace(ref.TaskID),
		"request_id":      strings.TrimSpace(ref.RequestID),
		"credential_id":   strings.TrimSpace(ref.CredentialID),
		"credential_name": strings.TrimSpace(ref.CredentialName),
		"created_at":      now,
	}
	s.reservations[id] = reservation
	s.addTransactionLocked(map[string]any{
		"user_id":         userID,
		"billing_type":    billingType,
		"unit":            BillingUnitImage,
		"action":          "reserve",
		"reserved_amount": amount,
		"endpoint":        ref.Endpoint,
		"model":           ref.Model,
		"task_id":         ref.TaskID,
		"request_id":      ref.RequestID,
	})
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return &BillingReservation{ID: id, UserID: userID, Amount: amount}, nil
}

func (s *BillingService) Settle(reservation *BillingReservation, consumed int) {
	if s == nil || reservation == nil || strings.TrimSpace(reservation.ID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settleLocked(reservation.ID, consumed)
	_ = s.saveLocked()
}

func (s *BillingService) Release(reservation *BillingReservation) {
	s.Settle(reservation, 0)
}

func (s *BillingService) SettleReservationID(reservationID string, consumed int) {
	if s == nil || strings.TrimSpace(reservationID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settleLocked(reservationID, consumed)
	_ = s.saveLocked()
}

func (s *BillingService) ReleaseReservationID(reservationID string) {
	s.SettleReservationID(reservationID, 0)
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
	if reason == "" {
		return nil, errors.New("adjustment reason is required")
	}

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
		if err := ensureNoBillingReservations(state); err != nil {
			return nil, err
		}
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
		if err := ensureNoBillingReservations(state); err != nil {
			return nil, err
		}
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
		if intField(standard, "balance")-amount-intField(standard, "balance_reserved") < 0 {
			return nil, errors.New("balance cannot be lower than reserved balance")
		}
		standard["balance"] = intField(standard, "balance") - amount
	case "release_balance_reserved":
		standard := billingStandardState(state)
		release := amount
		if release <= 0 || release > intField(standard, "balance_reserved") {
			release = intField(standard, "balance_reserved")
		}
		standard["balance_reserved"] = intField(standard, "balance_reserved") - release
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
	case "release_quota_reserved":
		subscription := billingSubscriptionState(state)
		release := amount
		if release <= 0 || release > intField(subscription, "quota_reserved") {
			release = intField(subscription, "quota_reserved")
		}
		subscription["quota_reserved"] = intField(subscription, "quota_reserved") - release
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

func (s *BillingService) settleLocked(reservationID string, consumed int) {
	reservation := s.reservations[reservationID]
	if reservation == nil {
		return
	}
	userID := util.Clean(reservation["user_id"])
	state, _ := s.ensureStateLocked(userID)
	s.resetSubscriptionIfDueLocked(state, time.Now())
	amount := intField(reservation, "amount")
	if consumed < 0 {
		consumed = 0
	}
	if consumed > amount {
		consumed = amount
	}
	billingType := normalizeBillingType(util.Clean(reservation["billing_type"]))
	switch billingType {
	case BillingTypeStandard:
		standard := billingStandardState(state)
		standard["balance_reserved"] = max(0, intField(standard, "balance_reserved")-amount)
		standard["balance"] = max(0, intField(standard, "balance")-consumed)
		standard["lifetime_consumed"] = intField(standard, "lifetime_consumed") + consumed
	case BillingTypeSubscription:
		subscription := billingSubscriptionState(state)
		subscription["quota_reserved"] = max(0, intField(subscription, "quota_reserved")-amount)
		subscription["quota_used"] = intField(subscription, "quota_used") + consumed
	}
	state["updated_at"] = util.NowISO()
	delete(s.reservations, reservationID)
	s.addTransactionLocked(map[string]any{
		"user_id":         userID,
		"billing_type":    billingType,
		"unit":            BillingUnitImage,
		"action":          "settle",
		"reservation_id":  reservationID,
		"reserved_amount": amount,
		"consumed_amount": consumed,
		"released_amount": amount - consumed,
		"endpoint":        reservation["endpoint"],
		"model":           reservation["model"],
		"task_id":         reservation["task_id"],
		"request_id":      reservation["request_id"],
	})
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
	s.reservations = map[string]map[string]any{}
	for _, item := range util.AsMapSlice(doc["reservations"]) {
		id := util.Clean(item["id"])
		if id != "" {
			s.reservations[id] = item
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
	reservations := make([]map[string]any, 0, len(s.reservations))
	for _, reservation := range s.reservations {
		reservations = append(reservations, reservation)
	}
	sort.SliceStable(reservations, func(i, j int) bool {
		return util.Clean(reservations[i]["created_at"]) < util.Clean(reservations[j]["created_at"])
	})
	doc := map[string]any{
		"states":       states,
		"reservations": reservations,
		"adjustments":  s.adjustments,
		"transactions": s.transactions,
		"updated_at":   util.NowISO(),
	}
	return saveStoredJSON(s.store, billingDocumentName, s.path, doc)
}

func (s *BillingService) releaseRecoveredReservationsLocked() bool {
	if len(s.reservations) == 0 {
		return false
	}
	for id, reservation := range s.reservations {
		userID := util.Clean(reservation["user_id"])
		state, _ := s.ensureStateLocked(userID)
		amount := intField(reservation, "amount")
		switch normalizeBillingType(util.Clean(reservation["billing_type"])) {
		case BillingTypeStandard:
			standard := billingStandardState(state)
			standard["balance_reserved"] = max(0, intField(standard, "balance_reserved")-amount)
		case BillingTypeSubscription:
			subscription := billingSubscriptionState(state)
			subscription["quota_reserved"] = max(0, intField(subscription, "quota_reserved")-amount)
		}
		state["updated_at"] = util.NowISO()
		s.addTransactionLocked(map[string]any{
			"user_id":         userID,
			"billing_type":    reservation["billing_type"],
			"unit":            BillingUnitImage,
			"action":          "release_recovered_reservation",
			"reservation_id":  id,
			"reserved_amount": amount,
			"released_amount": amount,
			"endpoint":        reservation["endpoint"],
			"model":           reservation["model"],
			"task_id":         reservation["task_id"],
			"request_id":      reservation["request_id"],
		})
	}
	s.reservations = map[string]map[string]any{}
	return true
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
			"balance_reserved":  0,
			"lifetime_consumed": 0,
		},
		"subscription": map[string]any{
			"quota_limit":             max(0, defaultSubscriptionQuota(defaults)),
			"quota_used":              0,
			"quota_reserved":          0,
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
			"balance_reserved":  0,
			"lifetime_consumed": 0,
		}
		changed = true
	}
	standard := billingStandardState(state)
	for key := range map[string]struct{}{"balance": {}, "balance_reserved": {}, "lifetime_consumed": {}} {
		value := max(0, intField(standard, key))
		if standard[key] != value {
			standard[key] = value
			changed = true
		}
	}
	if _, ok := state["subscription"].(map[string]any); !ok {
		period := defaultBillingPeriod(defaults)
		started, ends := billingPeriodBounds(period, time.Now())
		state["subscription"] = map[string]any{
			"quota_limit":             max(0, defaultSubscriptionQuota(defaults)),
			"quota_used":              0,
			"quota_reserved":          0,
			"manual_delta":            0,
			"quota_period":            period,
			"quota_period_started_at": started.Format(time.RFC3339),
			"quota_period_ends_at":    ends.Format(time.RFC3339),
		}
		changed = true
	}
	subscription := billingSubscriptionState(state)
	for key := range map[string]struct{}{"quota_limit": {}, "quota_used": {}, "quota_reserved": {}} {
		value := max(0, intField(subscription, key))
		if subscription[key] != value {
			subscription[key] = value
			changed = true
		}
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
	return max(0, intField(standard, "balance")-intField(standard, "balance_reserved"))
}

func availableSubscriptionQuota(subscription map[string]any) int {
	return max(0, intField(subscription, "quota_limit")+intField(subscription, "manual_delta")-intField(subscription, "quota_used")-intField(subscription, "quota_reserved"))
}

func setStandardBalance(state map[string]any, balance int) error {
	if balance < 0 {
		return errors.New("balance cannot be negative")
	}
	standard := billingStandardState(state)
	if balance-intField(standard, "balance_reserved") < 0 {
		return errors.New("balance cannot be lower than reserved balance")
	}
	standard["balance"] = balance
	return nil
}

func ensureNoBillingReservations(state map[string]any) error {
	if intField(billingStandardState(state), "balance_reserved") > 0 || intField(billingSubscriptionState(state), "quota_reserved") > 0 {
		return errors.New("billing type cannot be changed while reservations exist")
	}
	return nil
}

func resetSubscriptionPeriod(subscription map[string]any, now time.Time) {
	period := normalizeBillingPeriod(util.Clean(subscription["quota_period"]))
	if period == "" {
		period = BillingPeriodMonthly
	}
	started, ends := billingPeriodBounds(period, now)
	subscription["quota_used"] = 0
	subscription["quota_reserved"] = 0
	subscription["manual_delta"] = 0
	subscription["quota_period"] = period
	subscription["quota_period_started_at"] = started.Format(time.RFC3339)
	subscription["quota_period_ends_at"] = ends.Format(time.RFC3339)
}

func billingPeriodBounds(period string, now time.Time) (time.Time, time.Time) {
	local := now.Local()
	switch normalizeBillingPeriod(period) {
	case BillingPeriodDaily:
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
		return start, start.AddDate(0, 0, 1)
	case BillingPeriodWeekly:
		weekdayOffset := (int(local.Weekday()) + 6) % 7
		day := local.AddDate(0, 0, -weekdayOffset)
		start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, local.Location())
		return start, start.AddDate(0, 0, 7)
	default:
		start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, local.Location())
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

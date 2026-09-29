package protocol

import "testing"

// 部分挑战是连接级/出口级的：同一账号先换连接重试，不立刻消耗换号预算。
func TestCloudflareRetrySameAccountBeforeSwitching(t *testing.T) {
	state := newCloudflareRetryState()
	outcome, exhausted := state.next("token-a")
	if outcome != cloudflareRetrySameAccount {
		t.Fatalf("first outcome = %v, want same-account retry", outcome)
	}
	if exhausted {
		t.Fatal("same-account retry must not exclude the account")
	}
	if state.switchAttempts != 0 {
		t.Fatalf("switchAttempts = %d, want the switch budget untouched", state.switchAttempts)
	}
}

// 同号换连接仍被拦，才认定挑战与账号身份绑定，转而换号并排除该账号。
func TestCloudflareRetrySwitchesAfterSameAccountRetry(t *testing.T) {
	state := newCloudflareRetryState()
	state.next("token-a")
	outcome, exhausted := state.next("token-a")
	if outcome != cloudflareRetrySwitchAccount {
		t.Fatalf("second outcome = %v, want account switch", outcome)
	}
	if !exhausted {
		t.Fatal("a switched account must be excluded from further attempts")
	}
}

// 同号重试的额度是按账号计的：换成另一个账号时，它也应该先有一次同号机会。
func TestCloudflareRetrySameAccountQuotaIsPerToken(t *testing.T) {
	state := newCloudflareRetryState()
	state.next("token-a")
	if outcome, _ := state.next("token-b"); outcome != cloudflareRetrySameAccount {
		t.Fatalf("token-b first outcome = %v, want its own same-account retry", outcome)
	}
}

// 换号次数必须有上限：否则账号池里全被拦时会无限换下去。
func TestCloudflareRetryGivesUpAfterSwitchBudget(t *testing.T) {
	state := newCloudflareRetryState()
	// 每个账号各消耗一次同号重试，然后进入换号。
	for i := 0; i < maxCloudflareSwitchAttempts; i++ {
		token := "token-" + string(rune('a'+i))
		state.next(token)
		outcome, exhausted := state.next(token)
		if outcome != cloudflareRetrySwitchAccount {
			t.Fatalf("switch #%d outcome = %v, want account switch", i+1, outcome)
		}
		if !exhausted {
			t.Fatalf("switch #%d must exclude its account", i+1)
		}
	}
	// 预算耗尽后即使换到全新账号也不得再重试。
	fresh := "token-fresh"
	state.next(fresh)
	if outcome, _ := state.next(fresh); outcome != cloudflareRetryGiveUp {
		t.Fatalf("outcome after budget = %v, want give-up so the challenge error is reported", outcome)
	}
}

// 预算耗尽时不能把账号写进排除集：此时已不再换号，排除集没有意义，
// 写进去只会让后续诊断误以为该账号被挑战拦过。
func TestCloudflareRetryGiveUpDoesNotExclude(t *testing.T) {
	state := newCloudflareRetryState()
	state.switchAttempts = maxCloudflareSwitchAttempts
	state.next("token-a")
	outcome, exhausted := state.next("token-a")
	if outcome != cloudflareRetryGiveUp {
		t.Fatalf("outcome = %v, want give-up", outcome)
	}
	if exhausted {
		t.Fatal("give-up must not exclude the account")
	}
}

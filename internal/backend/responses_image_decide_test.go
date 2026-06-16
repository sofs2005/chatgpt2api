package backend

import "testing"

func TestDecideImagePollAction(t *testing.T) {
	cases := []struct {
		name           string
		hasIDs         bool
		checkBeforeHit bool
		settleEnabled  bool
		haveLastHit    bool
		hitMatchesLast bool
		want           imagePollAction
	}{
		{
			name:           "no ids keeps polling",
			hasIDs:         false,
			checkBeforeHit: true,
			settleEnabled:  true,
			want:           imagePollContinue,
		},
		{
			name:           "check disabled returns on first discovery",
			hasIDs:         true,
			checkBeforeHit: false,
			settleEnabled:  true,
			want:           imagePollReturn,
		},
		{
			name:           "first confirmed discovery with settle waits to re-confirm",
			hasIDs:         true,
			checkBeforeHit: true,
			settleEnabled:  true,
			haveLastHit:    false,
			want:           imagePollSettle,
		},
		{
			name:           "second confirmation with matching key returns",
			hasIDs:         true,
			checkBeforeHit: true,
			settleEnabled:  true,
			haveLastHit:    true,
			hitMatchesLast: true,
			want:           imagePollReturn,
		},
		{
			name:           "changed key with settle keeps settling",
			hasIDs:         true,
			checkBeforeHit: true,
			settleEnabled:  true,
			haveLastHit:    true,
			hitMatchesLast: false,
			want:           imagePollSettle,
		},
		{
			name:           "settle disabled returns on confirmed discovery",
			hasIDs:         true,
			checkBeforeHit: true,
			settleEnabled:  false,
			haveLastHit:    false,
			want:           imagePollReturn,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideImagePollAction(tc.hasIDs, tc.checkBeforeHit, tc.settleEnabled, tc.haveLastHit, tc.hitMatchesLast)
			if got != tc.want {
				t.Fatalf("decideImagePollAction() = %v, want %v", got, tc.want)
			}
		})
	}
}

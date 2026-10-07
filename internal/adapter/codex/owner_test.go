package codex

import (
	"encoding/json"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
)

// ownerClaims are the claims of a fixture JWT (workspaceJWT and ownerLogin
// build theirs from it); "" leaves a claim out.
type ownerClaims struct {
	workspace, user, legacyUser, email, profileEmail string
}

func (c ownerClaims) jwt() string {
	claims := map[string]any{}
	if c.email != "" {
		claims["email"] = c.email
	}
	if c.profileEmail != "" {
		claims["https://api.openai.com/profile"] = map[string]any{"email": c.profileEmail}
	}
	auth := map[string]any{}
	for k, v := range map[string]string{
		"chatgpt_account_id": c.workspace, "chatgpt_user_id": c.user, "user_id": c.legacyUser,
	} {
		if v != "" {
			auth[k] = v
		}
	}
	if len(auth) > 0 {
		claims["https://api.openai.com/auth"] = auth
	}
	body, _ := json.Marshal(claims)
	return makeJWT(string(body))
}

// ownerLogin renders a ChatGPT login whose id_token carries c, whose access token
// names the same workspace and whose tokens.account_id is accountID.
func ownerLogin(mode string, c ownerClaims, accountID string) []byte {
	return chatgptLoginJSON(mode, c.jwt(), ownerClaims{workspace: c.workspace}.jwt(), accountID)
}

// CompareOwner's verdict table (docs/ADAPTERS.md § Recapture attribution).
// Fixture ids and emails; not real accounts.
func TestCompareOwnerVerdicts(t *testing.T) {
	const (
		ws, wsOther       = "ws-main", "ws-other"
		user, userOther   = "user-main", "user-side"
		mail, mailOther   = "you@example.com", "other@example.com"
		mailRecased       = "You@Example.com"
		mailChanged       = "renamed@example.com"
		chatgpt, noMode   = "chatgpt", ""
		apiKeyLoginRecord = `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-x"}`
	)
	same := ownerClaims{workspace: ws, user: user, email: mail}
	login := func(c ownerClaims) []byte { return ownerLogin(chatgpt, c, c.workspace) }
	for name, tc := range map[string]struct {
		recorded, live []byte
		want           adapter.OwnerVerdict
	}{
		"same owner, tokens rotated": {login(same), ownerLogin(chatgpt, same, ws), adapter.OwnerSame},
		"auth_mode absent on one side": {
			login(same), ownerLogin(noMode, same, ws), adapter.OwnerSame,
		},
		"different user, same workspace": {
			login(same), login(ownerClaims{workspace: ws, user: userOther, email: mail}), adapter.OwnerDifferent,
		},
		"same user, different workspace": {
			login(same), login(ownerClaims{workspace: wsOther, user: user, email: mail}), adapter.OwnerDifferent,
		},
		"same user and workspace, email changed": {
			login(same), login(ownerClaims{workspace: ws, user: user, email: mailChanged}), adapter.OwnerSame,
		},
		"legacy user_id stands in for chatgpt_user_id": {
			login(same), login(ownerClaims{workspace: ws, legacyUser: userOther, email: mail}), adapter.OwnerDifferent,
		},
		"account_id decides when the id_token has no workspace claim": {
			login(same), ownerLogin(chatgpt, ownerClaims{user: user, email: mail}, wsOther), adapter.OwnerDifferent,
		},
		"workspace from the id_token when account_id is empty": {
			login(same), ownerLogin(chatgpt, ownerClaims{workspace: wsOther, user: user}, ""), adapter.OwnerDifferent,
		},
		"user id missing on one side, emails differ": {
			login(same), login(ownerClaims{workspace: ws, email: mailOther}), adapter.OwnerDifferent,
		},
		"user id missing, profile email differs": {
			login(same), login(ownerClaims{workspace: ws, profileEmail: mailOther}), adapter.OwnerDifferent,
		},
		"user id missing, emails equal is no evidence": {
			login(same), login(ownerClaims{workspace: ws, email: mail}), adapter.OwnerUnknown,
		},
		"user id missing, email recased": {
			login(same), login(ownerClaims{workspace: ws, email: mailRecased}), adapter.OwnerUnknown,
		},
		"user ids and emails missing on both sides": {
			login(ownerClaims{workspace: ws}), login(ownerClaims{workspace: ws}), adapter.OwnerUnknown,
		},
		"nothing readable on either side": {
			login(ownerClaims{}), login(ownerClaims{}), adapter.OwnerUnknown,
		},
		"api key login live":     {login(same), []byte(apiKeyLoginRecord), adapter.OwnerUnknown},
		"api key login recorded": {[]byte(apiKeyLoginRecord), login(same), adapter.OwnerUnknown},
		"external token mode": {
			login(same), ownerLogin("chatgptAuthTokens", ownerClaims{workspace: wsOther, user: userOther}, wsOther),
			adapter.OwnerUnknown,
		},
		"a mixed live login has no single owner": {
			login(same), ownerLogin(chatgpt, ownerClaims{workspace: wsOther, user: userOther}, ws), adapter.OwnerUnknown,
		},
		"not JSON": {login(same), []byte("not json"), adapter.OwnerUnknown},
		"a claim of the wrong type voids the id_token's claims": {
			login(same),
			chatgptLoginJSON(chatgpt,
				makeJWT(`{"email":7,"https://api.openai.com/auth":{"chatgpt_user_id":"`+userOther+`"}}`),
				ownerClaims{workspace: ws}.jwt(), ws),
			adapter.OwnerUnknown,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := (Codex{}).CompareOwner(tc.recorded, tc.live); got != tc.want {
				t.Fatalf("CompareOwner = %d, want %d", got, tc.want)
			}
		})
	}
}

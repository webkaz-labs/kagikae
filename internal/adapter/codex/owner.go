package codex

import (
	"encoding/json"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/jwt"
)

// loginOwner is what one ChatGPT login says about whose it is; "" is a value
// the payload does not carry readably. It never leaves this package.
type loginOwner struct {
	workspace string
	user      string
	email     string
}

// CompareOwner compares the owner of two codex logins (docs/ADAPTERS.md
// § Recapture attribution): the workspace, then the user, and the email only
// when a user id is unreadable on either side. It decides only when both are
// ChatGPT logins that each name one account throughout (CredentialConflict is
// not Detected); a value counts only when both sides carry it. A different
// workspace is a different account even for the same user.
func (c Codex) CompareOwner(recorded, live []byte) adapter.OwnerVerdict {
	a, ok := c.ownerOf(recorded)
	if !ok {
		return adapter.OwnerUnknown
	}
	b, ok := c.ownerOf(live)
	if !ok {
		return adapter.OwnerUnknown
	}
	if a.workspace != "" && b.workspace != "" && a.workspace != b.workspace {
		return adapter.OwnerDifferent
	}
	if a.user != "" && b.user != "" {
		if a.user != b.user {
			return adapter.OwnerDifferent
		}
		return adapter.OwnerSame
	}
	// Email only stands in for a missing user id, and only as evidence against:
	// an address can be changed, so equal ones prove nothing. Case is ignored so
	// that a recased address is not mistaken for another account.
	if a.email != "" && b.email != "" && !strings.EqualFold(a.email, b.email) {
		return adapter.OwnerDifferent
	}
	return adapter.OwnerUnknown
}

// ownerOf reads a ChatGPT login's owner. ok is false for a payload that is not
// one (any auth_mode but "chatgpt" or none, as CredentialConflict decides) and
// for one that mixes two accounts, which has no single owner to compare; the
// conflict verdict handles that one.
func (c Codex) ownerOf(payload []byte) (loginOwner, bool) {
	var doc struct {
		AuthMode *string `json:"auth_mode"`
		Tokens   *struct {
			IDToken   string `json:"id_token"`
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil || doc.Tokens == nil {
		return loginOwner{}, false
	}
	if doc.AuthMode != nil && *doc.AuthMode != authModeChatGPT {
		return loginOwner{}, false
	}
	if c.CredentialConflict(payload) == adapter.ConflictDetected {
		return loginOwner{}, false
	}
	owner := idTokenOwner(doc.Tokens.IDToken)
	if doc.Tokens.AccountID != "" {
		owner.workspace = doc.Tokens.AccountID
	}
	return owner, true
}

// idTokenOwner reads the id_token's claims: the workspace and user from the
// "https://api.openai.com/auth" object (chatgpt_user_id, else user_id), and the
// top-level email, else the "https://api.openai.com/profile" one. A token that
// does not decode, or claims of the wrong type, carry nothing.
func idTokenOwner(token string) loginOwner {
	claims, ok := jwt.Payload(token)
	if !ok {
		return loginOwner{}
	}
	var doc struct {
		Email string `json:"email"`
		Auth  *struct {
			AccountID     string `json:"chatgpt_account_id"`
			ChatGPTUserID string `json:"chatgpt_user_id"`
			UserID        string `json:"user_id"`
		} `json:"https://api.openai.com/auth"`
		Profile *struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
	}
	if json.Unmarshal(claims, &doc) != nil {
		return loginOwner{}
	}
	owner := loginOwner{email: doc.Email}
	if owner.email == "" && doc.Profile != nil {
		owner.email = doc.Profile.Email
	}
	if doc.Auth != nil {
		owner.workspace = doc.Auth.AccountID
		owner.user = doc.Auth.ChatGPTUserID
		if owner.user == "" {
			owner.user = doc.Auth.UserID
		}
	}
	return owner
}

var _ adapter.OwnerComparer = Codex{}

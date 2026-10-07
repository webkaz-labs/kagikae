package codex

import (
	"cmp"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
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
func (Codex) CompareOwner(recorded, live []byte) adapter.OwnerVerdict {
	a, ok := ownerOf(recorded)
	if !ok {
		return adapter.OwnerUnknown
	}
	b, ok := ownerOf(live)
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
// one (chatGPTLogin) and for one that mixes two accounts, which has no single
// owner to compare; the conflict verdict handles that one.
func ownerOf(payload []byte) (loginOwner, bool) {
	tokens, ok := chatGPTLogin(payload)
	if !ok || tokens.conflict() == adapter.ConflictDetected {
		return loginOwner{}, false
	}
	owner := idTokenOwner(tokens.IDToken)
	owner.workspace = cmp.Or(tokens.AccountID, owner.workspace)
	return owner, true
}

// idTokenOwner reads the id_token's claims: the workspace and user from the
// "https://api.openai.com/auth" object (chatgpt_user_id, else user_id), and the
// top-level email, else the "https://api.openai.com/profile" one. A token that
// does not decode, or claims of the wrong type, carry nothing.
func idTokenOwner(token string) loginOwner {
	var doc struct {
		Email string `json:"email"`
		Auth  struct {
			AccountID     string `json:"chatgpt_account_id"`
			ChatGPTUserID string `json:"chatgpt_user_id"`
			UserID        string `json:"user_id"`
		} `json:"https://api.openai.com/auth"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
	}
	if !jwtClaims(token, &doc) {
		return loginOwner{}
	}
	return loginOwner{
		workspace: doc.Auth.AccountID,
		user:      cmp.Or(doc.Auth.ChatGPTUserID, doc.Auth.UserID),
		email:     cmp.Or(doc.Email, doc.Profile.Email),
	}
}

var _ adapter.OwnerComparer = Codex{}

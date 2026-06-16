package jwt

import (
	"encoding/base64"
	"testing"
)

func TestPayload(t *testing.T) {
	claims := `{"email":"bob@example.com","exp":1700000000}`
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	token := seg(`{"alg":"none"}`) + "." + seg(claims) + "." + "sig"

	got, ok := Payload(token)
	if !ok || string(got) != claims {
		t.Fatalf("Payload = %q ok=%v; want %q", got, ok, claims)
	}
}

func TestPayloadPaddedEncoding(t *testing.T) {
	claims := `{"x":"y"}`
	// StdEncoding-with-padding URL variant must still decode.
	token := "h." + base64.URLEncoding.EncodeToString([]byte(claims)) + ".s"
	got, ok := Payload(token)
	if !ok || string(got) != claims {
		t.Fatalf("Payload(padded) = %q ok=%v; want %q", got, ok, claims)
	}
}

func TestPayloadRejectsMalformed(t *testing.T) {
	for _, token := range []string{"", "onlyone", "two.parts", "a.b.c.d", "h.!!!notbase64!!!.s"} {
		if _, ok := Payload(token); ok {
			t.Errorf("Payload(%q) ok=true, want false", token)
		}
	}
}

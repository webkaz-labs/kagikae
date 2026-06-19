package freshness

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// makeJWT builds a minimal unsigned-looking JWT whose payload carries exp.
func makeJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, exp))
	return header + "." + payload + ".sig"
}

func TestJWTExpiry(t *testing.T) {
	exp := time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC)
	got, ok := JWTExpiry(makeJWT(exp.Unix()))
	if !ok || !got.Equal(exp) {
		t.Fatalf("JWTExpiry = %v, %v (want %v)", got, ok, exp)
	}
	if _, ok := JWTExpiry("not-a-jwt"); ok {
		t.Fatalf("non-JWT should not parse")
	}
	if _, ok := JWTExpiry(makeJWT(0)); ok {
		t.Fatalf("exp=0 should be treated as no expiry")
	}
}

func TestEpochToTime(t *testing.T) {
	secs := time.Date(2028, 9, 9, 9, 9, 9, 0, time.UTC)
	if got := EpochToTime(float64(secs.Unix())); !got.Equal(secs) {
		t.Fatalf("seconds: %v != %v", got, secs)
	}
	ms := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := EpochToTime(float64(ms.UnixMilli())); !got.Equal(ms) {
		t.Fatalf("millis: %v != %v", got, ms)
	}
	if got := EpochToTime(0); !got.IsZero() {
		t.Fatalf("zero should map to zero time, got %v", got)
	}
}

func TestDecodeObjectAndScalars(t *testing.T) {
	obj, ok := DecodeObject([]byte(`{"expiresAt":1000000000000,"refreshToken":"r","empty":""}`))
	if !ok {
		t.Fatal("DecodeObject failed on a JSON object")
	}
	if n := NumberFrom(obj["expiresAt"]); n != 1000000000000 {
		t.Fatalf("NumberFrom = %v", n)
	}
	if !NonEmptyString(obj["refreshToken"]) {
		t.Fatal("refreshToken should be a non-empty string")
	}
	if NonEmptyString(obj["empty"]) {
		t.Fatal("empty string should be NonEmptyString=false")
	}
	if NonEmptyString(obj["missing"]) {
		t.Fatal("missing key should be NonEmptyString=false")
	}
	if NumberFrom(json.RawMessage(`"not-a-number"`)) != 0 {
		t.Fatal("non-number should be 0")
	}
	if _, ok := DecodeObject([]byte("not json")); ok {
		t.Fatal("non-JSON should not decode")
	}
}

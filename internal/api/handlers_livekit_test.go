package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestSignLiveKitToken(t *testing.T) {
	tok, err := signLiveKitToken("APIabc", "secret123", "phone-1", "jarvis-room", 3600_000_000_000)
	if err != nil {
		t.Fatalf("signLiveKitToken: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
	// Decode header
	h, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil { t.Fatalf("header decode: %v", err) }
	var hdr map[string]string
	_ = json.Unmarshal(h, &hdr)
	if hdr["alg"] != "HS256" || hdr["typ"] != "JWT" {
		t.Fatalf("bad header: %+v", hdr)
	}
	// Decode claims
	c, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil { t.Fatalf("claims decode: %v", err) }
	var cl map[string]any
	_ = json.Unmarshal(c, &cl)
	if cl["iss"] != "APIabc" || cl["sub"] != "phone-1" || cl["name"] != "phone-1" {
		t.Fatalf("bad claims: %+v", cl)
	}
	video, ok := cl["video"].(map[string]any)
	if !ok {
		t.Fatalf("video claim missing or wrong type: %T", cl["video"])
	}
	if video["room"] != "jarvis-room" || video["roomJoin"] != true || video["canPublish"] != true {
		t.Fatalf("bad video grants: %+v", video)
	}
	t.Logf("token OK, length=%d, claims=%+v", len(tok), cl)
}

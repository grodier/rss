package auth

import "testing"

func TestGenerateToken_Unique(t *testing.T) {
	raw1, _, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	raw2, _, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	if raw1 == raw2 {
		t.Fatal("two generated tokens should be unique")
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	raw := "test-token-value"
	hash1 := HashToken(raw)
	hash2 := HashToken(raw)

	if hash1 != hash2 {
		t.Fatalf("HashToken() not deterministic: %s != %s", hash1, hash2)
	}
}

func TestGenerateToken_RoundTrip(t *testing.T) {
	raw, hash, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	if raw == "" {
		t.Fatal("raw token should not be empty")
	}
	if hash == "" {
		t.Fatal("hash should not be empty")
	}

	if HashToken(raw) != hash {
		t.Fatal("HashToken(raw) should equal the hash returned by GenerateToken")
	}
}

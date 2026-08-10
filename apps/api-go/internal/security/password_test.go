package security

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("a-long-test-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	valid, err := VerifyPassword("a-long-test-password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !valid {
		t.Fatal("VerifyPassword() = false, want true")
	}
	invalid, err := VerifyPassword("different-password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword(wrong) error = %v", err)
	}
	if invalid {
		t.Fatal("VerifyPassword(wrong) = true, want false")
	}
}

func TestVerifyPasswordAcceptsLegacyNodeArgon2Hash(t *testing.T) {
	const legacyHash = "$argon2id$v=19$m=65536,p=4,t=3$tRJklxOdZO9aABYGMhSuPQ$efdf+S6xCWV0Qd61ZJlhMSYxZUR4weZFzfXrlb2FE9A"

	valid, err := VerifyPassword("nest-go-compatibility-password", legacyHash)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !valid {
		t.Fatal("VerifyPassword() rejected a valid hash produced by the legacy Node argon2 package")
	}
}

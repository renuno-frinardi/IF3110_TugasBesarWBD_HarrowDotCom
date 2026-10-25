package store

import (
	"strings"
	"testing"
)

func TestDecryptSecret(t *testing.T) {
	key := strings.Repeat("01", 32)
	for _, secret := range []string{"", "PasswordTes123!"} {
		encrypted, err := EncryptSecret(secret, key)
		if err != nil {
			t.Fatal("failed to prepare the encrypted password")
		}
		decrypted, err := DecryptSecret(encrypted, key)
		if err != nil || decrypted != secret {
			t.Fatal("decrypted password does not match the original password")
		}
	}

	encrypted, err := EncryptSecret("PasswordTes123!", key)
	if err != nil {
		t.Fatal("failed to prepare the encrypted password")
	}

	t.Run("key_salah", func(t *testing.T) {
		wrongKey := strings.Repeat("02", 32)
		decrypted, err := DecryptSecret(encrypted, wrongKey)
		if err == nil {
			t.Fatal("the wrong key must be rejected")
		}
		if decrypted != "" {
			t.Fatal("password must be empty when decryption fails")
		}
	})

	t.Run("key_bukan_hex", func(t *testing.T) {
		invalidKey := strings.Repeat("zz", 32)
		decrypted, err := DecryptSecret(encrypted, invalidKey)
		if err == nil {
			t.Fatal("invalid key format must be rejected")
		}
		if decrypted != "" {
			t.Fatal("password must be empty when decryption fails")
		}
	})

	t.Run("key_terlalu_pendek", func(t *testing.T) {
		decrypted, err := DecryptSecret(encrypted, "01")
		if err == nil {
			t.Fatal("a key that is too short must be rejected")
		}
		if decrypted != "" {
			t.Fatal("password must be empty when decryption fails")
		}
	})

	t.Run("ciphertext_rusak", func(t *testing.T) {
		damaged := make([]byte, len(encrypted))
		copy(damaged, encrypted)

		// Ubah byte terakhir supaya data berbeda dari hasil enkripsi awal.
		lastIndex := len(damaged) - 1
		if damaged[lastIndex] == 0 {
			damaged[lastIndex] = 1
		} else {
			damaged[lastIndex] = 0
		}

		decrypted, err := DecryptSecret(damaged, key)
		if err == nil {
			t.Fatal("corrupted password data must be rejected")
		}
		if decrypted != "" {
			t.Fatal("password must be empty when decryption fails")
		}
	})

	t.Run("ciphertext_terpotong", func(t *testing.T) {
		truncated := encrypted[:len(encrypted)-1]
		decrypted, err := DecryptSecret(truncated, key)
		if err == nil {
			t.Fatal("truncated password data must be rejected")
		}
		if decrypted != "" {
			t.Fatal("password must be empty when decryption fails")
		}
	})

	t.Run("ciphertext_kosong", func(t *testing.T) {
		decrypted, err := DecryptSecret(nil, key)
		if err == nil {
			t.Fatal("empty password data must be rejected")
		}
		if decrypted != "" {
			t.Fatal("password must be empty when decryption fails")
		}
	})

	t.Run("password_dummy", func(t *testing.T) {
		decrypted, err := DecryptSecret([]byte("dummy"), key)
		if err == nil {
			t.Fatal("unencrypted password data must be rejected")
		}
		if decrypted != "" {
			t.Fatal("password must be empty when decryption fails")
		}
	})
}

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
			t.Fatal("gagal menyiapkan password terenkripsi")
		}
		decrypted, err := DecryptSecret(encrypted, key)
		if err != nil || decrypted != secret {
			t.Fatal("password yang dibuka tidak sama dengan password awal")
		}
	}

	encrypted, err := EncryptSecret("PasswordTes123!", key)
	if err != nil {
		t.Fatal("gagal menyiapkan password terenkripsi")
	}

	t.Run("key_salah", func(t *testing.T) {
		wrongKey := strings.Repeat("02", 32)
		decrypted, err := DecryptSecret(encrypted, wrongKey)
		if err == nil {
			t.Fatal("kunci yang salah harus ditolak")
		}
		if decrypted != "" {
			t.Fatal("password harus kosong jika dekripsi gagal")
		}
	})

	t.Run("key_bukan_hex", func(t *testing.T) {
		invalidKey := strings.Repeat("zz", 32)
		decrypted, err := DecryptSecret(encrypted, invalidKey)
		if err == nil {
			t.Fatal("format kunci yang salah harus ditolak")
		}
		if decrypted != "" {
			t.Fatal("password harus kosong jika dekripsi gagal")
		}
	})

	t.Run("key_terlalu_pendek", func(t *testing.T) {
		decrypted, err := DecryptSecret(encrypted, "01")
		if err == nil {
			t.Fatal("kunci yang terlalu pendek harus ditolak")
		}
		if decrypted != "" {
			t.Fatal("password harus kosong jika dekripsi gagal")
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
			t.Fatal("data password yang rusak harus ditolak")
		}
		if decrypted != "" {
			t.Fatal("password harus kosong jika dekripsi gagal")
		}
	})

	t.Run("ciphertext_terpotong", func(t *testing.T) {
		truncated := encrypted[:len(encrypted)-1]
		decrypted, err := DecryptSecret(truncated, key)
		if err == nil {
			t.Fatal("data password yang terpotong harus ditolak")
		}
		if decrypted != "" {
			t.Fatal("password harus kosong jika dekripsi gagal")
		}
	})

	t.Run("ciphertext_kosong", func(t *testing.T) {
		decrypted, err := DecryptSecret(nil, key)
		if err == nil {
			t.Fatal("data password yang kosong harus ditolak")
		}
		if decrypted != "" {
			t.Fatal("password harus kosong jika dekripsi gagal")
		}
	})

	t.Run("password_dummy", func(t *testing.T) {
		decrypted, err := DecryptSecret([]byte("dummy"), key)
		if err == nil {
			t.Fatal("password yang belum dienkripsi harus ditolak")
		}
		if decrypted != "" {
			t.Fatal("password harus kosong jika dekripsi gagal")
		}
	})
}

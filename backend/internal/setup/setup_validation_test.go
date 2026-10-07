package setup

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin/binding"
)

func TestValidateEmailRequiresLoginCompatibleAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		email string
		valid bool
	}{
		{name: "normal address", email: "owner@example.com", valid: true},
		{name: "local domain", email: "admin-0123456789ab@sub2api.local", valid: true},
		{name: "plus address", email: "owner+admin@example.com", valid: true},
		{name: "empty"},
		{name: "missing domain", email: "owner"},
		{name: "single label domain", email: "a@b"},
		{name: "display name", email: "Owner <owner@example.com>"},
		{name: "angle brackets", email: "<owner@example.com>"},
		{name: "surrounding whitespace", email: " owner@example.com "},
		{name: "too long", email: strings.Repeat("a", 243) + "@example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateEmail(tc.email); got != tc.valid {
				t.Errorf("validateEmail(%q) = %v, want %v", tc.email, got, tc.valid)
			}
			if tc.valid {
				login := struct {
					Email string `binding:"required,email"`
				}{Email: tc.email}
				if err := binding.Validator.ValidateStruct(&login); err != nil {
					t.Errorf("setup accepted an email that login rejects: %v", err)
				}
			}
		})
	}
}

func TestValidatePasswordUsesBcryptByteLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
		valid    bool
	}{
		{name: "empty"},
		{name: "too short", password: "1234567"},
		{name: "minimum", password: "12345678", valid: true},
		{name: "72 ASCII bytes", password: strings.Repeat("a", 72), valid: true},
		{name: "73 ASCII bytes", password: strings.Repeat("a", 73)},
		{name: "72 Unicode bytes", password: strings.Repeat("\u4e2d", 24), valid: true},
		{name: "73 Unicode bytes", password: strings.Repeat("\u4e2d", 24) + "a"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePassword(tc.password)
			if (err == nil) != tc.valid {
				t.Errorf("validatePassword(%d bytes) error = %v, want valid %v", len(tc.password), err, tc.valid)
			}
		})
	}
}

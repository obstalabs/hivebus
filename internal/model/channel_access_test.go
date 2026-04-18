package model

import (
	"testing"
	"time"
)

func TestChannelValidateRequiresAllowRuleWhenRestricted(t *testing.T) {
	t.Helper()

	channel := Channel{
		ChannelID:   "security-private",
		DisplayName: "Security Private",
		Restricted:  true,
		CreatedAt:   time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC),
	}

	if err := channel.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want restricted allow-rule validation")
	}
}

func TestChannelValidateRejectsDuplicateRoles(t *testing.T) {
	t.Helper()

	channel := Channel{
		ChannelID:   "security-private",
		DisplayName: "Security Private",
		Restricted:  true,
		AllowedRoles: []string{
			"security",
			"security",
		},
		CreatedAt: time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC),
	}

	if err := channel.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate role validation")
	}
}

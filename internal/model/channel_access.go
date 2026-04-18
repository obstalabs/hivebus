package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Channel struct {
	ChannelID           string    `json:"channel_id"`
	DisplayName         string    `json:"display_name"`
	Description         string    `json:"description,omitempty"`
	Restricted          bool      `json:"restricted"`
	AllowedParticipants []string  `json:"allowed_participants,omitempty"`
	AllowedAgents       []string  `json:"allowed_agents,omitempty"`
	AllowedRoles        []string  `json:"allowed_roles,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (c Channel) Validate() error {
	switch {
	case strings.TrimSpace(c.ChannelID) == "":
		return errors.New("channel_id is required")
	case strings.TrimSpace(c.DisplayName) == "":
		return errors.New("display_name is required")
	case c.CreatedAt.IsZero():
		return errors.New("created_at is required")
	case c.UpdatedAt.IsZero():
		return errors.New("updated_at is required")
	case c.UpdatedAt.Before(c.CreatedAt):
		return errors.New("updated_at must not be before created_at")
	}

	if err := validateUniqueNonEmpty("allowed participant", c.AllowedParticipants); err != nil {
		return err
	}
	if err := validateUniqueNonEmpty("allowed agent", c.AllowedAgents); err != nil {
		return err
	}
	if err := validateUniqueNonEmpty("allowed role", c.AllowedRoles); err != nil {
		return err
	}
	if c.Restricted &&
		len(c.AllowedParticipants) == 0 &&
		len(c.AllowedAgents) == 0 &&
		len(c.AllowedRoles) == 0 {
		return errors.New("restricted channel requires at least one allow rule")
	}

	return nil
}

func validateUniqueNonEmpty(label string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%ss contains an empty value", label)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

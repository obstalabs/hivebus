package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ParticipantType is the top-level actor class inside a thread.
type ParticipantType string

const (
	ParticipantTypeHuman   ParticipantType = "human"
	ParticipantTypeAgent   ParticipantType = "agent"
	ParticipantTypeService ParticipantType = "service"
)

var validParticipantTypes = []ParticipantType{
	ParticipantTypeHuman,
	ParticipantTypeAgent,
	ParticipantTypeService,
}

// ParticipantKind is an optional role hint kept alongside participant_type.
type ParticipantKind string

const (
	ParticipantCollector ParticipantKind = "collector"
	ParticipantAgent     ParticipantKind = "agent"
	ParticipantHuman     ParticipantKind = "human"
	ParticipantService   ParticipantKind = "service"
)

// ParticipantVisibility controls whether the actor is visible in the thread.
type ParticipantVisibility string

const (
	ParticipantVisibilityThread   ParticipantVisibility = "thread"
	ParticipantVisibilityInternal ParticipantVisibility = "internal"
)

var validParticipantVisibilities = []ParticipantVisibility{
	ParticipantVisibilityThread,
	ParticipantVisibilityInternal,
}

type HumanDeliveryPreference string

const (
	HumanDeliveryInThread    HumanDeliveryPreference = "in_thread"
	HumanDeliveryTriageQueue HumanDeliveryPreference = "triage_queue"
)

var validHumanDeliveryPreferences = []HumanDeliveryPreference{
	HumanDeliveryInThread,
	HumanDeliveryTriageQueue,
}

type HumanParticipant struct {
	HumanID            string                  `json:"human_id"`
	Role               string                  `json:"role,omitempty"`
	DeliveryPreference HumanDeliveryPreference `json:"delivery_preference,omitempty"`
}

func (p HumanParticipant) Validate() error {
	if strings.TrimSpace(p.HumanID) == "" {
		return errors.New("human.human_id is required")
	}
	if p.DeliveryPreference != "" &&
		!slices.Contains(validHumanDeliveryPreferences, p.DeliveryPreference) {
		return fmt.Errorf("unsupported human delivery_preference %q", p.DeliveryPreference)
	}

	return nil
}

type AgentParticipant struct {
	AgentID        string `json:"agent_id"`
	InstallationID string `json:"installation_id,omitempty"`
}

func (p AgentParticipant) Validate() error {
	if strings.TrimSpace(p.AgentID) == "" {
		return errors.New("agent.agent_id is required")
	}

	return nil
}

type ServiceParticipant struct {
	ServiceName string `json:"service_name"`
}

func (p ServiceParticipant) Validate() error {
	if strings.TrimSpace(p.ServiceName) == "" {
		return errors.New("service.service_name is required")
	}

	return nil
}

// Participant is any actor with a stable identity inside a thread.
type Participant struct {
	ID           string                `json:"id"`
	Type         ParticipantType       `json:"participant_type,omitempty"`
	Kind         ParticipantKind       `json:"kind,omitempty"`
	DisplayName  string                `json:"display_name,omitempty"`
	Visibility   ParticipantVisibility `json:"visibility,omitempty"`
	Capabilities []string              `json:"capabilities,omitempty"`
	Human        *HumanParticipant     `json:"human,omitempty"`
	Agent        *AgentParticipant     `json:"agent,omitempty"`
	Service      *ServiceParticipant   `json:"service,omitempty"`
}

func (p Participant) Validate() error {
	switch {
	case strings.TrimSpace(p.ID) == "":
		return errors.New("participant id is required")
	}

	participantType, err := resolveParticipantType(p.Type, p.Kind)
	if err != nil {
		return err
	}

	visibility := p.Visibility
	if visibility == "" {
		visibility = ParticipantVisibilityThread
	}
	if !slices.Contains(validParticipantVisibilities, visibility) {
		return fmt.Errorf("unsupported visibility %q", visibility)
	}

	if err := validateParticipantCapabilities(p.Capabilities); err != nil {
		return err
	}

	if err := validateParticipantKindForType(p.Kind, participantType); err != nil {
		return err
	}

	switch participantType {
	case ParticipantTypeHuman:
		if visibility != ParticipantVisibilityThread {
			return errors.New("human participants must be thread-visible")
		}
		if p.Human == nil {
			return errors.New("human participant requires human identity")
		}
		if p.Agent != nil || p.Service != nil {
			return errors.New("human participant must not set agent or service identity")
		}
		if err := p.Human.Validate(); err != nil {
			return err
		}
	case ParticipantTypeAgent:
		if p.Agent == nil {
			return errors.New("agent participant requires agent identity")
		}
		if p.Human != nil || p.Service != nil {
			return errors.New("agent participant must not set human or service identity")
		}
		if err := p.Agent.Validate(); err != nil {
			return err
		}
	case ParticipantTypeService:
		if p.Service == nil {
			return errors.New("service participant requires service identity")
		}
		if p.Human != nil || p.Agent != nil {
			return errors.New("service participant must not set human or agent identity")
		}
		if err := p.Service.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported participant_type %q", participantType)
	}

	return nil
}

func resolveParticipantType(participantType ParticipantType, kind ParticipantKind) (ParticipantType, error) {
	if participantType != "" {
		if !slices.Contains(validParticipantTypes, participantType) {
			return "", fmt.Errorf("unsupported participant_type %q", participantType)
		}
		return participantType, nil
	}

	switch kind {
	case ParticipantCollector, ParticipantService:
		return ParticipantTypeService, nil
	case ParticipantAgent:
		return ParticipantTypeAgent, nil
	case ParticipantHuman:
		return ParticipantTypeHuman, nil
	case "":
		return "", errors.New("participant_type is required")
	default:
		return "", fmt.Errorf("unsupported participant kind %q", kind)
	}
}

func validateParticipantKindForType(kind ParticipantKind, participantType ParticipantType) error {
	if kind == "" {
		return nil
	}

	switch participantType {
	case ParticipantTypeHuman:
		if kind != ParticipantHuman {
			return fmt.Errorf("participant kind %q does not match type %q", kind, participantType)
		}
	case ParticipantTypeAgent:
		if kind != ParticipantAgent {
			return fmt.Errorf("participant kind %q does not match type %q", kind, participantType)
		}
	case ParticipantTypeService:
		if kind != ParticipantCollector && kind != ParticipantService {
			return fmt.Errorf("participant kind %q does not match type %q", kind, participantType)
		}
	}

	return nil
}

func validateParticipantCapabilities(capabilities []string) error {
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			return errors.New("capabilities contains an empty capability")
		}
		if _, exists := seen[capability]; exists {
			return fmt.Errorf("duplicate capability %q", capability)
		}
		seen[capability] = struct{}{}
	}

	return nil
}

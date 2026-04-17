package model

import "testing"

func TestParticipantValidateAcceptsHumanAgentAndService(t *testing.T) {
	t.Helper()

	participants := []Participant{
		{
			ID:          "human.reporter",
			Type:        ParticipantTypeHuman,
			Kind:        ParticipantHuman,
			DisplayName: "Alex Reporter",
			Visibility:  ParticipantVisibilityThread,
			Human: &HumanParticipant{
				HumanID:            "usr_alex_reporter",
				Role:               "reporter",
				DeliveryPreference: HumanDeliveryInThread,
			},
		},
		{
			ID:          "agent.field.nullbot",
			Type:        ParticipantTypeAgent,
			Kind:        ParticipantAgent,
			DisplayName: "Field Nullbot",
			Visibility:  ParticipantVisibilityThread,
			Capabilities: []string{
				"clarification.reply",
				"evidence.collect",
			},
			Agent: &AgentParticipant{
				AgentID:        "nullbot-edge",
				InstallationID: "install_nullbot_edge_001",
			},
		},
		{
			ID:          "service.hivebus",
			Type:        ParticipantTypeService,
			Kind:        ParticipantService,
			DisplayName: "Hivebus Runtime",
			Visibility:  ParticipantVisibilityInternal,
			Capabilities: []string{
				"route.case",
			},
			Service: &ServiceParticipant{
				ServiceName: "hivebus",
			},
		},
	}

	for _, participant := range participants {
		if err := participant.Validate(); err != nil {
			t.Fatalf("Validate(%s) error = %v", participant.ID, err)
		}
	}
}

func TestParticipantValidateRejectsHumanWithoutIdentity(t *testing.T) {
	t.Helper()

	participant := Participant{
		ID:          "human.reporter",
		Type:        ParticipantTypeHuman,
		Kind:        ParticipantHuman,
		DisplayName: "Alex Reporter",
		Visibility:  ParticipantVisibilityThread,
	}

	if err := participant.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestParticipantValidateRejectsKindTypeMismatch(t *testing.T) {
	t.Helper()

	participant := Participant{
		ID:          "agent.field.nullbot",
		Type:        ParticipantTypeAgent,
		Kind:        ParticipantService,
		DisplayName: "Field Nullbot",
		Visibility:  ParticipantVisibilityThread,
		Agent: &AgentParticipant{
			AgentID: "nullbot-edge",
		},
	}

	if err := participant.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestParticipantValidateRejectsDuplicateCapabilities(t *testing.T) {
	t.Helper()

	participant := Participant{
		ID:          "service.hivebus",
		Type:        ParticipantTypeService,
		Kind:        ParticipantService,
		DisplayName: "Hivebus Runtime",
		Visibility:  ParticipantVisibilityInternal,
		Capabilities: []string{
			"route.case",
			"route.case",
		},
		Service: &ServiceParticipant{
			ServiceName: "hivebus",
		},
	}

	if err := participant.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

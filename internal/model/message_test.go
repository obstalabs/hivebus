package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEnvelopeValidateAcceptsSignedStructuredEnvelope(t *testing.T) {
	t.Helper()

	sentAt := time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC)
	deadline := sentAt.Add(2 * time.Hour)
	payload, err := json.Marshal(map[string]string{
		"issue": "postgres latency spike",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Capability:     "incident.diagnose",
		Payload:        payload,
		Deadline:       &deadline,
		SentAt:         sentAt,
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
			Verified:      true,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
			Signed: true,
		},
	}

	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEnvelopeValidateAcceptsRoutingFieldsRoundTrip(t *testing.T) {
	t.Helper()

	redirect := validRoutingEnvelope()
	redirect.ReplyPolicy = ReplyPolicyReplyToTarget
	redirect.ReplyTarget = "agent.audit"
	redirect.Security.Signed = true
	redirect.Security.Signature = "sig_redirect"

	collect := validRoutingEnvelope()
	collect.MessageID = "msg_collect_round_trip"
	collect.To = nil
	collect.Visibility = VisibilityOffband
	collect.Scope = ScopeBroadcast
	collect.Recipient = ""
	collect.ReplyPolicy = ReplyPolicyCollect
	collect.CollectionPolicy = CollectionPolicyAllUntilTimeout
	collect.IdempotencyKey = "idem_collect_round_trip"
	collect.Security.Signed = true
	collect.Security.Signature = "sig_collect"

	testCases := []struct {
		name     string
		envelope Envelope
	}{
		{name: "redirect", envelope: redirect},
		{name: "broadcast_collect", envelope: collect},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.envelope.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}

			encoded, err := json.Marshal(testCase.envelope)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			var decoded Envelope
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			if decoded.Visibility != testCase.envelope.Visibility {
				t.Fatalf("visibility round-trip = %q", decoded.Visibility)
			}
			if decoded.Scope != testCase.envelope.Scope {
				t.Fatalf("scope round-trip = %q", decoded.Scope)
			}
			if decoded.Recipient != testCase.envelope.Recipient {
				t.Fatalf("recipient round-trip = %q", decoded.Recipient)
			}
			if decoded.ReplyPolicy != testCase.envelope.ReplyPolicy {
				t.Fatalf("reply_policy round-trip = %q", decoded.ReplyPolicy)
			}
			if decoded.ReplyTarget != testCase.envelope.ReplyTarget {
				t.Fatalf("reply_target round-trip = %q", decoded.ReplyTarget)
			}
			if decoded.CollectionPolicy != testCase.envelope.CollectionPolicy {
				t.Fatalf("collection_policy round-trip = %q", decoded.CollectionPolicy)
			}
		})
	}
}

func TestEnvelopeValidateAcceptsLegacyEnvelopeWithoutRoutingFields(t *testing.T) {
	t.Helper()

	const legacyEnvelope = `{
		"message_id":"msg_legacy",
		"thread_id":"thr_legacy",
		"from":"agent.sender",
		"to":["agent.worker"],
		"type":"task.request",
		"payload":{"question":"which checkout is canonical?"},
		"sent_at":"2026-03-31T08:00:00Z",
		"idempotency_key":"idem_legacy",
		"trace":{"correlation_id":"corr_legacy","verified":true},
		"security":{"scheme":"ed25519","nonce":"nonce_legacy","signed":false}
	}`

	var envelope Envelope
	if err := json.Unmarshal([]byte(legacyEnvelope), &envelope); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate() legacy error = %v", err)
	}
	if envelope.Visibility != "" || envelope.Scope != "" || envelope.ReplyPolicy != "" {
		t.Fatalf("legacy routing defaults changed: %#v", envelope)
	}
}

func TestEnvelopeValidateRejectsIncoherentRouting(t *testing.T) {
	t.Helper()

	testCases := []struct {
		name   string
		mutate func(*Envelope)
		want   string
	}{
		{
			name: "unsupported visibility",
			mutate: func(envelope *Envelope) {
				envelope.Visibility = Visibility("ether")
			},
			want: "unsupported visibility",
		},
		{
			name: "unsupported scope",
			mutate: func(envelope *Envelope) {
				envelope.Scope = Scope("direct")
			},
			want: "unsupported scope",
		},
		{
			name: "unsupported reply policy",
			mutate: func(envelope *Envelope) {
				envelope.ReplyPolicy = ReplyPolicy("mirror")
			},
			want: "unsupported reply_policy",
		},
		{
			name: "unsupported collection policy",
			mutate: func(envelope *Envelope) {
				envelope.CollectionPolicy = CollectionPolicy("forever")
			},
			want: "unsupported collection_policy",
		},
		{
			name: "broadcast recipient",
			mutate: func(envelope *Envelope) {
				envelope.To = nil
				envelope.Scope = ScopeBroadcast
				envelope.Recipient = "agent.worker"
			},
			want: "recipient must be empty",
		},
		{
			name: "broadcast legacy to",
			mutate: func(envelope *Envelope) {
				envelope.Scope = ScopeBroadcast
				envelope.Recipient = ""
			},
			want: "to must be empty",
		},
		{
			name: "targeted missing recipient",
			mutate: func(envelope *Envelope) {
				envelope.Recipient = ""
			},
			want: "recipient is required",
		},
		{
			name: "targeted recipient mismatch",
			mutate: func(envelope *Envelope) {
				envelope.To = []string{"agent.other"}
			},
			want: "to recipient must match recipient",
		},
		{
			// WO-92: a padded recipient diverges from the routed (trimmed) value even
			// though the legacy "to" matches after trimming — reject the non-canonical form.
			name: "targeted recipient whitespace",
			mutate: func(envelope *Envelope) {
				envelope.Recipient = " agent.worker "
			},
			want: "recipient must not have leading or trailing whitespace",
		},
		{
			name: "targeted multiple legacy recipients",
			mutate: func(envelope *Envelope) {
				envelope.To = []string{"agent.worker", "agent.backup"}
			},
			want: "to must contain exactly one recipient",
		},
		{
			name: "reply target missing",
			mutate: func(envelope *Envelope) {
				envelope.ReplyPolicy = ReplyPolicyReplyToTarget
			},
			want: "reply_target is required",
		},
		{
			name: "reply target without redirect policy",
			mutate: func(envelope *Envelope) {
				envelope.ReplyTarget = "agent.audit"
			},
			want: "reply_target is only allowed",
		},
		{
			name: "collection none without reply policy",
			mutate: func(envelope *Envelope) {
				envelope.ReplyPolicy = ""
				envelope.CollectionPolicy = CollectionPolicyNone
			},
			want: "collection_policy is only allowed",
		},
		{
			name: "collection none with reply none",
			mutate: func(envelope *Envelope) {
				envelope.ReplyPolicy = ReplyPolicyNone
				envelope.CollectionPolicy = CollectionPolicyNone
			},
			want: "collection_policy is only allowed",
		},
		{
			name: "collection none with reply to sender",
			mutate: func(envelope *Envelope) {
				envelope.CollectionPolicy = CollectionPolicyNone
			},
			want: "collection_policy is only allowed",
		},
		{
			name: "collection none with reply to target",
			mutate: func(envelope *Envelope) {
				envelope.ReplyPolicy = ReplyPolicyReplyToTarget
				envelope.ReplyTarget = "agent.owner"
				envelope.CollectionPolicy = CollectionPolicyNone
			},
			want: "collection_policy is only allowed",
		},
		{
			name: "collection policy without collect",
			mutate: func(envelope *Envelope) {
				envelope.CollectionPolicy = CollectionPolicyAllUntilTimeout
			},
			want: "collection_policy is only allowed",
		},
		{
			name: "collect missing collection policy",
			mutate: func(envelope *Envelope) {
				envelope.ReplyPolicy = ReplyPolicyCollect
			},
			want: "collection_policy is required",
		},
		{
			name: "collect none collection policy",
			mutate: func(envelope *Envelope) {
				envelope.ReplyPolicy = ReplyPolicyCollect
				envelope.CollectionPolicy = CollectionPolicyNone
			},
			want: "collection_policy is required",
		},
		{
			name: "offband unsigned",
			mutate: func(envelope *Envelope) {
				envelope.Visibility = VisibilityOffband
			},
			want: "security.signed is required",
		},
		{
			name: "signed offband without signature",
			mutate: func(envelope *Envelope) {
				envelope.Visibility = VisibilityOffband
				envelope.Security.Signed = true
			},
			want: "security.signature is required",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			envelope := validRoutingEnvelope()
			testCase.mutate(&envelope)

			err := envelope.Validate()
			if err == nil {
				t.Fatal("Validate() expected an error")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Validate() error = %q, want substring %q", err, testCase.want)
			}
		})
	}
}

func TestEnvelopeValidateAcceptsBroadcastCollectWithoutLegacyRecipients(t *testing.T) {
	t.Helper()

	envelope := validRoutingEnvelope()
	envelope.To = nil
	envelope.Scope = ScopeBroadcast
	envelope.Recipient = ""
	envelope.ReplyPolicy = ReplyPolicyCollect
	envelope.CollectionPolicy = CollectionPolicyAllUntilTimeout

	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEnvelopeValidateAcceptsCollectionModes(t *testing.T) {
	t.Helper()

	for _, policy := range []CollectionPolicy{
		CollectionPolicyFirst,
		CollectionPolicyAllUntilTimeout,
		CollectionPolicyQuorum,
		CollectionPolicyManualReview,
	} {
		t.Run(string(policy), func(t *testing.T) {
			envelope := validRoutingEnvelope()
			envelope.To = nil
			envelope.Scope = ScopeBroadcast
			envelope.Recipient = ""
			envelope.ReplyPolicy = ReplyPolicyCollect
			envelope.CollectionPolicy = policy

			if err := envelope.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestEnvelopeValidateAcceptsRecipientWithoutScope(t *testing.T) {
	t.Helper()

	envelope := validRoutingEnvelope()
	envelope.To = nil
	envelope.Scope = ""

	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSignEnvelopeBindsRoutingFields(t *testing.T) {
	t.Helper()

	publicKey, privateKey := deterministicSigningKey()
	redirect := validRoutingEnvelope()
	redirect.Visibility = VisibilityOffband
	redirect.ReplyPolicy = ReplyPolicyReplyToTarget
	redirect.ReplyTarget = "agent.audit"

	collect := validRoutingEnvelope()
	collect.MessageID = "msg_collect"
	collect.To = nil
	collect.Visibility = VisibilityOffband
	collect.Scope = ScopeBroadcast
	collect.Recipient = ""
	collect.ReplyPolicy = ReplyPolicyCollect
	collect.CollectionPolicy = CollectionPolicyAllUntilTimeout
	collect.IdempotencyKey = "idem_collect"

	testCases := []struct {
		name     string
		envelope Envelope
		mutate   func(*Envelope)
	}{
		{
			name:     "visibility",
			envelope: redirect,
			mutate: func(envelope *Envelope) {
				envelope.Visibility = VisibilityInband
			},
		},
		{
			name:     "scope",
			envelope: redirect,
			mutate: func(envelope *Envelope) {
				envelope.To = nil
				envelope.Scope = ScopeBroadcast
				envelope.Recipient = ""
			},
		},
		{
			name:     "recipient",
			envelope: redirect,
			mutate: func(envelope *Envelope) {
				envelope.To = []string{"agent.other"}
				envelope.Recipient = "agent.other"
			},
		},
		{
			name:     "reply policy",
			envelope: redirect,
			mutate: func(envelope *Envelope) {
				envelope.ReplyPolicy = ReplyPolicyReplyToSender
				envelope.ReplyTarget = ""
			},
		},
		{
			name:     "reply target",
			envelope: redirect,
			mutate: func(envelope *Envelope) {
				envelope.ReplyTarget = "agent.archive"
			},
		},
		{
			name:     "collection policy",
			envelope: collect,
			mutate: func(envelope *Envelope) {
				envelope.CollectionPolicy = CollectionPolicyFirst
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			signed, err := SignEnvelope(testCase.envelope, privateKey)
			if err != nil {
				t.Fatalf("SignEnvelope() error = %v", err)
			}
			if err := VerifyEnvelope(signed, publicKey); err != nil {
				t.Fatalf("VerifyEnvelope() before tamper error = %v", err)
			}

			tampered := signed
			testCase.mutate(&tampered)
			if err := tampered.Validate(); err != nil {
				t.Fatalf("mutator produced invalid envelope: %v", err)
			}
			if err := VerifyEnvelope(tampered, publicKey); err == nil {
				t.Fatal("VerifyEnvelope() expected tamper error")
			}
		})
	}
}

func TestEnvelopeValidateRejectsInvalidPayload(t *testing.T) {
	t.Helper()

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage("{"),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestEnvelopeValidateRejectsDuplicateRecipients(t *testing.T) {
	t.Helper()

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator", "agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestArtifactValidateRejectsNegativeSize(t *testing.T) {
	t.Helper()

	artifact := Artifact{
		ArtifactID:  "art_1",
		Name:        "log.txt",
		Kind:        "log",
		URI:         "s3://example/log.txt",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes:   -1,
		ContentType: "text/plain",
	}

	if err := artifact.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestEnvelopeValidateRejectsMissingRequiredFields(t *testing.T) {
	t.Helper()

	base := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	testCases := []Envelope{
		func() Envelope {
			sample := base
			sample.MessageID = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.Type = MessageType("weird")
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.To = nil
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.SentAt = time.Time{}
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.IdempotencyKey = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.Trace.CorrelationID = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.Security.Scheme = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.Security.Nonce = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.To = []string{""}
			return sample
		}(),
	}

	for _, envelope := range testCases {
		if err := envelope.Validate(); err == nil {
			t.Fatalf("Validate() expected an error for %#v", envelope)
		}
	}
}

func TestEnvelopeValidateRejectsDeadlineBeforeSentAt(t *testing.T) {
	t.Helper()

	sentAt := time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC)
	deadline := sentAt.Add(-1 * time.Minute)

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		Deadline:       &deadline,
		SentAt:         sentAt,
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestArtifactValidateRejectsMissingRequiredFields(t *testing.T) {
	t.Helper()

	testCases := []Artifact{
		{Name: "log.txt", Kind: "log", URI: "s3://example/log.txt", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContentType: "text/plain"},
		{ArtifactID: "art_1", Kind: "log", URI: "s3://example/log.txt", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContentType: "text/plain"},
		{ArtifactID: "art_1", Name: "log.txt", URI: "s3://example/log.txt", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContentType: "text/plain"},
		{ArtifactID: "art_1", Name: "log.txt", Kind: "log", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContentType: "text/plain"},
		{ArtifactID: "art_1", Name: "log.txt", Kind: "log", URI: "s3://example/log.txt", ContentType: "text/plain"},
		{ArtifactID: "art_1", Name: "log.txt", Kind: "log", URI: "s3://example/log.txt", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{ArtifactID: "art_1", Name: "log.txt", Kind: "log", URI: "s3://example/log.txt", SHA256: "short", ContentType: "text/plain"},
		{ArtifactID: "art_1", Name: "log.txt", Kind: "log", URI: "s3://example/log.txt", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContentType: "plain"},
	}

	for _, artifact := range testCases {
		if err := artifact.Validate(); err == nil {
			t.Fatalf("Validate() expected an error for %#v", artifact)
		}
	}
}

func TestEnvelopeValidateRejectsDuplicateArtifactIDs(t *testing.T) {
	t.Helper()

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		ArtifactIDs:    []string{"art_1", "art_1"},
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestDefaultContentTypeUsesHTTPDetection(t *testing.T) {
	t.Helper()

	if got := DefaultContentType([]byte("hello")); got != "text/plain; charset=utf-8" {
		t.Fatalf("DefaultContentType(text) = %q", got)
	}
	if got := DefaultContentType(nil); got != "application/octet-stream" {
		t.Fatalf("DefaultContentType(empty) = %q", got)
	}
}

func TestValidateTaskRequestAcceptsRuntimeEnvelope(t *testing.T) {
	t.Helper()

	request := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	if err := request.ValidateTaskRequest(); err != nil {
		t.Fatalf("ValidateTaskRequest() error = %v", err)
	}
}

func TestValidateTaskAcceptedRejectsWrongReplyTarget(t *testing.T) {
	t.Helper()

	request := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}
	accepted := Envelope{
		MessageID:      "msg_accepted",
		ThreadID:       request.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           MessageTypeTaskAccepted,
		Payload:        json.RawMessage(`{"status":"accepted"}`),
		ReplyTo:        "msg_other",
		SentAt:         request.SentAt.Add(1 * time.Minute),
		IdempotencyKey: "idem_accepted",
		Trace: Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_accepted",
		},
	}

	if err := accepted.ValidateTaskAccepted(request); err == nil {
		t.Fatal("ValidateTaskAccepted() expected an error")
	}
}

func TestValidateTaskResultFinalAcceptsMatchingRequest(t *testing.T) {
	t.Helper()

	request := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}
	result := Envelope{
		MessageID:      "msg_result",
		ThreadID:       request.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           MessageTypeTaskResultFinal,
		Payload:        json.RawMessage(`{"status":"done"}`),
		ReplyTo:        request.MessageID,
		SentAt:         request.SentAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_result",
		Trace: Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_result",
		},
	}

	if err := result.ValidateTaskResultFinal(request); err != nil {
		t.Fatalf("ValidateTaskResultFinal() error = %v", err)
	}
}

func TestValidateTaskResultPartAcceptsMatchingRequest(t *testing.T) {
	t.Helper()

	request := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}
	result := Envelope{
		MessageID:      "msg_partial",
		ThreadID:       request.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           MessageTypeTaskResultPart,
		Payload:        json.RawMessage(`{"chunk":"running tests"}`),
		ReplyTo:        request.MessageID,
		SentAt:         request.SentAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_partial",
		Trace: Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_partial",
		},
	}

	if err := result.ValidateTaskResultPart(request); err != nil {
		t.Fatalf("ValidateTaskResultPart() error = %v", err)
	}
}

func TestValidateTaskResultPartRejectsWrongReplyTarget(t *testing.T) {
	t.Helper()

	request := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}
	result := Envelope{
		MessageID:      "msg_partial",
		ThreadID:       request.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           MessageTypeTaskResultPart,
		Payload:        json.RawMessage(`{"chunk":"running tests"}`),
		ReplyTo:        "msg_other",
		SentAt:         request.SentAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_partial",
		Trace: Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_partial",
		},
	}

	if err := result.ValidateTaskResultPart(request); err == nil {
		t.Fatal("ValidateTaskResultPart() expected an error")
	}
}

func TestValidateClarificationRequestRequiresDeadline(t *testing.T) {
	t.Helper()

	request := Envelope{
		MessageID:      "msg_task",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_task",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_task",
		},
	}
	clarification := Envelope{
		MessageID:      "msg_clarify",
		ThreadID:       request.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           MessageTypeClarifyRequest,
		Payload:        json.RawMessage(`{"question":"which release?"}`),
		ReplyTo:        request.MessageID,
		SentAt:         request.SentAt.Add(1 * time.Minute),
		IdempotencyKey: "idem_clarify",
		Trace: Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_clarify",
		},
	}

	if err := clarification.ValidateClarificationRequest(request); err == nil {
		t.Fatal("ValidateClarificationRequest() expected an error")
	}
}

func TestValidateClarificationResponseAcceptsMatchingRequest(t *testing.T) {
	t.Helper()

	request := Envelope{
		MessageID:      "msg_task",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_task",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_task",
		},
	}
	deadline := request.SentAt.Add(5 * time.Minute)
	clarification := Envelope{
		MessageID:      "msg_clarify",
		ThreadID:       request.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           MessageTypeClarifyRequest,
		Payload:        json.RawMessage(`{"question":"which release?"}`),
		ReplyTo:        request.MessageID,
		Deadline:       &deadline,
		SentAt:         request.SentAt.Add(1 * time.Minute),
		IdempotencyKey: "idem_clarify",
		Trace: Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_clarify",
		},
	}
	response := Envelope{
		MessageID:      "msg_answer",
		ThreadID:       request.ThreadID,
		From:           "collector.nullbot",
		To:             []string{"worker.smokevm"},
		Type:           MessageTypeClarifyResponse,
		Payload:        json.RawMessage(`{"answer":"v0.17.2"}`),
		ReplyTo:        clarification.MessageID,
		SentAt:         request.SentAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_answer",
		Trace: Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_answer",
		},
	}

	if err := response.ValidateClarificationResponse(request, clarification); err != nil {
		t.Fatalf("ValidateClarificationResponse() error = %v", err)
	}
}

func validRoutingEnvelope() Envelope {
	sentAt := time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC)

	return Envelope{
		MessageID:      "msg_route",
		ThreadID:       "thr_route",
		From:           "agent.sender",
		To:             []string{"agent.worker"},
		Visibility:     VisibilityInband,
		Scope:          ScopeTargeted,
		Recipient:      "agent.worker",
		Type:           MessageTypeRequest,
		Payload:        json.RawMessage(`{"task":"inspect routing schema"}`),
		ReplyPolicy:    ReplyPolicyReplyToSender,
		SentAt:         sentAt,
		IdempotencyKey: "idem_route",
		Trace:          Trace{CorrelationID: "corr_route", Verified: true},
		Security:       Security{Scheme: "ed25519", Nonce: "nonce_route"},
	}
}

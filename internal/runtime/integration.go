package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/work"
)

type HandlerOptions struct {
	WorkOrders WorkOrderBridge
	SyncHooks  map[string]ExecutionSyncHook
}

type WorkOrderBridge interface {
	CreateWorkOrder(context.Context, work.Draft) (WorkOrderRef, error)
}

type ExecutionSyncHook interface {
	SyncWorkOrder(context.Context, work.Draft, WorkOrderRef) (SyncReceipt, error)
}

type WorkOrderRef struct {
	Project string `json:"project"`
	ID      int    `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
}

type SyncReceipt struct {
	Target string `json:"target"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type WorkledgerHTTPBridge struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type workledgerCreateRequest struct {
	Project        string            `json:"project"`
	Title          string            `json:"title"`
	Priority       string            `json:"priority"`
	Tags           []string          `json:"tags,omitempty"`
	Sections       map[string]string `json:"sections"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
}

type workledgerCreateResponse struct {
	WO struct {
		ID      int    `json:"id"`
		Project string `json:"project"`
		Title   string `json:"title"`
		URL     string `json:"url,omitempty"`
	} `json:"wo"`
}

func NewWorkledgerHTTPBridge(baseURL string, apiKey string) (*WorkledgerHTTPBridge, error) {
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		return nil, errors.New("workledger base url is required")
	}
	if apiKey == "" {
		return nil, errors.New("workledger api key is required")
	}

	return &WorkledgerHTTPBridge{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

func (b *WorkledgerHTTPBridge) CreateWorkOrder(ctx context.Context, draft work.Draft) (WorkOrderRef, error) {
	if b == nil {
		return WorkOrderRef{}, errors.New("workledger bridge is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	body, err := json.Marshal(workledgerCreateRequest{
		Project:  draft.WorkledgerProject,
		Title:    draft.Title,
		Priority: draft.Priority,
		Tags: []string{
			"hivebus",
			"source-thread:" + draft.SourceThreadID,
		},
		Sections: map[string]string{
			"summary":             draft.Summary,
			"scope":               strings.Join(draft.Scope, "\n"),
			"acceptance_criteria": strings.Join(draft.AcceptanceCriteria, "\n"),
			"evidence":            strings.Join(draft.EvidenceIDs, "\n"),
			"notes": fmt.Sprintf(
				"tracking_system=%s\nsource_thread_id=%s\ncustomer_tier=%s\nconfidence=%s",
				draft.TrackingSystem,
				draft.SourceThreadID,
				draft.CustomerTier,
				draft.Confidence,
			),
		},
		IdempotencyKey: "hivebus:" + draft.WorkledgerProject + ":" + draft.SourceThreadID,
	})
	if err != nil {
		return WorkOrderRef{}, fmt.Errorf("marshal workledger create request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/wo", bytes.NewReader(body))
	if err != nil {
		return WorkOrderRef{}, fmt.Errorf("build workledger request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return WorkOrderRef{}, fmt.Errorf("create workledger work order: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return WorkOrderRef{}, fmt.Errorf("workledger create returned status %d", resp.StatusCode)
	}

	var decoded workledgerCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return WorkOrderRef{}, fmt.Errorf("decode workledger response: %w", err)
	}

	return WorkOrderRef{
		Project: decoded.WO.Project,
		ID:      decoded.WO.ID,
		Title:   decoded.WO.Title,
		URL:     decoded.WO.URL,
	}, nil
}

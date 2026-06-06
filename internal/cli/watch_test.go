package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/ppiankov/hivebus/internal/store"
)

func TestRunWatchPrintsStructuredJSONLines(t *testing.T) {
	t.Helper()

	oldClient := watchHTTPClient
	watchHTTPClient = handlerBackedClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		event1 := `{"sequence":1,"thread_id":"thr_123","event_kind":"thread.created"}`
		event2 := `{"sequence":2,"thread_id":"thr_123","event_kind":"envelope.appended","envelope":{"message_id":"msg_partial","thread_id":"thr_123","from":"worker.smokevm","to":["collector.nullbot"],"type":"task.result.partial","payload":{"chunk":"running smoke"},"reply_to":"msg_task","sent_at":"2026-04-17T00:00:00Z","idempotency_key":"idem_partial","trace":{"correlation_id":"corr_123","verified":true},"security":{"scheme":"ed25519","nonce":"nonce_partial","signed":false}}}`
		fmt.Fprintf(w, "id: 1\nevent: thread.created\ndata: %s\n\n", event1)
		fmt.Fprintf(w, "id: 2\nevent: task.result.partial\ndata: %s\n\n", event2)
	}))
	t.Cleanup(func() {
		watchHTTPClient = oldClient
	})

	var out bytes.Buffer
	if err := runWatch(context.Background(), &out, "http://hivebus.test", "thr_123", "", ""); err != nil {
		t.Fatalf("runWatch() error = %v", err)
	}

	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 json lines, got %d: %s", len(lines), out.String())
	}

	var event store.ThreadEvent
	if err := json.Unmarshal(lines[1], &event); err != nil {
		t.Fatalf("Unmarshal(line) error = %v", err)
	}
	if event.Envelope == nil || event.Envelope.MessageID != "msg_partial" {
		t.Fatalf("expected partial envelope, got %#v", event)
	}
}

package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
)

const watchPollInterval = 250 * time.Millisecond

func (s *server) handleWatchThread(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming is not supported"))
		return
	}

	threadID := r.PathValue("threadID")
	afterSequence, err := parseLastEventID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if _, err := s.store.LoadThread(r.Context(), threadID); err != nil {
		switch {
		case errors.Is(err, store.ErrThreadNotFound):
			writeError(w, http.StatusNotFound, err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	for {
		events, err := s.store.ListThreadEvents(r.Context(), threadID, afterSequence, 128)
		switch {
		case errors.Is(err, store.ErrThreadNotFound):
			writeError(w, http.StatusNotFound, err)
			return
		case err != nil:
			return
		}

		for _, event := range events {
			if err := writeWatchEvent(w, event); err != nil {
				return
			}
			flusher.Flush()
			afterSequence = event.Sequence
			if watchEventCloses(event) {
				return
			}
		}

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func parseLastEventID(r *http.Request) (int64, error) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("last_event_id"))
	}
	if value == "" {
		return 0, nil
	}

	lastEventID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || lastEventID < 0 {
		return 0, errors.New("last-event-id must be a non-negative integer")
	}

	return lastEventID, nil
}

func writeWatchEvent(w http.ResponseWriter, event store.ThreadEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal watch event: %w", err)
	}

	if _, err := fmt.Fprintf(w, "id: %d\n", event.Sequence); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", sseEventName(event)); err != nil {
		return err
	}
	for _, line := range strings.Split(string(payload), "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err = fmt.Fprint(w, "\n")
	return err
}

func sseEventName(event store.ThreadEvent) string {
	if event.Envelope != nil {
		return string(event.Envelope.Type)
	}
	return event.EventKind
}

func watchEventCloses(event store.ThreadEvent) bool {
	return event.Envelope != nil && event.Envelope.Type == model.MessageTypeTaskResultFinal
}

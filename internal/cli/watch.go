package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ppiankov/hivebus/internal/store"
	"github.com/spf13/cobra"
)

// WO-108: unit tests dispatch watch requests in-process under restricted networking.
var watchHTTPClient askHTTPDoer = &http.Client{Timeout: 0}

func newWatchCommand() *cobra.Command {
	var addr string
	var token string
	var lastEventID string

	cmd := &cobra.Command{
		Use:   "watch <thread-id>",
		Short: "Watch a live thread over SSE",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				token = os.Getenv("HIVEBUS_TOKEN")
			}
			return runWatch(cmd.Context(), cmd.OutOrStdout(), addr, args[0], token, lastEventID)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "http://127.0.0.1:7081", "Hivebus base URL")
	cmd.Flags().StringVar(&token, "token", "", "Bearer token for the Hivebus runtime")
	cmd.Flags().StringVar(&lastEventID, "last-event-id", "", "Resume from a previously seen SSE event ID")

	return cmd
}

func runWatch(
	ctx context.Context,
	out io.Writer,
	addr string,
	threadID string,
	token string,
	lastEventID string,
) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("thread id is required")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		strings.TrimRight(addr, "/")+"/v0/threads/"+threadID+"/watch",
		nil,
	)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.TrimSpace(lastEventID) != "" {
		req.Header.Set("Last-Event-ID", strings.TrimSpace(lastEventID))
	}

	resp, err := watchHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		var response struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&response); err == nil && strings.TrimSpace(response.Error) != "" {
			return errors.New(response.Error)
		}
		return fmt.Errorf("watch request failed: %s", resp.Status)
	}

	return consumeSSE(out, resp.Body)
}

func consumeSSE(out io.Writer, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataLines []string

	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}

		var event store.ThreadEvent
		if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &event); err != nil {
			return err
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := out.Write(encoded); err != nil {
			return err
		}
		if _, err := io.WriteString(out, "\n"); err != nil {
			return err
		}

		dataLines = dataLines[:0]
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return flush()
}

//go:build unix

package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func signalNotify(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, signals...)
}

func signalNotifyContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}

	return signalNotify(ctx, os.Interrupt, syscall.SIGTERM)
}

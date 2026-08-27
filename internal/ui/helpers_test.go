package ui

import (
	"io"
	"log/slog"
)

// testLogger discards output so test runs stay quiet.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

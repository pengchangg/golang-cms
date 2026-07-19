package main

import (
	"io"
	"log/slog"
	"testing"
)

func TestExecuteRejectsUnknownCommand(t *testing.T) {
	code, err := execute([]string{"unknown"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if code != 2 || err == nil {
		t.Fatalf("execute() = (%d, %v)", code, err)
	}
}

func TestExecuteRejectsMissingCommand(t *testing.T) {
	code, err := execute(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if code != 2 || err == nil {
		t.Fatalf("execute() = (%d, %v)", code, err)
	}
}

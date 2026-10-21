package main

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestLockWaitsForHolder(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unlock, err := lock(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		second, err := lock(dir, log)
		if err != nil {
			t.Error(err)
		}
		close(got)
		second()
	}()
	select {
	case <-got:
		t.Fatal("second lock taken while the first was held")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("second lock not taken after the first was released")
	}
}

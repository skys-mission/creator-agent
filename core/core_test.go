package core

import (
	"context"
	"testing"
	"time"
)

func TestStatusReportsVersionAndUptime(t *testing.T) {
	c := New("1.2.3-test")
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Version != "1.2.3-test" {
		t.Fatalf("Version = %q, want 1.2.3-test", st.Version)
	}
	if st.Uptime < 0 {
		t.Fatalf("Uptime = %v, want >= 0", st.Uptime)
	}
}

func TestStatusHonorsCanceledContext(t *testing.T) {
	c := New("x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Status(ctx); err == nil {
		t.Fatal("Status with canceled ctx: want error, got nil")
	}
}

func TestStatusUptimeGrows(t *testing.T) {
	c := New("x")
	a, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	b, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if b.Uptime < a.Uptime {
		t.Fatalf("Uptime went backwards: %v -> %v", a.Uptime, b.Uptime)
	}
}

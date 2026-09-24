package grpcsvc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/skys-mission/creator-agent/server/auth"
)

// startServer brings up a real loopback server with a fresh token and returns
// a connected client plus the bearer token.
func startServer(t *testing.T) (healthpb.HealthClient, *grpc.ClientConn, string) {
	t.Helper()
	store := auth.New(filepath.Join(t.TempDir(), "rpc-token"))
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	srv, err := New(store, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go func() {
		_ = srv.Serve()
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})

	conn, err := grpc.NewClient(
		"passthrough:///"+srv.Addr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return healthpb.NewHealthClient(conn), conn, store.Value()
}

func authed(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("status code = %v (%v), want %v", got, err, want)
	}
}

// TestHealthRejectsMissingToken: a unary RPC without credentials is refused.
func TestHealthRejectsMissingToken(t *testing.T) {
	client, _, _ := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	wantCode(t, err, codes.Unauthenticated)
}

// TestHealthRejectsWrongToken: a near-miss token is refused.
func TestHealthRejectsWrongToken(t *testing.T) {
	client, _, token := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Check(authed(ctx, token+"x"), &healthpb.HealthCheckRequest{})
	wantCode(t, err, codes.Unauthenticated)
}

// TestHealthWithToken: the happy path of the P0 network-face acceptance.
func TestHealthWithToken(t *testing.T) {
	client, _, token := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Check(authed(ctx, token), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health status = %v, want SERVING", resp.Status)
	}
}

// TestStreamRejectsMissingToken: streams are guarded by their own interceptor;
// without it, session traffic would be an open door (docs/architecture.md §8).
func TestStreamRejectsMissingToken(t *testing.T) {
	client, _, _ := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
	if err == nil {
		_, err = stream.Recv()
	}
	if err == nil {
		t.Fatal("Watch without token: want error, got nil")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Watch without token hung instead of rejecting: %v", err)
	}
	wantCode(t, err, codes.Unauthenticated)
}

// TestStreamWithToken: an authenticated stream delivers updates.
func TestStreamWithToken(t *testing.T) {
	client, _, token := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.Watch(authed(ctx, token), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health status = %v, want SERVING", resp.Status)
	}
}

// Package grpcsvc assembles the gRPC network face of the runtime (channel 3 in
// docs/architecture.md §2). It is intentionally thin: transport + auth + a
// health probe. Business methods arrive in later phases as contract protos.
package grpcsvc

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/skys-mission/creator-agent/server/auth"
)

// maxRecvBytes caps a single request message (gate 3 hardening; default is
// effectively unlimited for our purposes).
const maxRecvBytes = 4 << 20

// Server is the gRPC listener with auth wired in.
type Server struct {
	gs  *grpc.Server
	lis net.Listener
}

// New binds addr and builds the server. Loopback by default is the caller's
// decision (see docs/architecture.md §8 gate 1); tests pass "127.0.0.1:0".
func New(store *auth.Store, addr string) (*Server, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("grpc: listen %s: %w", addr, err)
	}
	gs := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor(store)),
		grpc.StreamInterceptor(auth.StreamServerInterceptor(store)),
		grpc.MaxRecvMsgSize(maxRecvBytes),
	)
	healthpb.RegisterHealthServer(gs, health.NewServer())
	// Service reflection is deliberately NOT registered: it would hand the
	// method list to any prober (gate 3).
	return &Server{gs: gs, lis: lis}, nil
}

// Addr returns the bound address (resolved, useful with port 0).
func (s *Server) Addr() string { return s.lis.Addr().String() }

// Serve blocks until the server stops. Start it in a goroutine from a kernel
// component's OnStart.
func (s *Server) Serve() error {
	if err := s.gs.Serve(s.lis); err != nil {
		return fmt.Errorf("grpc: serve: %w", err)
	}
	return nil
}

// Stop shuts down gracefully, bounded by ctx; on timeout it hard-stops.
func (s *Server) Stop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.gs.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.gs.Stop()
		return ctx.Err()
	}
}

package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// authHeader is the metadata key carrying credentials ("Bearer <token>").
	authHeader = "authorization"
	// bearerPrefix is the required scheme prefix inside the header value.
	bearerPrefix = "Bearer "
)

// UnaryServerInterceptor rejects unary RPCs without a valid bearer token.
func UnaryServerInterceptor(s *Store) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := s.checkContext(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor rejects streaming RPCs without a valid bearer token.
// Both interceptors must be armed: streams are NOT covered by the unary one,
// and the session traffic on this server is streaming (docs/architecture.md §8).
func StreamServerInterceptor(s *Store) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := s.checkContext(ss.Context()); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// checkContext validates the bearer token in request metadata.
func (s *Store) checkContext(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	for _, v := range md.Get(authHeader) {
		if strings.HasPrefix(v, bearerPrefix) && s.Check(strings.TrimPrefix(v, bearerPrefix)) {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "invalid or missing bearer token")
}

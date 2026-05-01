package rpc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	headerAuthorization = "authorization"
	bearerPrefix        = "Bearer "
)

// UnaryAuthInterceptor returns a gRPC unary server interceptor that validates
// the bearer token in the Authorization metadata header. When token is empty
// the interceptor is a no-op, preserving the plaintext default.
func UnaryAuthInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if token == "" {
			return handler(ctx, req)
		}
		if err := checkToken(ctx, token); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamAuthInterceptor returns a gRPC stream server interceptor that validates
// the bearer token. When token is empty the interceptor is a no-op.
func StreamAuthInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if token == "" {
			return handler(srv, ss)
		}
		if err := checkToken(ss.Context(), token); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// checkToken extracts the Authorization header from ctx and verifies it matches
// the expected bearer token using constant-time comparison to prevent timing
// attacks. It also checks the Bearer prefix explicitly (TrimPrefix would silently
// pass a raw token with no prefix).
func checkToken(ctx context.Context, want string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	vals := md.Get(headerAuthorization)
	if len(vals) == 0 {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	raw := vals[0]
	if !strings.HasPrefix(raw, bearerPrefix) {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	got := raw[len(bearerPrefix):]
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	return nil
}

// certFingerprint reads the first PEM certificate block from certPath and
// returns its SHA-256 fingerprint as a colon-separated hex string suitable
// for operator verification at boot.
func certFingerprint(certPath string) (string, error) {
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("read cert %s: %w", certPath, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", fmt.Errorf("no PEM block in %s", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse cert %s: %w", certPath, err)
	}
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":"), nil
}

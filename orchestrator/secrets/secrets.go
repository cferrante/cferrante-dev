// Package secrets reads secrets from SSM Parameter Store SecureStrings
// (design §11): read once per process and kept in memory, so a secret is
// never in a file, an environment variable, a command line or a log.
package secrets

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// GetParameterAPI is the one SSM call needed; *ssm.Client satisfies it.
type GetParameterAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// SSM reads SecureString parameters, caching each value after its first
// successful read. Safe for concurrent use.
type SSM struct {
	API GetParameterAPI

	mu    sync.Mutex
	cache map[string]string
}

// Get returns the decrypted value of parameter name. Errors name the
// parameter, never the value. A failure isn't cached, so the next call
// retries (for example after `aws sso login`).
func (s *SSM) Get(ctx context.Context, name string) (string, error) {
	// Held across the call so concurrent first reads make one request.
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.cache[name]; ok {
		return v, nil
	}
	out, err := s.API.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
	if err != nil {
		return "", fmt.Errorf("reading SSM parameter %s: %w", name, err)
	}
	if out.Parameter == nil || strings.TrimSpace(aws.ToString(out.Parameter.Value)) == "" {
		return "", fmt.Errorf("SSM parameter %s is empty", name)
	}
	if s.cache == nil {
		s.cache = map[string]string{}
	}
	v := aws.ToString(out.Parameter.Value)
	s.cache[name] = v
	return v, nil
}

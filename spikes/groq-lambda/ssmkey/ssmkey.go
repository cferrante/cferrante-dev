// Package ssmkey reads a secret from an SSM SecureString parameter, so the
// Groq API key lives only in SSM and in process memory: never in files,
// environment variables, command lines or logs.
package ssmkey

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// DefaultName is where put-groq-key.sh stores the Groq API key.
const DefaultName = "/cferrante-dev/groq-api-key"

// GetParameterAPI is the one SSM call needed; *ssm.Client satisfies it.
type GetParameterAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Load returns the decrypted value of parameter name. Errors name the
// parameter, never the value.
func Load(ctx context.Context, api GetParameterAPI, name string) (string, error) {
	out, err := api.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("reading SSM parameter %s: %w", name, err)
	}
	if out.Parameter == nil || aws.ToString(out.Parameter.Value) == "" {
		return "", fmt.Errorf("SSM parameter %s is empty", name)
	}
	return aws.ToString(out.Parameter.Value), nil
}

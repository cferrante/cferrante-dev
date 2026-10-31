// Command bootstrap is spike 1.5's Lambda: each invoke runs one tool call
// round trip against Groq and returns its report. The API key is read from
// SSM once, at cold start, so it counts in Init Duration, not in a request.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/groq"
	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/roundtrip"
	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/ssmkey"
)

var (
	client = &groq.Client{}
	model  = "openai/gpt-oss-120b"
)

func handler(ctx context.Context, _ json.RawMessage) (roundtrip.Report, error) {
	rep, err := roundtrip.Run(ctx, client, model)
	if err != nil {
		rep.OK = false
		rep.Problems = append(rep.Problems, err.Error())
	}
	return rep, nil
}

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}
	name := ssmkey.DefaultName
	if v := os.Getenv("GROQ_KEY_PARAM"); v != "" {
		name = v
	}
	if client.APIKey, err = ssmkey.Load(ctx, ssm.NewFromConfig(cfg), name); err != nil {
		log.Fatal(err)
	}
	if v := os.Getenv("GROQ_MODEL"); v != "" {
		model = v
	}
	lambda.Start(handler)
}

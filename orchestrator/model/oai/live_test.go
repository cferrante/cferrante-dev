package oai

import (
	"context"
	"os"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/secrets"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestLiveGroq calls the real Groq API (plan step 5.1). Opt-in: it costs
// free-tier quota and needs an AWS session for the key, so `make test` and CI
// skip it. Run it with `make smoke-groq` after `aws sso login`.
func TestLiveGroq(t *testing.T) {
	if os.Getenv("LIVE_GROQ") != "1" {
		t.Skip("set LIVE_GROQ=1 (make smoke-groq) after aws sso login to call Groq")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile(envOr("AWS_PROFILE", "cferrante-dev-admin")),
		awsconfig.WithRegion(envOr("AWS_REGION", "us-east-1")))
	if err != nil {
		t.Fatal(err)
	}
	key, err := (&secrets.SSM{API: ssm.NewFromConfig(awsCfg)}).Get(ctx, envOr("GROQ_KEY_PARAM", "/cferrante-dev/groq-api-key"))
	if err != nil {
		t.Fatalf("%v (if your AWS session has expired: aws sso login --profile %s)", err, envOr("AWS_PROFILE", "cferrante-dev-admin"))
	}
	c := &Client{Provider: "groq", BaseURL: GroqBaseURL, APIKey: key, Model: envOr("GROQ_MODEL", "openai/gpt-oss-120b")}
	counts := map[model.EventKind]int{}
	start := time.Now()
	res, err := c.Stream(ctx, model.Request{
		System:   "Answer in one short sentence.",
		Messages: []model.Message{{Role: model.User, Content: "What is 17 + 25?"}},
	}, func(e model.Event) error { counts[e.Kind]++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	total := time.Since(start)
	if counts[model.Reasoning] == 0 || counts[model.Text] == 0 {
		t.Errorf("events %v: want reasoning and text", counts)
	}
	if res.TTFT <= 0 || res.TokensOut == 0 || res.StopReason != "stop" {
		t.Errorf("result %+v", res)
	}
	t.Logf("groq %s: TTFT %v, total %v, %d reasoning + %d text events, %d tokens in, %d out, %d cached",
		res.Model, res.TTFT.Round(time.Millisecond), total.Round(time.Millisecond),
		counts[model.Reasoning], counts[model.Text], res.TokensIn, res.TokensOut, res.CachedTokens)
}

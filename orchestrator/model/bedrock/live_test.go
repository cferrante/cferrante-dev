package bedrock

import (
	"context"
	"os"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestLiveBedrock calls the real Bedrock API once (plan step 5.2). Opt-in:
// it costs a fraction of a cent and needs an AWS session, so `make test` and
// CI skip it. Run it with `make smoke-bedrock` after `aws sso login`.
func TestLiveBedrock(t *testing.T) {
	if os.Getenv("LIVE_BEDROCK") != "1" {
		t.Skip("set LIVE_BEDROCK=1 (make smoke-bedrock) after aws sso login to call Bedrock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile(envOr("AWS_PROFILE", "cferrante-dev-admin")),
		awsconfig.WithRegion(envOr("AWS_REGION", "us-east-1")))
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{API: SDK{Client: bedrockruntime.NewFromConfig(awsCfg)}, Model: envOr("BEDROCK_MODEL", "us.amazon.nova-lite-v1:0")}
	var text int
	start := time.Now()
	res, err := c.Stream(ctx, model.Request{
		System:    "Answer in one short sentence.",
		Messages:  []model.Message{{Role: model.User, Content: "What is 17 + 25?"}},
		MaxTokens: 100,
	}, func(e model.Event) error {
		if e.Kind == model.Text {
			text++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%v (if your AWS session has expired: aws sso login --profile %s)", err, envOr("AWS_PROFILE", "cferrante-dev-admin"))
	}
	if text == 0 || res.TTFT <= 0 || res.TokensOut == 0 || res.StopReason != "stop" {
		t.Errorf("text events %d, result %+v", text, res)
	}
	t.Logf("bedrock %s: TTFT %v, total %v, %d text events, %d tokens in, %d out",
		res.Model, res.TTFT.Round(time.Millisecond), time.Since(start).Round(time.Millisecond), text, res.TokensIn, res.TokensOut)
}

package embed

import (
	"context"
	"math"
	"os"
	"slices"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// TestLiveTitan calls the real Titan v2 13 times (plan step 5.3). Opt-in:
// it costs a tiny fraction of a cent and needs an AWS session, so `make test`
// and CI skip it. Run it with `make smoke-titan` after `aws sso login`.
func TestLiveTitan(t *testing.T) {
	if os.Getenv("LIVE_TITAN") != "1" {
		t.Skip("set LIVE_TITAN=1 (make smoke-titan) after aws sso login to call Titan")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	profile := envOr("AWS_PROFILE", "cferrante-dev-admin")
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile(profile), awsconfig.WithRegion(envOr("AWS_REGION", "us-east-1")))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewTitan(bedrockruntime.NewFromConfig(awsCfg), envOr("TITAN_MODEL", TitanModel), 512)
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{
		"How does the search engine rank documents?",
		"Retrieval combines vector similarity with keyword scoring.",
		"My favourite recipe uses tomatoes and basil.",
	}
	start := time.Now()
	v, err := e.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("%v (if your AWS session has expired: aws sso login --profile %s)", err, profile)
	}
	elapsed := time.Since(start)
	for i := range v {
		if n := math.Sqrt(dot(v[i], v[i])); math.Abs(n-1) > 1e-3 {
			t.Errorf("vector %d has length %.4f, want 1 (normalized)", i, n)
		}
	}
	related, unrelated := dot(v[0], v[1]), dot(v[0], v[2])
	if related <= unrelated {
		t.Errorf("search~retrieval %.3f should beat search~recipe %.3f", related, unrelated)
	}
	t.Logf("titan %s: %d texts in %v (first calls, including connection setup), %d tokens; similarity related %.3f, unrelated %.3f",
		e.Model(), len(texts), elapsed.Round(time.Millisecond), e.Tokens(), related, unrelated)

	// A visitor's question is one warm call: measure that, as the agent sees it.
	var times []time.Duration
	for range 10 {
		start := time.Now()
		if _, err := e.Embed(ctx, []string{"What did you build with Go?"}); err != nil {
			t.Fatal(err)
		}
		times = append(times, time.Since(start))
	}
	slices.Sort(times)
	t.Logf("warm query embedding, 10 calls: min %v, median %v, max %v",
		times[0].Round(time.Millisecond), times[len(times)/2].Round(time.Millisecond), times[len(times)-1].Round(time.Millisecond))
}

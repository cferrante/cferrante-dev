package oai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

// liveClient reads the Groq key from SSM, or skips without LIVE_GROQ=1.
func liveClient(t *testing.T, ctx context.Context) *Client {
	t.Helper()
	if os.Getenv("LIVE_GROQ") != "1" {
		t.Skip("set LIVE_GROQ=1 (make smoke-groq) after aws sso login to call Groq")
	}
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
	return &Client{Provider: "groq", BaseURL: GroqBaseURL, APIKey: key, Model: envOr("GROQ_MODEL", "openai/gpt-oss-120b")}
}

// TestLiveGroqSchema checks strict json_schema output end to end (step 5.4).
func TestLiveGroqSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := liveClient(t, ctx)
	var text string
	res, err := c.Stream(ctx, model.Request{
		System:   "Rate how well the answer is supported by the excerpt, from 1 (not at all) to 5 (fully), and give one short reason.",
		Messages: []model.Message{{Role: model.User, Content: "Excerpt: Alpha is a small Go service.\nAnswer: Alpha is written in Go [1]."}},
		ResponseSchema: json.RawMessage(`{"type":"object","properties":{"score":{"type":"integer"},"reason":{"type":"string"}},
			"required":["score","reason"],"additionalProperties":false}`),
	}, func(e model.Event) error {
		if e.Kind == model.Text {
			text += e.Text
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Score  int    `json:"score"`
		Reason string `json:"reason"`
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil || v.Score < 1 || v.Score > 5 || v.Reason == "" {
		t.Errorf("output %q: %v", text, err)
	}
	t.Logf("groq schema %s: %v total, %d tokens in, %d out; output %s", res.Model, res.TTFT.Round(time.Millisecond), res.TokensIn, res.TokensOut, text)
}

// TestLiveGroqCache measures whether Groq reports cached tokens when one
// request with a long, unchanging prefix is repeated (step 5.4). It logs and
// never fails: on 2026-11-03 identical 1,460-token repeats came back with no
// prompt_tokens_details at all, streamed or not, while spike 1.5 (from
// Lambda) saw hits. Whether a request hits is Groq's behaviour, not ours;
// the stable-prefix test pins our side.
func TestLiveGroqCache(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := liveClient(t, ctx)
	var rules strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&rules, "Rule %d: answer only from the excerpts given, cite each claim, and keep the tone friendly and brief.\n", i)
	}
	req := model.Request{System: rules.String(), Messages: []model.Message{{Role: model.User, Content: "Reply with the word ok."}}}
	var log []string
	for try := 1; try <= 4; try++ {
		res, err := c.Stream(ctx, req, func(model.Event) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		log = append(log, fmt.Sprintf("try %d: %d in, %d cached, TTFT %v", try, res.TokensIn, res.CachedTokens, res.TTFT.Round(time.Millisecond)))
		if try > 1 && res.CachedTokens > 0 {
			t.Logf("groq cache %s: %s", res.Model, strings.Join(log, "; "))
			return
		}
		time.Sleep(time.Second)
	}
	t.Logf("groq cache: no cached tokens on any repeat: %s", strings.Join(log, "; "))
}

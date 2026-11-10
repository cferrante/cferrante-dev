package judge

import (
	"context"
	"os"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/cferrante/cferrante-dev/orchestrator/model/oai"
	"github.com/cferrante/cferrante-dev/orchestrator/secrets"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestLiveJudge runs the real judge on Groq (plan step 6.3a). Opt-in: it uses
// free-tier quota and needs an AWS session for the key, so `make test` and CI
// skip it. Run it with `make smoke-judge` after `aws sso login`.
func TestLiveJudge(t *testing.T) {
	if os.Getenv("LIVE_GROQ") != "1" {
		t.Skip("set LIVE_GROQ=1 (make smoke-judge) after aws sso login to call Groq")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	profile := envOr("AWS_PROFILE", "cferrante-dev-admin")
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithSharedConfigProfile(profile), awsconfig.WithRegion(envOr("AWS_REGION", "us-east-1")))
	if err != nil {
		t.Fatal(err)
	}
	key, err := (&secrets.SSM{API: ssm.NewFromConfig(awsCfg)}).Get(ctx, envOr("GROQ_KEY_PARAM", "/cferrante-dev/groq-api-key"))
	if err != nil {
		t.Fatalf("%v (if your AWS session has expired: aws sso login --profile %s)", err, profile)
	}
	j := Judge{Model: &oai.Client{Provider: "groq", BaseURL: oai.GroqBaseURL, APIKey: key, Model: envOr("JUDGE_MODEL", "openai/gpt-oss-20b")}}

	vartec := []Source{{ID: "exp-vartec#call-record-conversion", Title: "Senior Programmer, Vartec Telecom (Jan 2001 – Feb 2004)", Text: "At Vartec Telecom, Colleen re-architected the system that read binary call detail record (CDR) files from all 12 telecom switches and converted them to ASCII, replacing per-switch copies with one program configured for each switch."}}
	for _, tc := range []struct {
		name     string
		in       Input
		grounded bool
		answers  string
	}{
		{"known good", Input{Question: "Has Colleen worked in telecom?", Sources: vartec,
			Answer:   "Yes. At Vartec Telecom she re-architected the system that converted call records from all 12 switches, using one program configured per switch [1].",
			Expected: "Yes: Vartec Telecom, call-record conversion."}, true, "yes"},
		{"known bad: invented claims", Input{Question: "Has Colleen worked in telecom?", Sources: vartec,
			Answer:   "Yes. At Vartec Telecom she led a team of 40 engineers and saved the company $2 million a year [1].",
			Expected: "Yes: Vartec Telecom, call-record conversion."}, false, ""},
		{"a decline is grounded", Input{Question: "What's the weather in Paris?",
			Answer:   "I don't have information about the weather; I can only answer questions about Colleen's work.",
			Expected: "Politely says it doesn't have that information; no forecast."}, true, "yes"},
	} {
		start := time.Now()
		got, err := j.Judge(ctx, tc.in)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if (got.Verdict == Grounded) != tc.grounded || (tc.answers != "" && got.AnswersQuestion != tc.answers) {
			t.Errorf("%s: %s, answers %s, claims %q; reasoning: %s", tc.name, got.Verdict, got.AnswersQuestion, got.UnsupportedClaims, got.Reasoning)
		}
		t.Logf("%s: %s, answers %s, claims %q (%s, %v, %d tokens in, %d out)", tc.name, got.Verdict, got.AnswersQuestion,
			got.UnsupportedClaims, got.Model, time.Since(start).Round(time.Millisecond), got.TokensIn, got.TokensOut)
	}
}

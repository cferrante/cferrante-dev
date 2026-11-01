// Command local runs spike 1.5's round trip from the laptop, reading the
// Groq key from SSM with the SSO session (AWS_PROFILE, default
// cferrante-dev-admin). It prints one JSON report per run.
//
//	go run ./spikes/groq-lambda/cmd/local [-n RUNS] [-record DIR]
//
// -record saves each raw Groq response stream to DIR, to compare the test
// fixtures with what Groq really sends.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/groq"
	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/roundtrip"
	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/ssmkey"
)

func main() {
	runs := flag.Int("n", 1, "runs")
	record := flag.String("record", "", "directory to save raw response streams in")
	model := flag.String("model", "openai/gpt-oss-120b", "Groq model id")
	gap := flag.Duration("gap", 5*time.Second, "pause between runs (free-tier rate limits)")
	timeout := flag.Duration("timeout", 60*time.Second, "limit per run, so a stalled stream can't hang it")
	flag.Parse()

	if os.Getenv("AWS_PROFILE") == "" {
		os.Setenv("AWS_PROFILE", "cferrante-dev-admin")
	}
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}
	name := ssmkey.DefaultName
	if v := os.Getenv("GROQ_KEY_PARAM"); v != "" { // as deploy.sh and the Lambda
		name = v
	}
	key, err := ssmkey.Load(ctx, ssm.NewFromConfig(cfg), name)
	if err != nil {
		log.Fatal(err)
	}
	client := &groq.Client{APIKey: key}
	if *record != "" {
		if err := os.MkdirAll(*record, 0o755); err != nil {
			log.Fatal(err)
		}
		client.HTTP = &http.Client{Transport: &recorder{dir: *record}}
	}

	enc := json.NewEncoder(os.Stdout)
	failed := 0
	for i := 1; i <= *runs; i++ {
		if i > 1 {
			time.Sleep(*gap)
		}
		runCtx, cancel := context.WithTimeout(ctx, *timeout)
		rep, err := roundtrip.Run(runCtx, client, *model)
		cancel()
		if err != nil {
			rep.OK = false
			rep.Problems = append(rep.Problems, err.Error())
		}
		if !rep.OK {
			failed++
		}
		enc.Encode(rep)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d of %d runs failed\n", failed, *runs)
		os.Exit(1)
	}
}

// recorder copies each response body to a numbered file as it is read.
// Only responses are saved: requests carry the API key in a header.
type recorder struct {
	dir string
	n   int
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	r.n++
	f, err := os.Create(filepath.Join(r.dir, fmt.Sprintf("%s-%d.sse", time.Now().Format("20060102T150405"), r.n)))
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.TeeReader(resp.Body, f), closeBoth{resp.Body, f}}
	return resp, nil
}

type closeBoth [2]io.Closer

func (c closeBoth) Close() error {
	err := c[0].Close()
	if err2 := c[1].Close(); err == nil {
		err = err2
	}
	return err
}

// Command indexer builds the retrieval index from the Markdown corpus
// (design §5, plan steps 3.4 and 5.3). It reads the same variables as
// `make dev`, from the shell or a git-ignored .env, and a flag overrides them:
//
//	go run ./orchestrator/cmd/indexer -corpus corpus/public:corpus/private -embedder titan
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	cfg "github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/indexing"
)

func main() {
	getenv, err := cfg.WithDotEnv(".env", os.Getenv)
	if err == nil {
		err = run(os.Args[1:], getenv, os.Stdout, bedrockClient)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "indexer:", err)
		os.Exit(1)
	}
}

// bedrockClient creates the Bedrock client Titan uses, through the AWS profile.
func bedrockClient(profile, region string) (embed.Invoker, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithSharedConfigProfile(profile), awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return bedrockruntime.NewFromConfig(awsCfg), nil
}

func run(args []string, getenv cfg.Getenv, out io.Writer, newBedrock func(profile, region string) (embed.Invoker, error)) error {
	env := cfg.New(getenv)
	fs := flag.NewFlagSet("indexer", flag.ContinueOnError)
	s := indexing.Flags(fs, env)
	profile := fs.String("aws-profile", env.String("AWS_PROFILE", "cferrante-dev-admin"), "AWS profile for Titan [AWS_PROFILE]")
	region := fs.String("aws-region", env.String("AWS_REGION", "us-east-1"), "AWS region [AWS_REGION]")
	if err := env.Err(); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	e, err := s.NewEmbedder(func() (embed.Invoker, error) {
		if newBedrock == nil {
			return nil, fmt.Errorf("no Bedrock client")
		}
		return newBedrock(*profile, *region)
	})
	if err != nil {
		return err
	}
	st, err := s.Build(context.Background(), e)
	if err != nil {
		if s.Embedder == "titan" {
			return fmt.Errorf("%w (if your AWS session has expired: aws sso login --profile %s)", err, *profile)
		}
		return err
	}
	fmt.Fprintf(out, "indexed %d chunks (%d public, %d invitee) with %s -> %s in %v",
		st.Chunks, st.Chunks-st.Invitee, st.Invitee, e.Model(), s.IndexPath, st.Elapsed.Round(time.Millisecond))
	if st.Tokens > 0 {
		fmt.Fprintf(out, ", %d Titan tokens", st.Tokens)
	}
	fmt.Fprintln(out)
	return nil
}

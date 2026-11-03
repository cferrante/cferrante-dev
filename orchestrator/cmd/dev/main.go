// Command dev serves the orchestrator on the laptop over Ollama (plan step
// 3.5): it builds the index from the corpus, then serves POST /chat as SSE.
//
//	make dev                      # Ollama: qwen3:4b + nomic-embed-text
//	make dev DEV_FLAGS=-invitee   # as an invitee, so private chunks appear
//	make dev DEV_FLAGS="-provider=groq -embedder=titan"  # the production models
//
// Every option is also an environment variable, read from the shell or a
// git-ignored .env file (see .env.example); a flag overrides both.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/clock"
	cfg "github.com/cferrante/cferrante-dev/orchestrator/config"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
	"github.com/cferrante/cferrante-dev/orchestrator/indexing"
	"github.com/cferrante/cferrante-dev/orchestrator/model"
	"github.com/cferrante/cferrante-dev/orchestrator/model/bedrock"
	"github.com/cferrante/cferrante-dev/orchestrator/model/oai"
	"github.com/cferrante/cferrante-dev/orchestrator/retrieval"
	"github.com/cferrante/cferrante-dev/orchestrator/secrets"
	"github.com/cferrante/cferrante-dev/orchestrator/server"
	"github.com/cferrante/cferrante-dev/orchestrator/suggested"
	"github.com/cferrante/cferrante-dev/orchestrator/tools"
)

type config struct {
	addr, model, effort               string
	idx                               indexing.Settings // corpus, index and embedder, shared with the indexer
	invitee                           bool
	contact, availability             string        // tools' static answers
	deadline                          time.Duration // whole-turn limit
	webDir                            string        // the page (design §14)
	assistant                         string        // the assistant's name, in the system prompt
	suggestedFile                     string        // stored answers for the suggestion chips (design §14)
	provider, groqModel, groqKeyParam string        // CHAT_PROVIDER: ollama | groq
	awsProfile, awsRegion             string        // for SSM and Bedrock
	bedrockModel                      string        // the fallback when CHAT_PROVIDER=groq
}

// parse reads the configuration. Every option has an environment variable
// (design §11), and a flag overrides it: flag > environment > default.
func parse(args []string, getenv cfg.Getenv) (config, error) {
	env := cfg.New(getenv)
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	var c config
	fs.StringVar(&c.addr, "addr", env.String("LISTEN_ADDR", "127.0.0.1:8787"), "listen address, localhost only by default [LISTEN_ADDR]")
	idx := indexing.Flags(fs, env)
	fs.StringVar(&c.model, "model", env.String("CHAT_MODEL", "qwen3:4b"), "Ollama chat model [CHAT_MODEL]")
	fs.StringVar(&c.effort, "effort", env.String("REASONING_EFFORT", ""), "reasoning effort; unset = low for Ollama (qwen3 on a CPU), the provider's default for Groq; none omits it [REASONING_EFFORT]")
	fs.StringVar(&c.provider, "provider", env.String("CHAT_PROVIDER", "ollama"), "ollama or groq [CHAT_PROVIDER]")
	fs.StringVar(&c.groqModel, "groq-model", env.String("GROQ_MODEL", "openai/gpt-oss-120b"), "Groq model [GROQ_MODEL]")
	fs.StringVar(&c.groqKeyParam, "groq-key-param", env.String("GROQ_KEY_PARAM", "/cferrante-dev/groq-api-key"), "SSM SecureString holding the Groq key [GROQ_KEY_PARAM]")
	fs.StringVar(&c.awsProfile, "aws-profile", env.String("AWS_PROFILE", "cferrante-dev-admin"), "AWS profile for SSM [AWS_PROFILE]")
	fs.StringVar(&c.bedrockModel, "bedrock-model", env.String("BEDROCK_MODEL", "us.amazon.nova-lite-v1:0"), "Bedrock fallback model when using Groq [BEDROCK_MODEL]")
	fs.StringVar(&c.awsRegion, "aws-region", env.String("AWS_REGION", "us-east-1"), "AWS region [AWS_REGION]")
	fs.BoolVar(&c.invitee, "invitee", env.Bool("DEV_INVITEE", false), "act as an invitee, who sees private chunks [DEV_INVITEE]")
	fs.StringVar(&c.contact, "contact", env.String("CONTACT_OPTIONS", "Invitees can send a message through this chat."), "get_contact_options answer [CONTACT_OPTIONS]")
	fs.DurationVar(&c.deadline, "turn-deadline", env.Duration("TURN_DEADLINE", 5*time.Minute), "whole-turn limit; generous because CPU inference is slow [TURN_DEADLINE]")
	fs.StringVar(&c.assistant, "assistant", env.String("ASSISTANT_NAME", agent.DefaultAssistantName), "the assistant's name, also shown by the page [ASSISTANT_NAME]")
	fs.StringVar(&c.suggestedFile, "suggested", env.String("SUGGESTED_FILE", "orchestrator/corpus/testdata/suggested.json"), "stored answers for the suggestion chips; empty for none [SUGGESTED_FILE]")
	fs.StringVar(&c.webDir, "web", env.String("WEB_DIR", "web"), "directory of the page, served at / [WEB_DIR]")
	fs.StringVar(&c.availability, "availability", env.String("AVAILABILITY", "Weekdays, office hours (not configured)."), "check_availability answer [AVAILABILITY]")
	if err := env.Err(); err != nil {
		return c, err
	}
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	c.idx = *idx
	switch c.provider {
	case "ollama", "groq":
	default:
		return c, fmt.Errorf("CHAT_PROVIDER %q: want ollama or groq", c.provider)
	}
	switch c.effort {
	case "none": // for models without thinking, which reject the field
		c.effort = ""
	case "":
		if c.provider == "ollama" {
			c.effort = "low" // qwen3's default thinking takes minutes on a CPU
		} // Groq: leave it to the provider's default, as spike 1.5 measured
	}
	return c, nil
}

// setup builds the index and returns the HTTP handler and a cleanup func.
// newTitan creates the Bedrock client, only when EMBEDDER=titan.
func setup(ctx context.Context, c config, m model.Model, logw io.Writer, newTitan func() (embed.Invoker, error)) (http.Handler, func(), error) {
	e, err := c.idx.NewEmbedder(newTitan)
	if err != nil {
		return nil, nil, err
	}
	st, err := c.idx.Build(ctx, e)
	if errors.Is(err, retrieval.ErrEmbed) {
		switch c.idx.Embedder {
		case "ollama":
			err = fmt.Errorf("building the index (is Ollama running?): %w", err)
		case "titan":
			err = fmt.Errorf("building the index: %w (if your AWS session has expired: aws sso login --profile %s)", err, c.awsProfile)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	r, err := retrieval.Load(c.idx.IndexPath, e)
	if err != nil {
		return nil, nil, err
	}
	tokens := ""
	if st.Tokens > 0 {
		tokens = fmt.Sprintf(", %d Titan tokens", st.Tokens)
	}
	fmt.Fprintf(logw, "indexed %d chunks from %s with %s in %v%s\n", st.Chunks, c.idx.CorpusDirs, e.Model(), st.Elapsed.Round(time.Millisecond), tokens)

	// Suggested questions are checked against the index at startup: a bad
	// file stops the server rather than serving a broken answer instantly.
	store := &suggested.Store{}
	if c.suggestedFile != "" {
		if store, err = suggested.Load(c.suggestedFile, r.Chunk); err != nil {
			r.Close()
			return nil, nil, err
		}
	}

	caller := auth.Caller{ID: "dev-visitor"}
	if c.invitee {
		caller = auth.Caller{ID: "dev-invitee", Groups: []string{auth.InviteeGroup}}
	}
	// The outbox prints what confirmed actions send; Postgres and the
	// notification channel [D2] replace it later.
	outbox := &tools.MemoryOutbox{Log: logw}
	a := &agent.Agent{Model: m, Retriever: r, Store: agent.NewMemoryStore(), Clock: clock.Real{},
		Tools:   tools.New(tools.Static{ContactOptions: c.contact, Availability: c.availability}, outbox),
		Pending: tools.NewMemoryPending(), Deadline: c.deadline, AssistantName: c.assistant, Log: logw,
		Suggestions: store}
	mux := http.NewServeMux()
	mux.Handle("/chat", &server.Chat{Agent: a, Auth: auth.Fixed(caller), Clock: clock.Real{}})
	mux.Handle("/", server.Static(c.webDir))
	mux.Handle("/suggested", &server.SuggestedList{Suggestions: store, Auth: auth.Fixed(caller)})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	return mux, func() { r.Close() }, nil
}

func main() {
	// Settings: flag > shell environment > .env in the working directory > default.
	getenv, err := cfg.WithDotEnv(".env", os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	c, err := parse(os.Args[1:], getenv)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m, err := chatModel(ctx, c, ssmSecret(c), nil)
	if err != nil {
		log.Fatal(err)
	}
	h, cleanup, err := setup(ctx, c, m, os.Stdout, func() (embed.Invoker, error) {
		awsCfg, err := loadAWS(ctx, c)
		if err != nil {
			return nil, err
		}
		return bedrockruntime.NewFromConfig(awsCfg), nil
	})
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()
	srv := &http.Server{Addr: c.addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	fmt.Printf("open http://%s/ in a browser to talk to the %s\n", c.addr, c.assistant)
	where := fmt.Sprintf("model %s via Ollama at %s", c.model, c.idx.OllamaURL)
	if c.provider == "groq" {
		where = fmt.Sprintf("model %s via Groq, falling back to %s on Bedrock", c.groqModel, c.bedrockModel)
	}
	effort := c.effort
	if effort == "" {
		effort = "provider default"
	}
	fmt.Printf("serving http://%s/chat (%s, effort %s, invitee=%v); Ctrl+C to stop\n", c.addr, where, effort, c.invitee)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	fmt.Println("stopped")
}

// chatModel builds the model for c.provider; secret reads a parameter from
// SSM (stubbed in tests).
func chatModel(ctx context.Context, c config, secret func(context.Context, string) (string, error), br bedrock.Streamer) (model.Model, error) {
	if c.provider != "groq" {
		return &oai.Client{Provider: "ollama", BaseURL: c.idx.OllamaURL + "/v1", Model: c.model, ReasoningEffort: c.effort}, nil
	}
	key, err := secret(ctx, c.groqKeyParam)
	if err != nil {
		return nil, fmt.Errorf("%w (if your AWS session has expired: aws sso login --profile %s)", err, c.awsProfile)
	}
	groq := &oai.Client{Provider: "groq", BaseURL: oai.GroqBaseURL, APIKey: key, Model: c.groqModel, ReasoningEffort: c.effort}
	if br == nil {
		awsCfg, err := loadAWS(ctx, c)
		if err != nil {
			return nil, err
		}
		br = bedrock.SDK{Client: bedrockruntime.NewFromConfig(awsCfg)}
	}
	// Groq answers; Bedrock takes over only before Groq's first token (design §4).
	return &model.Router{Primary: groq, Fallback: &bedrock.Client{API: br, Model: c.bedrockModel}, Clock: clock.Real{}}, nil
}

// ssmSecret reads parameters through the AWS profile, creating the client on
// first use so Ollama-only runs never touch AWS.
func ssmSecret(c config) func(context.Context, string) (string, error) {
	var store *secrets.SSM
	return func(ctx context.Context, name string) (string, error) {
		if store == nil {
			awsCfg, err := loadAWS(ctx, c)
			if err != nil {
				return "", err
			}
			store = &secrets.SSM{API: ssm.NewFromConfig(awsCfg)}
		}
		return store.Get(ctx, name)
	}
}

// loadAWS reads the AWS profile's configuration, for SSM and Bedrock.
func loadAWS(ctx context.Context, c config) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithSharedConfigProfile(c.awsProfile), awsconfig.WithRegion(c.awsRegion))
}

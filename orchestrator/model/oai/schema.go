package oai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// ErrInvalidOutput is a structured-output reply that isn't a complete JSON
// object, so the caller never receives something in between.
var ErrInvalidOutput = errors.New("oai: structured output is not a complete JSON object")

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type wireCompletion struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

// structured runs a ResponseSchema request (design §4): Groq's structured
// outputs support neither streaming nor tools, so it is one plain call with a
// strict json_schema, whose reply is passed on as one Text event (reasoning
// first, if any). TTFT is the whole call, since nothing arrives earlier.
func (c *Client) structured(ctx context.Context, req model.Request, onEvent func(model.Event) error) (model.Result, error) {
	res := model.Result{Provider: c.Provider, Model: c.Model}
	if len(req.Tools) > 0 {
		return res, errors.New("oai: a ResponseSchema request can't also offer tools (Groq supports neither streaming nor tools with structured output)")
	}
	if err := checkStrict(req.ResponseSchema); err != nil {
		return res, fmt.Errorf("oai: ResponseSchema: %w", err)
	}
	start := time.Now()
	resp, err := c.post(ctx, wireRequest{
		Model: c.Model, Messages: wireMessages(req), ReasoningEffort: c.effort(req), MaxTokens: req.MaxTokens,
		Temperature: req.Temperature,
		ResponseFormat: &responseFormat{Type: "json_schema",
			JSONSchema: jsonSchema{Name: "response", Strict: true, Schema: req.ResponseSchema}},
	}, "application/json")
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	var out wireCompletion
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return res, fmt.Errorf("%w: decoding the reply: %v", ErrInvalidOutput, err)
	}
	res.TTFT = time.Since(start)
	if out.Usage != nil {
		res.TokensIn, res.TokensOut = out.Usage.PromptTokens, out.Usage.CompletionTokens
		res.CachedTokens = out.Usage.PromptDetails.CachedTokens
	}
	if len(out.Choices) == 0 {
		return res, fmt.Errorf("%w: no choices", ErrInvalidOutput)
	}
	ch := out.Choices[0]
	res.StopReason = ch.FinishReason
	if ch.FinishReason != "stop" {
		return res, fmt.Errorf("%w: finish reason %q", ErrInvalidOutput, ch.FinishReason)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ch.Message.Content), &obj); err != nil || obj == nil {
		return res, fmt.Errorf("%w (%d bytes)", ErrInvalidOutput, len(ch.Message.Content))
	}
	if ch.Message.Reasoning != "" {
		if err := onEvent(model.Event{Kind: model.Reasoning, Text: ch.Message.Reasoning}); err != nil {
			return res, err
		}
	}
	return res, onEvent(model.Event{Kind: model.Text, Text: ch.Message.Content})
}

// checkStrict enforces strict mode's rules (Groq docs): every object schema
// sets additionalProperties to false and lists all its properties in
// required. Checked here so a mistake fails offline, not as a live 400.
func checkStrict(raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("schema is not JSON: %w", err)
	}
	root, ok := v.(map[string]any)
	if !ok || root["type"] != "object" {
		return errors.New(`schema must be an object schema ("type": "object")`)
	}
	return walkStrict(root, "$")
}

func walkStrict(s map[string]any, path string) error {
	if s["type"] == "object" {
		if s["additionalProperties"] != false {
			return fmt.Errorf("%s: an object must set additionalProperties to false", path)
		}
		props, _ := s["properties"].(map[string]any)
		req, _ := s["required"].([]any)
		for name, p := range props {
			if !slices.Contains(req, any(name)) {
				return fmt.Errorf("%s: property %q must be in required", path, name)
			}
			if sub, ok := p.(map[string]any); ok {
				if err := walkStrict(sub, path+"."+name); err != nil {
					return err
				}
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		return walkStrict(items, path+"[]")
	}
	return nil
}

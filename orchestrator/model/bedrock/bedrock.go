// Package bedrock is the fallback model provider (design §4): Amazon Bedrock's
// ConverseStream API through the AWS SDK for Go v2, behind model.Model.
package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// Events is a stream of ConverseStream events; the SDK's
// *bedrockruntime.ConverseStreamEventStream satisfies it.
type Events interface {
	Events() <-chan types.ConverseStreamOutput
	Close() error
	Err() error
}

// Streamer starts a ConverseStream call. The SDK's output keeps its stream in
// an unexported field, so tests use this seam instead.
type Streamer interface {
	Stream(ctx context.Context, in *bedrockruntime.ConverseStreamInput) (Events, error)
}

// SDK adapts a *bedrockruntime.Client to Streamer.
type SDK struct{ Client *bedrockruntime.Client }

func (s SDK) Stream(ctx context.Context, in *bedrockruntime.ConverseStreamInput) (Events, error) {
	out, err := s.Client.ConverseStream(ctx, in)
	if err != nil {
		return nil, err
	}
	return out.GetStream(), nil
}

// Client calls one Bedrock model. Safe for concurrent use.
type Client struct {
	API   Streamer
	Model string // e.g. "us.amazon.nova-lite-v1:0"
}

// stopReasons maps Bedrock's stop reasons to the words model/oai uses, so
// logs and cards read the same for every provider.
var stopReasons = map[types.StopReason]string{
	types.StopReasonEndTurn:      "stop",
	types.StopReasonStopSequence: "stop",
	types.StopReasonToolUse:      "tool_calls",
	types.StopReasonMaxTokens:    "length",
}

type pendingTool struct {
	id, name string
	input    strings.Builder
}

// Stream implements model.Model. Text and reasoning are passed on as they
// arrive; a tool call's input arrives in pieces, so each call is passed on
// once, complete, at the end.
func (c *Client) Stream(ctx context.Context, req model.Request, onEvent func(model.Event) error) (model.Result, error) {
	res := model.Result{Provider: "bedrock", Model: c.Model}
	if req.ResponseSchema != nil {
		return res, errors.New("bedrock: ResponseSchema is not supported yet")
	}
	in, err := c.input(req)
	if err != nil {
		return res, err
	}
	start := time.Now()
	stream, err := c.API.Stream(ctx, in)
	if err != nil {
		return res, fmt.Errorf("bedrock: %w", err)
	}
	defer stream.Close()

	var tools []*pendingTool
	think := &thinkSplitter{} // Nova writes its reasoning inline in <thinking> tags
	byIndex := map[int32]*pendingTool{}
	token := func(kind model.EventKind, s string) error {
		if res.TTFT == 0 {
			res.TTFT = time.Since(start)
		}
		return onEvent(model.Event{Kind: kind, Text: s})
	}
	for ev := range stream.Events() {
		switch e := ev.(type) {
		case *types.ConverseStreamOutputMemberContentBlockStart:
			if tu, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				t := &pendingTool{id: aws.ToString(tu.Value.ToolUseId), name: aws.ToString(tu.Value.Name)}
				tools = append(tools, t)
				byIndex[aws.ToInt32(e.Value.ContentBlockIndex)] = t
			}
		case *types.ConverseStreamOutputMemberContentBlockDelta:
			switch d := e.Value.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				for _, p := range think.feed(d.Value) {
					if err := token(p.kind, p.text); err != nil {
						return res, err
					}
				}
			case *types.ContentBlockDeltaMemberReasoningContent:
				if r, ok := d.Value.(*types.ReasoningContentBlockDeltaMemberText); ok {
					if err := token(model.Reasoning, r.Value); err != nil {
						return res, err
					}
				}
			case *types.ContentBlockDeltaMemberToolUse:
				if t := byIndex[aws.ToInt32(e.Value.ContentBlockIndex)]; t != nil {
					t.input.WriteString(aws.ToString(d.Value.Input))
				}
			}
		case *types.ConverseStreamOutputMemberMessageStop:
			if s, ok := stopReasons[e.Value.StopReason]; ok {
				res.StopReason = s
			} else {
				res.StopReason = string(e.Value.StopReason)
			}
		case *types.ConverseStreamOutputMemberMetadata:
			if u := e.Value.Usage; u != nil {
				res.TokensIn = int(aws.ToInt32(u.InputTokens))
				res.TokensOut = int(aws.ToInt32(u.OutputTokens))
				res.CachedTokens = int(aws.ToInt32(u.CacheReadInputTokens))
			}
		}
	}
	if err := stream.Err(); err != nil {
		return res, fmt.Errorf("bedrock: stream: %w", err)
	}
	for _, p := range think.flush() {
		if err := token(p.kind, p.text); err != nil {
			return res, err
		}
	}
	for _, t := range tools {
		args := t.input.String()
		if args == "" {
			args = "{}"
		}
		call := &model.ToolCall{ID: t.id, Name: t.name, Arguments: json.RawMessage(args)}
		if err := onEvent(model.Event{Kind: model.ToolCallEvent, ToolCall: call}); err != nil {
			return res, err
		}
	}
	return res, nil
}

// input builds the request. Bedrock needs user and assistant turns to
// alternate, so tool results (role "tool" here) are sent as user content and
// consecutive ones share one message.
func (c *Client) input(req model.Request) (*bedrockruntime.ConverseStreamInput, error) {
	in := &bedrockruntime.ConverseStreamInput{ModelId: aws.String(c.Model)}
	if req.System != "" {
		in.System = []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: req.System}}
	}
	if req.MaxTokens > 0 || req.Temperature != nil {
		in.InferenceConfig = &types.InferenceConfiguration{}
		if req.MaxTokens > 0 {
			in.InferenceConfig.MaxTokens = aws.Int32(int32(req.MaxTokens))
		}
		if req.Temperature != nil {
			in.InferenceConfig.Temperature = aws.Float32(float32(*req.Temperature))
		}
	}
	for _, m := range req.Messages {
		var role types.ConversationRole
		var content []types.ContentBlock
		switch m.Role {
		case model.User:
			role = types.ConversationRoleUser
			content = append(content, &types.ContentBlockMemberText{Value: m.Content})
		case model.Assistant:
			role = types.ConversationRoleAssistant
			if m.Content != "" {
				content = append(content, &types.ContentBlockMemberText{Value: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input, err := jsonDoc(tc.Arguments)
				if err != nil {
					return nil, fmt.Errorf("bedrock: tool call %s arguments: %w", tc.ID, err)
				}
				content = append(content, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String(tc.ID), Name: aws.String(tc.Name), Input: input}})
			}
		case model.ToolRole:
			role = types.ConversationRoleUser
			content = append(content, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
				ToolUseId: aws.String(m.ToolCallID),
				Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: m.Content}}}})
		}
		if n := len(in.Messages); n > 0 && in.Messages[n-1].Role == role && m.Role == model.ToolRole {
			in.Messages[n-1].Content = append(in.Messages[n-1].Content, content...)
			continue
		}
		in.Messages = append(in.Messages, types.Message{Role: role, Content: content})
	}
	if len(req.Tools) > 0 {
		cfg := &types.ToolConfiguration{}
		for _, t := range req.Tools {
			schema, err := jsonDoc(t.Parameters)
			if err != nil {
				return nil, fmt.Errorf("bedrock: tool %s schema: %w", t.Name, err)
			}
			cfg.Tools = append(cfg.Tools, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
				Name: aws.String(t.Name), Description: aws.String(t.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{Value: schema}}})
		}
		in.ToolConfig = cfg
	}
	return in, nil
}

// jsonDoc turns raw JSON into the SDK's document type; empty means {}.
func jsonDoc(raw json.RawMessage) (document.Interface, error) {
	var v any = map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
	}
	return document.NewLazyDocument(v), nil
}

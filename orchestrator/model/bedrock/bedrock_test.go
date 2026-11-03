package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// fakeEvents replays SDK events through a channel, like the real stream.
type fakeEvents struct {
	ch     chan types.ConverseStreamOutput
	err    error
	closed bool
}

func (f *fakeEvents) Events() <-chan types.ConverseStreamOutput { return f.ch }
func (f *fakeEvents) Close() error                              { f.closed = true; return nil }
func (f *fakeEvents) Err() error                                { return f.err }

type fakeAPI struct {
	in     *bedrockruntime.ConverseStreamInput
	events []types.ConverseStreamOutput
	gap    time.Duration
	err    error // from the call itself
	tail   error // from the stream after its events
	stream *fakeEvents
}

func (f *fakeAPI) Stream(_ context.Context, in *bedrockruntime.ConverseStreamInput) (Events, error) {
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	f.stream = &fakeEvents{ch: make(chan types.ConverseStreamOutput), err: f.tail}
	go func() {
		defer close(f.stream.ch)
		for _, e := range f.events {
			time.Sleep(f.gap)
			f.stream.ch <- e
		}
	}()
	return f.stream, nil
}

func text(i int32, s string) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{
		ContentBlockIndex: aws.Int32(i), Delta: &types.ContentBlockDeltaMemberText{Value: s}}}
}

func stop(r types.StopReason) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: r}}
}

func usage(in, out, cached int32) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{
		Usage: &types.TokenUsage{InputTokens: aws.Int32(in), OutputTokens: aws.Int32(out), CacheReadInputTokens: aws.Int32(cached)}}}
}

func collect(t *testing.T, c *Client, req model.Request) ([]model.Event, model.Result) {
	t.Helper()
	var evs []model.Event
	res, err := c.Stream(context.Background(), req, func(e model.Event) error { evs = append(evs, e); return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return evs, res
}

func TestTextStreamsAsTextEvents(t *testing.T) {
	api := &fakeAPI{gap: 5 * time.Millisecond, events: []types.ConverseStreamOutput{
		&types.ConverseStreamOutputMemberMessageStart{Value: types.MessageStartEvent{Role: types.ConversationRoleAssistant}},
		text(0, "Alpha uses "), text(0, "a C++ engine [1]."),
		&types.ConverseStreamOutputMemberContentBlockStop{Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
		stop(types.StopReasonEndTurn), usage(1500, 40, 1024),
	}}
	evs, res := collect(t, &Client{API: api, Model: "us.amazon.nova-lite-v1:0"}, model.Request{})
	want := []model.Event{{Kind: model.Text, Text: "Alpha uses "}, {Kind: model.Text, Text: "a C++ engine [1]."}}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("events = %+v", evs)
	}
	if res.Provider != "bedrock" || res.Model != "us.amazon.nova-lite-v1:0" || res.StopReason != "stop" ||
		res.TokensIn != 1500 || res.TokensOut != 40 || res.CachedTokens != 1024 || res.TTFT <= 0 {
		t.Errorf("result = %+v", res)
	}
	if !api.stream.closed {
		t.Error("stream not closed")
	}
}

func TestToolUseBecomesOneCompleteToolCall(t *testing.T) {
	api := &fakeAPI{events: []types.ConverseStreamOutput{
		&types.ConverseStreamOutputMemberContentBlockStart{Value: types.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(1),
			Start: &types.ContentBlockStartMemberToolUse{Value: types.ToolUseBlockStart{ToolUseId: aws.String("tu_1"), Name: aws.String("send_message")}}}},
		&types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(1),
			Delta: &types.ContentBlockDeltaMemberToolUse{Value: types.ToolUseBlockDelta{Input: aws.String(`{"message":`)}}}},
		&types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(1),
			Delta: &types.ContentBlockDeltaMemberToolUse{Value: types.ToolUseBlockDelta{Input: aws.String(`"hi"}`)}}}},
		stop(types.StopReasonToolUse),
	}}
	evs, res := collect(t, &Client{API: api}, model.Request{})
	if len(evs) != 1 || evs[0].Kind != model.ToolCallEvent || evs[0].ToolCall.ID != "tu_1" ||
		evs[0].ToolCall.Name != "send_message" || string(evs[0].ToolCall.Arguments) != `{"message":"hi"}` {
		t.Fatalf("events = %+v", evs)
	}
	if res.StopReason != "tool_calls" {
		t.Errorf("stop = %q", res.StopReason)
	}
}

func TestReasoningDeltasAreReasoningEvents(t *testing.T) {
	api := &fakeAPI{events: []types.ConverseStreamOutput{
		&types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0),
			Delta: &types.ContentBlockDeltaMemberReasoningContent{Value: &types.ReasoningContentBlockDeltaMemberText{Value: "thinking"}}}},
		text(1, "answer"), stop(types.StopReasonMaxTokens),
	}}
	evs, res := collect(t, &Client{API: api}, model.Request{})
	if len(evs) != 2 || evs[0].Kind != model.Reasoning || evs[0].Text != "thinking" || evs[1].Kind != model.Text {
		t.Errorf("events = %+v", evs)
	}
	if res.StopReason != "length" {
		t.Errorf("stop = %q", res.StopReason)
	}
}

func TestRequestMapping(t *testing.T) {
	api := &fakeAPI{events: []types.ConverseStreamOutput{stop(types.StopReasonEndTurn)}}
	c := &Client{API: api, Model: "m"}
	collect(t, c, model.Request{
		System: "Be brief.",
		Messages: []model.Message{
			{Role: model.User, Content: "earlier question"},
			{Role: model.Assistant, Content: "earlier answer"},
			{Role: model.User, Content: "send a message"},
			{Role: model.Assistant, ToolCalls: []model.ToolCall{
				{ID: "t1", Name: "send_message", Arguments: json.RawMessage(`{"message":"hi"}`)},
				{ID: "t2", Name: "get_contact_options", Arguments: json.RawMessage(`{}`)},
			}},
			{Role: model.ToolRole, ToolCallID: "t1", Content: `{"status":"awaiting_confirmation"}`},
			{Role: model.ToolRole, ToolCallID: "t2", Content: `{"contact_options":"x"}`},
		},
		Tools:     []model.ToolSpec{{Name: "send_message", Description: "Send it.", Parameters: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`)}},
		MaxTokens: 800,
	})
	in := api.in
	if in == nil {
		t.Fatal("no request was sent")
	}
	if aws.ToString(in.ModelId) != "m" || len(in.System) != 1 || in.System[0].(*types.SystemContentBlockMemberText).Value != "Be brief." {
		t.Fatalf("model/system = %v / %+v", aws.ToString(in.ModelId), in.System)
	}
	if in.InferenceConfig == nil || aws.ToInt32(in.InferenceConfig.MaxTokens) != 800 {
		t.Errorf("inference config = %+v", in.InferenceConfig)
	}
	// Bedrock needs alternating roles: both tool results go in one user message.
	var roles []string
	for _, m := range in.Messages {
		roles = append(roles, string(m.Role))
	}
	if strings.Join(roles, ",") != "user,assistant,user,assistant,user" {
		t.Fatalf("roles = %v", roles)
	}
	asst := in.Messages[3].Content
	if len(asst) != 2 { // no empty text block, just the two tool uses
		t.Fatalf("assistant content = %+v", asst)
	}
	tu := asst[0].(*types.ContentBlockMemberToolUse).Value
	if aws.ToString(tu.ToolUseId) != "t1" || aws.ToString(tu.Name) != "send_message" {
		t.Errorf("tool use = %+v", tu)
	}
	results := in.Messages[4].Content
	if len(results) != 2 || aws.ToString(results[1].(*types.ContentBlockMemberToolResult).Value.ToolUseId) != "t2" {
		t.Errorf("tool results = %+v", results)
	}
	spec := in.ToolConfig.Tools[0].(*types.ToolMemberToolSpec).Value
	if aws.ToString(spec.Name) != "send_message" || aws.ToString(spec.Description) != "Send it." || spec.InputSchema == nil {
		t.Errorf("tool spec = %+v", spec)
	}
}

func TestErrors(t *testing.T) {
	throttled := errors.New("ThrottlingException: too many requests")
	if _, err := (&Client{API: &fakeAPI{err: throttled}}).Stream(context.Background(), model.Request{}, func(model.Event) error { return nil }); !errors.Is(err, throttled) {
		t.Errorf("call error = %v", err)
	}
	broken := errors.New("stream reset")
	api := &fakeAPI{events: []types.ConverseStreamOutput{text(0, "half")}, tail: broken}
	if _, err := (&Client{API: api}).Stream(context.Background(), model.Request{}, func(model.Event) error { return nil }); !errors.Is(err, broken) {
		t.Errorf("stream error = %v", err)
	}
	gone := errors.New("client went away")
	api = &fakeAPI{events: []types.ConverseStreamOutput{text(0, "a"), text(0, "b"), stop(types.StopReasonEndTurn)}}
	if _, err := (&Client{API: api}).Stream(context.Background(), model.Request{}, func(model.Event) error { return gone }); !errors.Is(err, gone) || !api.stream.closed {
		t.Errorf("callback error = %v, closed %v", err, api.stream.closed)
	}
	if _, err := (&Client{API: &fakeAPI{}}).Stream(context.Background(), model.Request{ResponseSchema: json.RawMessage(`{}`)}, func(model.Event) error { return nil }); err == nil {
		t.Error("ResponseSchema: want a clear not-supported error")
	}
}

func TestNovaThinkingIsStreamedAsReasoning(t *testing.T) {
	api := &fakeAPI{events: []types.ConverseStreamOutput{
		text(0, "<thinking"), text(0, "> The visitor asks. </"), text(0, "thinking>\n\nAnswer [1]."), stop(types.StopReasonEndTurn),
	}}
	evs, _ := collect(t, &Client{API: api}, model.Request{})
	var reasoning, answer string
	for _, e := range evs {
		switch e.Kind {
		case model.Reasoning:
			reasoning += e.Text
		case model.Text:
			answer += e.Text
		}
	}
	if reasoning != " The visitor asks. " || answer != "Answer [1]." {
		t.Errorf("reasoning %q, answer %q", reasoning, answer)
	}
}

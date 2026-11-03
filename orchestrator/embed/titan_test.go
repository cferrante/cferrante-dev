package embed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// fakeInvoker records each InvokeModel call and answers with reply.
type fakeInvoker struct {
	calls []*bedrockruntime.InvokeModelInput
	reply func(body map[string]any) (string, error)
}

func (f *fakeInvoker) InvokeModel(_ context.Context, in *bedrockruntime.InvokeModelInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	f.calls = append(f.calls, in)
	var body map[string]any
	if err := json.Unmarshal(in.Body, &body); err != nil {
		return nil, err
	}
	out, err := f.reply(body)
	if err != nil {
		return nil, err
	}
	return &bedrockruntime.InvokeModelOutput{Body: []byte(out), ContentType: aws.String("application/json")}, nil
}

// vecOf returns a JSON embedding of size n whose first value encodes the text's length.
func vecOf(text string, n int) string {
	v := make([]float32, n)
	v[0] = float32(len(text))
	raw, _ := json.Marshal(map[string]any{"embedding": v, "inputTextTokenCount": len(strings.Fields(text))})
	return string(raw)
}

func TestTitanRequestAndResponseShape(t *testing.T) {
	f := &fakeInvoker{reply: func(body map[string]any) (string, error) {
		return vecOf(body["inputText"].(string), 512), nil
	}}
	e, err := NewTitan(f, "amazon.titan-embed-text-v2:0", 512)
	if err != nil {
		t.Fatal(err)
	}
	if e.Model() != "bedrock/amazon.titan-embed-text-v2:0/512" || e.Dim() != 512 {
		t.Errorf("Model %q Dim %d", e.Model(), e.Dim())
	}
	got, err := e.Embed(context.Background(), []string{"one", "two words"})
	if err != nil {
		t.Fatal(err)
	}
	// Titan v2 embeds one text per call.
	if len(f.calls) != 2 {
		t.Fatalf("%d calls, want 2", len(f.calls))
	}
	for i, text := range []string{"one", "two words"} {
		in := f.calls[i]
		if aws.ToString(in.ModelId) != "amazon.titan-embed-text-v2:0" ||
			aws.ToString(in.ContentType) != "application/json" || aws.ToString(in.Accept) != "application/json" {
			t.Errorf("call %d: model %q content type %q accept %q", i, aws.ToString(in.ModelId), aws.ToString(in.ContentType), aws.ToString(in.Accept))
		}
		var body map[string]any
		json.Unmarshal(in.Body, &body)
		if body["inputText"] != text || body["dimensions"] != float64(512) || body["normalize"] != true || len(body) != 3 {
			t.Errorf("call %d body = %v", i, body)
		}
		if len(got[i]) != 512 || got[i][0] != float32(len(text)) {
			t.Errorf("vector %d: size %d, first %v (want the vector for %q, in order)", i, len(got[i]), got[i][0], text)
		}
	}
	if e.Tokens() != 3 {
		t.Errorf("Tokens() = %d, want 3 (1 + 2)", e.Tokens())
	}
}

func TestTitanErrors(t *testing.T) {
	for name, reply := range map[string]func(map[string]any) (string, error){
		"api error":  func(map[string]any) (string, error) { return "", errors.New("AccessDeniedException") },
		"wrong size": func(b map[string]any) (string, error) { return vecOf("x", 256), nil },
		"bad json":   func(map[string]any) (string, error) { return "{", nil },
		"no vector":  func(map[string]any) (string, error) { return `{"inputTextTokenCount":1}`, nil },
	} {
		e, _ := NewTitan(&fakeInvoker{reply: reply}, "amazon.titan-embed-text-v2:0", 512)
		_, err := e.Embed(context.Background(), []string{"x"})
		if err == nil || !strings.Contains(err.Error(), "titan") {
			t.Errorf("%s: err = %v, want an error naming titan", name, err)
		}
	}
}

func TestTitanDimensions(t *testing.T) {
	for _, d := range []int{256, 512, 1024} {
		if _, err := NewTitan(&fakeInvoker{}, "m", d); err != nil {
			t.Errorf("dim %d: %v", d, err)
		}
	}
	for _, d := range []int{0, 768, 100} {
		if _, err := NewTitan(&fakeInvoker{}, "m", d); err == nil || !strings.Contains(err.Error(), "256, 512 or 1024") {
			t.Errorf("dim %d: err = %v, want the allowed sizes", d, err)
		}
	}
}

func TestTitanEmbedsNothingWithoutACall(t *testing.T) {
	f := &fakeInvoker{}
	e, _ := NewTitan(f, "m", 256)
	if v, err := e.Embed(context.Background(), nil); v != nil || err != nil || len(f.calls) != 0 {
		t.Errorf("got %v, %v after %d calls", v, err, len(f.calls))
	}
}

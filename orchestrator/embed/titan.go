package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// TitanModel is Amazon Titan Text Embeddings v2 on Bedrock ([D1]).
const TitanModel = "amazon.titan-embed-text-v2:0"

// Invoker calls a Bedrock model once; *bedrockruntime.Client satisfies it.
type Invoker interface {
	InvokeModel(ctx context.Context, in *bedrockruntime.InvokeModelInput, opts ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error)
}

// Titan embeds through Bedrock's InvokeModel. Titan v2 takes one text per
// call and returns a normalized vector of 256, 512 or 1024 values. Safe for
// concurrent use.
type Titan struct {
	api    Invoker
	name   string
	d      int
	tokens atomic.Int64
}

// NewTitan checks the size, since Titan v2 accepts only three.
func NewTitan(api Invoker, model string, dim int) (*Titan, error) {
	switch dim {
	case 256, 512, 1024:
	default:
		return nil, fmt.Errorf("titan: dimension %d: want 256, 512 or 1024", dim)
	}
	return &Titan{api: api, name: model, d: dim}, nil
}

func (t *Titan) Model() string { return fmt.Sprintf("bedrock/%s/%d", t.name, t.d) }
func (t *Titan) Dim() int      { return t.d }

// Tokens is the total input tokens Titan has counted, which is what it bills.
func (t *Titan) Tokens() int64 { return t.tokens.Load() }

// Embed calls Titan once per text, in order.
func (t *Titan) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		body, err := json.Marshal(map[string]any{"inputText": text, "dimensions": t.d, "normalize": true})
		if err != nil {
			return nil, err
		}
		resp, err := t.api.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
			ModelId: aws.String(t.name), Body: body,
			ContentType: aws.String("application/json"), Accept: aws.String("application/json"),
		})
		if err != nil {
			return nil, fmt.Errorf("titan %s: %w", t.name, err)
		}
		var r struct {
			Embedding  []float32 `json:"embedding"`
			TokenCount int64     `json:"inputTextTokenCount"`
		}
		if err := json.Unmarshal(resp.Body, &r); err != nil {
			return nil, fmt.Errorf("titan %s: decode: %w", t.name, err)
		}
		if len(r.Embedding) != t.d {
			return nil, fmt.Errorf("titan %s: vector %d has size %d, want %d", t.name, i, len(r.Embedding), t.d)
		}
		t.tokens.Add(r.TokenCount)
		out[i] = r.Embedding
	}
	return out, nil
}

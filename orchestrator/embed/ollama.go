package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OllamaBaseURL is Ollama's native API on the laptop.
const OllamaBaseURL = "http://localhost:11434"

// Ollama embeds with a local Ollama model through its native /api/embed.
type Ollama struct {
	BaseURL string // default OllamaBaseURL
	Name    string // e.g. "nomic-embed-text"
	D       int    // the model's vector size, e.g. 768
	HTTP    *http.Client
}

// Model names the model and size, so an index built with one is never
// queried with another.
func (o Ollama) Model() string { return fmt.Sprintf("ollama/%s/%d", o.Name, o.D) }
func (o Ollama) Dim() int      { return o.D }

// Embed sends the texts in one request and checks the reply has one vector
// of the expected size per text.
func (o Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"model": o.Name, "input": texts})
	if err != nil {
		return nil, err
	}
	base := o.BaseURL
	if base == "" {
		base = OllamaBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := o.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama embed: HTTP %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama embed: decode: %w", err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embed: got %d vectors for %d texts", len(out.Embeddings), len(texts))
	}
	for i, v := range out.Embeddings {
		if len(v) != o.D {
			return nil, fmt.Errorf("ollama embed: vector %d has size %d, want %d", i, len(v), o.D)
		}
	}
	return out.Embeddings, nil
}

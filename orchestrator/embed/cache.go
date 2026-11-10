package embed

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// cacheFile is the committed embedding cache (design §10): one vector per
// text, keyed by the text's SHA-256, as base64 of little-endian float32s.
// encoding/json writes map keys sorted, so a refresh diffs cleanly.
type cacheFile struct {
	Model   string            `json:"model"`
	Dim     int               `json:"dim"`
	Vectors map[string]string `json:"vectors"`
}

func textKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Cache answers only from saved vectors, so CI gets the production
// embedder's results with no model, network call or secret. It reports the
// recorded model's name, so an index built from it records that model.
type Cache struct {
	model string
	d     int
	vecs  map[string][]float32
}

// LoadCache reads a cache file, refusing any vector whose length isn't dim.
func LoadCache(path string) (*Cache, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f cacheFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Model == "" || f.Dim <= 0 {
		return nil, fmt.Errorf("%s: no model or dim", path)
	}
	c := &Cache{model: f.Model, d: f.Dim, vecs: make(map[string][]float32, len(f.Vectors))}
	for key, enc := range f.Vectors {
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("%s: vector %s: %w", path, key, err)
		}
		if len(b)%4 != 0 || len(b)/4 != f.Dim {
			return nil, fmt.Errorf("%s: vector %s has %d values, want %d", path, key, len(b)/4, f.Dim)
		}
		v := make([]float32, f.Dim)
		for i := range v {
			v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		}
		c.vecs[key] = v
	}
	return c, nil
}

func (c *Cache) Model() string { return c.model }
func (c *Cache) Dim() int      { return c.d }

// Embed returns the saved vectors. A text the cache lacks is an error naming
// its start: scoring an edited document against stale vectors would pass or
// fail for the wrong reason.
func (c *Cache) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, ok := c.vecs[textKey(t)]
		if !ok {
			return nil, fmt.Errorf("no cached vector for %q (the text changed or is new): run make embed-cache", preview(t))
		}
		out[i] = v
	}
	return out, nil
}

// preview is the first 60 bytes of a text, cut at a word, for error messages.
func preview(t string) string {
	const max = 60
	if len(t) <= max {
		return t
	}
	cut := max
	for cut > 0 && t[cut] != ' ' {
		cut--
	}
	if cut == 0 {
		cut = max
	}
	return t[:cut] + "…"
}

// Recorder wraps an embedder and keeps every vector it returns, so a run with
// the real model can be saved as a cache. Safe for concurrent use.
type Recorder struct {
	Embedder
	mu   sync.Mutex
	vecs map[string][]float32
}

func NewRecorder(e Embedder) *Recorder {
	return &Recorder{Embedder: e, vecs: map[string][]float32{}}
}

func (r *Recorder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out, err := r.Embedder.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, t := range texts {
		r.vecs[textKey(t)] = out[i]
	}
	return out, nil
}

// Save writes every recorded vector, atomically: a failed write leaves the
// old cache in place.
func (r *Recorder) Save(path string) error {
	r.mu.Lock()
	f := cacheFile{Model: r.Model(), Dim: r.Dim(), Vectors: make(map[string]string, len(r.vecs))}
	for key, v := range r.vecs {
		b := make([]byte, 4*len(v))
		for i, x := range v {
			binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
		}
		f.Vectors[key] = base64.StdEncoding.EncodeToString(b)
	}
	r.mu.Unlock()
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cache-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

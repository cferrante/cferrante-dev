// Package retrieval builds the index from corpus chunks and serves searches
// over it (design §5): the engine holds vectors and keyword postings, and a
// sidecar JSON file next to it holds each chunk's id, title, URL and text.
package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	rag "github.com/cferrante/cferrante-dev/engine/go"
	"github.com/cferrante/cferrante-dev/orchestrator/agent"
	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/corpus"
	"github.com/cferrante/cferrante-dev/orchestrator/embed"
)

// SidecarVersion is the sidecar format version.
const SidecarVersion = 1

// Sidecar is the JSON file written next to an index, at "<index>.json".
type Sidecar struct {
	Version   int            `json:"version"`
	Embedding EmbeddingInfo  `json:"embedding"`
	Chunks    []SidecarChunk `json:"chunks"` // position = engine chunk id
}

// EmbeddingInfo records which model built the index's vectors.
type EmbeddingInfo struct {
	Model string `json:"model"`
	Dim   int    `json:"dim"`
}

// SidecarChunk is one chunk's metadata.
type SidecarChunk struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Heading string `json:"heading,omitempty"`
	URL     string `json:"url"`
	Access  string `json:"access"`
	Text    string `json:"text"`
}

// batchSize is how many texts go to the embedder per call.
const batchSize = 32

// indexText is what gets embedded and keyword-indexed for a chunk: the
// document title and heading make a chunk findable by them, not only by its
// body text.
func indexText(c corpus.Chunk) string {
	head := c.Title
	if c.Heading != "" {
		head += " — " + c.Heading
	}
	return head + "\n\n" + c.Text
}

func accessBits(access string) uint32 {
	if access == "invitee" {
		return auth.Invitee
	}
	return auth.Public
}

// Build embeds the chunks and writes the index and its sidecar. Both are
// written to temporary files in the target directory and renamed into place,
// sidecar first, so a failed build leaves the previous index untouched and
// no partial files behind.
func Build(ctx context.Context, chunks []corpus.Chunk, e embed.Embedder, indexPath string) (err error) {
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = indexText(c)
	}
	vecs := make([][]float32, 0, len(chunks))
	for i := 0; i < len(texts); i += batchSize {
		batch, err := e.Embed(ctx, texts[i:min(i+batchSize, len(texts))])
		if err != nil {
			return fmt.Errorf("embed: %w", err)
		}
		vecs = append(vecs, batch...)
	}

	b, err := rag.NewBuilder(e.Dim())
	if err != nil {
		return err
	}
	defer b.Close()
	sc := Sidecar{Version: SidecarVersion, Embedding: EmbeddingInfo{Model: e.Model(), Dim: e.Dim()}}
	for i, c := range chunks {
		if err := b.Add(texts[i], vecs[i], accessBits(c.Access)); err != nil {
			return fmt.Errorf("chunk %s: %w", c.ID, err)
		}
		sc.Chunks = append(sc.Chunks, SidecarChunk{
			ID: c.ID, Title: c.Title, Heading: c.Heading, URL: c.URL, Access: c.Access, Text: c.Text,
		})
	}

	dir := filepath.Dir(indexPath)
	tmpIndex, err := tempName(dir, ".index-*")
	if err != nil {
		return err
	}
	tmpSidecar, err := tempName(dir, ".sidecar-*")
	if err != nil {
		os.Remove(tmpIndex)
		return err
	}
	defer func() {
		if err != nil { // clean up whatever wasn't renamed into place
			os.Remove(tmpIndex)
			os.Remove(tmpSidecar)
		}
	}()
	if err := b.Write(tmpIndex); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpSidecar, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpSidecar, indexPath+".json"); err != nil {
		return err
	}
	return os.Rename(tmpIndex, indexPath)
}

// tempName reserves a unique file name in dir for an atomic replace.
func tempName(dir, pattern string) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	return name, f.Close()
}

// Retriever searches a loaded index. Safe for concurrent use.
type Retriever struct {
	index    *rag.Index
	chunks   []SidecarChunk
	embedder embed.Embedder
}

// Load opens an index and its sidecar and checks that they describe the same
// chunks and were built with the same embedding model as e: vectors from
// different models aren't comparable, so a mismatch would quietly return
// nonsense.
func Load(indexPath string, e embed.Embedder) (*Retriever, error) {
	raw, err := os.ReadFile(indexPath + ".json")
	if err != nil {
		return nil, fmt.Errorf("sidecar: %w", err)
	}
	var sc Sidecar
	if err := json.Unmarshal(raw, &sc); err != nil {
		return nil, fmt.Errorf("sidecar: %w", err)
	}
	if sc.Version != SidecarVersion {
		return nil, fmt.Errorf("sidecar version %d, want %d", sc.Version, SidecarVersion)
	}
	if sc.Embedding.Model != e.Model() || sc.Embedding.Dim != e.Dim() {
		return nil, fmt.Errorf("index built with %s (dim %d), but the embedder is %s (dim %d): rebuild the index",
			sc.Embedding.Model, sc.Embedding.Dim, e.Model(), e.Dim())
	}
	ix, err := rag.Load(indexPath)
	if err != nil {
		return nil, err
	}
	if ix.Count() != len(sc.Chunks) || ix.Dim() != e.Dim() {
		ix.Close()
		return nil, fmt.Errorf("index has %d chunks of dim %d, sidecar has %d of dim %d: rebuild the index",
			ix.Count(), ix.Dim(), len(sc.Chunks), sc.Embedding.Dim)
	}
	return &Retriever{index: ix, chunks: sc.Chunks, embedder: e}, nil
}

// Retrieve implements agent.Retriever.
func (r *Retriever) Retrieve(ctx context.Context, query string, mask uint32, k int) ([]agent.Chunk, error) {
	vecs, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	hits, err := r.index.Search(vecs[0], query, mask, k)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Chunk, len(hits))
	for i, h := range hits {
		c := r.chunks[h.ChunkID]
		out[i] = agent.Chunk{ID: c.ID, Title: c.Title, URL: c.URL, Text: c.Text, Score: h.Score}
	}
	return out, nil
}

// Close frees the index.
func (r *Retriever) Close() error { return r.index.Close() }

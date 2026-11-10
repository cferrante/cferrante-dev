package embed

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A recorded run, saved and loaded back, must give the same vectors and
// report the recorded model, so an index built from the cache says Titan.
func TestRecordedVectorsRoundTripThroughTheCache(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.json")
	rec := NewRecorder(Hash{D: 8})
	texts := []string{"call records from switches", "kiln firings", ""}
	want, err := rec.Embed(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Save(path); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model() != "hash-v1-8" || c.Dim() != 8 {
		t.Errorf("model %q dim %d, want hash-v1-8 and 8", c.Model(), c.Dim())
	}
	got, err := c.Embed(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cached vectors differ:\n got %v\nwant %v", got, want)
	}
}

// An edited document must never be scored against stale vectors.
func TestATextMissingFromTheCacheStopsTheRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	rec := NewRecorder(Hash{D: 8})
	if _, err := rec.Embed(context.Background(), []string{"known text"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Save(path); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Embed(context.Background(), []string{"known text", "Colleen re-architected the system that read binary call detail record files from all 12 switches"})
	if err == nil {
		t.Fatal("a text missing from the cache was embedded")
	}
	for _, want := range []string{`"Colleen re-architected the system that read binary call…"`, "make embed-cache"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestACacheWithAVectorOfTheWrongLengthIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	// One float32 (4 bytes, base64 "AAAAAA==") where dim says 2.
	os.WriteFile(path, []byte(`{"model":"m","dim":2,"vectors":{"abc":"AAAAAA=="}}`), 0o644)
	_, err := LoadCache(path)
	if err == nil || !strings.Contains(err.Error(), "abc") || !strings.Contains(err.Error(), "1 values, want 2") {
		t.Errorf("err = %v, want it to name the entry and the sizes", err)
	}
}

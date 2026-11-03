package bedrock

import (
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// split runs chunks through a splitter and joins adjacent pieces of a kind.
func split(chunks ...string) []piece {
	s := &thinkSplitter{}
	var out []piece
	add := func(ps []piece) {
		for _, p := range ps {
			if p.text == "" {
				continue
			}
			if n := len(out); n > 0 && out[n-1].kind == p.kind {
				out[n-1].text += p.text
			} else {
				out = append(out, p)
			}
		}
	}
	for _, c := range chunks {
		add(s.feed(c))
	}
	add(s.flush())
	return out
}

// Recorded from Nova Lite via the fallback on 2026-11-02.
const nova = "<thinking> The visitor is asking how Project Alpha performs its search. </thinking>\n\n\nAlpha uses a C++ engine for retrieval, which fuses keyword and vector search [1]."

func TestThinkingBecomesReasoningAndTheAnswerStaysClean(t *testing.T) {
	got := split(nova)
	want := []piece{
		{model.Reasoning, " The visitor is asking how Project Alpha performs its search. "},
		{model.Text, "Alpha uses a C++ engine for retrieval, which fuses keyword and vector search [1]."},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("pieces = %+v", got)
	}
}

func TestTagsSplitAtAnyPointAreRecognised(t *testing.T) {
	want := split(nova)
	for i := 1; i < len(nova); i++ {
		got := split(nova[:i], nova[i:])
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("split at %d: %+v", i, got)
		}
	}
	// And one character at a time.
	var chars []string
	for _, r := range nova {
		chars = append(chars, string(r))
	}
	if got := split(chars...); len(got) != 2 || got[1] != want[1] {
		t.Errorf("char by char: %+v", got)
	}
}

func TestTextWithoutTagsPassesThrough(t *testing.T) {
	got := split("Use a < b and <b>bold</b> ", "freely.")
	if len(got) != 1 || got[0] != (piece{model.Text, "Use a < b and <b>bold</b> freely."}) {
		t.Errorf("pieces = %+v", got)
	}
}

func TestAnUnclosedThinkingBlockIsReasoning(t *testing.T) {
	got := split("<thinking>still going")
	if len(got) != 1 || got[0].kind != model.Reasoning || got[0].text != "still going" {
		t.Errorf("pieces = %+v", got)
	}
}

func TestATrailingPartialTagIsKeptAsText(t *testing.T) {
	got := split("Answer ends with <thin")
	if len(got) != 1 || got[0] != (piece{model.Text, "Answer ends with <thin"}) {
		t.Errorf("pieces = %+v", got)
	}
}

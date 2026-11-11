package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/auth"
	"github.com/cferrante/cferrante-dev/orchestrator/model/fake"
)

// stream feeds parts one at a time and returns what would reach the page.
func stream(parts ...string) string {
	var n normaliser
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(n.feed(p))
	}
	b.WriteString(n.flush())
	return b.String()
}

// gpt-oss writes its training format's characters in about one answer in
// eight (6.3b): lenticular brackets for citations, a narrow no-break space
// for thousands, non-breaking hyphens everywhere.
func TestNormaliserRewritesGptOssCharacters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
		want  string
	}{
		{"lenticular citation", []string{"Vartec 【4】 and 【1, 2】."}, "Vartec [4] and [1, 2]."},
		{"citation split across tokens", []string{"Vartec 【", "4】."}, "Vartec [4]."},
		{"thousands", []string{"2 000 chunks, 142 000 records"}, "2,000 chunks, 142,000 records"},
		{"thousands split after the space", []string{"2 ", "000 chunks"}, "2,000 chunks"},
		{"thousands split before the space", []string{"2", " 000"}, "2,000"},
		{"before a unit", []string{"0.12 ms and 5 "}, "0.12 ms and 5 "},
		{"not after a digit", []string{"a b"}, "a b"},
		{"non-breaking hyphen", []string{"re‑architected"}, "re-architected"},
		{"a rune split across chunks", []string{"Vartec \xe3\x80", "\x90" + "4】"}, "Vartec [4]"},
		{"plain text untouched", []string{"Café [2] – 2,000 “quotes”"}, "Café [2] – 2,000 “quotes”"},
	} {
		if got := stream(tc.parts...); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The page and the citation cards must both see the normalised text.
func TestALenticularCitationBecomesACitation(t *testing.T) {
	m := &fake.Model{Name: "groq", Events: answer("A is Go 【", "2】 with 2 ", "000 tests.")}
	h := newHarness(m, &fakeRetriever{chunks: twoChunks})
	evs := h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "What has he built?"})
	var text strings.Builder
	cited := 0
	for _, e := range evs {
		switch e.name {
		case "token":
			text.WriteString(e.data["text"].(string))
		case "citation":
			cited++
			if e.data["chunk_id"] != "proj-b#summary" {
				t.Errorf("citation %v", e.data)
			}
		}
	}
	if text.String() != "A is Go [2] with 2,000 tests." || cited != 1 {
		t.Errorf("tokens %q, %d citations; want the normalised text and one citation", text.String(), cited)
	}
}

// A narrow no-break space held at the very end must still reach the page.
func TestAHeldCharacterIsWrittenWhenTheModelStops(t *testing.T) {
	m := &fake.Model{Name: "groq", Events: answer("Up to 5 ")}
	h := newHarness(m, &fakeRetriever{chunks: twoChunks})
	var text strings.Builder
	for _, e := range h.turn(t, context.Background(), auth.Caller{}, TurnRequest{Message: "q"}) {
		if e.name == "token" {
			text.WriteString(e.data["text"].(string))
		}
	}
	if text.String() != "Up to 5 " {
		t.Errorf("tokens %q, want %q", text.String(), "Up to 5 ")
	}
}

package fake

import (
	"context"
	"testing"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

func TestTurnsScriptSuccessiveCalls(t *testing.T) {
	m := &Model{Turns: [][]model.Event{{{Kind: model.Text, Text: "one"}}, {{Kind: model.Text, Text: "two"}}}}
	var got []string
	for i := 0; i < 3; i++ {
		m.Stream(context.Background(), model.Request{}, func(e model.Event) error { got = append(got, e.Text); return nil })
	}
	if len(got) != 3 || got[0] != "one" || got[1] != "two" || got[2] != "two" {
		t.Errorf("got %v, want one, two, two (the last turn repeats)", got)
	}
}

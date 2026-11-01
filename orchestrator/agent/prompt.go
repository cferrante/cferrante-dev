package agent

import (
	"fmt"
	"strings"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// systemPrompt is a first version; Phase 5 and the evals in Phase 6 tune it.
const systemPrompt = `You are the receptionist on the site owner's portfolio website.
Answer visitors' questions about the owner's work using only the numbered passages
in the user's message. After each claim, cite the passage it comes from as [n],
for example [1] or [1][3]. If the passages don't answer the question, say so
plainly instead of guessing. Keep answers short and friendly.`

// prompt builds the model request: system prompt, earlier turns, then the
// question with the retrieved passages numbered 1..k so [n] refers to chunks[n-1].
func prompt(chunks []Chunk, history []model.Message, question string) model.Request {
	var b strings.Builder
	if len(chunks) == 0 {
		b.WriteString("No passages matched this question.\n")
	} else {
		b.WriteString("Passages:\n")
		for i, c := range chunks {
			fmt.Fprintf(&b, "\n[%d] %s (%s)\n%s\n", i+1, c.Title, c.URL, c.Text)
		}
	}
	fmt.Fprintf(&b, "\nQuestion: %s", question)
	msgs := append(append([]model.Message(nil), history...), model.Message{Role: model.User, Content: b.String()})
	return model.Request{System: systemPrompt, Messages: msgs}
}

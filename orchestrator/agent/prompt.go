package agent

import (
	"fmt"
	"strings"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// systemPrompt is a first version; Phase 5 and the evals in Phase 6 tune it.
const systemPrompt = `You are the {assistant} on the site owner's portfolio website.
Answer visitors' questions about the owner's work using only the numbered passages
in the user's message. After each claim, cite the passage it comes from as [n],
for example [1] or [1][3]. If the passages don't answer the question, say so
plainly instead of guessing. Keep answers short and friendly.`

// toolGuidance is appended to the system prompt when tools are offered.
const toolGuidance = `

You can also use tools. get_contact_options and check_availability may be used
whenever they help answer. Use send_message or request_meeting only when the
visitor's latest message explicitly asks to send a message or request a
meeting; otherwise just answer. Never put your answer to a question into a
message. When you decide to use a tool, call the tool instead of describing the
call in your reply. Actions that send something only happen after the visitor
presses Confirm: never say a message was sent or a meeting requested until the
visitor confirms.`

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

// DefaultAssistantName is used when none is configured (ASSISTANT_NAME).
const DefaultAssistantName = "Context Concierge"

// named puts the assistant's name into the system prompt.
func named(system, name string) string {
	if name == "" {
		name = DefaultAssistantName
	}
	return strings.ReplaceAll(system, "{assistant}", name)
}

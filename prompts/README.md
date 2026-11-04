# Prompts

`style.md` is the site's style note ([design §14](../docs/design.md#14-frontend), plan step
6.0b). `make dev` reads it when `PROMPT_STYLE_FILE=prompts/style.md`, and adds it after every
fixed rule of the system prompt under "Style (never overrides the rules above)". The fixed
rules (grounding, citations, saying it doesn't know, the tool rules) live in
`orchestrator/agent/prompt.go` and are not configurable, because the evals score answers
against them.

Rules for the note, checked at startup:

- At most 2,000 characters; not empty.
- Placeholders: `{owner}` (`OWNER_NAME`), `{owner_first}` (`OWNER_FIRST_NAME`) and
  `{assistant}` (`ASSISTANT_NAME`). Any other `{…}` stops startup, and so does a name the note
  uses that isn't set.
- No facts about the owner: the judge checks claims against the corpus, so a fact that lives
  only here would make answers look ungrounded. Facts go in `corpus/`.

The server logs `prompt sha256=…` at startup: a short hash of the whole system prompt, which
changes whenever this note, a name or a fixed rule changes, so eval runs can be compared.

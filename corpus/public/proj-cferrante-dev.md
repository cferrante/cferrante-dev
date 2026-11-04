---
title: cferrante.dev Context Concierge
url: /projects/cferrante-dev
access: public
---
## Summary

The Context Concierge is the AI assistant on cferrante.dev, designed and built by Colleen
Ferrante. Visitors ask about Colleen's work; it answers only from Colleen's own documents,
cites the source of every claim, and shows its work: the left pane streams its reasoning and
each step it takes, and the right pane shows a card per backend step with its real, measured
timing. The goals are to prove speed and answer quality at close to $0 a month. As of November
2026 it is built and tested locally, with real models; deploying it to AWS is the next phase.

## Retrieval engine

Search runs in a C++ engine that Colleen wrote for this project, linked into the Go server
through cgo, so a search is a function call rather than a network hop. It combines keyword
search (BM25) with vector search and merges the two rankings with Reciprocal Rank Fusion, so a
question finds a document whether it shares its exact words or only its meaning. Measured on a
laptop, one search over 2,000 chunks of 512 dimensions takes 0.12 ms at the median and 0.23 ms
at the 99th percentile; a CI benchmark fails any change that pushes the 99th percentile past
1 ms. Unrolling the vector dot product into eight independent sums let the compiler vectorise
it, which made search about three times faster; the same search in pure Go measured 0.41 ms.
The engine also enforces access: a visitor's search can never return an invitee-only document,
and keyword statistics are computed only over the documents the caller may see, so private
text can't even shift the order of public results.

## Grounding and citations

Every answer must come from numbered excerpts of Colleen's documents, with each claim cited as
[1], [2] and so on; when the excerpts don't answer a question, the assistant says it doesn't
have that information rather than guessing. Citations become links only for safe addresses:
this site's own pages or secure https links. The system prompt's rules on grounding, citation and tools are fixed in code and
can't be changed by configuration, because the evaluations score answers against them; a
separate style note may only add to them.

## Models

Answers come from gpt-oss-120b on Groq, chosen for speed on a free tier, with Amazon Nova Lite
on Bedrock as a fallback when Groq is rate-limited; the thought stream says when the backup
model is answering. Development runs on local models through Ollama at no cost. Documents are
embedded with Amazon Titan v2 at 512 dimensions. There is no LLM framework: one small client
written with Go's standard library talks to every OpenAI-compatible provider behind the
project's own model interface, which kept hundreds of dependencies out and every request
under the project's own tests.

## Actions and privacy

Invited visitors can ask the assistant to send Colleen a message or request a meeting. Nothing
is sent until the visitor presses Confirm on a card showing exactly what will be sent, and an
action runs at most once even if the button is pressed twice. Visitors can stop an answer
mid-stream. Invitee-only documents live in a separate private repository, and an automated
check stops any commit that would publish one. Secrets are read from AWS Parameter Store at
start-up and never written to disk, logs or the command line.

## Cost

The design targets close to $0 a month: static hosting, Lambda, DynamoDB and Cognito on AWS
always-free tiers, Groq and Neon Postgres on free plans, and Bedrock only as a fallback costing
cents. The domain is about $15 a year, and an AWS budget alarm warns at $1.

## Performance

Speed targets are written down before code and measured, never assumed. The target is the
first visible thought within 500 ms for half of visitors and within 1 s for 95%. A suggested
question with a stored, pre-checked answer measured 0.9 ms at the median on a laptop, with no
model call at all. A spike measured a Go Lambda cold start at 68 ms. Every result is recorded
with the date, the code version and the method used.

## How it was built

Colleen built the project with Claude Code using spec-driven, test-driven development. A
design spec defines each component's contract before any code; a step-by-step plan names, for
every step, the test that must fail first. Each change starts with that failing test, then the
code is broken on purpose to prove the test catches the fault. Every departure from the plan
and every code-review finding is logged with its reason. Risky assumptions were settled first
by small spikes, each answering one question and then torn down; one showed that the planned
fix for running C++ on Lambda didn't work and that a fully static build did. Each step is its
own pull request, merged only with CI green.

## Evaluations

Answers are to be checked by evaluations, not by impression; as of November 2026 they are
being built. A golden set of questions, each with the document that should answer it,
measures whether search finds the right excerpt in its top five results (hit@5); that check
runs today. Next, a second model acting as a judge will score whether every claim in an
answer is supported by the excerpts, and an adversarial suite will try jailbreaks, prompt
extraction, planted instructions in documents, tool abuse and private-data leaks. Unique
canary strings hidden in the system prompt and private documents will make any leak
detectable without relying on a judge's opinion.

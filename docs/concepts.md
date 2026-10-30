# Concepts

A plain-language guide to the ideas this project uses, with *why* each one matters here. Grouped
by topic; terms are used in [design.md](design.md), [benchmarks.md](benchmarks.md) and the
[README](../README.md).

Every term is a heading, so it has a stable link: `concepts.md#<term-name>` (lowercase, spaces
become hyphens, punctuation dropped). Keep headings to plain words so anchors stay predictable.

## Retrieval

### RAG

**Retrieval-Augmented Generation.** Before the model answers, search my documents and put the
best passages into the prompt. The model then answers *from evidence* instead of from memory.
*Why here:* the model knows nothing about me; RAG is how it can answer accurately and cite sources.

### Chunk

A short passage (a few hundred words) cut from a longer document. Search returns chunks, not whole
documents, so the prompt contains only relevant text.

### Embedding

A list of numbers (a vector, e.g. 512 of them) that represents a text's meaning. Texts with
similar meaning get similar vectors, even if they use different words ("Go developer" ≈ "Golang
engineer"). Produced by an embedding model. The question and the chunks must be embedded by the
*same* model, or their vectors aren't comparable ([design §4](design.md#4-model-layer), [[D1]](design.md#d1-embedding-model)).

### Cosine similarity and dot product

How we compare two vectors: closer to 1 means more similar. If every vector is scaled to length 1
ahead of time (*normalized*), cosine similarity equals the simpler dot product (multiply matching
numbers and add them up), which is faster to compute.

### BM25

A classic keyword-ranking formula (what search engines used before embeddings). It rewards chunks
that contain the query's words, especially rare ones. *Why here:* embeddings can miss exact terms
like product names ("Kubernetes", "OpenTofu"); BM25 catches them.

### Inverted index

A map from each word to the list of chunks containing it, like the index at the back of a book. It
makes [BM25](#bm25) fast because only chunks containing a query word are scored.

### Hybrid search and RRF

Run vector search and [BM25](#bm25) separately, then merge the two ranked lists with **Reciprocal
Rank Fusion**: each chunk scores `1/(60 + its rank)` in each list, summed. It needs no tuning and is
robust, which is why it's the common default ([design §5](design.md#5-retrieval-engine-c)).

### Brute-force vs approximate search

Brute force compares the question to *every* chunk: always exact. Approximate indexes like
**HNSW** skip most comparisons using a graph, trading a little accuracy for speed at large scale
(~100k+ vectors). *Why brute force here:* with ~2k chunks it is already sub-millisecond and never
misses the best match.

### Top-k

Return only the best *k* results (e.g. 5). A small heap finds them without sorting all scores.

### Access mask

A per-chunk bit field saying who may see it (public, invitee). Filtering happens *inside* the
engine before ranking, so private text never reaches the model for a public caller.

## Performance

### SIMD

**Single Instruction, Multiple Data.** CPU instructions that process several numbers at once, e.g.
8 multiply-adds in one step with AVX2/FMA on x86, or NEON on ARM. This is the main reason C++ can be
so fast at [dot products](#cosine-similarity-and-dot-product).

### CPU cache

Small, very fast memory on the CPU. Data that fits in cache (a few MB) is read many times faster
than main memory. A compact index ([int8](#quantization), ~1 MB) stays in cache.

### Quantization

Storing each vector number as a 1-byte integer (**int8**) instead of a 4-byte float. A quarter of
the memory, faster to scan, with a small accuracy cost that must be measured.

### cgo

Go's mechanism for calling C (and C++ through a [C ABI](#c-abi)). Each call costs roughly 100 ns,
which is negligible here, but it complicates builds (needs a C++ toolchain, and the binary must
match the target system's C library; see the README's [Known risks](../README.md#known-risks)).

### C ABI

The calling convention C uses. C++ code exposes plain C functions (`extern "C"`) so Go can call it;
C++ classes and exceptions can't cross the boundary.

### Hot path

The steps a visitor waits on. Anything slow or unreliable (like a database that may be waking up)
is kept *off* it.

### Cold start

The extra delay when Lambda starts a new instance: load the runtime, run init code (load index,
fetch secrets). Later requests to the same warm instance skip it.

### Scale to zero

A service that stops running (and billing) when idle, then wakes on demand (Lambda, Neon). Cheap,
at the cost of a wake-up delay.

### Percentiles

**p50, p95, p99.** p95 = 95% of requests were at least this fast. Averages hide slow outliers;
percentiles show what real visitors experience ([benchmarks: Methodology](benchmarks.md#methodology)).

## Language models

### Token

The unit models read and write: roughly ¾ of an English word. Pricing, limits and speed are all
measured in tokens.

### Prefill and decode

The two phases of generating: *prefill* reads the whole prompt, *decode* writes the answer one
[token](#token) at a time. Long prompts make prefill slow; long answers make decode slow.

### TTFT

**Time to first token.** How long until the first token arrives: mostly queueing plus
[prefill](#prefill-and-decode). It decides how fast a reply *feels*.

### Tokens per second

Decode speed. Groq's custom chips are fast at this, which is why full answers arrive in about a
second.

### Streaming

Sending tokens to the browser as they're generated instead of waiting for the whole answer.

### SSE

**Server-Sent Events.** A simple standard for a server to push a stream of named events over one
HTTP response. One-way (server → browser), plain text, works through CDNs. Simpler than WebSockets,
which are two-way and need more infrastructure ([design §3](design.md#3-streaming-protocol-sse)).

### Reasoning models

Models that write out intermediate reasoning ("thinking") before the final answer (e.g. gpt-oss).
That reasoning is what the left pane displays.

### Prompt caching

The provider remembers the already-processed start of a prompt. If the next request starts with
the exact same text, that part skips [prefill](#prefill-and-decode). Works only for an identical
*prefix*, so static content (system prompt, tool definitions) goes first.

### Tool calling

Also called function calling. The model returns a structured request ("call `check_availability`
with these arguments") instead of text; our code runs it and returns the result. The model never
executes anything itself.

### Structured output

Also called constrained decoding. Forcing the model to output only text matching a JSON schema or
grammar. Guarantees well-formed JSON, *not* correct values, so code still validates.

### Agent loop

A model in a loop: think → maybe [call a tool](#tool-calling) → read the result → repeat → answer.
Our Go orchestrator runs the loop so it controls permissions, timing and events.

### Model router and fallback

Code that picks which provider handles a request and switches to a backup if the primary fails
before answering.

### Provider-agnostic interface

One Go interface (`Model`) with one implementation per provider, so swapping Groq ↔ Bedrock ↔
Ollama is a config change, and tests can use a fake.

## Quality and safety

### Hallucination

The model stating something false or unsupported, confidently.

### Groundedness

Whether each claim in an answer is supported by the retrieved sources. The opposite of
[hallucination](#hallucination).

### LLM-as-judge

Using a second model call to grade an output (here: is the answer [grounded](#groundedness)?).
Cheap and scalable, but a judge can be wrong too, so the judge itself is evaluated.

### Golden dataset

A fixed set of questions with known good answers and expected sources, used to score every change.
It turns "does it seem smarter?" into a number tracked over time.

### Hit at k

Also written **hit@k** or recall@k. Retrieval metric: did the expected chunk appear in the
[top *k*](#top-k) results? It tests search separately from the model.

### Jailbreak

A prompt that tries to get the model to ignore its rules ("pretend you're an AI with no
restrictions…").

### Prompt injection

Instructions smuggled in as *data*:
- *direct*: typed by the visitor;
- *indirect*: hidden inside a retrieved document;
- *stored*: planted in content saved now and retrieved later (why logs never feed retrieval).

### Canary string

A unique marker hidden in secret text (system prompt, private documents). If it appears in output
where it shouldn't, the text leaked: a deterministic test, no judgment needed.

### Red-teaming

Also called adversarial testing. Systematically attacking your own system with a growing library of
attacks, each with an expected safe outcome, run on every change.

### Defense in depth

Several independent safeguards, so one failure isn't enough to cause harm. Here: hide tools from
unauthorized callers *and* re-check permissions when executing.

### Human-in-the-loop confirmation

The agent proposes an action; a person clicks to confirm before it happens. It stops an
[injected](#prompt-injection) instruction from triggering real actions.

### Idempotency key

A unique id attached to an action so that retrying it doesn't perform it twice (e.g. no duplicate
emails).

## Identity and access

### Authentication vs authorization

*Authentication* proves who you are (login). *Authorization* decides what you may do. We rent
authentication (Cognito) and own authorization (Go code).

### JWT

**JSON Web Token.** A signed token the browser sends with each request, containing claims like
user id and groups. The server checks the signature with the issuer's public keys: no database
lookup, microseconds of work.

### Cognito groups

Labels such as `invitee` attached to users and included in their [JWT](#jwt), which is how Go knows
what a caller may do.

### Passwordless sign-in

Signing in with a one-time code (OTP) sent by email instead of a password.

### OIDC

**OpenID Connect.** A standard for one system to trust identity tokens issued by another. It lets,
e.g., GitHub Actions deploy to AWS without storing AWS keys.

### Least privilege

Every component gets only the permissions it needs, nothing more.

## Data and operations

### Serverless Postgres

Postgres where compute starts and stops automatically and storage is separate (Neon). Free when
idle, with a short wake-up delay (see [scale to zero](#scale-to-zero)).

### Connection pooling

Reusing a small set of database connections across many requests. Lambda can start many instances
at once, and each opening its own connections would overwhelm Postgres.

### Migrations

Versioned, ordered SQL files that evolve the database schema, applied the same way everywhere.

### TTL

**Time to live.** An expiry time on a record; DynamoDB deletes expired items automatically. Used
for rate-limit counters.

### Data retention

How long data is kept before deletion (90 days for messages). Keeping less data means less risk.

### 12-factor config

Configuration comes from environment variables, not code, so the same build runs locally and in
production. One of the "Twelve-Factor App" practices.

### Infrastructure as code

Cloud resources defined in versioned files and created by a tool (here OpenTofu), so the setup is
reviewable, repeatable and can be rebuilt from scratch.

## Frontend

### Utility-first CSS

Styling with small single-purpose classes in the HTML (Tailwind). A build step keeps only the
classes actually used.

### Design tokens

Named values (colors, fonts, spacing) defined once and reused everywhere.

### Reduced motion

An OS accessibility setting; respecting it means disabling non-essential animation.

### Performance budget

A hard limit (e.g. CSS < 15 KB gzipped) checked automatically, so "fast" stays a rule rather than a
hope.

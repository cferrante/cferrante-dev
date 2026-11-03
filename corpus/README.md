# Corpus

The documents the Context Concierge answers from ([design §5](../docs/design.md#5-retrieval-engine-c)).

| Folder | Who can read the answers | Where it lives |
|---|---|---|
| `public/` | every visitor | this repo, reviewed like code |
| `private/` | invitees only | a separate **private** GitHub repo, cloned here; git-ignored |

This repo is public. The access mask keeps invitee chunks out of a visitor's chat, but only
`make corpus-check` (run by CI) keeps them off GitHub: it fails if any tracked file under
`corpus/` lacks `access: public`, if anything under `corpus/private/` is tracked, or if any
other tracked Markdown file has an `access:` other than `public`.

Each document is Markdown with front matter, one `key: value` per line:

```
---
title: Project Alpha
url: /projects/alpha
access: public
---
## Summary

Alpha answers questions about a portfolio.
```

To index both folders (they must exist; a missing folder is an error so a typo can't drop
documents):

```
git clone git@github.com:<you>/<private-corpus-repo>.git corpus/private
CORPUS_DIRS=corpus/public:corpus/private go run ./orchestrator/cmd/indexer -embedder titan
```

The built index and its sidecar hold every chunk's text, private ones included: they are never
committed or served as static files, only shipped inside the Lambda package.

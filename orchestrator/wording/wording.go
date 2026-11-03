// Package wording holds the visitor-facing wording that MESSAGES_FILE may
// override (design §14): the friendly error messages and the retrieval
// wording (sources, excerpts, the portfolio). The page build and the server
// both load the file through this package, so they validate it the same way
// and can't disagree. Wording is configurable; behaviour (which errors offer
// Retry, the prompt's instructions) is not.
package wording

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type spec struct {
	def      string
	allowed  []string // placeholders the value may use
	required []string // placeholders it must use
}

var site = []string{"assistant", "site"}

func msg(def string) spec { return spec{def: def, allowed: site} }

var specs = map[string]spec{
	"network":             msg("I can't reach the {assistant} right now. Check your connection, then try again."),
	"timeout":             msg("That took too long to answer. Please try again."),
	"model_unavailable":   msg("The AI model isn't available right now. Please try again in a moment."),
	"retrieval_failed":    msg("I couldn't search the portfolio just now. Please try again."),
	"rate_limited":        msg("You've asked a lot in a short time. Please wait a few minutes."),
	"tool_limit":          msg("That needed too many steps. Try asking more simply."),
	"incomplete":          msg("The answer was cut off. Please try again."),
	"session_ended":       msg("Your session has ended. Please sign in again."),
	"server_error":        msg("Something went wrong on our side. Please try again."),
	"empty_answer":        msg("I didn't manage to put an answer together that time. Please try again."),
	"action_expired":      msg("That request expired before it was confirmed. Ask again if you still want it."),
	"action_already_done": msg("That was already done; nothing was sent twice."),
	"action_not_found":    msg("That request is no longer available. Ask again if you still want it."),
	"not_authorized":      msg("You can no longer do that. Please sign in again."),
	"action_failed":       msg("That didn't go through, and it wasn't retried. Please try again later."),

	"collection":    {def: "portfolio"},
	"source_one":    {def: "source"},
	"source_other":  {def: "sources"},
	"excerpt_one":   {def: "excerpt"},
	"excerpt_other": {def: "excerpts"},
	"retrieval_found": {def: "Found {excerpts} from {sources}: {titles}.",
		allowed:  []string{"excerpts", "sources", "titles", "collection", "assistant", "site"},
		required: []string{"excerpts", "sources", "titles"}},
	"retrieval_none": {def: "Nothing in the {collection} matched; I'll say so rather than guess.",
		allowed: []string{"collection", "assistant", "site"}},
	"retrieval_card": {def: "{excerpts} · {sources} · top score {score}",
		allowed:  []string{"excerpts", "sources", "score"},
		required: []string{"excerpts"}},
}

// Keys lists every key a MESSAGES_FILE may set, sorted.
func Keys() []string {
	keys := make([]string, 0, len(specs))
	for k := range specs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Default is a key's built-in wording.
func Default(key string) string { return specs[key].def }

// Wording is the defaults plus a file's overrides. The zero value is the defaults.
type Wording struct{ overrides map[string]string }

// Get returns the key's wording: the override, or the default.
func (w Wording) Get(key string) string {
	if v, ok := w.overrides[key]; ok {
		return v
	}
	return specs[key].def
}

// Overrides are the keys the file set, for building the page's config.
func (w Wording) Overrides() map[string]string {
	out := make(map[string]string, len(w.overrides))
	for k, v := range w.overrides {
		out[k] = v
	}
	return out
}

// Count renders a count with its noun, "1 excerpt" or "4 excerpts", using
// the noun's _one form for exactly 1 and _other otherwise.
func (w Wording) Count(n int, noun string) string {
	form := "_other"
	if n == 1 {
		form = "_one"
	}
	return fmt.Sprintf("%d %s", n, w.Get(noun+form))
}

// Fill replaces each {name} in template with vals[name].
func Fill(template string, vals map[string]string) string {
	for k, v := range vals {
		template = strings.ReplaceAll(template, "{"+k+"}", v)
	}
	return template
}

var placeholder = regexp.MustCompile(`\{[^{}]*\}`)

// Load reads a JSON object of overrides from path; an empty path means the
// defaults. Anything that would show a visitor a broken sentence is an
// error: an unknown key, a value that isn't a non-empty string, a
// placeholder the key doesn't allow, or a missing required one.
func Load(path string, read func(string) ([]byte, error)) (Wording, error) {
	if path == "" {
		return Wording{}, nil
	}
	raw, err := read(path)
	if err != nil {
		return Wording{}, fmt.Errorf("MESSAGES_FILE: %w", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return Wording{}, fmt.Errorf("%s: want a JSON object of strings: %w", path, err)
	}
	w := Wording{overrides: map[string]string{}}
	for key, value := range m {
		sp, known := specs[key]
		if !known {
			return Wording{}, fmt.Errorf("%s: unknown key %q (known: %s)", path, key, strings.Join(Keys(), ", "))
		}
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return Wording{}, fmt.Errorf("%s: %q: want a JSON string: %w", path, key, err)
		}
		if strings.TrimSpace(s) == "" {
			return Wording{}, fmt.Errorf("%s: %q is empty", path, key)
		}
		for _, p := range placeholder.FindAllString(s, -1) {
			if !slices.Contains(sp.allowed, p[1:len(p)-1]) {
				return Wording{}, fmt.Errorf("%s: %q uses %s, which it can't (allowed: %s)", path, key, p, list(sp.allowed))
			}
		}
		for _, r := range sp.required {
			if !strings.Contains(s, "{"+r+"}") {
				return Wording{}, fmt.Errorf("%s: %q needs {%s}", path, key, r)
			}
		}
		w.overrides[key] = s
	}
	return w, nil
}

func list(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "{" + n + "}"
	}
	return strings.Join(out, ", ")
}

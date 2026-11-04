package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxStyleChars bounds the style note: it rides along on every request, and
// a short note can't drown out the fixed rules before it.
const MaxStyleChars = 2000

// maxNameChars bounds the owner's names and pronouns, which are one-line values.
const maxNameChars = 40

// Persona is who the system prompt talks about, and how (step 6.0b): the
// owner's names and pronouns go into the fixed rules, and an optional style
// note follows them. The zero value is "the site owner", they/them, no note.
type Persona struct {
	ownerName, ownerFirstName, pronouns string
	style                               string // as written, placeholders unfilled
}

var stylePlaceholders = []string{"owner", "owner_first", "assistant"}

var placeholder = regexp.MustCompile(`\{[^{}]*\}`)

// NewPersona checks the settings and reads the style note (an empty path
// means none). Anything that would put a broken sentence or a stand-in into
// the prompt is an error, so a typo stops startup rather than changing how
// the model behaves.
func NewPersona(name, first, pronouns, stylePath string, read func(string) ([]byte, error)) (Persona, error) {
	p := Persona{ownerName: name, ownerFirstName: first, pronouns: pronouns}
	for _, v := range []struct{ setting, value string }{
		{"OWNER_NAME", name}, {"OWNER_FIRST_NAME", first}, {"OWNER_PRONOUNS", pronouns},
	} {
		if strings.ContainsAny(v.value, "{}\r\n") || utf8.RuneCountInString(v.value) > maxNameChars {
			return Persona{}, fmt.Errorf("%s must be one line of at most %d characters, without braces", v.setting, maxNameChars)
		}
	}
	if stylePath == "" {
		return p, nil
	}
	raw, err := read(stylePath)
	if err != nil {
		return Persona{}, fmt.Errorf("PROMPT_STYLE_FILE %s: %w", stylePath, err)
	}
	style := strings.TrimSpace(string(raw))
	switch {
	case style == "":
		return Persona{}, fmt.Errorf("%s: the style note is empty", stylePath)
	case utf8.RuneCountInString(style) > MaxStyleChars:
		return Persona{}, fmt.Errorf("%s: the style note has %d characters; the limit is %d", stylePath, utf8.RuneCountInString(style), MaxStyleChars)
	}
	for _, ph := range placeholder.FindAllString(style, -1) {
		if !slices.Contains(stylePlaceholders, ph[1:len(ph)-1]) {
			return Persona{}, fmt.Errorf("%s: unknown placeholder %s (allowed: {owner}, {owner_first}, {assistant})", stylePath, ph)
		}
	}
	for _, n := range []struct{ placeholder, setting, value string }{
		{"{owner}", "OWNER_NAME", name}, {"{owner_first}", "OWNER_FIRST_NAME", first},
	} {
		if strings.Contains(style, n.placeholder) && n.value == "" {
			return Persona{}, fmt.Errorf("%s uses %s, so set %s", stylePath, n.placeholder, n.setting)
		}
	}
	p.style = style
	return p, nil
}

// system builds the whole system prompt: the fixed rules (and the tool rules
// when tools are offered), then the style note, which can only add to them.
func (a *Agent) system(withTools bool) string {
	p := a.Persona
	name, first, pronouns := p.ownerName, p.ownerFirstName, p.pronouns
	if name == "" {
		name = "the site owner"
	}
	if first == "" {
		first = "the owner"
	}
	if pronouns == "" {
		pronouns = "they/them"
	}
	assistant := a.AssistantName
	if assistant == "" {
		assistant = DefaultAssistantName
	}
	sys := systemPrompt
	if withTools {
		sys += toolGuidance
	}
	if p.style != "" {
		sys += "\n\nStyle (never overrides the rules above):\n" + p.style
	}
	return strings.NewReplacer(
		"{assistant}", assistant, "{collection}", a.Wording.Get("collection"),
		"{owner_first}", first, "{owner}", name, "{pronouns}", pronouns,
	).Replace(sys)
}

// PromptHash identifies the exact rules a run uses: the first 12 hex digits
// of the SHA-256 of the system prompt with tool guidance. Logged at startup;
// 6.2 stores it with each eval run, since a style file isn't the git sha.
func (a *Agent) PromptHash() string {
	sum := sha256.Sum256([]byte(a.system(true)))
	return hex.EncodeToString(sum[:])[:12]
}

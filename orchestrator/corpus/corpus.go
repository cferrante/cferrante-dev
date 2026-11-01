// Package corpus reads the Markdown corpus and splits it into chunks (design §5).
package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Document is one parsed corpus file.
type Document struct {
	Slug   string // from the file name
	Title  string
	URL    string
	Access string // "public" or "invitee"
	Body   string // after the front matter
}

// Chunk is one indexable piece of a document.
type Chunk struct {
	ID      string // "<slug>#<anchor>", with "-2", "-3"... for later pieces
	Title   string // document title
	Heading string // section heading; "" before the first heading
	URL     string // document URL plus "#anchor" for sections
	Access  string
	Text    string
}

var allowedKeys = map[string]bool{"title": true, "url": true, "access": true}

// Parse reads a corpus file's front matter and body. Front matter is one
// "key: value" per line between "---" lines; title, url and access are all
// required, and an unknown key is an error so a typo can't publish a private
// document as public.
func Parse(filename string, content []byte) (Document, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Document{}, fmt.Errorf("no front matter: the file must start with a --- line")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	body := ""
	switch {
	case end >= 0:
		body = rest[end+len("\n---\n"):]
	case strings.HasSuffix(rest, "\n---"):
		end = len(rest) - len("\n---")
	default:
		return Document{}, fmt.Errorf("front matter has no closing --- line")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || !allowedKeys[key] {
			return Document{}, fmt.Errorf("front matter line %q: want one of title, url, access", line)
		}
		if _, dup := fields[key]; dup {
			return Document{}, fmt.Errorf("front matter key %q appears twice", key)
		}
		fields[key] = strings.TrimSpace(value)
	}
	for key := range allowedKeys {
		if fields[key] == "" {
			return Document{}, fmt.Errorf("front matter is missing %q", key)
		}
	}
	if a := fields["access"]; a != "public" && a != "invitee" {
		return Document{}, fmt.Errorf("access %q: want public or invitee", a)
	}
	return Document{
		Slug: slug(filename), Title: fields["title"], URL: fields["url"],
		Access: fields["access"], Body: body,
	}, nil
}

func slug(filename string) string {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, base)
}

// anchor makes a GitHub-style heading anchor: lowercase, keep letters, digits,
// spaces, hyphens and underscores, then spaces become hyphens.
func anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// Chunker splits documents. Target is the preferred chunk size in bytes.
type Chunker struct{ Target int }

type section struct {
	heading, anchor string
	blocks          []string // paragraphs and whole code blocks
}

// sections splits a body at headings, ignoring "#" lines inside code fences,
// and each section into blocks at blank lines, keeping code blocks whole.
func sections(body string) []section {
	cur := section{anchor: "intro"}
	var out []section
	var block []string
	inFence := false
	flushBlock := func() {
		if t := strings.TrimSpace(strings.Join(block, "\n")); t != "" {
			cur.blocks = append(cur.blocks, t)
		}
		block = nil
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			block = append(block, line)
			continue
		}
		if !inFence {
			if h, ok := heading(trimmed); ok {
				flushBlock()
				out = append(out, cur)
				cur = section{heading: h, anchor: anchor(h)}
				continue
			}
			if trimmed == "" {
				flushBlock()
				continue
			}
		}
		block = append(block, line)
	}
	flushBlock()
	return append(out, cur)
}

func heading(line string) (string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return "", false
	}
	return strings.TrimSpace(line[level:]), true
}

// unit is a piece the packer places whole: a paragraph or code block that
// fits, or one sentence of a paragraph that doesn't.
type unit struct {
	text     string
	sentence bool // part of a split paragraph: joined with a space
	newPara  bool // first unit of its paragraph
}

func (c Chunker) units(blocks []string) []unit {
	var out []unit
	for _, b := range blocks {
		if len(b) <= c.Target || strings.HasPrefix(b, "```") {
			out = append(out, unit{text: b, newPara: true})
			continue
		}
		for i, s := range sentences(b) {
			out = append(out, unit{text: s, sentence: true, newPara: i == 0})
		}
	}
	return out
}

// sentences splits after ".", "!" or "?" followed by whitespace. Abbreviations
// such as "e.g. " split too; overlap between chunks softens that.
func sentences(p string) []string {
	var out []string
	start := 0
	for i := 0; i < len(p)-1; i++ {
		if (p[i] == '.' || p[i] == '!' || p[i] == '?') && unicode.IsSpace(rune(p[i+1])) {
			if s := strings.TrimSpace(p[start : i+1]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(p[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// lastSentence is the overlap carried into the next chunk.
func lastSentence(u unit) string {
	if strings.HasPrefix(u.text, "```") {
		return ""
	}
	s := sentences(u.text)
	return s[len(s)-1]
}

// Split returns the document's chunks in order. Each section is packed into
// chunks of up to Target bytes; a chunk after the first starts with the
// previous chunk's last sentence and always adds at least one new unit.
func (c Chunker) Split(d Document) []Chunk {
	var out []Chunk
	for _, s := range sections(d.Body) {
		units := c.units(s.blocks)
		url := d.URL
		if s.heading != "" {
			url += "#" + s.anchor
		}
		piece := 0
		for i := 0; i < len(units); {
			var b strings.Builder
			if piece > 0 {
				b.WriteString(lastSentence(units[i-1]))
			}
			added := 0
			for i < len(units) {
				u := units[i]
				sep := ""
				if b.Len() > 0 {
					sep = " "
					if u.newPara {
						sep = "\n\n"
					}
				}
				if added > 0 && b.Len()+len(sep)+len(u.text) > c.Target {
					break
				}
				b.WriteString(sep + u.text)
				added++
				i++
			}
			piece++
			id := d.Slug + "#" + s.anchor
			if piece > 1 {
				id += fmt.Sprintf("-%d", piece)
			}
			out = append(out, Chunk{ID: id, Title: d.Title, Heading: s.heading, URL: url, Access: d.Access, Text: b.String()})
		}
	}
	return out
}

// ReadDir parses and splits every .md file in dir, in name order, and checks
// that chunk ids are unique across the corpus.
func (c Chunker) ReadDir(dir string) ([]Chunk, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []Chunk
	seen := map[string]string{}
	for _, name := range names {
		content, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		d, err := Parse(name, content)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, ch := range c.Split(d) {
			if prev, dup := seen[ch.ID]; dup {
				return nil, fmt.Errorf("duplicate chunk id %q in %s (also in %s)", ch.ID, name, prev)
			}
			seen[ch.ID] = name
			out = append(out, ch)
		}
	}
	return out, nil
}

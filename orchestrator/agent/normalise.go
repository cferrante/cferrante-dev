package agent

import (
	"strings"
	"unicode/utf8"
)

// normaliser rewrites characters gpt-oss carries over from its training
// format, before the answer reaches the page, the citation parser or the
// stored conversation (plan 6.3, found in 6.3b's runs):
//
//   - 【n】 (U+3010, U+3011) becomes [n], so the citation is recognised;
//   - a narrow no-break space (U+202F) between digits becomes a comma
//     ("2 000" -> "2,000") and anywhere else a plain space;
//   - a non-breaking hyphen (U+2011) becomes "-".
//
// It works on a stream: a rune split across chunks waits for its other
// bytes, and a U+202F after a digit waits one rune to see what follows.
type normaliser struct {
	partial   []byte // the start of a rune whose remaining bytes haven't arrived
	held      bool   // a U+202F after a digit, not yet written
	lastDigit bool   // the last rune written or held was a digit
}

const narrowNBSP = ' '

func (n *normaliser) feed(s string) string {
	if len(n.partial) > 0 {
		s = string(n.partial) + s
		n.partial = n.partial[:0]
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 && !utf8.FullRuneInString(s[i:]) {
			n.partial = append(n.partial, s[i:]...) // wait for the rest of this rune
			break
		}
		i += size
		if n.held {
			n.held = false
			if r >= '0' && r <= '9' {
				b.WriteByte(',')
			} else {
				b.WriteByte(' ')
			}
		}
		switch r {
		case '【':
			b.WriteByte('[')
		case '】':
			b.WriteByte(']')
		case '‑':
			b.WriteByte('-')
		case narrowNBSP:
			if n.lastDigit {
				n.held, n.lastDigit = true, false
				continue
			}
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
		n.lastDigit = r >= '0' && r <= '9'
	}
	return b.String()
}

// flush writes whatever is still waiting when the model stops.
func (n *normaliser) flush() string {
	out := string(n.partial)
	n.partial = n.partial[:0]
	if n.held {
		n.held = false
		out = " " + out
	}
	return out
}

package agent

import "strings"

// maxMarker bounds how much text after "[" is held while waiting for "]", so
// ordinary brackets in prose can't make the parser buffer without limit.
const maxMarker = 64

// citer finds citation markers in streamed answer text (design §3): [n],
// [n][m] and [n, m], for 1 <= n <= k, reporting each n the first time it
// appears. A marker may be split across chunks.
type citer struct {
	k      int
	seen   map[int]bool
	inside bool   // after "[" and before "]"
	cur    []byte // text since "["
}

func newCiter(k int) *citer { return &citer{k: k, seen: map[int]bool{}} }

// feed takes the next chunk of answer text and returns newly cited numbers in
// the order they appear.
func (c *citer) feed(chunk string) []int {
	var out []int
	for i := 0; i < len(chunk); i++ {
		b := chunk[i]
		switch {
		case b == '[':
			c.inside, c.cur = true, c.cur[:0] // a new "[" restarts the marker
		case !c.inside:
		case b == ']':
			c.inside = false
			out = append(out, c.marker(string(c.cur))...)
		case len(c.cur) >= maxMarker:
			c.inside = false // too long to be a citation
		default:
			c.cur = append(c.cur, b)
		}
	}
	return out
}

// marker returns the unseen, in-range numbers of a complete "n" or "n, m"
// marker body, or nothing if any part isn't a number.
func (c *citer) marker(body string) []int {
	var nums []int
	for _, part := range strings.Split(body, ",") {
		part = strings.TrimSpace(part)
		if part == "" || len(part) > 3 {
			return nil
		}
		n := 0
		for j := 0; j < len(part); j++ {
			if part[j] < '0' || part[j] > '9' {
				return nil
			}
			n = n*10 + int(part[j]-'0')
		}
		nums = append(nums, n)
	}
	var out []int
	for _, n := range nums {
		if n >= 1 && n <= c.k && !c.seen[n] {
			c.seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

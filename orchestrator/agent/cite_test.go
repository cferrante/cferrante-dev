package agent

import (
	"slices"
	"strings"
	"testing"
)

func TestCiter(t *testing.T) {
	tests := []struct {
		name   string
		k      int
		chunks []string
		want   []int
	}{
		{"single", 5, []string{"Go [2] is fun."}, []int{2}},
		{"first time only", 5, []string{"a [1] b [1] c [2] d [1]"}, []int{1, 2}},
		{"split before the number", 5, []string{"text [", "3] more"}, []int{3}},
		{"split before the bracket", 5, []string{"text [3", "] more"}, []int{3}},
		{"split across three chunks", 12, []string{"x [1", "1", "]"}, []int{11}},
		{"adjacent markers", 5, []string{"both [1][2]."}, []int{1, 2}},
		{"comma list", 5, []string{"both [2, 1]."}, []int{2, 1}},
		{"out of range ignored", 5, []string{"bad [9] and [0] good [5]"}, []int{5}},
		{"two digits within k", 12, []string{"see [12]"}, []int{12}},
		{"not a citation", 5, []string{"array[i] and [a] and [] and [1x]"}, nil},
		{"unclosed at end", 5, []string{"dangling [1"}, nil},
		{"long junk in brackets", 5, []string{"[1111111111111111111111111]"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newCiter(tc.k)
			var got []int
			for _, ch := range tc.chunks {
				got = append(got, c.feed(ch)...)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("cited %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCiterLongListAndBoundedBuffer(t *testing.T) {
	c := newCiter(9)
	if got := c.feed("all [1, 2, 3, 4, 5, 6, 7, 8, 9]"); len(got) != 9 {
		t.Errorf("long list cited %v, want 1..9", got)
	}
	// An unclosed bracket followed by a lot of prose must not be held in full.
	c = newCiter(5)
	c.feed("[" + strings.Repeat("prose ", 2000))
	if len(c.cur) > maxMarker {
		t.Errorf("holding %d bytes after an unclosed bracket, want at most %d", len(c.cur), maxMarker)
	}
}

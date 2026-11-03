package bedrock

import (
	"strings"

	"github.com/cferrante/cferrante-dev/orchestrator/model"
)

// piece is a run of streamed text that is either reasoning or answer.
type piece struct {
	kind model.EventKind
	text string
}

const (
	openTag  = "<thinking>"
	closeTag = "</thinking>"
)

// thinkSplitter separates Nova's inline <thinking>...</thinking> blocks from
// the answer as text streams in, even when a tag is split across chunks, so
// the reasoning goes to the thought stream and the answer stays clean. Text
// that could be the start of a tag is held until the next chunk shows
// whether it is one.
type thinkSplitter struct {
	inThink bool
	buf     string
	started bool // the answer has begun (leading blank lines are trimmed)
}

func (s *thinkSplitter) feed(chunk string) []piece {
	s.buf += chunk
	var out []piece
	for {
		tag := openTag
		if s.inThink {
			tag = closeTag
		}
		if i := strings.Index(s.buf, tag); i >= 0 {
			out = s.emit(out, s.buf[:i])
			s.buf = s.buf[i+len(tag):]
			s.inThink = !s.inThink
			continue
		}
		// Keep back the longest tail that might begin the tag.
		keep := 0
		for n := min(len(tag)-1, len(s.buf)); n > 0; n-- {
			if strings.HasPrefix(tag, s.buf[len(s.buf)-n:]) {
				keep = n
				break
			}
		}
		out = s.emit(out, s.buf[:len(s.buf)-keep])
		s.buf = s.buf[len(s.buf)-keep:]
		return out
	}
}

// flush emits whatever is held at the end of the stream; a partial tag that
// never completed is just text.
func (s *thinkSplitter) flush() []piece {
	out := s.emit(nil, s.buf)
	s.buf = ""
	return out
}

func (s *thinkSplitter) emit(out []piece, text string) []piece {
	if s.inThink {
		if text != "" {
			out = append(out, piece{model.Reasoning, text})
		}
		return out
	}
	if !s.started {
		text = strings.TrimLeft(text, " \t\r\n")
	}
	if text == "" {
		return out
	}
	s.started = true
	return append(out, piece{model.Text, text})
}

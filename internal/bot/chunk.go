package bot

import "strings"

// maxMessageLen is Discord's hard limit for the content of one message. A
// longer body is rejected with HTTP 400, so splitting is mandatory.
const maxMessageLen = 2000

const (
	fenceToken = "```"
	fenceOpen  = fenceToken + "\n"
	fenceClose = "\n" + fenceToken
)

// Chunk splits text into Discord-sized messages.
//
// Splitting works on runes, so multi-byte characters and emoji are never cut in
// half.
func Chunk(text string) []string {
	body := strings.TrimRight(text, "\n")
	if strings.TrimSpace(body) == "" {
		return nil
	}

	remaining := []rune(body)
	var chunks []string
	reopen := false

	for {
		extra := 0
		if reopen {
			extra = len([]rune(fenceOpen))
		}
		if len(remaining)+extra <= maxMessageLen {
			break
		}

		limit := maxMessageLen
		if reopen {
			limit -= extra
		}
		if limit < 1 {
			limit = 1
		}

		cut := splitPoint(remaining, limit)
		piece := renderPiece(remaining[:cut], reopen)

		// A code block longer than one message would leave this chunk with an
		// unclosed fence, which Discord renders as broken formatting. Close it
		// here and reopen it on the next chunk instead.
		closeInjected := false
		if !balancedFences(piece) {
			over := len([]rune(piece)) + len([]rune(fenceClose)) - maxMessageLen
			if over > 0 && over < cut {
				cut -= over
				piece = renderPiece(remaining[:cut], reopen)
			}
			if !balancedFences(piece) {
				piece += fenceClose
				closeInjected = true
			}
		}

		chunks = append(chunks, piece)
		remaining = remaining[cut:]
		reopen = closeInjected
	}

	return append(chunks, renderPiece(remaining, reopen))
}

// renderPiece prefixes the reopening fence when this chunk continues a code
// block that was started in an earlier message.
func renderPiece(runes []rune, reopen bool) string {
	if reopen {
		return fenceOpen + string(runes)
	}
	return string(runes)
}

func balancedFences(s string) bool { return strings.Count(s, fenceToken)%2 == 0 }

// splitPoint chooses where to break, preferring a paragraph break over a line
// break over a sentence end over a word break, and never leaving an odd number
// of ``` fences inside the chunk when it can avoid it.
func splitPoint(runes []rune, limit int) int {
	if len(runes) <= limit {
		return len(runes)
	}
	if limit < 1 {
		return 1
	}

	prefix := string(runes[:limit])
	cut := 0
	for _, sep := range []string{"\n\n", "\n", ". ", " "} {
		if i := strings.LastIndex(prefix, sep); i > 0 {
			cut = len([]rune(prefix[:i+len(sep)]))
			break
		}
	}
	if cut == 0 {
		cut = limit
	}
	return fenceSafe(runes, cut)
}

// fenceSafe moves the cut back to the start of the last ``` fence when the
// chunk would otherwise end inside a code block that opened part way through.
//
// When the fence opens at the very first character there is nothing to move
// back to; Chunk closes and reopens the block instead.
func fenceSafe(runes []rune, cut int) int {
	prefix := string(runes[:cut])
	if balancedFences(prefix) {
		return cut
	}
	if i := strings.LastIndex(prefix, fenceToken); i > 0 {
		return len([]rune(prefix[:i]))
	}
	return cut
}

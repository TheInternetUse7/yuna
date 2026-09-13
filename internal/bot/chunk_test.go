package bot

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkEmptyAndSmall(t *testing.T) {
	if got := Chunk(""); got != nil {
		t.Fatalf("Chunk(\"\") = %v, want nil", got)
	}
	if got := Chunk("   \n  "); got != nil {
		t.Fatalf("Chunk(whitespace) = %v, want nil", got)
	}

	got := Chunk("hello")
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("Chunk(hello) = %v, want [hello]", got)
	}
}

func TestChunkBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		size    int
		wantLen int
	}{
		{"exactly one message", maxMessageLen, 1},
		{"one over", maxMessageLen + 1, 2},
		{"ten thousand", 10000, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A body of 'a' has no separators at all, forcing hard cuts.
			body := strings.Repeat("a", tc.size)
			chunks := Chunk(body)
			if len(chunks) != tc.wantLen {
				t.Fatalf("got %d chunks, want %d", len(chunks), tc.wantLen)
			}
			assertChunksValid(t, chunks, body)
		})
	}
}

func TestChunkNeverExceedsLimit(t *testing.T) {
	body := strings.Repeat("word ", 4000) // ~20000 chars

	chunks := Chunk(body)
	if len(chunks) < 2 {
		t.Fatalf("expected a long body to split, got %d chunk(s)", len(chunks))
	}
	assertChunksValid(t, chunks, body)
}

func TestChunkPrefersParagraphBreak(t *testing.T) {
	// 1000 'a', a blank line, then 1500 'b'. A hard cut at 2000 would land
	// inside the 'b' run; the paragraph break must win instead.
	body := strings.Repeat("a", 1000) + "\n\n" + strings.Repeat("b", 1500)
	chunks := Chunk(body)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if strings.ContainsRune(chunks[1], 'a') {
		t.Fatalf("split did not use the paragraph break: first chunk ends %q",
			chunks[0][len(chunks[0])-8:])
	}
	assertChunksValid(t, chunks, body)
}

func TestChunkPreservesContent(t *testing.T) {
	body := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 300)
	assertChunksValid(t, Chunk(body), body)
}

func TestChunkHandlesMultiByteRunes(t *testing.T) {
	// Each emoji is one rune but four bytes; a byte-based splitter would cut one
	// in half and produce invalid UTF-8.
	body := strings.Repeat("\U0001F600", 3000)
	chunks := Chunk(body)
	for i, c := range chunks {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d is not valid UTF-8", i)
		}
		if n := utf8.RuneCountInString(c); n > maxMessageLen {
			t.Fatalf("chunk %d is %d runes, over the limit", i, n)
		}
	}
	assertChunksValid(t, chunks, body)
}

func TestChunkDoesNotSplitOpenCodeFence(t *testing.T) {
	// 1500 chars of prose, then an opening fence and a long code block whose
	// 2000-rune boundary falls inside it.
	prose := strings.Repeat("x", 1500)
	code := "```go\n" + strings.Repeat("line of code\n", 400) + "```"
	body := prose + "\n" + code

	chunks := Chunk(body)
	for i, c := range chunks {
		if !balancedRunes(c, "```") {
			t.Fatalf("chunk %d leaves an unclosed code fence:\n%s", i, c)
		}
		if n := utf8.RuneCountInString(c); n > maxMessageLen {
			t.Fatalf("chunk %d is %d runes, over the limit", i, n)
		}
	}

	// Closing and reopening the block adds fence markers, so the input is not
	// reproduced byte for byte. The code itself must survive intact.
	joined := strings.Join(chunks, "")
	if got := strings.Count(joined, "line of code"); got != 400 {
		t.Fatalf("code content lost: found %d of 400 lines", got)
	}
	if !strings.Contains(joined, prose) {
		t.Fatal("prose before the code block was not preserved")
	}
}

// assertChunksValid checks the invariants every split must hold: no chunk over
// the limit, and the concatenation reproducing the input exactly. A reply must
// carry nothing the model did not write, so nothing may be appended.
func assertChunksValid(t *testing.T, chunks []string, body string) {
	t.Helper()
	for i, c := range chunks {
		if n := utf8.RuneCountInString(c); n > maxMessageLen {
			t.Fatalf("chunk %d is %d runes, over the %d limit", i, n, maxMessageLen)
		}
	}

	if joined := strings.Join(chunks, ""); joined != body {
		t.Fatalf("chunks do not reconstruct the input:\n got %q\nwant %q", joined, body)
	}
}

func balancedRunes(s, token string) bool {
	return strings.Count(s, token)%2 == 0
}

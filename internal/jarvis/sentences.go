package jarvis

import (
	"strings"
	"unicode"
)

// commonAbbreviations is the set of abbreviations that should NOT be treated as
// sentence terminators when followed by whitespace. All entries are stored
// lower-cased; matching is case-insensitive.
var commonAbbreviations = map[string]bool{
	"mr":     true,
	"mrs":    true,
	"ms":     true,
	"dr":     true,
	"vs":     true,
	"e.g":    true,
	"i.e":    true,
	"etc":    true,
	"jr":     true,
	"sr":     true,
	"prof":   true,
	"rev":    true,
	"gen":    true,
	"gov":    true,
	"sgt":    true,
	"corp":   true,
	"inc":    true,
	"ltd":    true,
	"co":     true,
	"st":     true,
	"ave":    true,
	"blvd":   true,
	"approx": true,
}

// SplitSentences splits text into individual sentences suitable for streaming
// TTS. It splits on sentence-ending punctuation (. ! ?) followed by whitespace
// or end-of-string, while preserving abbreviations, decimal numbers, ellipses,
// single-letter abbreviations (e.g. "U.S.A."), and dots in URLs/file paths.
//
// Returns nil for empty or whitespace-only input. A single sentence without
// terminal punctuation is returned as a one-element slice.
func SplitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	runes := []rune(text)
	n := len(runes)
	var sentences []string
	start := 0

	for i := 0; i < n; i++ {
		ch := runes[i]

		// Only consider sentence-ending punctuation.
		if ch != '.' && ch != '!' && ch != '?' {
			continue
		}

		// Must be followed by whitespace or be at end of string to be a
		// sentence boundary. Punctuation mid-word (e.g. "file.txt") is
		// never a boundary.
		atEnd := i == n-1
		followedBySpace := i+1 < n && unicode.IsSpace(runes[i+1])
		if !atEnd && !followedBySpace {
			continue
		}

		// --- Period-specific edge cases ---
		if ch == '.' {
			// Ellipsis: three or more consecutive dots, or spaced dots
			// like ". . .". Do not split.
			if isEllipsis(runes, i) {
				continue
			}

			// Decimal number: digit immediately before the dot and digit
			// after. e.g. "3.14", "$1.23".
			if i > 0 && i+1 < n && unicode.IsDigit(runes[i-1]) && unicode.IsDigit(runes[i+1]) {
				continue
			}

			// Single-letter abbreviation: one letter before the dot,
			// like "U.S.A." or "J.K.". Look for pattern X. where X is
			// a single letter preceded by start, space, or another dot.
			if isSingleLetterAbbrev(runes, i) {
				continue
			}

			// Known multi-letter abbreviation: extract the word
			// preceding the dot and check against the list.
			if isKnownAbbreviation(runes, i) {
				continue
			}
		}

		// This is a sentence boundary. Extract the sentence from start
		// through the current punctuation mark.
		sentence := strings.TrimSpace(string(runes[start : i+1]))
		if sentence != "" {
			sentences = append(sentences, sentence)
		}
		start = i + 1
	}

	// Remainder after the last split point.
	if start < n {
		remainder := strings.TrimSpace(string(runes[start:]))
		if remainder != "" {
			sentences = append(sentences, remainder)
		}
	}

	if len(sentences) == 0 {
		return nil
	}
	return sentences
}

// isEllipsis returns true when the dot at position i is part of an ellipsis
// sequence: "..." or ". . ." patterns.
func isEllipsis(runes []rune, i int) bool {
	// Classic ellipsis: at least two more dots follow.
	if i+2 < len(runes) && runes[i+1] == '.' && runes[i+2] == '.' {
		return true
	}
	// Also catch the middle or trailing dot of "...".
	if i >= 1 && runes[i-1] == '.' {
		return true
	}
	// Spaced ellipsis: ". . ." — dot-space-dot pattern ahead.
	if i+4 < len(runes) && runes[i+1] == ' ' && runes[i+2] == '.' && runes[i+3] == ' ' && runes[i+4] == '.' {
		return true
	}
	// Middle/trailing portion of spaced ellipsis.
	if i >= 2 && runes[i-1] == ' ' && runes[i-2] == '.' {
		return true
	}
	return false
}

// isSingleLetterAbbrev returns true when the dot at position i terminates a
// single-letter abbreviation like "U." in "U.S.A." or "J." in "J.K.".
func isSingleLetterAbbrev(runes []rune, i int) bool {
	if i < 1 {
		return false
	}
	if !unicode.IsLetter(runes[i-1]) {
		return false
	}
	// The letter before the dot must be preceded by start-of-string,
	// whitespace, or another dot (chained abbreviation).
	if i >= 2 {
		prev := runes[i-2]
		if prev != '.' && !unicode.IsSpace(prev) {
			return false
		}
	}
	return true
}

// isKnownAbbreviation returns true when the dot at position i terminates a
// known abbreviation word like "Mr.", "Dr.", "etc.".
func isKnownAbbreviation(runes []rune, i int) bool {
	// Walk backwards from the character before the dot to find the word.
	j := i - 1
	for j >= 0 && (unicode.IsLetter(runes[j]) || runes[j] == '.') {
		j--
	}
	word := strings.ToLower(string(runes[j+1 : i]))
	return commonAbbreviations[word]
}

package jarvis

import (
	"testing"
)

func TestSplitSentences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "basic two sentences",
			input: "Hello. How are you?",
			want:  []string{"Hello.", "How are you?"},
		},
		{
			name:  "question and exclamation",
			input: "Really? Yes!",
			want:  []string{"Really?", "Yes!"},
		},
		{
			name:  "known abbreviation Mr",
			input: "Mr. Smith said hello. Goodbye.",
			want:  []string{"Mr. Smith said hello.", "Goodbye."},
		},
		{
			name:  "decimal number",
			input: "It costs $1.23 today. Nice.",
			want:  []string{"It costs $1.23 today.", "Nice."},
		},
		{
			name:  "ellipsis not split",
			input: "Wait... really?",
			want:  []string{"Wait... really?"},
		},
		{
			name:  "single letter abbreviation USA",
			input: "The U.S.A. is large. Indeed.",
			want:  []string{"The U.S.A. is large.", "Indeed."},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "no punctuation returns whole text",
			input: "Hello world",
			want:  []string{"Hello world"},
		},
		{
			name:  "multiple sentence types",
			input: "First. Second! Third?",
			want:  []string{"First.", "Second!", "Third?"},
		},
		{
			name:  "trailing whitespace trimmed",
			input: "Hello.  World.",
			want:  []string{"Hello.", "World."},
		},
		{
			name:  "whitespace only input",
			input: "   ",
			want:  nil,
		},
		{
			name:  "abbreviation Dr",
			input: "Dr. Jones is here. Please wait.",
			want:  []string{"Dr. Jones is here.", "Please wait."},
		},
		{
			name:  "sentence ending at EOF without space after",
			input: "Done.",
			want:  []string{"Done."},
		},
		{
			name:  "multiple abbreviations in sequence",
			input: "Prof. Rev. Smith arrived. Welcome.",
			want:  []string{"Prof. Rev. Smith arrived.", "Welcome."},
		},
		{
			name:  "exclamation only",
			input: "Wow!",
			want:  []string{"Wow!"},
		},
		{
			name:  "mixed punctuation no spaces between does not split",
			input: "file.txt is ready. Open it.",
			want:  []string{"file.txt is ready.", "Open it."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := SplitSentences(tt.input)

			if tt.want == nil {
				if got != nil {
					t.Fatalf("SplitSentences(%q) = %v, want nil", tt.input, got)
				}
				return
			}

			if len(got) != len(tt.want) {
				t.Fatalf("SplitSentences(%q) returned %d sentences %v, want %d %v",
					tt.input, len(got), got, len(tt.want), tt.want)
			}

			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("sentence[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

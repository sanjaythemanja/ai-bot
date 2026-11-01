package plugin

import "testing"

func TestCleanTextAndPlaceholder(t *testing.T) {
	got := CleanText(`  Hey Rakshit,\n\nRun at 6.\n\nBest,\nSanjay  `)
	if got != "Hey Rakshit,\n\nRun at 6.\n\nBest,\nSanjay" {
		t.Fatalf("literal \\n not turned into line breaks: %q", got)
	}
	for text, want := range map[string]string{
		"Best,\n[Your Name]":      "[Your Name]",
		"Hi [Recipient's name],":  "[Recipient's name]",
		"See [insert date here]":  "[insert date here]",
		"Notes [1] and [Q3 plan]": "",
		"Thanks,\nSanjay":         "",
	} {
		if ph := Placeholder(text); ph != want {
			t.Errorf("Placeholder(%q) = %q, want %q", text, ph, want)
		}
	}
}

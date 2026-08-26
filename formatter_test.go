package smartlimbo

import (
	"strings"
	"testing"

	c "go.minekube.com/common/minecraft/component"
)

func extractPlainText(comp c.Component) string {
	var b strings.Builder
	if t, ok := comp.(*c.Text); ok {
		b.WriteString(t.Content)
		for _, child := range t.Extra {
			b.WriteString(extractPlainText(child))
		}
	}
	return b.String()
}

func TestFormatText(t *testing.T) {
	comp := FormatText("<yellow>Waiting for <aqua>SMP</aqua> | Position <gold>#1</gold></yellow>")
	text := extractPlainText(comp)
	if !strings.Contains(text, "SMP") || !strings.Contains(text, "#1") {
		t.Errorf("Expected formatted text to contain SMP and #1, got %q", text)
	}
}

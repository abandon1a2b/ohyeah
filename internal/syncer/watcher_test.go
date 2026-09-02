package syncer

import (
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestWatchEnabledAcceptsYAMLBooleanAndLegacyStrings(t *testing.T) {
	tests := []struct {
		value any
		want  bool
	}{{true, true}, {false, false}, {"true", true}, {"1", true}, {"false", false}, {nil, false}}
	for _, test := range tests {
		source := model.Source{Options: map[string]any{"watch": test.value}}
		if got := watchEnabled(source); got != test.want {
			t.Fatalf("watchEnabled(%v) = %v, want %v", test.value, got, test.want)
		}
	}
}

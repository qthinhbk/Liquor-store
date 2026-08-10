package server

import (
	"encoding/json"
	"testing"
)

func TestValidNormalizedPolygon(t *testing.T) {
	tests := []struct {
		name    string
		polygon string
		want    bool
	}{
		{"valid", `[[0.1,0.1],[0.9,0.1],[0.5,0.9]]`, true},
		{"too few points", `[[0.1,0.1],[0.9,0.1]]`, false},
		{"outside frame", `[[0.1,0.1],[1.2,0.1],[0.5,0.9]]`, false},
		{"object shape", `{"points":[]}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validNormalizedPolygon(json.RawMessage(test.polygon)); got != test.want {
				t.Fatalf("validNormalizedPolygon() = %v, want %v", got, test.want)
			}
		})
	}
}

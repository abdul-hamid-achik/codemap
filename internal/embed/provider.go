// Package embed turns text into vectors via a pluggable provider. The default
// provider talks to a local Ollama HTTP API; no third-party SDK is used, which
// keeps the binary small and pure-Go.
package embed

import (
	"context"
	"fmt"
)

// Provider produces embeddings for a batch of texts.
type Provider interface {
	// Embed returns one vector per input text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Profile describes the embedding space this provider produces.
	Profile() EmbeddingProfile
}

// EmbeddingProfile identifies an embedding space. It is stored in the vector
// collection's metadata so a later index run with a different provider/model/
// dimension can be detected and forced to rebuild instead of corrupting the
// space.
type EmbeddingProfile struct {
	Provider   string `json:"provider" yaml:"provider"`
	Model      string `json:"model" yaml:"model"`
	Dimensions int    `json:"dimensions" yaml:"dimensions"`
	Distance   string `json:"distance" yaml:"distance"`
}

// Compatible reports whether two profiles describe the same embedding space.
func (p EmbeddingProfile) Compatible(o EmbeddingProfile) bool {
	return p.Provider == o.Provider &&
		p.Model == o.Model &&
		p.Dimensions == o.Dimensions &&
		p.Distance == o.Distance
}

// String renders the profile as "provider/model dimsd distance".
func (p EmbeddingProfile) String() string {
	return fmt.Sprintf("%s/%s %dd %s", p.Provider, p.Model, p.Dimensions, p.Distance)
}

// IncompatibleError describes a profile mismatch with rebuild guidance.
type IncompatibleError struct {
	Have EmbeddingProfile // stored in the index
	Want EmbeddingProfile // current configuration
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf(
		"embedding profile changed: index was built with %q but config wants %q; reindex with 'codemap index --reindex'",
		e.Have, e.Want)
}

// CheckCompatible returns an *IncompatibleError if have and want differ.
func CheckCompatible(have, want EmbeddingProfile) error {
	if have.Compatible(want) {
		return nil
	}
	return &IncompatibleError{Have: have, Want: want}
}

package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"unicode/utf8"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// BenchmarkProfileSchemaTax is hermetic: it drives a real MCP tools/list
// round-trip over the SDK's in-memory transport and never starts a language
// server, embedding provider, or paid model. schema-approx-tokens uses the
// conventional chars/4 planning estimate; it is deliberately labelled as an
// approximation rather than pretending to be a model-specific tokenizer.
func BenchmarkProfileSchemaTax(b *testing.B) {
	for _, profile := range []string{ProfileCore, ProfileAgent, ProfileFull} {
		b.Run(profile, func(b *testing.B) {
			home := b.TempDir()
			b.Setenv("HOME", home)
			b.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			b.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
			b.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
			b.Setenv("CODEMAP_CONFIG", "")
			b.Setenv("XDG_DATA_HOME", "")

			sess, err := app.Open("")
			if err != nil {
				b.Fatal(err)
			}
			defer sess.Close()
			sess.Config.MCP.Profile = profile
			srv := NewServer(sess)

			clientT, serverT := sdkmcp.NewInMemoryTransports()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _ = srv.serve(ctx, serverT) }()

			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "schema-bench", Version: "0"}, nil)
			cs, err := client.Connect(ctx, clientT, nil)
			if err != nil {
				b.Fatal(err)
			}
			defer cs.Close()

			listed, err := cs.ListTools(ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			payload, err := json.Marshal(listed.Tools)
			if err != nil {
				b.Fatal(err)
			}
			schemaChars := utf8.RuneCount(payload)
			b.ResetTimer()
			for range b.N {
				if _, err := cs.ListTools(ctx, nil); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(listed.Tools)), "tools")
			b.ReportMetric(float64(schemaChars), "schema-chars")
			b.ReportMetric(float64((schemaChars+3)/4), "schema-approx-tokens")
		})
	}
}

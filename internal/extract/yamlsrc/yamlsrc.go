// Package yamlsrc indexes YAML key paths and explicit Task/Compose/workflow
// dependencies without evaluating templates, commands, tags, or aliases.
package yamlsrc

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"gopkg.in/yaml.v3"
)

type Extractor struct{}

func New() *Extractor               { return &Extractor{} }
func (*Extractor) Language() string { return "yaml" }

// KeyFQN uses JSON Pointer escaping: a literal dot/slash in a key cannot
// collide with a nested mapping. The first document has no numeric suffix.
func KeyFQN(file string, doc int, keys ...string) string {
	base := filepath.ToSlash(file) + "#yaml"
	if doc > 0 {
		base += "@" + strconv.Itoa(doc+1)
	}
	for _, k := range keys {
		base += "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
	}
	return base
}

func (*Extractor) ExtractFile(file string, src []byte) (*extract.FileResult, error) {
	file = filepath.ToSlash(file)
	res := &extract.FileResult{Path: file, Language: "yaml"}
	lines := strings.Split(string(src), "\n")
	dec := yaml.NewDecoder(bytes.NewReader(src))
	for doc := 0; ; doc++ {
		var tree yaml.Node
		if err := dec.Decode(&tree); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if doc >= 128 {
			return nil, fmt.Errorf("YAML exceeds 128 documents")
		}
		if len(tree.Content) == 0 {
			continue
		}
		var walk func(*yaml.Node, []string, int, int) error
		walk = func(n *yaml.Node, keys []string, limit, depth int) error {
			if depth > 64 || len(res.Symbols) > 100000 {
				return fmt.Errorf("YAML structure exceeds index limits")
			}
			switch n.Kind {
			case yaml.MappingNode:
				seen := map[string]bool{}
				for i := 0; i+1 < len(n.Content); i += 2 {
					key, value := n.Content[i], n.Content[i+1]
					if key.Kind != yaml.ScalarNode {
						continue
					}
					if seen[key.Value] {
						return fmt.Errorf("duplicate YAML key at line %d", key.Line)
					}
					seen[key.Value] = true
					path := append(append([]string{}, keys...), key.Value)
					end := limit
					if i+2 < len(n.Content) {
						end = n.Content[i+2].Line - 1
					}
					if end < key.Line {
						end = key.Line
					}
					if end > len(lines) {
						end = len(lines)
					}
					for end > key.Line && strings.TrimSpace(lines[end-1]) == "" {
						end--
					}
					fqn := KeyFQN(file, doc, path...)
					res.Symbols = append(res.Symbols, extract.Symbol{Name: strings.Join(path, "."), FQN: fqn, Kind: extract.KindKey, Language: "yaml", StartLine: key.Line, EndLine: end, Signature: strings.Join(path, "."), Source: strings.Join(lines[key.Line-1:end], "\n")})
					if doc == 0 {
						references(res, file, path, value, fqn)
					}
					if err := walk(value, path, end, depth+1); err != nil {
						return err
					}
				}
			case yaml.SequenceNode:
				for i, value := range n.Content {
					end := limit
					if i+1 < len(n.Content) {
						end = n.Content[i+1].Line - 1
					}
					if err := walk(value, append(append([]string{}, keys...), strconv.Itoa(i)), end, depth+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := walk(tree.Content[0], nil, documentEnd(tree.Content[0].Line, lines), 0); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// Source boundaries must use raw lines: YAML folds scalar values, losing newlines.
func documentEnd(start int, lines []string) int {
	for i := start; i < len(lines); i++ {
		line := lines[i]
		for _, marker := range []string{"---", "..."} {
			if strings.TrimRight(line, "\r\t ") == marker || strings.HasPrefix(line, marker+" ") || strings.HasPrefix(line, marker+"\t") {
				return i
			}
		}
	}
	return len(lines)
}

func references(res *extract.FileResult, file string, path []string, value *yaml.Node, from string) {
	if len(path) != 3 {
		return
	}
	group, field := path[0], path[2]
	base := strings.ToLower(filepath.Base(file))
	workflow := strings.HasPrefix(file, ".github/workflows/")
	isTask := base == "taskfile.yml" || base == "taskfile.yaml" || base == "taskfile.dist.yml" || base == "taskfile.dist.yaml"
	isCompose := strings.Contains(base, "compose")
	if !((isTask && group == "tasks" && field == "deps") || (workflow && group == "jobs" && field == "needs") || (isCompose && group == "services" && field == "depends_on")) {
		return
	}
	var names []string
	switch value.Kind {
	case yaml.ScalarNode:
		names = append(names, value.Value)
	case yaml.SequenceNode:
		for _, c := range value.Content {
			if c.Kind == yaml.ScalarNode {
				names = append(names, c.Value)
			} else if isTask && c.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(c.Content); i += 2 {
					if c.Content[i].Value == "task" && c.Content[i+1].Kind == yaml.ScalarNode {
						names = append(names, c.Content[i+1].Value)
					}
				}
			}
		}
	case yaml.MappingNode:
		if isCompose {
			for i := 0; i+1 < len(value.Content); i += 2 {
				names = append(names, value.Content[i].Value)
			}
		}
	}
	for _, name := range names {
		if name == "" || strings.ContainsAny(name, "${}*") {
			continue
		}
		res.References = append(res.References, extract.Reference{From: from, ToFQN: KeyFQN(file, 0, group, name), Kind: "depends_on", Line: value.Line})
	}
}

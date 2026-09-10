package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ErrPortAlreadyDeclared reports that the port AddPortToRepoConfig was
// asked to add is already in the file. A sentinel rather than a silent
// no-op so `claudio ports --add` can tell the user their declaration was
// already there instead of implying it wrote something.
var ErrPortAlreadyDeclared = fmt.Errorf("config: port already declared")

// AddPortToRepoConfig appends a port declaration to <dir>/.claudio.yml,
// creating the file if it does not exist.
//
// The file is edited as a yaml.Node tree rather than by unmarshalling
// into RepoConfig and re-marshalling it. That matters: a round-trip
// through the struct would silently drop every key this Go type does not
// model and rewrite the user's formatting and comments wholesale. This
// file belongs to the user, so the edit is kept to the smallest one that
// does the job — the ports list gains an entry and nothing else moves.
//
// Callers pass the instance worktree, whose .claudio.yml is what a
// restart re-derives its ports from — that is what makes an added port
// survive the restart rather than being released with the rest of the
// instance's reservations. The edit is left uncommitted for the user to
// keep or discard; this never commits on their behalf.
func AddPortToRepoConfig(dir string, name string, container int) error {
	path := dir + "/.claudio.yml"

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}

	// Reject a port that is already declared before touching the file, so
	// a repeated --add cannot produce a duplicate entry that would then
	// fail LoadRepoConfig's validation on the next create.
	var existing RepoConfig
	if len(data) > 0 {
		if err := decodeStrict(data, &existing); err != nil {
			return fmt.Errorf("config: parse %s: %w", path, err)
		}
		for _, p := range existing.Ports {
			if p.Container == container {
				return fmt.Errorf("%w: %d (%s)", ErrPortAlreadyDeclared, container, p.Name)
			}
		}
	}

	var root yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	doc := documentMapping(&root)

	entry := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			scalar("name"), scalar(name),
			scalar("container"), intScalar(container),
		},
	}

	if ports := valueFor(doc, "ports"); ports != nil {
		// An explicit `ports:` with no entries parses as a null scalar, not
		// an empty sequence; turn it into one before appending.
		if ports.Kind != yaml.SequenceNode {
			ports.Kind = yaml.SequenceNode
			ports.Tag = "!!seq"
			ports.Value = ""
			ports.Content = nil
		}
		ports.Content = append(ports.Content, entry)
	} else {
		doc.Content = append(doc.Content, scalar("ports"), &yaml.Node{
			Kind:    yaml.SequenceNode,
			Tag:     "!!seq",
			Content: []*yaml.Node{entry},
		})
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("config: render %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// documentMapping returns the mapping node to edit, initializing root if
// the file was empty or held nothing but comments.
func documentMapping(root *yaml.Node) *yaml.Node {
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc := root.Content[0]
		if doc.Kind == yaml.MappingNode {
			return doc
		}
		// A file holding a non-mapping (e.g. a bare list) is not a config
		// this can extend; replace it rather than corrupt it further. Callers
		// only reach here for files that already decoded into RepoConfig
		// cleanly, so in practice this is the empty/null case.
		doc.Kind = yaml.MappingNode
		doc.Tag = "!!map"
		doc.Value = ""
		doc.Content = nil
		return doc
	}
	doc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Kind = yaml.DocumentNode
	root.Content = []*yaml.Node{doc}
	return doc
}

// valueFor returns the value node for key in a mapping, or nil.
func valueFor(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func intScalar(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v)}
}

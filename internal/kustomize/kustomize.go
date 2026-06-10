// Package kustomize edits Kustomize overlay manifests in place to update
// image tags.  Implements `stratt deploy <env> <version>` (R2.5).
//
// Implementation note: we parse and re-emit via gopkg.in/yaml.v3 Node
// trees rather than shelling out to `kustomize edit set image`.  The
// Node API preserves comments and field ordering — `kustomize edit`
// reformats files in ways teams find annoying.
package kustomize

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	yaml "gopkg.in/yaml.v3"
)

// OverlayPath returns the standard kustomization.yaml path for env.
func OverlayPath(root, env string) string {
	return filepath.Join(root, "deploy", "overlays", env, "kustomization.yaml")
}

// ImageChange describes a single image-tag update found in a manifest.
type ImageChange struct {
	Image  string // the image name (`images[i].name`)
	OldTag string
	NewTag string
}

// SetImage updates the newTag for the named image in overlayPath to
// version, in place.  Returns the change actually made (with OldTag
// populated for display), or an error if the image isn't present.
//
// If imageName is empty, SetImage will succeed only when the overlay
// has exactly one image entry, and update that one.  This is the
// "primary image" path used by single-image repos.
func SetImage(overlayPath, imageName, version string) (*ImageChange, error) {
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		return nil, err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", overlayPath, err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("parse %s: unexpected YAML shape", overlayPath)
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse %s: expected top-level mapping", overlayPath)
	}

	imagesSeq := findKey(doc, "images")
	if imagesSeq == nil || imagesSeq.Kind != yaml.SequenceNode || len(imagesSeq.Content) == 0 {
		return nil, errors.New("no `images` entries in this overlay")
	}

	var (
		target  *yaml.Node
		matched string
	)
	if imageName == "" && len(imagesSeq.Content) == 1 {
		target = imagesSeq.Content[0]
		if n := findKey(target, "name"); n != nil {
			matched = n.Value
		}
	} else {
		for _, entry := range imagesSeq.Content {
			n := findKey(entry, "name")
			if n != nil && n.Value == imageName {
				target = entry
				matched = imageName
				break
			}
		}
	}
	if target == nil {
		if imageName == "" {
			return nil, errors.New("multiple images in overlay; pass --image=<name> to choose")
		}
		return nil, fmt.Errorf("image %q not found in overlay", imageName)
	}

	tagNode := findOrCreateKey(target, "newTag")
	change := &ImageChange{Image: matched, OldTag: tagNode.Value, NewTag: version}
	tagNode.Kind = yaml.ScalarNode
	tagNode.Tag = "!!str"
	tagNode.Value = version

	// Encode with a 2-space indent (yaml.Marshal defaults to 4), matching
	// the near-universal kustomization.yaml convention so the edit doesn't
	// re-indent the whole file — the comment/formatting preservation this
	// package promises extends to indentation width.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("re-marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("re-marshal: %w", err)
	}
	if err := os.WriteFile(overlayPath, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}
	return change, nil
}

// findKey returns the value node for key in a mapping node, or nil if
// the key isn't present.
func findKey(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		k := m.Content[i]
		if k.Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// findOrCreateKey ensures key exists in mapping m and returns its value
// node.  If created, the new entry is appended with an empty scalar value.
func findOrCreateKey(m *yaml.Node, key string) *yaml.Node {
	if n := findKey(m, key); n != nil {
		return n
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ""}
	m.Content = append(m.Content, keyNode, valNode)
	return valNode
}

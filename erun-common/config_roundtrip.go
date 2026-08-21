package eruncommon

import (
	"bytes"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// marshalConfigPreservingUnknownFields marshals config the same way
// yaml.Marshal does, then overlays the result onto whatever document already
// lives in existing (the config file's current bytes) so that a binary built
// before some field existed can never delete that field when it writes: it
// never gets a chance to, because the merge only touches keys config's own
// type actually declares. Every other key — including nested ones this
// binary's struct types cannot represent at all — survives untouched.
//
// A mixed-version install (desktop and CLI updating on separate cadences) is
// the normal state, not an edge case, so every config.yaml writer routes
// through this before writing (see erun-common/AGENTS.md, and #1075).
func marshalConfigPreservingUnknownFields(existing []byte, config any) ([]byte, error) {
	fresh, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(existing)) == 0 {
		return fresh, nil
	}

	var oldDoc, newDoc yaml.Node
	if err := yaml.Unmarshal(existing, &oldDoc); err != nil {
		// The previous file is not parseable YAML at all; there is nothing
		// sound to preserve from it, so fall back to the plain marshal.
		return fresh, nil
	}
	if err := yaml.Unmarshal(fresh, &newDoc); err != nil {
		return fresh, nil
	}

	oldRoot, newRoot := documentRoot(&oldDoc), documentRoot(&newDoc)
	if oldRoot == nil || newRoot == nil {
		return fresh, nil
	}

	merged := mergeConfigNode(oldRoot, newRoot, reflect.TypeOf(config))
	out, err := yaml.Marshal(merged)
	if err != nil {
		return fresh, nil
	}
	return out, nil
}

func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}

// mergeConfigNode overlays new onto old for exactly the keys t's fields (recursively,
// through embedded/inline fields) declare. Every key old carries that t does not
// declare is copied through untouched, in its original position — that is the whole
// point: t cannot drop what it does not know exists.
func mergeConfigNode(old, new *yaml.Node, t reflect.Type) *yaml.Node {
	if old == nil {
		return new
	}
	if new == nil {
		return old
	}
	if old.Kind != yaml.MappingNode || new.Kind != yaml.MappingNode {
		return new
	}

	known := knownYAMLFields(t)
	merged := &yaml.Node{Kind: yaml.MappingNode, Tag: new.Tag, Style: new.Style}
	handled := make(map[string]bool, len(known))

	for i := 0; i+1 < len(old.Content); i += 2 {
		key := old.Content[i]
		fieldType, isKnown := known[key.Value]
		if !isKnown {
			merged.Content = append(merged.Content, key, old.Content[i+1])
			continue
		}
		handled[key.Value] = true
		newValue := mappingValue(new, key.Value)
		if newValue == nil {
			// A known field with no current value (omitempty dropped it from the
			// fresh marshal): this run cleared it, so drop the key.
			continue
		}
		merged.Content = append(merged.Content, key, mergeConfigValue(old.Content[i+1], newValue, fieldType))
	}

	for i := 0; i+1 < len(new.Content); i += 2 {
		key := new.Content[i].Value
		if handled[key] {
			continue
		}
		merged.Content = append(merged.Content, new.Content[i], new.Content[i+1])
	}

	return merged
}

// mergeConfigValue recurses into struct-, slice-of-struct-, and map-of-struct-
// shaped field values so an unknown field nested inside one of them (e.g. a
// cloud provider alias's "erun:" block, several levels below the top of
// config.yaml) survives too. Scalars, and slices/maps whose element type is
// not itself a struct, have nothing further to preserve, so the fresh value
// wins outright.
func mergeConfigValue(old, new *yaml.Node, fieldType reflect.Type) *yaml.Node {
	if fieldType == nil {
		return new
	}
	for fieldType.Kind() == reflect.Ptr {
		fieldType = fieldType.Elem()
	}
	switch fieldType.Kind() {
	case reflect.Struct:
		return mergeConfigNode(old, new, fieldType)
	case reflect.Slice, reflect.Array:
		return mergeConfigSliceNode(old, new, fieldType.Elem())
	case reflect.Map:
		return mergeConfigMapNode(old, new, fieldType.Elem())
	default:
		return new
	}
}

// mergeConfigSliceNode merges a sequence field whose elements are (pointers
// to) structs. When the element type exposes a natural identity field (Alias,
// ID, or Name — the conventions every persisted list in this package already
// uses), items are matched by that value so an append or removal elsewhere in
// the list does not fall back to a wholesale replace. Otherwise, same-length
// sequences merge positionally; a length change with no identity field to key
// on can't be aligned soundly, so the fresh sequence wins as-is.
func mergeConfigSliceNode(old, new *yaml.Node, elemType reflect.Type) *yaml.Node {
	if old.Kind != yaml.SequenceNode || new.Kind != yaml.SequenceNode {
		return new
	}
	for elemType.Kind() == reflect.Ptr {
		elemType = elemType.Elem()
	}
	if elemType.Kind() != reflect.Struct {
		return new
	}

	if identityKey, ok := sliceIdentityField(elemType); ok {
		return mergeConfigSliceNodeByIdentity(old, new, elemType, identityKey)
	}

	if len(old.Content) != len(new.Content) {
		return new
	}
	merged := &yaml.Node{Kind: yaml.SequenceNode, Tag: new.Tag, Style: new.Style}
	for i := range new.Content {
		merged.Content = append(merged.Content, mergeConfigNode(old.Content[i], new.Content[i], elemType))
	}
	return merged
}

// mergeConfigSliceNodeByIdentity is mergeConfigSliceNode's identity-keyed path:
// old items are indexed by identityKey's value, and each new item merges
// against the old item sharing that value (an add or removal elsewhere in the
// list does not disturb the rest).
func mergeConfigSliceNodeByIdentity(old, new *yaml.Node, elemType reflect.Type, identityKey string) *yaml.Node {
	oldByKey := make(map[string]*yaml.Node, len(old.Content))
	for _, item := range old.Content {
		if key := mappingValue(item, identityKey); key != nil {
			oldByKey[key.Value] = item
		}
	}
	merged := &yaml.Node{Kind: yaml.SequenceNode, Tag: new.Tag, Style: new.Style}
	for _, item := range new.Content {
		key := mappingValue(item, identityKey)
		if key == nil {
			merged.Content = append(merged.Content, item)
			continue
		}
		if oldItem, found := oldByKey[key.Value]; found {
			merged.Content = append(merged.Content, mergeConfigNode(oldItem, item, elemType))
			continue
		}
		merged.Content = append(merged.Content, item)
	}
	return merged
}

// mergeConfigMapNode merges a mapping field whose values are (pointers to)
// structs, keyed by the map's own keys (already a stable identity, unlike a
// slice index). A key present in new but absent from old is added; a key
// present in old but dropped from new is treated as an intentional removal
// (map keys are user data the current binary fully owns, not a schema the
// binary might not recognize yet — that ambiguity only applies to struct
// fields, handled in mergeConfigNode).
func mergeConfigMapNode(old, new *yaml.Node, elemType reflect.Type) *yaml.Node {
	if old.Kind != yaml.MappingNode || new.Kind != yaml.MappingNode {
		return new
	}
	merged := &yaml.Node{Kind: yaml.MappingNode, Tag: new.Tag, Style: new.Style}
	for i := 0; i+1 < len(new.Content); i += 2 {
		key := new.Content[i]
		oldValue := mappingValue(old, key.Value)
		if oldValue == nil {
			merged.Content = append(merged.Content, key, new.Content[i+1])
			continue
		}
		merged.Content = append(merged.Content, key, mergeConfigValue(oldValue, new.Content[i+1], elemType))
	}
	return merged
}

func sliceIdentityField(elemType reflect.Type) (string, bool) {
	for _, candidate := range []string{"Alias", "ID", "Name"} {
		field, ok := elemType.FieldByName(candidate)
		if !ok || field.Type.Kind() != reflect.String {
			continue
		}
		name, inline, skip := parseYAMLFieldTag(field)
		if skip || inline {
			continue
		}
		return name, true
	}
	return "", false
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// knownYAMLFields returns the yaml key -> field-type map t declares, inlining
// embedded/",inline" fields so their keys count as t's own. Non-struct types
// (including the zero reflect.Type) declare no keys.
func knownYAMLFields(t reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	if t == nil {
		return fields
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fields
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" && !field.Anonymous {
			continue // unexported and not embedded: yaml.v3 never marshals it
		}
		name, inline, skip := parseYAMLFieldTag(field)
		if skip {
			continue
		}
		if inline {
			for k, v := range knownYAMLFields(field.Type) {
				fields[k] = v
			}
			continue
		}
		fields[name] = field.Type
	}
	return fields
}

// parseYAMLFieldTag mirrors yaml.v3's own field-name resolution closely enough
// for schema purposes: an explicit "-" tag is skipped, an explicit name wins,
// an anonymous field with no name defaults to inline, and everything else
// defaults to the lowercased Go field name.
func parseYAMLFieldTag(field reflect.StructField) (name string, inline bool, skip bool) {
	tag := field.Tag.Get("yaml")
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	for _, opt := range parts[1:] {
		if opt == "inline" {
			inline = true
		}
	}
	name = parts[0]
	if name == "" {
		if field.Anonymous {
			inline = true
		} else {
			name = strings.ToLower(field.Name)
		}
	}
	return name, inline, false
}

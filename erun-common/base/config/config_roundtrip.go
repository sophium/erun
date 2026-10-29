package config

import (
	"bytes"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// MarshalConfigPreservingUnknownFields marshals config onto the existing document,
// touching only keys config's type declares, so an older binary never deletes newer fields.
func MarshalConfigPreservingUnknownFields(existing []byte, config any) ([]byte, error) {
	fresh, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(existing)) == 0 {
		return fresh, nil
	}

	if out, ok := mergeConfigDocuments(existing, fresh, reflect.TypeOf(config)); ok {
		return out, nil
	}
	return fresh, nil
}

func mergeConfigDocuments(existing, fresh []byte, t reflect.Type) ([]byte, bool) {
	// Unparseable existing YAML has nothing to preserve; callers fall back to fresh.
	var oldDoc, newDoc yaml.Node
	if err := yaml.Unmarshal(existing, &oldDoc); err != nil {
		return nil, false
	}
	if err := yaml.Unmarshal(fresh, &newDoc); err != nil {
		return nil, false
	}

	oldRoot, newRoot := documentRoot(&oldDoc), documentRoot(&newDoc)
	if oldRoot == nil || newRoot == nil {
		return nil, false
	}

	out, err := yaml.Marshal(mergeConfigNode(oldRoot, newRoot, t))
	if err != nil {
		return nil, false
	}
	return out, true
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

// mergeConfigNode overlays updated onto old for the keys t declares; undeclared
// keys are kept in place.
func mergeConfigNode(old, updated *yaml.Node, t reflect.Type) *yaml.Node {
	if old == nil {
		return updated
	}
	if updated == nil {
		return old
	}
	if old.Kind != yaml.MappingNode || updated.Kind != yaml.MappingNode {
		return updated
	}

	known := knownYAMLFields(t)
	merged := &yaml.Node{Kind: yaml.MappingNode, Tag: updated.Tag, Style: updated.Style}
	handled := make(map[string]bool, len(known))

	for i := 0; i+1 < len(old.Content); i += 2 {
		key := old.Content[i]
		fieldType, isKnown := known[key.Value]
		if !isKnown {
			merged.Content = append(merged.Content, key, old.Content[i+1])
			continue
		}
		handled[key.Value] = true
		newValue := mappingValue(updated, key.Value)
		if newValue == nil {
			// omitempty dropped it: this run cleared the field.
			continue
		}
		merged.Content = append(merged.Content, key, mergeConfigValue(old.Content[i+1], newValue, fieldType))
	}

	for i := 0; i+1 < len(updated.Content); i += 2 {
		key := updated.Content[i].Value
		if handled[key] {
			continue
		}
		merged.Content = append(merged.Content, updated.Content[i], updated.Content[i+1])
	}

	return merged
}

// mergeConfigValue recurses into struct-shaped values so nested unknown keys survive.
func mergeConfigValue(old, updated *yaml.Node, fieldType reflect.Type) *yaml.Node {
	if fieldType == nil {
		return updated
	}
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	kind := fieldType.Kind()
	if kind == reflect.Struct {
		return mergeConfigNode(old, updated, fieldType)
	}
	if kind == reflect.Slice || kind == reflect.Array {
		return mergeConfigSliceNode(old, updated, fieldType.Elem())
	}
	if kind == reflect.Map {
		return mergeConfigMapNode(old, updated, fieldType.Elem())
	}
	return updated
}

// mergeConfigSliceNode matches struct elements by Alias/ID/Name when present, else
// by position; a length change without identity takes the fresh sequence.
func mergeConfigSliceNode(old, updated *yaml.Node, elemType reflect.Type) *yaml.Node {
	if old.Kind != yaml.SequenceNode || updated.Kind != yaml.SequenceNode {
		return updated
	}
	for elemType.Kind() == reflect.Pointer {
		elemType = elemType.Elem()
	}
	if elemType.Kind() != reflect.Struct {
		return updated
	}

	if identityKey, ok := sliceIdentityField(elemType); ok {
		return mergeConfigSliceNodeByIdentity(old, updated, elemType, identityKey)
	}

	if len(old.Content) != len(updated.Content) {
		return updated
	}
	merged := &yaml.Node{Kind: yaml.SequenceNode, Tag: updated.Tag, Style: updated.Style}
	for i := range updated.Content {
		merged.Content = append(merged.Content, mergeConfigNode(old.Content[i], updated.Content[i], elemType))
	}
	return merged
}

func mergeConfigSliceNodeByIdentity(old, updated *yaml.Node, elemType reflect.Type, identityKey string) *yaml.Node {
	oldByKey := make(map[string]*yaml.Node, len(old.Content))
	for _, item := range old.Content {
		if key := mappingValue(item, identityKey); key != nil {
			oldByKey[key.Value] = item
		}
	}
	merged := &yaml.Node{Kind: yaml.SequenceNode, Tag: updated.Tag, Style: updated.Style}
	for _, item := range updated.Content {
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

// mergeConfigMapNode merges struct-valued maps by key; a key missing from updated
// is an intentional removal, since map keys are user data, not schema.
func mergeConfigMapNode(old, updated *yaml.Node, elemType reflect.Type) *yaml.Node {
	if old.Kind != yaml.MappingNode || updated.Kind != yaml.MappingNode {
		return updated
	}
	merged := &yaml.Node{Kind: yaml.MappingNode, Tag: updated.Tag, Style: updated.Style}
	for i := 0; i+1 < len(updated.Content); i += 2 {
		key := updated.Content[i]
		oldValue := mappingValue(old, key.Value)
		if oldValue == nil {
			merged.Content = append(merged.Content, key, updated.Content[i+1])
			continue
		}
		merged.Content = append(merged.Content, key, mergeConfigValue(oldValue, updated.Content[i+1], elemType))
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

// legacyYAMLKeys lists keys UnmarshalYAML migrates away from; without it the merge
// would keep a legacy key forever and re-migrate it on every load.
type legacyYAMLKeys interface {
	legacyYAMLKeys() []string
}

// knownYAMLFields returns t's yaml keys and field types, including inline and legacy keys.
func knownYAMLFields(t reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	if t == nil {
		return fields
	}
	for t.Kind() == reflect.Pointer {
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
	addLegacyYAMLKeys(fields, t)
	return fields
}

// addLegacyYAMLKeys maps legacy keys to a nil type; the merge drops them without
// consulting the type.
func addLegacyYAMLKeys(fields map[string]reflect.Type, t reflect.Type) {
	legacy, ok := reflect.New(t).Elem().Interface().(legacyYAMLKeys)
	if !ok {
		return
	}
	for _, key := range legacy.legacyYAMLKeys() {
		if _, exists := fields[key]; !exists {
			fields[key] = nil
		}
	}
}

// parseYAMLFieldTag mirrors yaml.v3's field-name resolution.
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

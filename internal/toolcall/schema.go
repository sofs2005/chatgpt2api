package toolcall

import "encoding/json"

func NormalizeForSchemas(calls []ParsedCall, tools any) []ParsedCall {
	if len(calls) == 0 {
		return nil
	}
	schemas := map[string]any{}
	for _, meta := range toolMetas(tools) {
		schemas[meta.Name] = meta.Schema
	}

	out := make([]ParsedCall, 0, len(calls))
	for _, call := range calls {
		normalized := ParsedCall{Name: call.Name, Input: cloneMap(call.Input)}
		if schema, ok := schemas[call.Name]; ok {
			if schemaMap, ok := schema.(map[string]any); ok {
				if input, ok := normalizeValue(normalized.Input, schemaMap).(map[string]any); ok {
					normalized.Input = input
				}
			}
		}
		out = append(out, normalized)
	}
	return out
}

func normalizeValue(value any, schema map[string]any) any {
	if schema == nil {
		return value
	}
	if shouldJSONStringify(schema) {
		if _, ok := value.(string); ok {
			return value
		}
		if buf, err := json.Marshal(value); err == nil {
			return string(buf)
		}
		return value
	}

	typeName := asString(schema["type"])
	if typeName == "object" {
		obj, ok := value.(map[string]any)
		if !ok {
			return value
		}
		out := cloneMap(obj)
		props, _ := schema["properties"].(map[string]any)
		for key, prop := range props {
			propSchema, ok := prop.(map[string]any)
			if !ok {
				continue
			}
			current, exists := out[key]
			if !exists {
				continue
			}
			out[key] = normalizeValue(current, propSchema)
		}
		return out
	}
	return value
}

func shouldJSONStringify(schema map[string]any) bool {
	if asString(schema["type"]) == "string" {
		return true
	}
	if constValue, ok := schema["const"]; ok {
		_, isString := constValue.(string)
		return isString
	}
	if enumValues, ok := schema["enum"].([]any); ok && len(enumValues) > 0 {
		for _, item := range enumValues {
			if _, isString := item.(string); !isString {
				return false
			}
		}
		return true
	}
	return false
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

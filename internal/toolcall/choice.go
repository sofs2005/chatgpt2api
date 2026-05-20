package toolcall

import "strings"

func PolicyFromToolChoice(choice any) ChoicePolicy {
	switch v := choice.(type) {
	case nil:
		return ChoicePolicy{Mode: ChoiceAuto}
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case ChoiceNone:
			return ChoicePolicy{Mode: ChoiceNone}
		case ChoiceRequired:
			return ChoicePolicy{Mode: ChoiceRequired}
		case ChoiceAuto:
			return ChoicePolicy{Mode: ChoiceAuto}
		default:
			return ChoicePolicy{Mode: ChoiceAuto}
		}
	case map[string]any:
		kind := strings.ToLower(strings.TrimSpace(asString(v["type"])))
		switch kind {
		case "function":
			if fn, ok := v["function"].(map[string]any); ok {
				if name := strings.TrimSpace(asString(fn["name"])); name != "" {
					return ChoicePolicy{Mode: ChoiceForced, Name: name}
				}
			}
		case "tool":
			if name := strings.TrimSpace(asString(v["name"])); name != "" {
				return ChoicePolicy{Mode: ChoiceForced, Name: name}
			}
		case "any":
			return ChoicePolicy{Mode: ChoiceRequired}
		case "auto":
			return ChoicePolicy{Mode: ChoiceAuto}
		case "none":
			return ChoicePolicy{Mode: ChoiceNone}
		}
	}
	return ChoicePolicy{Mode: ChoiceAuto}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

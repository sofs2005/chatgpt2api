package protocol

import "strings"

func BuildOpenAISearchPayload(query, model string) map[string]any {
	query = strings.TrimSpace(query)
	model = strings.TrimSpace(model)
	if model == "" {
		model = "auto"
	}
	return map[string]any{
		"query":  query,
		"model":  model,
		"source": "chatgpt-search",
	}
}

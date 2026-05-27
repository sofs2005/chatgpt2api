package toolcall

import (
	"fmt"
	"strconv"
)

func NewBridgeCatalog(tools any, policy ChoicePolicy) BridgeCatalog {
	metas := toolMetas(tools)
	if policy.Mode == ChoiceForced && policy.Name != "" {
		for _, meta := range metas {
			if meta.Name != policy.Name {
				continue
			}
			return BridgeCatalog{Tools: []BridgeTool{{
				BridgeName:   "bridge-0",
				OriginalName: meta.Name,
				Description:  meta.Description,
				Schema:       meta.Schema,
			}}}
		}
		return BridgeCatalog{MissingForcedName: policy.Name}
	}

	usedNames := make(map[string]struct{}, len(metas)*2)
	for _, meta := range metas {
		if meta.Name != "" {
			usedNames[meta.Name] = struct{}{}
		}
	}

	out := make([]BridgeTool, 0, len(metas))
	for _, meta := range metas {
		bridgeName := nextBridgeName(len(out), usedNames)
		usedNames[bridgeName] = struct{}{}
		out = append(out, BridgeTool{
			BridgeName:   bridgeName,
			OriginalName: meta.Name,
			Description:  meta.Description,
			Schema:       meta.Schema,
		})
	}

	return BridgeCatalog{Tools: out}
}

func nextBridgeName(index int, used map[string]struct{}) string {
	name := "bridge-" + asDecimal(index)
	for {
		if _, exists := used[name]; !exists {
			return name
		}
		name += "-slot"
	}
}

func (catalog BridgeCatalog) ValidationError() error {
	if catalog.MissingForcedName != "" {
		return fmt.Errorf("tool_choice forced %s but no matching tool was found", catalog.MissingForcedName)
	}
	return nil
}

func (catalog BridgeCatalog) BridgeNames() []string {
	names := make([]string, 0, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		if tool.BridgeName != "" {
			names = append(names, tool.BridgeName)
		}
	}
	return names
}

func (catalog BridgeCatalog) OriginalNames() []string {
	names := make([]string, 0, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		if tool.OriginalName != "" {
			names = append(names, tool.OriginalName)
		}
	}
	return names
}

func (catalog BridgeCatalog) BridgeAliases() map[string]string {
	aliases := make(map[string]string, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		if tool.BridgeName == "" || tool.OriginalName == "" {
			continue
		}
		aliases[tool.BridgeName] = tool.OriginalName
	}
	return aliases
}

func (catalog BridgeCatalog) AllowedParseNames() []string {
	names := make([]string, 0, len(catalog.Tools)*2)
	seen := make(map[string]struct{}, len(catalog.Tools)*2)
	for _, name := range catalog.BridgeNames() {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, name := range catalog.OriginalNames() {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func (catalog BridgeCatalog) OriginalName(name string) string {
	for _, tool := range catalog.Tools {
		if name == tool.BridgeName || name == tool.OriginalName {
			return tool.OriginalName
		}
	}
	return name
}

func (catalog BridgeCatalog) ResolveCalls(calls []ParsedCall) []ParsedCall {
	resolved := make([]ParsedCall, len(calls))
	for i, call := range calls {
		resolved[i] = ParsedCall{
			Name:  catalog.OriginalName(call.Name),
			Input: call.Input,
		}
	}
	return resolved
}

func asDecimal(n int) string {
	return strconv.Itoa(n)
}

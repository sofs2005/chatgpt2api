package toolcall

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

var supportedRoots = []string{"<tool_calls", "<tool_call", "<function_call", "<invoke"}

type xmlNode struct {
	Name     string
	Attrs    map[string]string
	Text     string
	Children []*xmlNode
}

func Parse(text string, availableNames []string, policy ChoicePolicy) ([]ParsedCall, string, error) {
	visible := strings.TrimSpace(StripMarkup(text))
	ranges := markupRanges(text)
	if len(ranges) == 0 {
		return applyPolicy(nil, nil, strings.TrimSpace(text), policy)
	}

	rawCalls := make([]ParsedCall, 0, len(ranges))
	for _, rng := range ranges {
		root, err := parseXML(text[rng.start:rng.end])
		if err != nil {
			continue
		}
		rawCalls = append(rawCalls, collectCalls(root)...)
	}

	calls := filterCalls(rawCalls, availableNames)
	return applyPolicy(calls, rawCalls, visible, policy)
}

func StripMarkup(text string) string {
	ranges := markupRanges(text)
	if len(ranges) == 0 {
		return strings.TrimSpace(text)
	}

	var b strings.Builder
	start := 0
	for _, rng := range ranges {
		b.WriteString(text[start:rng.start])
		start = rng.end
	}
	b.WriteString(text[start:])
	return strings.TrimSpace(b.String())
}

func StreamableText(text string) string {
	masked := maskFencedBlocks(text)
	idx := -1
	for _, root := range supportedRoots {
		if pos := strings.Index(masked, root); pos >= 0 && (idx < 0 || pos < idx) {
			idx = pos
		}
	}
	if idx < 0 {
		return text
	}
	return strings.TrimSpace(text[:idx])
}

func applyPolicy(calls []ParsedCall, rawCalls []ParsedCall, visible string, policy ChoicePolicy) ([]ParsedCall, string, error) {
	switch policy.Mode {
	case ChoiceRequired:
		if len(calls) == 0 {
			return nil, visible, errors.New("tool_choice required but no valid tool call was produced")
		}
	case ChoiceForced:
		if policy.Name != "" {
			for _, call := range rawCalls {
				if call.Name != policy.Name {
					return nil, visible, errors.New("tool_choice forced " + policy.Name + " but model produced " + call.Name)
				}
			}
			calls = filterCalls(calls, []string{policy.Name})
		}
		if len(calls) == 0 {
			return nil, visible, errors.New("tool_choice required but no valid tool call was produced")
		}
	}
	return calls, visible, nil
}

func collectCalls(root *xmlNode) []ParsedCall {
	var nodes []*xmlNode
	switch root.Name {
	case "tool_calls":
		for _, child := range root.Children {
			if child.Name == "tool_call" || child.Name == "function_call" || child.Name == "invoke" {
				nodes = append(nodes, child)
			}
		}
	case "tool_call", "function_call", "invoke":
		nodes = append(nodes, root)
	}

	calls := make([]ParsedCall, 0, len(nodes))
	for _, node := range nodes {
		call, ok := parseCall(node)
		if !ok || call.Name == "" {
			continue
		}
		calls = append(calls, call)
	}
	return calls
}

func filterCalls(calls []ParsedCall, availableNames []string) []ParsedCall {
	if len(availableNames) == 0 {
		return calls
	}
	allowed := make(map[string]struct{}, len(availableNames))
	for _, name := range availableNames {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	out := make([]ParsedCall, 0, len(calls))
	for _, call := range calls {
		if _, ok := allowed[call.Name]; ok {
			out = append(out, call)
		}
	}
	return out
}

func parseCall(node *xmlNode) (ParsedCall, bool) {
	name := strings.TrimSpace(node.Attrs["name"])
	if name == "" {
		name = childScalar(node, "tool_name")
	}
	if name == "" {
		name = childScalar(node, "name")
	}
	if name == "" {
		return ParsedCall{}, false
	}

	for _, key := range []string{"parameters", "params", "arguments", "input"} {
		if child := firstChild(node, key); child != nil {
			if input, ok := parseParamsNode(child); ok {
				return ParsedCall{Name: name, Input: input}, true
			}
			return ParsedCall{Name: name, Input: map[string]any{}}, true
		}
	}

	input := map[string]any{}
	for _, child := range node.Children {
		if child.Name == "tool_name" || child.Name == "name" {
			continue
		}
		mergeField(input, child.Name, parseNodeValue(child))
	}
	return ParsedCall{Name: name, Input: input}, true
}

func parseParamsNode(node *xmlNode) (map[string]any, bool) {
	text := strings.TrimSpace(node.Text)
	if text != "" {
		var out map[string]any
		if err := json.Unmarshal([]byte(text), &out); err == nil && out != nil {
			return out, true
		}
	}
	if len(node.Children) == 0 {
		return map[string]any{}, true
	}
	out := map[string]any{}
	for _, child := range node.Children {
		mergeField(out, child.Name, parseNodeValue(child))
	}
	return out, true
}

func parseNodeValue(node *xmlNode) any {
	if len(node.Children) == 0 {
		return parseScalar(node.Text)
	}
	out := map[string]any{}
	for _, child := range node.Children {
		mergeField(out, child.Name, parseNodeValue(child))
	}
	return out
}

func mergeField(dst map[string]any, key string, value any) {
	if existing, ok := dst[key]; ok {
		switch items := existing.(type) {
		case []any:
			dst[key] = append(items, value)
		default:
			dst[key] = []any{existing, value}
		}
		return
	}
	dst[key] = value
}

func parseScalar(text string) any {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err == nil {
		return value
	}
	return text
}

func childScalar(node *xmlNode, name string) string {
	child := firstChild(node, name)
	if child == nil {
		return ""
	}
	return strings.TrimSpace(child.Text)
}

func firstChild(node *xmlNode, name string) *xmlNode {
	for _, child := range node.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

type markupRange struct {
	start int
	end   int
}

func firstMarkup(text string) (string, bool) {
	rng, ok := firstMarkupRange(text)
	if !ok {
		return "", false
	}
	return text[rng.start:rng.end], true
}

func firstMarkupRange(text string) (markupRange, bool) {
	ranges := markupRanges(text)
	if len(ranges) == 0 {
		return markupRange{}, false
	}
	return ranges[0], true
}

func markupRanges(text string) []markupRange {
	masked := maskFencedBlocks(text)
	ranges := []markupRange{}
	for offset := 0; offset < len(text); {
		idx := nextMarkupStart(masked, offset)
		if idx < 0 {
			break
		}
		segment, ok := extractMarkup(text[idx:])
		if !ok {
			offset = idx + 1
			continue
		}
		rng := markupRange{start: idx, end: idx + len(segment)}
		ranges = append(ranges, rng)
		offset = rng.end
	}
	return ranges
}

func nextMarkupStart(masked string, offset int) int {
	idx := -1
	for _, root := range supportedRoots {
		if pos := strings.Index(masked[offset:], root); pos >= 0 {
			pos += offset
			if idx < 0 || pos < idx {
				idx = pos
			}
		}
	}
	return idx
}

func extractMarkup(text string) (string, bool) {
	dec := xml.NewDecoder(strings.NewReader(text))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		if start, ok := tok.(xml.StartElement); ok {
			depth := 1
			for depth > 0 {
				tok, err = dec.Token()
				if err != nil {
					return "", false
				}
				switch tok.(type) {
				case xml.StartElement:
					depth++
				case xml.EndElement:
					depth--
				}
			}
			_ = start
			return text[:dec.InputOffset()], true
		}
	}
}

func parseXML(text string) (*xmlNode, error) {
	dec := xml.NewDecoder(strings.NewReader(text))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			return readNode(dec, start)
		}
	}
}

func readNode(dec *xml.Decoder, start xml.StartElement) (*xmlNode, error) {
	node := &xmlNode{
		Name:  start.Name.Local,
		Attrs: map[string]string{},
	}
	for _, attr := range start.Attr {
		node.Attrs[attr.Name.Local] = strings.TrimSpace(attr.Value)
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return node, nil
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := readNode(dec, t)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, child)
		case xml.CharData:
			node.Text += string(t)
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				node.Text = strings.TrimSpace(node.Text)
				return node, nil
			}
		}
	}
}

func maskFencedBlocks(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	inFence := false
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "```") {
			inFence = !inFence
			b.WriteString("   ")
			i += 3
			continue
		}
		if inFence {
			b.WriteByte(' ')
		} else {
			b.WriteByte(text[i])
		}
		i++
	}
	return b.String()
}

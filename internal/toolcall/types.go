package toolcall

const (
	ChoiceAuto     = "auto"
	ChoiceNone     = "none"
	ChoiceRequired = "required"
	ChoiceForced   = "forced"
)

type ParsedCall struct {
	Name  string
	Input map[string]any
}

type ChoicePolicy struct {
	Mode string
	Name string
}

type BridgeTool struct {
	BridgeName   string
	OriginalName string
	Description  string
	Schema       any
}

type BridgeCatalog struct {
	Tools              []BridgeTool
	MissingForcedName string
}

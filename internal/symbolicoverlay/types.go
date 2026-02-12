package symbolicoverlay

type Symbol struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

type LogicLink struct {
	From     string `json:"from"`
	Relation string `json:"relation"`
	To       string `json:"to"`
}

type LogicMap struct {
	Entities []string    `json:"entities,omitempty"`
	Links    []LogicLink `json:"links,omitempty"`
}

type Constraint struct {
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Keywords []string `json:"keywords,omitempty"`
}

type ConstraintSet struct {
	Items []Constraint `json:"items,omitempty"`
}

type RiskSignal struct {
	Trigger  string `json:"trigger"`
	Severity string `json:"severity"`
	Evidence string `json:"evidence,omitempty"`
}

type RiskLens struct {
	Signals []RiskSignal `json:"signals,omitempty"`
}

type OverlayArtifact struct {
	SchemaVersion  string        `json:"schema_version,omitempty"`
	Profile        string        `json:"profile,omitempty"`
	MaxOverlayHops int           `json:"max_overlay_hops,omitempty"`
	Mode           string        `json:"mode"`
	Types          []string      `json:"types"`
	LogicMap       LogicMap      `json:"logic_map,omitempty"`
	ConstraintSet  ConstraintSet `json:"constraint_set,omitempty"`
	RiskLens       RiskLens      `json:"risk_lens,omitempty"`
}

type Result struct {
	Applied       bool            `json:"applied"`
	Mode          string          `json:"mode,omitempty"`
	SchemaVersion string          `json:"schema_version,omitempty"`
	Profile       string          `json:"profile,omitempty"`
	Types         []string        `json:"types,omitempty"`
	SymbolCount   int             `json:"symbol_count,omitempty"`
	Artifact      OverlayArtifact `json:"artifact,omitempty"`
	Flags         []string        `json:"flags,omitempty"`
}

type ComplianceResult struct {
	Checked        bool     `json:"checked"`
	ViolationCount int      `json:"violation_count"`
	Warnings       []string `json:"warnings,omitempty"`
	Score          float64  `json:"score"`
}

type SupervisionNode struct {
	NodeID     string  `json:"node_id"`
	NodeType   string  `json:"node_type"`
	Severity   string  `json:"severity,omitempty"`
	Score      float64 `json:"score,omitempty"`
	Decision   string  `json:"decision,omitempty"`
	Action     string  `json:"action,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Source     string  `json:"source,omitempty"`
	Violations int     `json:"violations,omitempty"`
}

type SupervisionResult struct {
	Enabled         bool              `json:"enabled"`
	Applied         bool              `json:"applied"`
	Decision        string            `json:"decision"`
	Action          string            `json:"action"`
	Reason          string            `json:"reason,omitempty"`
	Passes          int               `json:"passes"`
	ViolationCount  int               `json:"violation_count,omitempty"`
	ComplianceScore float64           `json:"compliance_score,omitempty"`
	Nodes           []SupervisionNode `json:"nodes,omitempty"`
}

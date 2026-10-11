package sandbox

// Limits is GET /api/v1/sandbox/limits's data.
type Limits struct {
	SchemaVersions []int                 `json:"schema_versions"`
	Manifest       ManifestLimits        `json:"manifest"`
	Fields         map[string]FieldLimit `json:"fields"`
	Rules          []Rule                `json:"rules"`
	HindsightTerms []string              `json:"hindsight_terms"`
}

// ManifestLimits caps a manifest's size. A zero cap is treated as no cap.
type ManifestLimits struct {
	MaxPartBytes   int `json:"max_part_bytes"`
	MaxTopBytes    int `json:"max_top_bytes"`
	MaxParts       int `json:"max_parts"`
	MaxItems       int `json:"max_items"`
	MaxCheckpoints int `json:"max_checkpoints"`
	MaxBars        int `json:"max_bars"`
	MaxBarsPerItem int `json:"max_bars_per_item"`
	MaxBarsPerPart int `json:"max_bars_per_part"`
}

// FieldLimit bounds one string field. Lengths are in Unicode code points.
type FieldLimit struct {
	Min     *int   `json:"min,omitempty"`
	Max     *int   `json:"max,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

// Rule is one lint rule the server applies (T5 is hindsight wording).
type Rule struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

package codereview

import (
	"encoding/json"
	"fmt"

	"github.com/yevgetman/fry/internal/config"
)

// SARIF 2.1.0 schema types.

type SARIFLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []SARIFRun `json:"runs"`
}

type SARIFRun struct {
	Tool    SARIFTool     `json:"tool"`
	Results []SARIFResult `json:"results"`
}

type SARIFTool struct {
	Driver SARIFDriver `json:"driver"`
}

type SARIFDriver struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	InformationURI string `json:"informationUri,omitempty"`
}

type SARIFResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   SARIFMessage    `json:"message"`
	Locations []SARIFLocation `json:"locations,omitempty"`
}

type SARIFMessage struct {
	Text string `json:"text"`
}

type SARIFLocation struct {
	PhysicalLocation SARIFPhysicalLocation `json:"physicalLocation"`
}

type SARIFPhysicalLocation struct {
	ArtifactLocation SARIFArtifactLocation `json:"artifactLocation"`
}

type SARIFArtifactLocation struct {
	URI string `json:"uri"`
}

func severityToSARIFLevel(sev string) string {
	switch sev {
	case "CRITICAL", "HIGH":
		return "error"
	case "MODERATE":
		return "warning"
	case "LOW":
		return "note"
	default:
		return "warning"
	}
}

// ConvertToSARIF converts review findings to a SARIF 2.1.0 JSON document.
func ConvertToSARIF(findings []Finding) ([]byte, error) {
	results := make([]SARIFResult, 0, len(findings))
	for i, f := range findings {
		ruleID := fmt.Sprintf("FRY%04d", i+1)

		text := f.Description
		if f.RecommendedFix != "" {
			text = fmt.Sprintf("%s\n\nRecommended fix: %s", f.Description, f.RecommendedFix)
		}

		result := SARIFResult{
			RuleID:  ruleID,
			Level:   severityToSARIFLevel(f.Severity),
			Message: SARIFMessage{Text: text},
		}

		if f.Location != "" {
			result.Locations = []SARIFLocation{
				{
					PhysicalLocation: SARIFPhysicalLocation{
						ArtifactLocation: SARIFArtifactLocation{URI: f.Location},
					},
				},
			}
		}

		results = append(results, result)
	}

	log := SARIFLog{
		Version: "2.1.0",
		Schema:  "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
		Runs: []SARIFRun{
			{
				Tool: SARIFTool{
					Driver: SARIFDriver{
						Name:    "fry",
						Version: config.Version,
					},
				},
				Results: results,
			},
		},
	}

	return json.MarshalIndent(log, "", "  ")
}

package output

import (
	"encoding/json"
	"io"

	"github.com/primerouter/pr-audit/internal/model"
)

// RenderJSON writes the Result as indented JSON. Consumers (CI pipelines,
// scripts) should rely on: version, trust_level_reached, result, exit_code,
// checks[], next_steps[]. Other fields may be added without a major bump.
func RenderJSON(w io.Writer, r model.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

package models

import (
	"fmt"
	"strings"
)

// Selection is the catalog-owned identity needed to invoke one model.
// Callers never need their own model-to-engine or family allowlist.
type Selection struct {
	ID           string
	Engine       string
	Family       string
	Capabilities []string
}

// Resolve selects one catalogued model by id, alias, or engine id and checks
// its capability. An empty value selects the first matching default engine.
func (m Manifest) Resolve(value, capability, defaultEngine string) (Selection, error) {
	value = strings.TrimSpace(value)
	for _, model := range m.Models {
		if !hasCapability(model, capability) {
			continue
		}
		matched := value == model.ID || selectionContains(model.Aliases, value)
		if value == "" {
			matched = model.Engine == defaultEngine
		} else if value == model.Engine {
			matched = true
		}
		if matched {
			return Selection{ID: model.ID, Engine: model.Engine, Family: model.Family, Capabilities: append([]string(nil), model.Capabilities...)}, nil
		}
	}
	return Selection{}, fmt.Errorf("%s model %q is not supported", capability, value)
}

func hasCapability(model Model, capability string) bool {
	return selectionContains(model.Capabilities, capability)
}

func selectionContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

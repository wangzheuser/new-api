package common

import "errors"

// ResolveMappedModel follows channel aliases and rejects cycles other than a self alias.
func ResolveMappedModel(mapping, model string) (string, error) {
	if mapping == "" || mapping == "{}" {
		return model, nil
	}
	var aliases map[string]string
	if err := UnmarshalJsonStr(mapping, &aliases); err != nil {
		return "", errors.New("unmarshal_model_mapping_failed")
	}
	seen := map[string]bool{model: true}
	for {
		next := aliases[model]
		if next == "" || next == model {
			return model, nil
		}
		if seen[next] {
			return "", errors.New("model_mapping_contains_cycle")
		}
		seen[next] = true
		model = next
	}
}

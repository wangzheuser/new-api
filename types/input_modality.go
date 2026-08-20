package types

import (
	"fmt"
	"strings"
)

const (
	// MaxModelInputModalityEntries limits one configuration document to a manageable size.
	MaxModelInputModalityEntries = 256
	maxInputModalityModelNameLen = 255
)

// InputModality identifies one input type accepted by a requested model.
type InputModality string

const (
	InputModalityText  InputModality = "text"
	InputModalityImage InputModality = "image"
)

// ModelInputModalities maps exact client-requested model names to allowed input modalities.
type ModelInputModalities map[string][]InputModality

// Validate checks the persisted model capability declaration.
func (m ModelInputModalities) Validate() error {
	if len(m) > MaxModelInputModalityEntries {
		return fmt.Errorf("model input modality entries cannot exceed %d", MaxModelInputModalityEntries)
	}

	for model, modalities := range m {
		trimmedModel := strings.TrimSpace(model)
		if trimmedModel == "" || trimmedModel != model || len(model) > maxInputModalityModelNameLen {
			return fmt.Errorf("invalid model input modality model: %s", model)
		}
		if len(modalities) == 0 {
			return fmt.Errorf("model input modalities cannot be empty: %s", model)
		}

		seen := make(map[InputModality]struct{}, len(modalities))
		hasText := false
		for _, modality := range modalities {
			if modality != InputModalityText && modality != InputModalityImage {
				return fmt.Errorf("invalid input modality %q for model %s", modality, model)
			}
			if _, exists := seen[modality]; exists {
				return fmt.Errorf("duplicate input modality %q for model %s", modality, model)
			}
			seen[modality] = struct{}{}
			if modality == InputModalityText {
				hasText = true
			}
		}
		if !hasText {
			return fmt.Errorf("text input modality is required for model %s", model)
		}
	}
	return nil
}

// Normalized returns a copy with the stable text-before-image modality order.
func (m ModelInputModalities) Normalized() ModelInputModalities {
	normalized := make(ModelInputModalities, len(m))
	for model, modalities := range m {
		normalized[model] = []InputModality{InputModalityText}
		for _, modality := range modalities {
			if modality == InputModalityImage {
				normalized[model] = append(normalized[model], InputModalityImage)
				break
			}
		}
	}
	return normalized
}

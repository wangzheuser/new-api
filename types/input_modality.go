package types

import (
	"fmt"
	"net/http"
)

func UnsupportedImageInputError(modelName string) *NewAPIError {
	return NewOpenAIError(fmt.Errorf("model %s has no declared image input capability", modelName),
		ErrorCodeUnsupportedInputModality, http.StatusBadRequest, ErrOptionWithSkipRetry())
}

// ModelInputModalities is kept as an alias for the standalone relaykit type.

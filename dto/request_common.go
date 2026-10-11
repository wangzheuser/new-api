package dto

import (
	"github.com/QuantumNous/new-api/types"
)

type Request interface {
	GetTokenCountMeta() *types.TokenCountMeta
	IsStream(c any) bool
	SetModelName(modelName string)
}

type BaseRequest struct {
}

func (b *BaseRequest) GetTokenCountMeta() *types.TokenCountMeta {
	return &types.TokenCountMeta{
		TokenType: types.TokenTypeTokenizer,
	}
}
func (b *BaseRequest) IsStream(c any) bool {
	return false
}
func (b *BaseRequest) SetModelName(modelName string) {}

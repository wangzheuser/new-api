package dto

import kitdto "github.com/QuantumNous/new-api/relaykit/dto"

type UserSetting = kitdto.UserSetting

var (
	NotifyTypeEmail   = kitdto.NotifyTypeEmail
	NotifyTypeWebhook = kitdto.NotifyTypeWebhook
	NotifyTypeBark    = kitdto.NotifyTypeBark
	NotifyTypeGotify  = kitdto.NotifyTypeGotify
)

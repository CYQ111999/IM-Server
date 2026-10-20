package api

import (
	"github.com/go-playground/validator/v10"
	"github.com/openimsdk/protocol/constant"
)

// RequiredIf 根据会话类型校验指定字段是否为必填。
func RequiredIf(fl validator.FieldLevel) bool {
	sessionType := fl.Parent().FieldByName("SessionType").Int()

	switch sessionType {
	case constant.SingleChatType, constant.NotificationChatType:
		return fl.FieldName() != "RecvID" || fl.Field().String() != ""
	case constant.WriteGroupChatType, constant.ReadGroupChatType:
		return fl.FieldName() != "GroupID" || fl.Field().String() != ""
	default:
		return true
	}
}

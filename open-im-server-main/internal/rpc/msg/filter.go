package msg

import (
	"strconv"
	"strings"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/protocol/constant"
	pbchat "github.com/openimsdk/protocol/msg"
	"github.com/openimsdk/tools/utils/datautil"
)

const (
	separator = "-"
)

// filterAfterMsg 按“消息发送后”过滤配置（AfterConfig）判断消息是否允许发送
func filterAfterMsg(msg *pbchat.SendMsgReq, after *config.AfterConfig) bool {
	return filterMsg(msg, after.AttentionIds, after.DeniedTypes)
}

// filterBeforeMsg 按“消息发送前”过滤配置（BeforeConfig）判断消息是否允许发送
func filterBeforeMsg(msg *pbchat.SendMsgReq, before *config.BeforeConfig) bool {
	return filterMsg(msg, nil, before.DeniedTypes)
}

// filterMsg 通用消息过滤逻辑：
// 1. 若配置了 attentionIds，则只允许发送给列表中的接收者；
// 2. 默认禁止的类型（通知类消息、正在输入状态）一律拦截；
// 3. 若配置了 deniedTypes，命中其中的类型一律拦截。
func filterMsg(msg *pbchat.SendMsgReq, attentionIds []string, deniedTypes []int32) bool {
	// 根据 attentionIds 配置，只对列表中的部分用户生效
	if len(attentionIds) != 0 && !datautil.Contain(msg.MsgData.RecvID, attentionIds...) {
		return false
	}

	if defaultDeniedTypes(msg.MsgData.ContentType) {
		return false
	}

	if len(deniedTypes) != 0 && datautil.Contain(msg.MsgData.ContentType, deniedTypes...) {
		return false
	}
	//if len(allowedTypes) != 0 && !isInInterval(msg.MsgData.ContentType, allowedTypes) {
	//	return false
	//}
	//if len(deniedTypes) != 0 && isInInterval(msg.MsgData.ContentType, deniedTypes) {
	//	return false
	//}
	return true
}

// defaultDeniedTypes 默认禁止发送的消息类型：通知类消息（NotificationBegin~NotificationEnd）与“正在输入”状态
func defaultDeniedTypes(contentType int32) bool {
	if contentType >= constant.NotificationBegin && contentType <= constant.NotificationEnd {
		return true
	}
	if contentType == constant.Typing {
		return true
	}
	return false
}

// isInInterval 判断 data 是否命中区间列表
// 支持两种格式：单个类型值，或用连字符（"-"）连接的上下界区间。
// 例如 [1, 100, 200-500, 600-700] 表示只有落在
// {1, 100} ∪ [200, 500] ∪ [600, 700] 范围内的数据才会返回 true。
func isInInterval(data int32, interval []string) bool {
	for _, v := range interval {
		if strings.Contains(v, separator) {
			// 是区间格式（形如 200-500）
			bounds := strings.Split(v, separator)
			if len(bounds) != 2 {
				continue
			}
			bottom, err := strconv.Atoi(bounds[0])
			if err != nil {
				continue
			}
			top, err := strconv.Atoi(bounds[1])
			if err != nil {
				continue
			}
			if datautil.BetweenEq(int(data), bottom, top) {
				return true
			}
		} else {
			iv, err := strconv.Atoi(v)
			if err != nil {
				continue
			}
			if int(data) == iv {
				return true
			}
		}
	}
	return false
}

package offlinepush

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/dummy"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/options"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache"
)

// OfflinePusher 定义离线推送能力。
//
// 当用户不在线时，业务侧希望由系统级推送通道（APNs / 厂商通道）兜底提醒，
// 因此抽象出该接口。实现方负责把「发件人 + 消息摘要」投递到具体通道。
type OfflinePusher interface {
	Push(ctx context.Context, userIDs []string, title, content string, opts *options.Opts) error
}

// NewOfflinePusher 构造离线推送实现。
//
// 目前只提供 dummy 实现：它不向任何第三方推送服务发起网络请求，
// 仅记录一次告警日志后返回成功，用于本地开发与学习环境。
//
// 若后续需要接入真实通道（APNs、FCM、个推等），在这里新增一个分支并返回对应实现即可，
// 上层调用方无需改动——这正是把推送能力抽象成接口的价值。
func NewOfflinePusher(_ *config.Push, _ cache.ThirdCache) (OfflinePusher, error) {
	return dummy.NewClient(), nil
}

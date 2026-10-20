package database

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"
	"github.com/openimsdk/tools/db/pagination"
)

type FriendRequest interface {
	// 批量插入记录
	Create(ctx context.Context, friendRequests []*model.FriendRequest) (err error)
	// 删除记录
	Delete(ctx context.Context, fromUserID, toUserID string) (err error)
	// 更新（允许写入零值）
	UpdateByMap(ctx context.Context, formUserID string, toUserID string, args map[string]any) (err error)
	// 批量更新记录（忽略零值字段）
	Update(ctx context.Context, friendRequest *model.FriendRequest) (err error)
	// 获取发给指定用户的好友申请；不存在时不返回错误
	Find(ctx context.Context, fromUserID, toUserID string) (friendRequest *model.FriendRequest, err error)
	Take(ctx context.Context, fromUserID, toUserID string) (friendRequest *model.FriendRequest, err error)
	// 获取 toUserID 收到的好友申请列表
	FindToUserID(ctx context.Context, toUserID string, handleResults []int, pagination pagination.Pagination) (total int64, friendRequests []*model.FriendRequest, err error)
	// 获取 fromUserID 发出的好友申请列表
	FindFromUserID(ctx context.Context, fromUserID string, handleResults []int, pagination pagination.Pagination) (total int64, friendRequests []*model.FriendRequest, err error)
	FindBothFriendRequests(ctx context.Context, fromUserID, toUserID string) (friends []*model.FriendRequest, err error)
	GetUnhandledCount(ctx context.Context, userID string, ts int64) (int64, error)
}

package database

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"
	"github.com/openimsdk/tools/db/pagination"
)

// Friend 定义 MongoDB 中好友管理的操作接口。
type Friend interface {
	// Create 插入多条好友记录。
	Create(ctx context.Context, friends []*model.Friend) (err error)
	// Delete 删除指定用户的若干好友。
	Delete(ctx context.Context, ownerUserID string, friendUserIDs []string) (err error)
	// UpdateByMap 使用 map 更新好友文档的指定字段。
	UpdateByMap(ctx context.Context, ownerUserID string, friendUserID string, args map[string]any) (err error)
	// UpdateRemark 修改备注。
	UpdateRemark(ctx context.Context, ownerUserID, friendUserID, remark string) (err error)
	// Take 获取单条好友文档，不存在时返回错误。
	Take(ctx context.Context, ownerUserID, friendUserID string) (friend *model.Friend, err error)
	// FindUserState 查询两个用户之间的好友状态。
	FindUserState(ctx context.Context, userID1, userID2 string) (friends []*model.Friend, err error)
	// FindFriends 获取指定用户的好友列表，部分好友缺失不会报错。
	FindFriends(ctx context.Context, ownerUserID string, friendUserIDs []string) (friends []*model.Friend, err error)
	// FindReversalFriends 查找将指定用户加为好友的用户（反向好友）。
	FindReversalFriends(ctx context.Context, friendUserID string, ownerUserIDs []string) (friends []*model.Friend, err error)
	// FindOwnerFriends 分页获取指定用户的好友列表。
	FindOwnerFriends(ctx context.Context, ownerUserID string, pagination pagination.Pagination) (total int64, friends []*model.Friend, err error)
	// FindInWhoseFriends 分页查找将指定用户加为好友的用户。
	FindInWhoseFriends(ctx context.Context, friendUserID string, pagination pagination.Pagination) (total int64, friends []*model.Friend, err error)
	// FindFriendUserIDs 获取指定用户的好友 userID 列表。
	FindFriendUserIDs(ctx context.Context, ownerUserID string) (friendUserIDs []string, err error)
	// UpdateFriends 更新好友的字段
	UpdateFriends(ctx context.Context, ownerUserID string, friendUserIDs []string, val map[string]any) (err error)

	FindIncrVersion(ctx context.Context, ownerUserID string, version uint, limit int) (*model.VersionLog, error)

	FindFriendUserID(ctx context.Context, friendUserID string) ([]string, error)

	//SearchFriend(ctx context.Context, ownerUserID, keyword string, pagination pagination.Pagination) (int64, []*model.Friend, error)

	FindOwnerFriendUserIds(ctx context.Context, ownerUserID string, limit int) ([]string, error)

	IncrVersion(ctx context.Context, ownerUserID string, friendUserIDs []string, state int32) error
}

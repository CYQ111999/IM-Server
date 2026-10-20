package cache

import (
	"context"
	relationtb "github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"
)

// FriendCache 是好友相关数据缓存的接口。
type FriendCache interface {
	BatchDeleter
	CloneFriendCache() FriendCache
	GetFriendIDs(ctx context.Context, ownerUserID string) (friendIDs []string, err error)
	// 好友 ID 列表变化时调用
	DelFriendIDs(ownerUserID ...string) FriendCache
	// 从缓存中获取单个好友信息
	GetFriend(ctx context.Context, ownerUserID, friendUserID string) (friend *relationtb.Friend, err error)
	// 好友信息变化时删除该好友缓存
	DelFriend(ownerUserID, friendUserID string) FriendCache
	// 多个好友信息变化时批量删除好友缓存
	DelFriends(ownerUserID string, friendUserIDs []string) FriendCache

	DelOwner(friendUserID string, ownerUserIDs []string) FriendCache

	DelMaxFriendVersion(ownerUserIDs ...string) FriendCache

	//DelSortFriendUserIDs(ownerUserIDs ...string) FriendCache

	//FindSortFriendUserIDs(ctx context.Context, ownerUserID string) ([]string, error)

	//FindFriendIncrVersion(ctx context.Context, ownerUserID string, version uint, limit int) (*relationtb.VersionLog, error)

	FindMaxFriendVersion(ctx context.Context, ownerUserID string) (*relationtb.VersionLog, error)
}

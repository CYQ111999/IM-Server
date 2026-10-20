package redis

import (
	"context"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache/cachekey"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/database"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"
	"github.com/openimsdk/tools/utils/datautil"
	"github.com/redis/go-redis/v9"
)

const (
	friendExpireTime = time.Second * 60 * 60 * 12
)

// FriendCacheRedis 是基于 Redis 的 FriendCache 接口实现。
type FriendCacheRedis struct {
	cache.BatchDeleter
	friendDB   database.Friend
	expireTime time.Duration
	rcClient   *rocksCacheClient
	syncCount  int
}

// NewFriendCacheRedis 创建 FriendCacheRedis 实例。
func NewFriendCacheRedis(rdb redis.UniversalClient, localCache *config.LocalCache, friendDB database.Friend) cache.FriendCache {
	rc := newRocksCacheClient(rdb)
	return &FriendCacheRedis{
		BatchDeleter: rc.GetBatchDeleter(localCache.Friend.Topic),
		friendDB:     friendDB,
		expireTime:   friendExpireTime,
		rcClient:     rc,
	}
}

func (f *FriendCacheRedis) CloneFriendCache() cache.FriendCache {
	return &FriendCacheRedis{
		BatchDeleter: f.BatchDeleter.Clone(),
		friendDB:     f.friendDB,
		expireTime:   f.expireTime,
		rcClient:     f.rcClient,
	}
}

// getFriendIDsKey 返回缓存中存储好友 ID 的 key。
func (f *FriendCacheRedis) getFriendIDsKey(ownerUserID string) string {
	return cachekey.GetFriendIDsKey(ownerUserID)
}

func (f *FriendCacheRedis) getFriendMaxVersionKey(ownerUserID string) string {
	return cachekey.GetFriendMaxVersionKey(ownerUserID)
}

// getTwoWayFriendsIDsKey 返回缓存中存储双向好友 ID 的 key。
func (f *FriendCacheRedis) getTwoWayFriendsIDsKey(ownerUserID string) string {
	return cachekey.GetTwoWayFriendsIDsKey(ownerUserID)
}

// getFriendKey 返回缓存中存储好友信息的 key。
func (f *FriendCacheRedis) getFriendKey(ownerUserID, friendUserID string) string {
	return cachekey.GetFriendKey(ownerUserID, friendUserID)
}

// GetFriendIDs 从缓存获取好友 ID，未命中时回源到数据库。
func (f *FriendCacheRedis) GetFriendIDs(ctx context.Context, ownerUserID string) (friendIDs []string, err error) {
	return getCache(ctx, f.rcClient, f.getFriendIDsKey(ownerUserID), f.expireTime, func(ctx context.Context) ([]string, error) {
		return f.friendDB.FindFriendUserIDs(ctx, ownerUserID)
	})
}

// DelFriendIDs 从缓存删除好友 ID。
func (f *FriendCacheRedis) DelFriendIDs(ownerUserIDs ...string) cache.FriendCache {
	newFriendCache := f.CloneFriendCache()
	keys := make([]string, 0, len(ownerUserIDs))
	for _, userID := range ownerUserIDs {
		keys = append(keys, f.getFriendIDsKey(userID))
	}
	newFriendCache.AddKeys(keys...)

	return newFriendCache
}

// GetTwoWayFriendIDs 从缓存获取双向好友 ID。
func (f *FriendCacheRedis) GetTwoWayFriendIDs(ctx context.Context, ownerUserID string) (twoWayFriendIDs []string, err error) {
	friendIDs, err := f.GetFriendIDs(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	for _, friendID := range friendIDs {
		friendFriendID, err := f.GetFriendIDs(ctx, friendID)
		if err != nil {
			return nil, err
		}
		if datautil.Contain(ownerUserID, friendFriendID...) {
			twoWayFriendIDs = append(twoWayFriendIDs, ownerUserID)
		}
	}

	return twoWayFriendIDs, nil
}

// DelTwoWayFriendIDs 从缓存删除双向好友 ID。
func (f *FriendCacheRedis) DelTwoWayFriendIDs(ctx context.Context, ownerUserID string) cache.FriendCache {
	newFriendCache := f.CloneFriendCache()
	newFriendCache.AddKeys(f.getTwoWayFriendsIDsKey(ownerUserID))

	return newFriendCache
}

// GetFriend 从缓存获取好友信息，未命中时回源到数据库。
func (f *FriendCacheRedis) GetFriend(ctx context.Context, ownerUserID, friendUserID string) (friend *model.Friend, err error) {
	return getCache(ctx, f.rcClient, f.getFriendKey(ownerUserID,
		friendUserID), f.expireTime, func(ctx context.Context) (*model.Friend, error) {
		return f.friendDB.Take(ctx, ownerUserID, friendUserID)
	})
}

// DelFriend 从缓存删除好友信息。
func (f *FriendCacheRedis) DelFriend(ownerUserID, friendUserID string) cache.FriendCache {
	newFriendCache := f.CloneFriendCache()
	newFriendCache.AddKeys(f.getFriendKey(ownerUserID, friendUserID))

	return newFriendCache
}

// DelFriends 从缓存批量删除好友信息。
func (f *FriendCacheRedis) DelFriends(ownerUserID string, friendUserIDs []string) cache.FriendCache {
	newFriendCache := f.CloneFriendCache()

	for _, friendUserID := range friendUserIDs {
		key := f.getFriendKey(ownerUserID, friendUserID)
		newFriendCache.AddKeys(key) // 这里假定 AddKeys 会把 key 标记为待删除
	}

	return newFriendCache
}

func (f *FriendCacheRedis) DelOwner(friendUserID string, ownerUserIDs []string) cache.FriendCache {
	newFriendCache := f.CloneFriendCache()

	for _, ownerUserID := range ownerUserIDs {
		key := f.getFriendKey(ownerUserID, friendUserID)
		newFriendCache.AddKeys(key) // 这里假定 AddKeys 会把 key 标记为待删除
	}

	return newFriendCache
}

func (f *FriendCacheRedis) DelMaxFriendVersion(ownerUserIDs ...string) cache.FriendCache {
	newFriendCache := f.CloneFriendCache()
	for _, ownerUserID := range ownerUserIDs {
		key := f.getFriendMaxVersionKey(ownerUserID)
		newFriendCache.AddKeys(key) // 这里假定 AddKeys 会把 key 标记为待删除
	}

	return newFriendCache
}

func (f *FriendCacheRedis) FindMaxFriendVersion(ctx context.Context, ownerUserID string) (*model.VersionLog, error) {
	return getCache(ctx, f.rcClient, f.getFriendMaxVersionKey(ownerUserID), f.expireTime, func(ctx context.Context) (*model.VersionLog, error) {
		return f.friendDB.FindIncrVersion(ctx, ownerUserID, 0, 0)
	})
}

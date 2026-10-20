package cache

import (
	"context"
)

type BlackCache interface {
	BatchDeleter
	CloneBlackCache() BlackCache
	GetBlackIDs(ctx context.Context, userID string) (blackIDs []string, err error)
	// 删除用户黑名单 ID 缓存，在用户黑名单发生变化时执行
	DelBlackIDs(ctx context.Context, userID string) BlackCache
}

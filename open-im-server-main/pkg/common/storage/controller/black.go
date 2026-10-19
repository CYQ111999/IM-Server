// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/database"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"
	"github.com/openimsdk/tools/db/pagination"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/utils/datautil"
)

// BlackDatabase 黑名单数据库操作接口
type BlackDatabase interface {
	// Create 添加黑名单
	Create(ctx context.Context, blacks []*model.Black) (err error)
	// Delete 删除黑名单
	Delete(ctx context.Context, blacks []*model.Black) (err error)
	// FindOwnerBlacks 获取黑名单列表
	FindOwnerBlacks(ctx context.Context, ownerUserID string, pagination pagination.Pagination) (total int64, blacks []*model.Black, err error)
	// FindBlackInfos 获取黑名单明细
	FindBlackInfos(ctx context.Context, ownerUserID string, userIDs []string) (blacks []*model.Black, err error)
	// CheckIn 检查 user2 是否在 user1 的黑名单中（inUser1Blacks 为 true），以及 user1 是否在 user2 的黑名单中（inUser2Blacks 为 true）
	CheckIn(ctx context.Context, userID1, userID2 string) (inUser1Blacks bool, inUser2Blacks bool, err error)
}

type blackDatabase struct {
	black database.Black
	cache cache.BlackCache
}

func NewBlackDatabase(black database.Black, cache cache.BlackCache) BlackDatabase {
	return &blackDatabase{black, cache}
}

// Create 添加黑名单。
func (b *blackDatabase) Create(ctx context.Context, blacks []*model.Black) (err error) {
	if err := b.black.Create(ctx, blacks); err != nil {
		return err
	}
	return b.deleteBlackIDsCache(ctx, blacks)
}

// Delete 删除黑名单。
func (b *blackDatabase) Delete(ctx context.Context, blacks []*model.Black) (err error) {
	if err := b.black.Delete(ctx, blacks); err != nil {
		return err
	}
	return b.deleteBlackIDsCache(ctx, blacks)
}

// deleteBlackIDsCache 清除受影响用户的黑名单 ID 缓存
func (b *blackDatabase) deleteBlackIDsCache(ctx context.Context, blacks []*model.Black) (err error) {
	cache := b.cache.CloneBlackCache()
	for _, black := range blacks {
		cache = cache.DelBlackIDs(ctx, black.OwnerUserID)
	}
	return cache.ChainExecDel(ctx)
}

// FindOwnerBlacks 获取黑名单列表。
func (b *blackDatabase) FindOwnerBlacks(ctx context.Context, ownerUserID string, pagination pagination.Pagination) (total int64, blacks []*model.Black, err error) {
	return b.black.FindOwnerBlacks(ctx, ownerUserID, pagination)
}

// CheckIn 检查 user2 是否在 user1 的黑名单中、user1 是否在 user2 的黑名单中。
func (b *blackDatabase) CheckIn(ctx context.Context, userID1, userID2 string) (inUser1Blacks bool, inUser2Blacks bool, err error) {
	userID1BlackIDs, err := b.cache.GetBlackIDs(ctx, userID1)
	if err != nil {
		return
	}
	userID2BlackIDs, err := b.cache.GetBlackIDs(ctx, userID2)
	if err != nil {
		return
	}
	log.ZDebug(ctx, "blackIDs", "user1BlackIDs", userID1BlackIDs, "user2BlackIDs", userID2BlackIDs)
	return datautil.Contain(userID2, userID1BlackIDs...), datautil.Contain(userID1, userID2BlackIDs...), nil
}

// FindBlackIDs 获取用户的黑名单 ID 列表。
func (b *blackDatabase) FindBlackIDs(ctx context.Context, ownerUserID string) (blackIDs []string, err error) {
	return b.cache.GetBlackIDs(ctx, ownerUserID)
}

// FindBlackInfos 获取黑名单明细。
func (b *blackDatabase) FindBlackInfos(ctx context.Context, ownerUserID string, userIDs []string) (blacks []*model.Black, err error) {
	return b.black.FindOwnerBlackInfos(ctx, ownerUserID, userIDs)
}

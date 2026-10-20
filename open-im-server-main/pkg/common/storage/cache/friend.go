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

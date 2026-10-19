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
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/servererrs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/database"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/database/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"

	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/db/pagination"
	"github.com/openimsdk/tools/db/tx"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/mcontext"
	"github.com/openimsdk/tools/utils/datautil"
)

// FriendDatabase 好友关系数据库操作接口（Mongo 持久化 + Redis 缓存的组合视图）
type FriendDatabase interface {
	// CheckIn 检查 user2 是否在 user1 的好友列表中（inUser1Friends 为 true），
	// 以及 user1 是否在 user2 的好友列表中（inUser2Friends 为 true）
	CheckIn(ctx context.Context, user1, user2 string) (inUser1Friends bool, inUser2Friends bool, err error)

	// AddFriendRequest 新增或更新一条好友申请
	AddFriendRequest(ctx context.Context, fromUserID, toUserID string, reqMsg string, ex string) (err error)

	// BecomeFriends 先检查双方是否已在好友表中，不在则插入为好友关系
	BecomeFriends(ctx context.Context, ownerUserID string, friendUserIDs []string, addSource int32) (err error)

	// RefuseFriendRequest 拒绝一条好友申请
	RefuseFriendRequest(ctx context.Context, friendRequest *model.FriendRequest) (err error)

	// AgreeFriendRequest 同意一条好友申请
	AgreeFriendRequest(ctx context.Context, friendRequest *model.FriendRequest) (err error)

	// Delete 从属主的好友列表中删除一个或多个好友
	Delete(ctx context.Context, ownerUserID string, friendUserIDs []string) (err error)

	// UpdateRemark 更新好友备注
	UpdateRemark(ctx context.Context, ownerUserID, friendUserID, remark string) (err error)

	// PageOwnerFriends 分页获取 ownerUserID 的好友列表
	PageOwnerFriends(ctx context.Context, ownerUserID string, pagination pagination.Pagination) (total int64, friends []*model.Friend, err error)

	// PageInWhoseFriends 分页查找哪些用户的好友列表中包含 friendUserID
	PageInWhoseFriends(ctx context.Context, friendUserID string, pagination pagination.Pagination) (total int64, friends []*model.Friend, err error)

	// PageFriendRequestFromMe 分页获取该用户发出的好友申请
	PageFriendRequestFromMe(ctx context.Context, userID string, handleResults []int, pagination pagination.Pagination) (total int64, friends []*model.FriendRequest, err error)

	// PageFriendRequestToMe 分页获取该用户收到的好友申请
	PageFriendRequestToMe(ctx context.Context, userID string, handleResults []int, pagination pagination.Pagination) (total int64, friends []*model.FriendRequest, err error)

	// FindFriendsWithError 获取用户的指定好友，任一不存在则返回错误
	FindFriendsWithError(ctx context.Context, ownerUserID string, friendUserIDs []string) (friends []*model.Friend, err error)

	// FindFriendUserIDs 获取一个用户的好友 ID 列表
	FindFriendUserIDs(ctx context.Context, ownerUserID string) (friendUserIDs []string, err error)

	// FindBothFriendRequests 查找双方互发/单发的好友申请
	FindBothFriendRequests(ctx context.Context, fromUserID, toUserID string) (friends []*model.FriendRequest, err error)

	// UpdateFriends 批量更新好友关系的字段
	UpdateFriends(ctx context.Context, ownerUserID string, friendUserIDs []string, val map[string]any) (err error)

	//FindSortFriendUserIDs(ctx context.Context, ownerUserID string) ([]string, error)

	// FindFriendIncrVersion 查找好友关系的增量版本日志（用于多端同步）
	FindFriendIncrVersion(ctx context.Context, ownerUserID string, version uint, limit int) (*model.VersionLog, error)

	// FindMaxFriendVersionCache 从缓存查找好友关系的最大版本号
	FindMaxFriendVersionCache(ctx context.Context, ownerUserID string) (*model.VersionLog, error)

	// FindFriendUserID 反向查找：获取所有把 friendUserID 当好友的用户 ID
	FindFriendUserID(ctx context.Context, friendUserID string) ([]string, error)

	// OwnerIncrVersion 递增属主侧好友关系的版本号
	OwnerIncrVersion(ctx context.Context, ownerUserID string, friendUserIDs []string, state int32) error

	// GetUnhandledCount 统计未处理的好友申请数量
	GetUnhandledCount(ctx context.Context, userID string, ts int64) (int64, error)
}

type friendDatabase struct {
	friend        database.Friend
	friendRequest database.FriendRequest
	tx            tx.Tx
	cache         cache.FriendCache
}

func NewFriendDatabase(friend database.Friend, friendRequest database.FriendRequest, cache cache.FriendCache, tx tx.Tx) FriendDatabase {
	return &friendDatabase{friend: friend, friendRequest: friendRequest, cache: cache, tx: tx}
}

// CheckIn 校验 user2 是否在 user1 的好友列表中（inUser1Friends 返回 true），
// 以及 user1 是否在 user2 的好友列表中（inUser2Friends 返回 true）。
func (f *friendDatabase) CheckIn(ctx context.Context, userID1, userID2 string) (inUser1Friends bool, inUser2Friends bool, err error) {
	// 从缓存获取 userID1 的好友 ID 列表
	userID1FriendIDs, err := f.cache.GetFriendIDs(ctx, userID1)
	if err != nil {
		return false, false, err
	}

	// 从缓存获取 userID2 的好友 ID 列表
	userID2FriendIDs, err := f.cache.GetFriendIDs(ctx, userID2)
	if err != nil {
		return false, false, err
	}

	// 分别检查对方是否在自己的好友列表中
	inUser1Friends = datautil.Contain(userID2, userID1FriendIDs...)
	inUser2Friends = datautil.Contain(userID1, userID2FriendIDs...)
	return inUser1Friends, inUser2Friends, nil
}

// AddFriendRequest 新增或更新一条好友申请。
func (f *friendDatabase) AddFriendRequest(ctx context.Context, fromUserID, toUserID string, reqMsg string, ex string) (err error) {
	return f.tx.Transaction(ctx, func(ctx context.Context) error {
		_, err := f.friendRequest.Take(ctx, fromUserID, toUserID)
		switch {
		case err == nil:
			// 已存在申请：重置处理状态并更新内容
			m := make(map[string]any, 1)
			m["handle_result"] = 0
			m["handle_msg"] = ""
			m["req_msg"] = reqMsg
			m["ex"] = ex
			m["create_time"] = time.Now()
			return f.friendRequest.UpdateByMap(ctx, fromUserID, toUserID, m)
		case mgo.IsNotFound(err):
			// 不存在申请：创建新记录
			return f.friendRequest.Create(
				ctx,
				[]*model.FriendRequest{{FromUserID: fromUserID, ToUserID: toUserID, ReqMsg: reqMsg, Ex: ex, CreateTime: time.Now(), HandleTime: time.Unix(0, 0)}},
			)
		default:
			return err
		}
	})
}

// BecomeFriends (1) 先判断是否已在好友表中（在或不在都不返回错误）(2) 不在的才执行插入。
func (f *friendDatabase) BecomeFriends(ctx context.Context, ownerUserID string, friendUserIDs []string, addSource int32) (err error) {
	return f.tx.Transaction(ctx, func(ctx context.Context) error {
		cache := f.cache.CloneFriendCache()
		// 查询我方已存在的好友记录
		myFriends, err := f.friend.FindFriends(ctx, ownerUserID, friendUserIDs)
		if err != nil {
			return err
		}
		// 查询对方已把我方当好友的记录（反向关系）
		addOwners, err := f.friend.FindReversalFriends(ctx, ownerUserID, friendUserIDs)
		if err != nil {
			return err
		}
		opUserID := mcontext.GetOpUserID(ctx)
		friends := make([]*model.Friend, 0, len(friendUserIDs)*2)
		myFriendsSet := datautil.SliceSetAny(myFriends, func(friend *model.Friend) string {
			return friend.FriendUserID
		})
		addOwnersSet := datautil.SliceSetAny(addOwners, func(friend *model.Friend) string {
			return friend.OwnerUserID
		})
		newMyFriendIDs := make([]string, 0, len(friendUserIDs))
		newMyOwnerIDs := make([]string, 0, len(friendUserIDs))
		for _, userID := range friendUserIDs {
			if ownerUserID == userID {
				continue
			}
			// 补齐「我 → 好友」方向的记录
			if _, ok := myFriendsSet[userID]; !ok {
				myFriendsSet[userID] = struct{}{}
				newMyFriendIDs = append(newMyFriendIDs, userID)
				friends = append(friends, &model.Friend{OwnerUserID: ownerUserID, FriendUserID: userID, AddSource: addSource, OperatorUserID: opUserID})
			}
			// 补齐「好友 → 我」方向的记录
			if _, ok := addOwnersSet[userID]; !ok {
				addOwnersSet[userID] = struct{}{}
				newMyOwnerIDs = append(newMyOwnerIDs, userID)
				friends = append(friends, &model.Friend{OwnerUserID: userID, FriendUserID: ownerUserID, AddSource: addSource, OperatorUserID: opUserID})
			}
		}
		if len(friends) == 0 {
			return nil
		}
		err = f.friend.Create(ctx, friends)
		if err != nil {
			return err
		}
		// 写库成功后清理相关缓存
		cache = cache.DelFriendIDs(ownerUserID).DelMaxFriendVersion(ownerUserID)
		if len(newMyFriendIDs) > 0 {
			cache = cache.DelFriendIDs(newMyFriendIDs...)
			cache = cache.DelFriends(ownerUserID, newMyFriendIDs).DelMaxFriendVersion(newMyFriendIDs...)
		}
		if len(newMyOwnerIDs) > 0 {
			cache = cache.DelFriendIDs(newMyOwnerIDs...)
			cache = cache.DelOwner(ownerUserID, newMyOwnerIDs).DelMaxFriendVersion(newMyOwnerIDs...)
		}
		return cache.ChainExecDel(ctx)
	})
}

// RefuseFriendRequest 拒绝好友申请。先检查是否存在未处理的申请，
// 不存在则返回错误；存在则标记为已拒绝。
func (f *friendDatabase) RefuseFriendRequest(ctx context.Context, friendRequest *model.FriendRequest) error {
	// 从数据库取出该好友申请
	fr, err := f.friendRequest.Take(ctx, friendRequest.FromUserID, friendRequest.ToUserID)
	if err != nil {
		return err
	}

	// 检查该好友申请是否已被处理过
	if fr.HandleResult != 0 {
		return servererrs.ErrFriendRequestHandled.WrapMsg("friend request has already been processed", "from", friendRequest.FromUserID, "to", friendRequest.ToUserID)
	}

	// 记录拒绝好友申请的动作，便于调试与审计
	log.ZDebug(ctx, "Refusing friend request", map[string]interface{}{
		"DB_FriendRequest":  fr,
		"Arg_FriendRequest": friendRequest,
	})

	// 标记为已拒绝并更新处理时间
	friendRequest.HandleResult = constant.FriendResponseRefuse
	friendRequest.HandleTime = time.Now()
	if err := f.friendRequest.Update(ctx, friendRequest); err != nil {
		return err
	}

	return nil
}

// AgreeFriendRequest 同意好友申请。先检查是否存在未处理的申请。
func (f *friendDatabase) AgreeFriendRequest(ctx context.Context, friendRequest *model.FriendRequest) (err error) {
	return f.tx.Transaction(ctx, func(ctx context.Context) error {
		now := time.Now()
		fr, err := f.friendRequest.Take(ctx, friendRequest.FromUserID, friendRequest.ToUserID)
		if err != nil {
			return err
		}
		if fr.HandleResult != 0 {
			return errs.ErrArgs.WrapMsg("the friend request has been processed")
		}
		friendRequest.HandlerUserID = mcontext.GetOpUserID(ctx)
		friendRequest.HandleResult = constant.FriendResponseAgree
		friendRequest.HandleTime = now
		err = f.friendRequest.Update(ctx, friendRequest)
		if err != nil {
			return err
		}

		// 若对方也给我方发过申请，一并标记为同意
		fr2, err := f.friendRequest.Take(ctx, friendRequest.ToUserID, friendRequest.FromUserID)
		if err == nil && fr2.HandleResult == constant.FriendResponseNotHandle {
			fr2.HandlerUserID = mcontext.GetOpUserID(ctx)
			fr2.HandleResult = constant.FriendResponseAgree
			fr2.HandleTime = now
			err = f.friendRequest.Update(ctx, fr2)
			if err != nil {
				return err
			}
		} else if err != nil && (!mgo.IsNotFound(err)) {
			return err
		}

		// 查询已有的双向好友记录，缺哪个方向就补哪个方向
		exists, err := f.friend.FindUserState(ctx, friendRequest.FromUserID, friendRequest.ToUserID)
		if err != nil {
			return err
		}
		existsMap := datautil.SliceSet(datautil.Slice(exists, func(friend *model.Friend) [2]string {
			return [...]string{friend.OwnerUserID, friend.FriendUserID} // 我 - 好友
		}))
		var adds []*model.Friend
		if _, ok := existsMap[[...]string{friendRequest.ToUserID, friendRequest.FromUserID}]; !ok { // 我 - 好友
			adds = append(
				adds,
				&model.Friend{
					OwnerUserID:    friendRequest.ToUserID,
					FriendUserID:   friendRequest.FromUserID,
					AddSource:      int32(constant.BecomeFriendByApply),
					OperatorUserID: friendRequest.FromUserID,
				},
			)
		}
		if _, ok := existsMap[[...]string{friendRequest.FromUserID, friendRequest.ToUserID}]; !ok { // 我 - 好友
			adds = append(
				adds,
				&model.Friend{
					OwnerUserID:    friendRequest.FromUserID,
					FriendUserID:   friendRequest.ToUserID,
					AddSource:      int32(constant.BecomeFriendByApply),
					OperatorUserID: friendRequest.FromUserID,
				},
			)
		}
		if len(adds) > 0 {
			if err := f.friend.Create(ctx, adds); err != nil {
				return err
			}
		}
		return f.cache.DelFriendIDs(friendRequest.ToUserID, friendRequest.FromUserID).DelMaxFriendVersion(friendRequest.ToUserID, friendRequest.FromUserID).ChainExecDel(ctx)
	})
}

// Delete 删除好友关系。假定调用方已提前校验过好友状态。
func (f *friendDatabase) Delete(ctx context.Context, ownerUserID string, friendUserIDs []string) (err error) {
	if err := f.friend.Delete(ctx, ownerUserID, friendUserIDs); err != nil {
		return err
	}
	userIds := append(friendUserIDs, ownerUserID)
	return f.cache.DelFriendIDs(userIds...).DelMaxFriendVersion(userIds...).ChainExecDel(ctx)
}

// UpdateRemark 更新好友备注。备注为零值（清空）也支持。
func (f *friendDatabase) UpdateRemark(ctx context.Context, ownerUserID, friendUserID, remark string) (err error) {
	if err := f.friend.UpdateRemark(ctx, ownerUserID, friendUserID, remark); err != nil {
		return err
	}
	return f.cache.DelFriend(ownerUserID, friendUserID).DelMaxFriendVersion(ownerUserID).ChainExecDel(ctx)
}

// PageOwnerFriends 获取 ownerUserID 的好友列表。结果为空时不返回错误。
func (f *friendDatabase) PageOwnerFriends(ctx context.Context, ownerUserID string, pagination pagination.Pagination) (total int64, friends []*model.Friend, err error) {
	return f.friend.FindOwnerFriends(ctx, ownerUserID, pagination)
}

// PageInWhoseFriends 查找 friendUserID 出现在哪些用户的好友列表中。
func (f *friendDatabase) PageInWhoseFriends(ctx context.Context, friendUserID string, pagination pagination.Pagination) (total int64, friends []*model.Friend, err error) {
	return f.friend.FindInWhoseFriends(ctx, friendUserID, pagination)
}

// PageFriendRequestFromMe 获取我发出的好友申请。结果为空时不返回错误。
func (f *friendDatabase) PageFriendRequestFromMe(ctx context.Context, userID string, handleResults []int, pagination pagination.Pagination) (total int64, friends []*model.FriendRequest, err error) {
	return f.friendRequest.FindFromUserID(ctx, userID, handleResults, pagination)
}

// PageFriendRequestToMe 获取我收到的好友申请。结果为空时不返回错误。
func (f *friendDatabase) PageFriendRequestToMe(ctx context.Context, userID string, handleResults []int, pagination pagination.Pagination) (total int64, friends []*model.FriendRequest, err error) {
	return f.friendRequest.FindToUserID(ctx, userID, handleResults, pagination)
}

// FindFriendsWithError 获取 ownerUserID 的指定好友信息。任一好友不存在则返回错误。
func (f *friendDatabase) FindFriendsWithError(ctx context.Context, ownerUserID string, friendUserIDs []string) (friends []*model.Friend, err error) {
	friends, err = f.friend.FindFriends(ctx, ownerUserID, friendUserIDs)
	if err != nil {
		return nil, err
	}
	return friends, nil
}

func (f *friendDatabase) FindFriendUserIDs(ctx context.Context, ownerUserID string) (friendUserIDs []string, err error) {
	return f.cache.GetFriendIDs(ctx, ownerUserID)
}

func (f *friendDatabase) FindBothFriendRequests(ctx context.Context, fromUserID, toUserID string) (friends []*model.FriendRequest, err error) {
	return f.friendRequest.FindBothFriendRequests(ctx, fromUserID, toUserID)
}
func (f *friendDatabase) UpdateFriends(ctx context.Context, ownerUserID string, friendUserIDs []string, val map[string]any) (err error) {
	if len(val) == 0 {
		return nil
	}
	return f.tx.Transaction(ctx, func(ctx context.Context) error {
		if err := f.friend.UpdateFriends(ctx, ownerUserID, friendUserIDs, val); err != nil {
			return err
		}
		return f.cache.DelFriends(ownerUserID, friendUserIDs).DelMaxFriendVersion(ownerUserID).ChainExecDel(ctx)
	})
}

//func (f *friendDatabase) FindSortFriendUserIDs(ctx context.Context, ownerUserID string) ([]string, error) {
//	return f.cache.FindSortFriendUserIDs(ctx, ownerUserID)
//}

func (f *friendDatabase) FindFriendIncrVersion(ctx context.Context, ownerUserID string, version uint, limit int) (*model.VersionLog, error) {
	return f.friend.FindIncrVersion(ctx, ownerUserID, version, limit)
}

func (f *friendDatabase) FindMaxFriendVersionCache(ctx context.Context, ownerUserID string) (*model.VersionLog, error) {
	return f.cache.FindMaxFriendVersion(ctx, ownerUserID)
}

func (f *friendDatabase) FindFriendUserID(ctx context.Context, friendUserID string) ([]string, error) {
	return f.friend.FindFriendUserID(ctx, friendUserID)
}

//func (f *friendDatabase) SearchFriend(ctx context.Context, ownerUserID, keyword string, pagination pagination.Pagination) (int64, []*model.Friend, error) {
//	return f.friend.SearchFriend(ctx, ownerUserID, keyword, pagination)
//}

func (f *friendDatabase) OwnerIncrVersion(ctx context.Context, ownerUserID string, friendUserIDs []string, state int32) error {
	if err := f.friend.IncrVersion(ctx, ownerUserID, friendUserIDs, state); err != nil {
		return err
	}
	return f.cache.DelMaxFriendVersion(ownerUserID).ChainExecDel(ctx)
}

func (f *friendDatabase) GetUnhandledCount(ctx context.Context, userID string, ts int64) (int64, error) {
	return f.friendRequest.GetUnhandledCount(ctx, userID, ts)
}

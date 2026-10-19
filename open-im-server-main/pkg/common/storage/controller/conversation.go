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

	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/database"
	relationtb "github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"

	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/db/pagination"
	"github.com/openimsdk/tools/db/tx"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/utils/datautil"
	"github.com/openimsdk/tools/utils/stringutil"
)

// ConversationDatabase 会话数据库操作接口
type ConversationDatabase interface {
	// UpdateUsersConversationField 更新指定用户某个会话的属性
	UpdateUsersConversationField(ctx context.Context, userIDs []string, conversationID string, args map[string]any) error
	// CreateConversation 批量创建新会话
	CreateConversation(ctx context.Context, conversations []*relationtb.Conversation) error
	// SyncPeerUserPrivateConversationTx 在事务中同步双方私聊会话（阅后即焚等属性）
	SyncPeerUserPrivateConversationTx(ctx context.Context, conversation []*relationtb.Conversation) error
	// FindConversations 按会话 ID 批量获取某用户的会话
	FindConversations(ctx context.Context, ownerUserID string, conversationIDs []string) ([]*relationtb.Conversation, error)
	// GetUserAllConversation 获取某用户在服务端的全部会话
	GetUserAllConversation(ctx context.Context, ownerUserID string) ([]*relationtb.Conversation, error)
	// SetUserConversations 批量设置某用户的会话属性，不存在则创建、存在则更新。该操作是原子的
	SetUserConversations(ctx context.Context, ownerUserID string, conversations []*relationtb.Conversation) error
	// SetUsersConversationFieldTx 更新多个用户某个会话的指定字段，不存在则创建、存在则更新。该操作是事务的
	SetUsersConversationFieldTx(ctx context.Context, userIDs []string, conversation *relationtb.Conversation, fieldMap map[string]any) error
	// UpdateUserConversations 更新与指定用户相关的所有会话。
	// 注意：不更新该用户自己的会话，而是更新涉及该用户的会话（例如其他人引用该用户的会话）
	UpdateUserConversations(ctx context.Context, userID string, args map[string]any) error
	// CreateGroupChatConversation 为指定群 ID 与用户 ID 列表创建群聊会话
	CreateGroupChatConversation(ctx context.Context, groupID string, userIDs []string, conversations *relationtb.Conversation) error
	// GetConversationIDs 获取某用户的会话 ID 列表
	GetConversationIDs(ctx context.Context, userID string) ([]string, error)
	// GetUserConversationIDsHash 获取某用户会话 ID 列表的哈希（用于增量同步比对）
	GetUserConversationIDsHash(ctx context.Context, ownerUserID string) (hash uint64, err error)
	// GetAllConversationIDs 获取所有会话 ID
	GetAllConversationIDs(ctx context.Context) ([]string, error)
	// GetAllConversationIDsNumber 返回所有会话 ID 的数量
	GetAllConversationIDsNumber(ctx context.Context) (int64, error)
	// PageConversationIDs 按分页配置遍历会话 ID
	PageConversationIDs(ctx context.Context, pagination pagination.Pagination) (conversationIDs []string, err error)
	// GetConversationIDsNeedDestruct 获取满足阅后即焚销毁条件的会话
	GetConversationIDsNeedDestruct(ctx context.Context) ([]*relationtb.Conversation, error)
	// GetConversationNotReceiveMessageUserIDs 获取会话中设置了不接收消息的用户 ID
	GetConversationNotReceiveMessageUserIDs(ctx context.Context, conversationID string) ([]string, error)
	// GetUserAllHasReadSeqs(ctx context.Context, ownerUserID string) (map[string]int64, error)
	// FindRecvMsgNotNotifyUserIDs(ctx context.Context, groupID string) ([]string, error)
	FindConversationUserVersion(ctx context.Context, userID string, version uint, limit int) (*relationtb.VersionLog, error)
	FindMaxConversationUserVersionCache(ctx context.Context, userID string) (*relationtb.VersionLog, error)
	GetOwnerConversation(ctx context.Context, ownerUserID string, pagination pagination.Pagination) (int64, []*relationtb.Conversation, error)
	// GetNotNotifyConversationIDs 按用户 ID 获取免打扰会话 ID
	GetNotNotifyConversationIDs(ctx context.Context, userID string) ([]string, error)
	// GetPinnedConversationIDs 按用户 ID 获取置顶会话 ID
	GetPinnedConversationIDs(ctx context.Context, userID string) ([]string, error)
	// FindRandConversation 按指定时间戳与数量随机查找会话
	FindRandConversation(ctx context.Context, ts int64, limit int) ([]*relationtb.Conversation, error)

	DeleteUsersConversations(ctx context.Context, userID string, conversationIDs []string) (err error)
}

func NewConversationDatabase(conversation database.Conversation, cache cache.ConversationCache, tx tx.Tx) ConversationDatabase {
	return &conversationDatabase{
		conversationDB: conversation,
		cache:          cache,
		tx:             tx,
	}
}

type conversationDatabase struct {
	conversationDB database.Conversation
	cache          cache.ConversationCache
	tx             tx.Tx
}

func (c *conversationDatabase) SetUsersConversationFieldTx(ctx context.Context, userIDs []string, conversation *relationtb.Conversation, fieldMap map[string]any) (err error) {
	return c.tx.Transaction(ctx, func(ctx context.Context) error {
		cache := c.cache.CloneConversationCache()
		if conversation.GroupID != "" {
			cache = cache.DelSuperGroupRecvMsgNotNotifyUserIDs(conversation.GroupID).DelSuperGroupRecvMsgNotNotifyUserIDsHash(conversation.GroupID)
		}
		haveUserIDs, err := c.conversationDB.FindUserID(ctx, userIDs, []string{conversation.ConversationID})
		if err != nil {
			return err
		}
		if len(haveUserIDs) > 0 {
			_, err = c.conversationDB.UpdateByMap(ctx, haveUserIDs, conversation.ConversationID, fieldMap)
			if err != nil {
				return err
			}
			cache = cache.DelUsersConversation(conversation.ConversationID, haveUserIDs...)
			if _, ok := fieldMap["has_read_seq"]; ok {
				for _, userID := range haveUserIDs {
					cache = cache.DelUserAllHasReadSeqs(userID, conversation.ConversationID)
				}
			}
			if _, ok := fieldMap["recv_msg_opt"]; ok {
				cache = cache.DelConversationNotReceiveMessageUserIDs(conversation.ConversationID)
				cache = cache.DelConversationNotNotifyMessageUserIDs(userIDs...)
			}
			if _, ok := fieldMap["is_pinned"]; ok {
				cache = cache.DelUserPinnedConversations(userIDs...)
			}
			cache = cache.DelConversationVersionUserIDs(haveUserIDs...)
		}
		// 对还没有该会话的用户，创建新会话记录
		NotUserIDs := stringutil.DifferenceString(haveUserIDs, userIDs)
		log.ZDebug(ctx, "SetUsersConversationFieldTx", "NotUserIDs", NotUserIDs, "haveUserIDs", haveUserIDs, "userIDs", userIDs)
		var conversations []*relationtb.Conversation
		now := time.Now()
		for _, v := range NotUserIDs {
			temp := new(relationtb.Conversation)
			if err = datautil.CopyStructFields(temp, conversation); err != nil {
				return err
			}
			temp.OwnerUserID = v
			temp.CreateTime = now
			conversations = append(conversations, temp)
		}
		if len(conversations) > 0 {
			err = c.conversationDB.Create(ctx, conversations)
			if err != nil {
				return err
			}
			cache = cache.DelConversationIDs(NotUserIDs...).DelUserConversationIDsHash(NotUserIDs...).DelConversations(conversation.ConversationID, NotUserIDs...)
		}
		return cache.ChainExecDel(ctx)
	})
}

func (c *conversationDatabase) UpdateUserConversations(ctx context.Context, userID string, args map[string]any) error {
	conversations, err := c.conversationDB.UpdateUserConversations(ctx, userID, args)
	if err != nil {
		return err
	}
	cache := c.cache.CloneConversationCache()
	for _, conversation := range conversations {
		cache = cache.DelUsersConversation(conversation.ConversationID, conversation.OwnerUserID).DelConversationVersionUserIDs(conversation.OwnerUserID)
	}
	return cache.ChainExecDel(ctx)
}

func (c *conversationDatabase) UpdateUsersConversationField(ctx context.Context, userIDs []string, conversationID string, args map[string]any) error {
	_, err := c.conversationDB.UpdateByMap(ctx, userIDs, conversationID, args)
	if err != nil {
		return err
	}
	cache := c.cache.CloneConversationCache()
	cache = cache.DelUsersConversation(conversationID, userIDs...).DelConversationVersionUserIDs(userIDs...)
	if _, ok := args["recv_msg_opt"]; ok {
		cache = cache.DelConversationNotReceiveMessageUserIDs(conversationID)
		cache = cache.DelConversationNotNotifyMessageUserIDs(userIDs...)
	}
	if _, ok := args["is_pinned"]; ok {
		cache = cache.DelUserPinnedConversations(userIDs...)
	}
	return cache.ChainExecDel(ctx)
}

func (c *conversationDatabase) CreateConversation(ctx context.Context, conversations []*relationtb.Conversation) error {
	if err := c.conversationDB.Create(ctx, conversations); err != nil {
		return err
	}
	var (
		userIDs          []string
		notNotifyUserIDs []string
		pinnedUserIDs    []string
	)

	cache := c.cache.CloneConversationCache()
	for _, conversation := range conversations {
		cache = cache.DelConversations(conversation.OwnerUserID, conversation.ConversationID)
		cache = cache.DelConversationNotReceiveMessageUserIDs(conversation.ConversationID)
		userIDs = append(userIDs, conversation.OwnerUserID)
		if conversation.RecvMsgOpt == constant.ReceiveNotNotifyMessage {
			notNotifyUserIDs = append(notNotifyUserIDs, conversation.OwnerUserID)
		}
		if conversation.IsPinned {
			pinnedUserIDs = append(pinnedUserIDs, conversation.OwnerUserID)
		}
	}
	return cache.DelConversationIDs(userIDs...).
		DelUserConversationIDsHash(userIDs...).
		DelConversationVersionUserIDs(userIDs...).
		DelConversationNotNotifyMessageUserIDs(notNotifyUserIDs...).
		DelUserPinnedConversations(pinnedUserIDs...).
		ChainExecDel(ctx)
}

func (c *conversationDatabase) SyncPeerUserPrivateConversationTx(ctx context.Context, conversations []*relationtb.Conversation) error {
	return c.tx.Transaction(ctx, func(ctx context.Context) error {
		cache := c.cache.CloneConversationCache()
		for _, conversation := range conversations {
			cache = cache.DelConversationVersionUserIDs(conversation.OwnerUserID, conversation.UserID)
			// 双向同步：我方与对方各更新/创建一份会话记录
			for _, v := range [][2]string{{conversation.OwnerUserID, conversation.UserID}, {conversation.UserID, conversation.OwnerUserID}} {
				ownerUserID := v[0]
				userID := v[1]
				haveUserIDs, err := c.conversationDB.FindUserID(ctx, []string{ownerUserID}, []string{conversation.ConversationID})
				if err != nil {
					return err
				}
				if len(haveUserIDs) > 0 {
					_, err := c.conversationDB.UpdateByMap(ctx, []string{ownerUserID}, conversation.ConversationID, map[string]any{"is_private_chat": conversation.IsPrivateChat})
					if err != nil {
						return err
					}
					cache = cache.DelUsersConversation(conversation.ConversationID, ownerUserID)
				} else {
					newConversation := *conversation
					newConversation.OwnerUserID = ownerUserID
					newConversation.UserID = userID
					newConversation.ConversationID = conversation.ConversationID
					newConversation.IsPrivateChat = conversation.IsPrivateChat
					if err := c.conversationDB.Create(ctx, []*relationtb.Conversation{&newConversation}); err != nil {
						return err
					}
					cache = cache.DelConversationIDs(ownerUserID).DelUserConversationIDsHash(ownerUserID)
				}
			}
		}
		return cache.ChainExecDel(ctx)
	})
}

func (c *conversationDatabase) FindConversations(ctx context.Context, ownerUserID string, conversationIDs []string) ([]*relationtb.Conversation, error) {
	return c.cache.GetConversations(ctx, ownerUserID, conversationIDs)
}

func (c *conversationDatabase) GetConversation(ctx context.Context, ownerUserID string, conversationID string) (*relationtb.Conversation, error) {
	return c.cache.GetConversation(ctx, ownerUserID, conversationID)
}

func (c *conversationDatabase) GetUserAllConversation(ctx context.Context, ownerUserID string) ([]*relationtb.Conversation, error) {
	return c.cache.GetUserAllConversations(ctx, ownerUserID)
}

func (c *conversationDatabase) SetUserConversations(ctx context.Context, ownerUserID string, conversations []*relationtb.Conversation) error {
	return c.tx.Transaction(ctx, func(ctx context.Context) error {
		cache := c.cache.CloneConversationCache()
		cache = cache.DelConversationVersionUserIDs(ownerUserID).
			DelConversationNotNotifyMessageUserIDs(ownerUserID).
			DelUserPinnedConversations(ownerUserID)

		groupIDs := datautil.Distinct(datautil.Filter(conversations, func(e *relationtb.Conversation) (string, bool) {
			return e.GroupID, e.GroupID != ""
		}))
		for _, groupID := range groupIDs {
			cache = cache.DelSuperGroupRecvMsgNotNotifyUserIDs(groupID).DelSuperGroupRecvMsgNotNotifyUserIDsHash(groupID)
		}
		var conversationIDs []string
		for _, conversation := range conversations {
			conversationIDs = append(conversationIDs, conversation.ConversationID)
			cache = cache.DelConversations(conversation.OwnerUserID, conversation.ConversationID)
		}
		existConversations, err := c.conversationDB.Find(ctx, ownerUserID, conversationIDs)
		if err != nil {
			return err
		}
		if len(existConversations) > 0 {
			for _, conversation := range conversations {
				err = c.conversationDB.Update(ctx, conversation)
				if err != nil {
					return err
				}
			}
		}
		var existConversationIDs []string
		for _, conversation := range existConversations {
			existConversationIDs = append(existConversationIDs, conversation.ConversationID)
		}

		// 不存在的会话执行创建
		var notExistConversations []*relationtb.Conversation
		for _, conversation := range conversations {
			if !datautil.Contain(conversation.ConversationID, existConversationIDs...) {
				notExistConversations = append(notExistConversations, conversation)
			}
		}
		if len(notExistConversations) > 0 {
			err = c.conversationDB.Create(ctx, notExistConversations)
			if err != nil {
				return err
			}
			cache = cache.DelConversationIDs(ownerUserID).
				DelUserConversationIDsHash(ownerUserID).
				DelConversationNotReceiveMessageUserIDs(datautil.Slice(notExistConversations, func(e *relationtb.Conversation) string { return e.ConversationID })...)
		}
		return cache.ChainExecDel(ctx)
	})
}

// func (c *conversationDatabase) FindRecvMsgNotNotifyUserIDs(ctx context.Context, groupID string) ([]string, error) {
//	return c.cache.GetSuperGroupRecvMsgNotNotifyUserIDs(ctx, groupID)
//}

func (c *conversationDatabase) CreateGroupChatConversation(ctx context.Context, groupID string, userIDs []string, conversation *relationtb.Conversation) error {
	return c.tx.Transaction(ctx, func(ctx context.Context) error {
		cache := c.cache.CloneConversationCache()
		conversationID := conversation.ConversationID
		existConversationUserIDs, err := c.conversationDB.FindUserID(ctx, userIDs, []string{conversationID})
		if err != nil {
			return err
		}
		notExistUserIDs := stringutil.DifferenceString(userIDs, existConversationUserIDs)
		var conversations []*relationtb.Conversation
		for _, v := range notExistUserIDs {
			conversation := relationtb.Conversation{
				ConversationType: conversation.ConversationType, GroupID: groupID, OwnerUserID: v, ConversationID: conversationID,
				// 以下参数均有默认值
				RecvMsgOpt: conversation.RecvMsgOpt, IsPinned: conversation.IsPinned, IsPrivateChat: conversation.IsPrivateChat,
				BurnDuration: conversation.BurnDuration, GroupAtType: conversation.GroupAtType, AttachedInfo: conversation.AttachedInfo,
				Ex: conversation.Ex, MaxSeq: conversation.MaxSeq, MinSeq: conversation.MinSeq, CreateTime: conversation.CreateTime,
				MsgDestructTime: conversation.MsgDestructTime, IsMsgDestruct: conversation.IsMsgDestruct, LatestMsgDestructTime: conversation.LatestMsgDestructTime,
			}

			conversations = append(conversations, &conversation)
			cache = cache.DelConversations(v, conversationID).DelConversationNotReceiveMessageUserIDs(conversationID)
		}
		cache = cache.DelConversationIDs(notExistUserIDs...).DelUserConversationIDsHash(notExistUserIDs...)
		if len(conversations) > 0 {
			err = c.conversationDB.Create(ctx, conversations)
			if err != nil {
				return err
			}
		}
		_, err = c.conversationDB.UpdateByMap(ctx, existConversationUserIDs, conversationID, map[string]any{"max_seq": conversation.MaxSeq})
		if err != nil {
			return err
		}
		for _, v := range existConversationUserIDs {
			cache = cache.DelConversations(v, conversationID)
		}
		return cache.ChainExecDel(ctx)
	})
}

func (c *conversationDatabase) GetConversationIDs(ctx context.Context, userID string) ([]string, error) {
	return c.cache.GetUserConversationIDs(ctx, userID)
}

func (c *conversationDatabase) GetUserConversationIDsHash(ctx context.Context, ownerUserID string) (hash uint64, err error) {
	return c.cache.GetUserConversationIDsHash(ctx, ownerUserID)
}

func (c *conversationDatabase) GetAllConversationIDs(ctx context.Context) ([]string, error) {
	return c.conversationDB.GetAllConversationIDs(ctx)
}

func (c *conversationDatabase) GetAllConversationIDsNumber(ctx context.Context) (int64, error) {
	return c.conversationDB.GetAllConversationIDsNumber(ctx)
}

func (c *conversationDatabase) PageConversationIDs(ctx context.Context, pagination pagination.Pagination) ([]string, error) {
	return c.conversationDB.PageConversationIDs(ctx, pagination)
}

func (c *conversationDatabase) GetConversationIDsNeedDestruct(ctx context.Context) ([]*relationtb.Conversation, error) {
	return c.conversationDB.GetConversationIDsNeedDestruct(ctx)
}

func (c *conversationDatabase) GetConversationNotReceiveMessageUserIDs(ctx context.Context, conversationID string) ([]string, error) {
	return c.cache.GetConversationNotReceiveMessageUserIDs(ctx, conversationID)
}

func (c *conversationDatabase) FindConversationUserVersion(ctx context.Context, userID string, version uint, limit int) (*relationtb.VersionLog, error) {
	return c.conversationDB.FindConversationUserVersion(ctx, userID, version, limit)
}

func (c *conversationDatabase) FindMaxConversationUserVersionCache(ctx context.Context, userID string) (*relationtb.VersionLog, error) {
	return c.cache.FindMaxConversationUserVersion(ctx, userID)
}

func (c *conversationDatabase) GetOwnerConversation(ctx context.Context, ownerUserID string, pagination pagination.Pagination) (int64, []*relationtb.Conversation, error) {
	conversationIDs, err := c.cache.GetUserConversationIDs(ctx, ownerUserID)
	if err != nil {
		return 0, nil, err
	}
	findConversationIDs := datautil.Paginate(conversationIDs, int(pagination.GetPageNumber()), int(pagination.GetShowNumber()))
	conversations := make([]*relationtb.Conversation, 0, len(findConversationIDs))
	for _, conversationID := range findConversationIDs {
		conversation, err := c.cache.GetConversation(ctx, ownerUserID, conversationID)
		if err != nil {
			return 0, nil, err
		}
		conversations = append(conversations, conversation)
	}
	return int64(len(conversationIDs)), conversations, nil
}

func (c *conversationDatabase) GetNotNotifyConversationIDs(ctx context.Context, userID string) ([]string, error) {
	conversationIDs, err := c.cache.GetUserNotNotifyConversationIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	return conversationIDs, nil
}

func (c *conversationDatabase) GetPinnedConversationIDs(ctx context.Context, userID string) ([]string, error) {
	conversationIDs, err := c.cache.GetPinnedConversationIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	return conversationIDs, nil
}

func (c *conversationDatabase) FindRandConversation(ctx context.Context, ts int64, limit int) ([]*relationtb.Conversation, error) {
	return c.conversationDB.FindRandConversation(ctx, ts, limit)
}

func (c *conversationDatabase) DeleteUsersConversations(ctx context.Context, userID string, conversationIDs []string) (err error) {
	return c.tx.Transaction(ctx, func(ctx context.Context) error {
		err = c.conversationDB.DeleteUsersConversations(ctx, userID, conversationIDs)
		if err != nil {
			return err
		}
		cache := c.cache.CloneConversationCache()
		cache = cache.DelConversations(userID, conversationIDs...).
			DelConversationVersionUserIDs(userID).
			DelConversationIDs(userID).
			DelUserConversationIDsHash(userID).
			DelConversationNotNotifyMessageUserIDs(userID).
			DelUserPinnedConversations(userID)

		return cache.ChainExecDel(ctx)
	})
}

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

package msg

import (
	"github.com/openimsdk/protocol/msg"
	"github.com/openimsdk/tools/errs"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

// IsNotFound 判断错误是否为“数据不存在”（Redis 返回 Nil 或 Mongo 返回 ErrNoDocuments）
func IsNotFound(err error) bool {
	switch errs.Unwrap(err) {
	case redis.Nil, mongo.ErrNoDocuments:
		return true
	default:
		return false
	}
}

// activeConversations 按会话最近活跃时间排序的实现（sort.Interface）
type activeConversations []*msg.ActiveConversation

func (s activeConversations) Len() int {
	return len(s)
}

func (s activeConversations) Less(i, j int) bool {
	return s[i].LastTime > s[j].LastTime
}

func (s activeConversations) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

//type seqTime struct {
//	ConversationID string
//	Seq            int64
//	Time           int64
//	Unread         int64
//	Pinned         bool
//}
//
//func (s seqTime) String() string {
//	return fmt.Sprintf("<Time_%d,Unread_%d,Pinned_%t>", s.Time, s.Unread, s.Pinned)
//}
//
//type seqTimes []seqTime
//
//func (s seqTimes) Len() int {
//	return len(s)
//}
//
//// Less 排序规则：置顶优先、未读优先、时间倒序
//func (s seqTimes) Less(i, j int) bool {
//	iv, jv := s[i], s[j]
//	if iv.Pinned && (!jv.Pinned) {
//		return true
//	}
//	if jv.Pinned && (!iv.Pinned) {
//		return false
//	}
//	if iv.Unread > 0 && jv.Unread == 0 {
//		return true
//	}
//	if jv.Unread > 0 && iv.Unread == 0 {
//		return false
//	}
//	return iv.Time > jv.Time
//}
//
//func (s seqTimes) Swap(i, j int) {
//	s[i], s[j] = s[j], s[i]
//}
//
//type conversationStatus struct {
//	ConversationID string
//	Pinned         bool
//	Recv           bool
//}

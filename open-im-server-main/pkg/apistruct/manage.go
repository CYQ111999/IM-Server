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

package apistruct

import (
	pbmsg "github.com/openimsdk/protocol/msg"
	"github.com/openimsdk/protocol/sdkws"
)

// SendMsg 定义发送消息的结构体，包含各类元数据。
type SendMsg struct {
	// SendID 发送者的唯一标识。
	SendID string `json:"sendID" binding:"required"`

	// GroupID 群组标识，SessionType 为 2 或 3 时必填。
	GroupID string `json:"groupID" binding:"required_if=SessionType 2|required_if=SessionType 3"`

	// SenderNickname 发送者昵称。
	SenderNickname string `json:"senderNickname"`

	// SenderFaceURL 发送者头像 URL。
	SenderFaceURL string `json:"senderFaceURL"`

	// SenderPlatformID 发送者平台的整数标识。
	SenderPlatformID int32 `json:"senderPlatformID"`

	// Content 消息的实际内容，必填，Swagger 文档中不展示。
	Content map[string]any `json:"content" binding:"required" swaggerignore:"true"`

	// ContentType 表示内容类型的整数。
	ContentType int32 `json:"contentType" binding:"required"`

	// SessionType 表示消息会话类型的整数。
	SessionType int32 `json:"sessionType" binding:"required"`

	// IsOnlineOnly 指定消息是否仅在接收方在线时发送。
	IsOnlineOnly bool `json:"isOnlineOnly"`

	// NotOfflinePush 指定消息是否不触发离线推送通知。
	NotOfflinePush bool `json:"notOfflinePush"`

	// SendTime 消息发送时间的时间戳。
	SendTime int64 `json:"sendTime"`

	// OfflinePushInfo 离线推送通知的相关信息。
	OfflinePushInfo *sdkws.OfflinePushInfo `json:"offlinePushInfo"`

	// Ex 扩展字段
	Ex string `json:"ex"`
}

// SendMsgReq 在 SendMsg 基础上扩展：当 SessionType 为单聊或通知聊天时，要求提供 RecvID。
type SendMsgReq struct {
	// RecvID 接收者的唯一标识，单聊或通知聊天类型时必填。
	RecvID string `json:"recvID" binding:"required_if" message:"recvID is required if sessionType is SingleChatType or NotificationChatType"`
	SendMsg
}

type GetConversationListReq struct {
	// userID 用户的唯一标识。
	UserID string `protobuf:"bytes,1,opt,name=userID,proto3" json:"userID,omitempty" binding:"required"`

	// ConversationIDs 会话唯一标识列表。
	ConversationIDs []string `protobuf:"bytes,2,rep,name=conversationIDs,proto3" json:"conversationIDs,omitempty"`
}

type GetConversationListResp struct {
	// ConversationElems 将会话 ID 与其详细信息关联起来的 map。
	ConversationElems map[string]*ConversationElem `protobuf:"bytes,1,rep,name=conversationElems,proto3" json:"conversationElems,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
}

type ConversationElem struct {
	// MaxSeq 会话内的最大序号。
	MaxSeq int64 `protobuf:"varint,1,opt,name=maxSeq,proto3" json:"maxSeq,omitempty"`

	// UnreadSeq 会话内的未读消息数。
	UnreadSeq int64 `protobuf:"varint,2,opt,name=unreadSeq,proto3" json:"unreadSeq,omitempty"`

	// LastSeqTime 会话内最后一个序号的时间戳。
	LastSeqTime int64 `protobuf:"varint,3,opt,name=LastSeqTime,proto3" json:"LastSeqTime,omitempty"`
}

// BatchSendMsgReq 定义向多个接收者批量发送消息的结构体。
type BatchSendMsgReq struct {
	SendMsg

	// IsSendAll 指示是否发送给所有用户。
	IsSendAll bool `json:"isSendAll"`

	// RecvIDs 接收者标识切片，消息将发送给这些用户，必填字段。
	RecvIDs []string `json:"recvIDs" binding:"required"`
}

// BatchSendMsgResp 包含批量消息发送操作的结果。
type BatchSendMsgResp struct {
	// Results SingleReturnResult 切片，表示每条消息的发送结果。
	Results []*SingleReturnResult `json:"results"`

	// FailedIDs 消息发送失败的用户 ID 切片。
	FailedIDs []string `json:"failedUserIDs"`
}

// SendSingleMsgReq 定义单条消息发送的结构体。
type SendSingleMsgReq struct {
	// 群消息需指定 sendID
	SendID          string                 `json:"sendID"`
	Content         string                 `json:"content" binding:"required"`
	OfflinePushInfo *sdkws.OfflinePushInfo `json:"offlinePushInfo"`
	Ex              string                 `json:"ex"`
}

type KeyMsgData struct {
	SendID  string `json:"sendID"`
	RecvID  string `json:"recvID"`
	GroupID string `json:"groupID"`
}

// SingleReturnResult 封装单条消息发送的结果。
type SingleReturnResult struct {
	// ServerMsgID 服务端消息标识。
	ServerMsgID string `json:"serverMsgID"`

	// ClientMsgID 客户端消息标识。
	ClientMsgID string `json:"clientMsgID"`

	// SendTime 消息发送时间的时间戳。
	SendTime int64 `json:"sendTime"`

	// RecvID 消息接收者的唯一标识。
	RecvID string `json:"recvID"`

	// Modify 通过 webhook 修改的字段。
	Modify map[string]any `json:"modify,omitempty"`
}

type SendMsgResp struct {
	// SendMsgResp 原始响应。
	*pbmsg.SendMsgResp

	// Modify 通过 webhook 修改的字段。
	Modify map[string]any `json:"modify,omitempty"`
}

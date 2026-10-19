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

package msggateway

import "time"

const (
	WsUserID                = "sendID"
	CommonUserID            = "userID"
	PlatformID              = "platformID"
	ConnID                  = "connID"
	Token                   = "token"
	OperationID             = "operationID"
	Compression             = "compression"
	GzipCompressionProtocol = "gzip"
	BackgroundStatus        = "isBackground"
	SendResponse            = "isMsgResp"
	SDKType                 = "sdkType"
	SDKVersion              = "sdkVersion"
)

const (
	GoSDK = "go"
	JsSDK = "js"
)

const (
	WebSocket = iota + 1
)

const (
	// WebSocket 协议指令码。
	WSGetNewestSeq        = 1001
	WSPullMsgBySeqList    = 1002
	WSSendMsg             = 1003
	WSSendSignalMsg       = 1004
	WSPullMsg             = 1005
	WSGetConvMaxReadSeq   = 1006
	WsPullConvLastMessage = 1007
	WSPushMsg             = 2001
	WSKickOnlineMsg       = 2002
	WsLogoutMsg           = 2003
	WsSetBackgroundStatus = 2004
	WsSubUserOnlineStatus = 2005
	WSDataError           = 3001
)

const (
	// 向对端写入消息的超时时间。
	writeWait = 10 * time.Second

	// 读取对端下一个 pong 消息的超时时间。
	pongWait = 30 * time.Second

	// 向对端发送 ping 的周期，必须小于 pongWait。
	pingPeriod = (pongWait * 9) / 10

	// 允许接收的对端最大消息大小。
	maxMessageSize = 51200
)

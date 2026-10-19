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

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/apiresp"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/mcontext"
)

var (
	ErrConnClosed                = errs.New("conn has closed")
	ErrNotSupportMessageProtocol = errs.New("not support message protocol")
	ErrClientClosed              = errs.New("client actively close the connection")
	ErrPanic                     = errs.New("panic error")
)

const (
	// MessageText 表示 UTF-8 编码的文本消息（如 JSON）。
	MessageText = iota + 1
	// MessageBinary 表示二进制消息（如 protobuf）。
	MessageBinary
	// CloseMessage 表示关闭控制消息。可选的消息载荷包含数字代码和文本，
	// 可使用 FormatCloseMessage 函数格式化关闭消息载荷。
	CloseMessage = 8

	// PingMessage 表示 ping 控制消息。可选的消息载荷为 UTF-8 编码的文本。
	PingMessage = 9

	// PongMessage 表示 pong 控制消息。可选的消息载荷为 UTF-8 编码的文本。
	PongMessage = 10
)

type PingPongHandler func(string) error

type Client struct {
	w              *sync.Mutex
	conn           ClientConn
	PlatformID     int    `json:"platformID"`
	IsCompress     bool   `json:"isCompress"`
	UserID         string `json:"userID"`
	IsBackground   bool   `json:"isBackground"`
	SDKType        string `json:"sdkType"`
	SDKVersion     string `json:"sdkVersion"`
	Encoder        Encoder
	ctx            *UserConnContext
	longConnServer LongConnServer
	closed         atomic.Bool
	closedErr      error
	token          string
	hbCtx          context.Context
	hbCancel       context.CancelFunc
	subLock        *sync.Mutex
	subUserIDs     map[string]struct{} // 客户端连接订阅的用户列表
}

// ResetClient 使用新的连接与上下文信息重置客户端状态。
func (c *Client) ResetClient(ctx *UserConnContext, conn ClientConn, longConnServer LongConnServer) {
	c.w = new(sync.Mutex)
	c.conn = conn
	c.PlatformID = ctx.GetPlatformID()
	c.IsCompress = ctx.GetCompression()
	c.IsBackground = ctx.GetBackground()
	c.UserID = ctx.GetUserID()
	c.ctx = ctx
	c.longConnServer = longConnServer
	c.IsBackground = false
	c.closed.Store(false)
	c.closedErr = nil
	c.token = ctx.GetToken()
	c.SDKType = ctx.GetSDKType()
	c.SDKVersion = ctx.GetSDKVersion()
	c.hbCtx, c.hbCancel = context.WithCancel(c.ctx)
	c.subLock = new(sync.Mutex)
	if c.subUserIDs != nil {
		clear(c.subUserIDs)
	}
	if c.SDKType == GoSDK {
		c.Encoder = NewGobEncoder()
	} else {
		c.Encoder = NewJsonEncoder()
	}
	c.subUserIDs = make(map[string]struct{})
}

// readMessage 持续从连接中读取消息的循环。
func (c *Client) readMessage() {
	defer func() {
		if r := recover(); r != nil {
			c.closedErr = ErrPanic
			log.ZPanic(c.ctx, "socket have panic err:", errs.ErrPanic(r))
		}
		c.close()
	}()

	for {
		log.ZDebug(c.ctx, "readMessage")
		message, returnErr := c.conn.ReadMessage()
		if returnErr != nil {
			log.ZWarn(c.ctx, "readMessage", returnErr)
			c.closedErr = returnErr
			return
		}

		if c.closed.Load() {
			// 连接刚被关闭、但读取协程尚未退出的场景
			c.closedErr = ErrConnClosed
			return
		}

		parseDataErr := c.handleMessage(message)
		if parseDataErr != nil {
			c.closedErr = parseDataErr
			return
		}
	}
}

// handleMessage 处理客户端接收到的一条消息。
func (c *Client) handleMessage(message []byte) error {
	if c.IsCompress {
		var err error
		message, err = c.longConnServer.DecompressWithPool(message)
		if err != nil {
			return errs.Wrap(err)
		}
	}

	var binaryReq = getReq()
	defer freeReq(binaryReq)

	err := c.Encoder.Decode(message, binaryReq)
	if err != nil {
		return err
	}

	if err := c.longConnServer.Validate(binaryReq); err != nil {
		return err
	}

	if binaryReq.SendID != c.UserID {
		return errs.New("exception conn userID not same to req userID", "binaryReq", binaryReq.String())
	}

	ctx := mcontext.WithMustInfoCtx(
		[]string{binaryReq.OperationID, binaryReq.SendID, constant.PlatformIDToName(c.PlatformID), c.ctx.GetConnID()},
	)

	log.ZDebug(ctx, "gateway req message", "req", binaryReq.String())

	var (
		resp       []byte
		messageErr error
	)

	switch binaryReq.ReqIdentifier {
	case WSGetNewestSeq:
		resp, messageErr = c.longConnServer.GetSeq(ctx, binaryReq)
	case WSSendMsg:
		resp, messageErr = c.longConnServer.SendMessage(ctx, binaryReq)
	case WSSendSignalMsg:
		resp, messageErr = c.longConnServer.SendSignalMessage(ctx, binaryReq)
	case WSPullMsgBySeqList:
		resp, messageErr = c.longConnServer.PullMessageBySeqList(ctx, binaryReq)
	case WSPullMsg:
		resp, messageErr = c.longConnServer.GetSeqMessage(ctx, binaryReq)
	case WSGetConvMaxReadSeq:
		resp, messageErr = c.longConnServer.GetConversationsHasReadAndMaxSeq(ctx, binaryReq)
	case WsPullConvLastMessage:
		resp, messageErr = c.longConnServer.GetLastMessage(ctx, binaryReq)
	case WsLogoutMsg:
		resp, messageErr = c.longConnServer.UserLogout(ctx, binaryReq)
	case WsSetBackgroundStatus:
		resp, messageErr = c.setAppBackgroundStatus(ctx, binaryReq)
	case WsSubUserOnlineStatus:
		resp, messageErr = c.longConnServer.SubUserOnlineStatus(ctx, c, binaryReq)
	default:
		return fmt.Errorf(
			"ReqIdentifier failed,sendID:%s,msgIncr:%s,reqIdentifier:%d",
			binaryReq.SendID,
			binaryReq.MsgIncr,
			binaryReq.ReqIdentifier,
		)
	}

	return c.replyMessage(ctx, binaryReq, messageErr, resp)
}

func (c *Client) setAppBackgroundStatus(ctx context.Context, req *Req) ([]byte, error) {
	resp, isBackground, messageErr := c.longConnServer.SetUserDeviceBackground(ctx, req)
	if messageErr != nil {
		return nil, messageErr
	}

	c.IsBackground = isBackground
	// TODO: 补充回调逻辑
	return resp, nil
}

func (c *Client) close() {
	c.w.Lock()
	defer c.w.Unlock()
	if c.closed.Load() {
		return
	}
	c.closed.Store(true)
	c.conn.Close()
	c.hbCancel() // 取消服务端主动心跳
	c.longConnServer.UnRegister(c)
}

func (c *Client) replyMessage(ctx context.Context, binaryReq *Req, err error, resp []byte) error {
	errResp := apiresp.ParseError(err)
	mReply := Resp{
		ReqIdentifier: binaryReq.ReqIdentifier,
		MsgIncr:       binaryReq.MsgIncr,
		OperationID:   binaryReq.OperationID,
		ErrCode:       errResp.ErrCode,
		ErrMsg:        errResp.ErrMsg,
		Data:          resp,
	}
	t := time.Now()
	log.ZDebug(ctx, "gateway reply message", "resp", mReply.String())
	err = c.writeBinaryMsg(mReply)
	if err != nil {
		log.ZWarn(ctx, "wireBinaryMsg replyMessage", err, "resp", mReply.String())
	}
	log.ZDebug(ctx, "wireBinaryMsg end", "time cost", time.Since(t))

	if binaryReq.ReqIdentifier == WsLogoutMsg {
		return errs.New("user logout", "operationID", binaryReq.OperationID).Wrap()
	}
	return nil
}

func (c *Client) PushMessage(ctx context.Context, msgData *sdkws.MsgData) error {
	var msg sdkws.PushMessages
	conversationID := msgprocessor.GetConversationIDByMsg(msgData)
	m := map[string]*sdkws.PullMsgs{conversationID: {Msgs: []*sdkws.MsgData{msgData}}}
	if msgprocessor.IsNotification(conversationID) {
		msg.NotificationMsgs = m
	} else {
		msg.Msgs = m
	}
	log.ZDebug(ctx, "PushMessage", "msg", &msg)
	data, err := proto.Marshal(&msg)
	if err != nil {
		return err
	}
	resp := Resp{
		ReqIdentifier: WSPushMsg,
		OperationID:   mcontext.GetOperationID(ctx),
		Data:          data,
	}
	return c.writeBinaryMsg(resp)
}

func (c *Client) KickOnlineMessage() error {
	resp := Resp{
		ReqIdentifier: WSKickOnlineMsg,
	}
	log.ZDebug(c.ctx, "KickOnlineMessage debug ")
	err := c.writeBinaryMsg(resp)
	c.close()
	return err
}

func (c *Client) PushUserOnlineStatus(data []byte) error {
	resp := Resp{
		ReqIdentifier: WsSubUserOnlineStatus,
		Data:          data,
	}
	return c.writeBinaryMsg(resp)
}

func (c *Client) writeBinaryMsg(resp Resp) error {
	if c.closed.Load() {
		return nil
	}

	encodedBuf, err := c.Encoder.Encode(resp)
	if err != nil {
		return err
	}

	c.w.Lock()
	defer c.w.Unlock()

	if c.IsCompress {
		resultBuf, compressErr := c.longConnServer.CompressWithPool(encodedBuf)
		if compressErr != nil {
			return compressErr
		}
		return c.conn.WriteMessage(resultBuf)
	}

	return c.conn.WriteMessage(encodedBuf)
}

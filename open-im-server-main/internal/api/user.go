package api

import (
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/msggateway"
	"github.com/openimsdk/protocol/user"
	"github.com/openimsdk/tools/a2r"
	"github.com/openimsdk/tools/apiresp"
	"github.com/openimsdk/tools/discovery"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
)

type UserApi struct {
	Client user.UserClient
	discov discovery.Conn
	config config.RpcService
}

func NewUserApi(client user.UserClient, discov discovery.Conn, config config.RpcService) UserApi {
	return UserApi{Client: client, discov: discov, config: config}
}

func (u *UserApi) UserRegister(c *gin.Context) {
	a2r.Call(c, user.UserClient.UserRegister, u.Client)
}

// UpdateUserInfo 已废弃，请使用 UpdateUserInfoEx
func (u *UserApi) UpdateUserInfo(c *gin.Context) {
	a2r.Call(c, user.UserClient.UpdateUserInfo, u.Client)
}

func (u *UserApi) UpdateUserInfoEx(c *gin.Context) {
	a2r.Call(c, user.UserClient.UpdateUserInfoEx, u.Client)
}
func (u *UserApi) SetGlobalRecvMessageOpt(c *gin.Context) {
	a2r.Call(c, user.UserClient.SetGlobalRecvMessageOpt, u.Client)
}

func (u *UserApi) GetUsersPublicInfo(c *gin.Context) {
	a2r.Call(c, user.UserClient.GetDesignateUsers, u.Client)
}

func (u *UserApi) GetAllUsersID(c *gin.Context) {
	a2r.Call(c, user.UserClient.GetAllUserID, u.Client)
}

func (u *UserApi) AccountCheck(c *gin.Context) {
	a2r.Call(c, user.UserClient.AccountCheck, u.Client)
}

func (u *UserApi) GetUsers(c *gin.Context) {
	a2r.Call(c, user.UserClient.GetPaginationUsers, u.Client)
}

// GetUsersOnlineStatus 获取用户在线状态。
func (u *UserApi) GetUsersOnlineStatus(c *gin.Context) {
	var req msggateway.GetUsersOnlineStatusReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	conns, err := u.discov.GetConns(c, u.config.MessageGateway)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	var wsResult []*msggateway.GetUsersOnlineStatusResp_SuccessResult
	var respResult []*msggateway.GetUsersOnlineStatusResp_SuccessResult
	flag := false

	// 在线推送消息
	for _, v := range conns {
		msgClient := msggateway.NewMsgGatewayClient(v)
		reply, err := msgClient.GetUsersOnlineStatus(c, &req)
		if err != nil {
			log.ZDebug(c, "GetUsersOnlineStatus rpc error", err)

			parseError := apiresp.ParseError(err)
			if parseError.ErrCode == errs.NoPermissionError {
				apiresp.GinError(c, err)
				return
			}
		} else {
			wsResult = append(wsResult, reply.SuccessResult...)
		}
	}
	// 遍历 API 请求体中的 userIDs
	for _, v1 := range req.UserIDs {
		flag = false
		res := new(msggateway.GetUsersOnlineStatusResp_SuccessResult)
		// 遍历从各个网关获取的在线结果
		for _, v2 := range wsResult {
			// 若请求中的用户与网关返回结果中的 userID 相互匹配
			if v2.UserID == v1 {
				flag = true
				res.UserID = v1
				res.Status = constant.Online
				res.DetailPlatformStatus = append(res.DetailPlatformStatus, v2.DetailPlatformStatus...)
				break
			}
		}
		if !flag {
			res.UserID = v1
			res.Status = constant.Offline
		}
		respResult = append(respResult, res)
	}
	apiresp.GinSuccess(c, respResult)
}

func (u *UserApi) UserRegisterCount(c *gin.Context) {
	a2r.Call(c, user.UserClient.UserRegisterCount, u.Client)
}

// GetUsersOnlineTokenDetail 获取用户在线 Token 详情。
func (u *UserApi) GetUsersOnlineTokenDetail(c *gin.Context) {
	var wsResult []*msggateway.GetUsersOnlineStatusResp_SuccessResult
	var respResult []*msggateway.SingleDetail
	flag := false
	var req msggateway.GetUsersOnlineStatusReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	conns, err := u.discov.GetConns(c, u.config.MessageGateway)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	// 在线推送消息
	for _, v := range conns {
		msgClient := msggateway.NewMsgGatewayClient(v)
		reply, err := msgClient.GetUsersOnlineStatus(c, &req)
		if err != nil {
			log.ZWarn(c, "GetUsersOnlineStatus rpc err", err)
			continue
		} else {
			wsResult = append(wsResult, reply.SuccessResult...)
		}
	}

	for _, v1 := range req.UserIDs {
		m := make(map[int32][]string, 10)
		flag = false
		temp := new(msggateway.SingleDetail)
		for _, v2 := range wsResult {
			if v2.UserID == v1 {
				flag = true
				temp.UserID = v1
				temp.Status = constant.Online
				for _, status := range v2.DetailPlatformStatus {
					if v, ok := m[status.PlatformID]; ok {
						m[status.PlatformID] = append(v, status.Token)
					} else {
						m[status.PlatformID] = []string{status.Token}
					}
				}
			}
		}
		for p, tokens := range m {
			t := new(msggateway.SinglePlatformToken)
			t.PlatformID = p
			t.Token = tokens
			t.Total = int32(len(tokens))
			temp.SinglePlatformToken = append(temp.SinglePlatformToken, t)
		}

		if flag {
			respResult = append(respResult, temp)
		}
	}

	apiresp.GinSuccess(c, respResult)
}

// SubscriberStatus 被订阅用户的存在状态。
func (u *UserApi) SubscriberStatus(c *gin.Context) {
	a2r.Call(c, user.UserClient.SubscribeOrCancelUsersStatus, u.Client)
}

// GetUserStatus 获取用户的在线状态。
func (u *UserApi) GetUserStatus(c *gin.Context) {
	a2r.Call(c, user.UserClient.GetUserStatus, u.Client)
}

// GetSubscribeUsersStatus 获取订阅者的在线状态。
func (u *UserApi) GetSubscribeUsersStatus(c *gin.Context) {
	a2r.Call(c, user.UserClient.GetSubscribeUsersStatus, u.Client)
}

// ProcessUserCommandAdd 新增用户指令。
func (u *UserApi) ProcessUserCommandAdd(c *gin.Context) {
	a2r.Call(c, user.UserClient.ProcessUserCommandAdd, u.Client)
}

// ProcessUserCommandDelete 删除用户指令。
func (u *UserApi) ProcessUserCommandDelete(c *gin.Context) {
	a2r.Call(c, user.UserClient.ProcessUserCommandDelete, u.Client)
}

// ProcessUserCommandUpdate 更新用户指令。
func (u *UserApi) ProcessUserCommandUpdate(c *gin.Context) {
	a2r.Call(c, user.UserClient.ProcessUserCommandUpdate, u.Client)
}

// ProcessUserCommandGet 获取用户指令。
func (u *UserApi) ProcessUserCommandGet(c *gin.Context) {
	a2r.Call(c, user.UserClient.ProcessUserCommandGet, u.Client)
}

// ProcessUserCommandGetAll 获取全部用户指令。
func (u *UserApi) ProcessUserCommandGetAll(c *gin.Context) {
	a2r.Call(c, user.UserClient.ProcessUserCommandGetAll, u.Client)
}

func (u *UserApi) AddNotificationAccount(c *gin.Context) {
	a2r.Call(c, user.UserClient.AddNotificationAccount, u.Client)
}

func (u *UserApi) UpdateNotificationAccountInfo(c *gin.Context) {
	a2r.Call(c, user.UserClient.UpdateNotificationAccountInfo, u.Client)
}

func (u *UserApi) SearchNotificationAccount(c *gin.Context) {
	a2r.Call(c, user.UserClient.SearchNotificationAccount, u.Client)
}

func (u *UserApi) GetUserClientConfig(c *gin.Context) {
	a2r.Call(c, user.UserClient.GetUserClientConfig, u.Client)
}

func (u *UserApi) SetUserClientConfig(c *gin.Context) {
	a2r.Call(c, user.UserClient.SetUserClientConfig, u.Client)
}

func (u *UserApi) DelUserClientConfig(c *gin.Context) {
	a2r.Call(c, user.UserClient.DelUserClientConfig, u.Client)
}

func (u *UserApi) PageUserClientConfig(c *gin.Context) {
	a2r.Call(c, user.UserClient.PageUserClientConfig, u.Client)
}

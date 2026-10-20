package user

import (
	"fmt"

	gettoken "github.com/openimsdk/open-im-server/v3/test/e2e/api/token"
	"github.com/openimsdk/open-im-server/v3/test/e2e/framework/config"
)

// UserInfoRequest 表示获取或更新用户信息的请求。
type UserInfoRequest struct {
	UserIDs  []string       `json:"userIDs,omitempty"`
	UserInfo *gettoken.User `json:"userInfo,omitempty"`
}

// GetUsersOnlineStatusRequest 表示获取用户在线状态的请求。
type GetUsersOnlineStatusRequest struct {
	UserIDs []string `json:"userIDs"`
}

// GetUsersInfo 批量查询用户 ID 对应的详细信息。
func GetUsersInfo(token string, userIDs []string) error {

	url := fmt.Sprintf("http://%s:%s/user/get_users_info", config.LoadConfig().APIHost, config.LoadConfig().APIPort)

	requestBody := UserInfoRequest{
		UserIDs: userIDs,
	}
	return sendPostRequestWithToken(url, token, requestBody)
}

// UpdateUserInfo 更新某个用户的信息。
func UpdateUserInfo(token, userID, nickname, faceURL string) error {

	url := fmt.Sprintf("http://%s:%s/user/update_user_info", config.LoadConfig().APIHost, config.LoadConfig().APIPort)

	requestBody := UserInfoRequest{
		UserInfo: &gettoken.User{
			UserID:   userID,
			Nickname: nickname,
			FaceURL:  faceURL,
		},
	}
	return sendPostRequestWithToken(url, token, requestBody)
}

// GetUsersOnlineStatus 批量查询用户的在线状态。
func GetUsersOnlineStatus(token string, userIDs []string) error {

	url := fmt.Sprintf("http://%s:%s/user/get_users_online_status", config.LoadConfig().APIHost, config.LoadConfig().APIPort)

	requestBody := GetUsersOnlineStatusRequest{
		UserIDs: userIDs,
	}

	return sendPostRequestWithToken(url, token, requestBody)
}

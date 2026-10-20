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

package user

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	gettoken "github.com/openimsdk/open-im-server/v3/test/e2e/api/token"
	"github.com/openimsdk/open-im-server/v3/test/e2e/framework/config"
)

// ForceLogoutRequest 表示强制用户下线的请求。
type ForceLogoutRequest struct {
	PlatformID int    `json:"platformID"`
	UserID     string `json:"userID"`
}

// CheckUserAccountRequest 表示校验用户账号的请求。
type CheckUserAccountRequest struct {
	CheckUserIDs []string `json:"checkUserIDs"`
}

// GetUsersRequest 表示批量获取用户列表的请求。
type GetUsersRequest struct {
	Pagination Pagination `json:"pagination"`
}

// Pagination 指定页码和每页条数。
type Pagination struct {
	PageNumber int `json:"pageNumber"`
	ShowNumber int `json:"showNumber"`
}

// ForceLogout 强制指定用户下线。
func ForceLogout(token, userID string, platformID int) error {

	url := fmt.Sprintf("http://%s:%s/auth/force_logout", config.LoadConfig().APIHost, config.LoadConfig().APIPort)

	requestBody := ForceLogoutRequest{
		PlatformID: platformID,
		UserID:     userID,
	}
	return sendPostRequestWithToken(url, token, requestBody)
}

// CheckUserAccount 检查用户账号是否存在。
func CheckUserAccount(token string, userIDs []string) error {

	url := fmt.Sprintf("http://%s:%s/user/account_check", config.LoadConfig().APIHost, config.LoadConfig().APIPort)

	requestBody := CheckUserAccountRequest{
		CheckUserIDs: userIDs,
	}
	return sendPostRequestWithToken(url, token, requestBody)
}

// GetUsers 分页获取用户列表。
func GetUsers(token string, pageNumber, showNumber int) error {

	url := fmt.Sprintf("http://%s:%s/user/account_check", config.LoadConfig().APIHost, config.LoadConfig().APIPort)

	requestBody := GetUsersRequest{
		Pagination: Pagination{
			PageNumber: pageNumber,
			ShowNumber: showNumber,
		},
	}
	return sendPostRequestWithToken(url, token, requestBody)
}

// sendPostRequestWithToken 发送请求头中携带 token 的 POST 请求。
func sendPostRequestWithToken(url, token string, body any) error {
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBytes))
	if err != nil {
		return err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("operationID", gettoken.OperationID)
	req.Header.Add("token", token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var respData map[string]any
	if err := json.Unmarshal(respBody, &respData); err != nil {
		return err
	}

	if errCode, ok := respData["errCode"].(float64); ok && errCode != 0 {
		return fmt.Errorf("error in response: %v", respData)
	}

	return nil
}

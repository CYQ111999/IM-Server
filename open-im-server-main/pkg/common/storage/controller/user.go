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
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/model"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/user"
	"github.com/openimsdk/tools/db/pagination"
	"github.com/openimsdk/tools/db/tx"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/utils/datautil"

	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache"
)

// UserDatabase 用户数据库操作接口
type UserDatabase interface {
	// FindWithError 获取指定用户信息，userID 不存在时也返回错误
	FindWithError(ctx context.Context, userIDs []string) (users []*model.User, err error)
	// Find 获取指定用户信息，userID 不存在时不返回错误
	Find(ctx context.Context, userIDs []string) (users []*model.User, err error)
	// FindByNickname 按昵称查找用户
	FindByNickname(ctx context.Context, nickname string) (users []*model.User, err error)
	// FindNotification 按等级查找系统账号
	FindNotification(ctx context.Context, level int64) (users []*model.User, err error)
	// FindSystemAccount 查找所有系统账号
	FindSystemAccount(ctx context.Context) (users []*model.User, err error)
	// Create 批量插入用户，调用方保证 userID 不重复且不存在于存储中
	Create(ctx context.Context, users []*model.User) (err error)
	// UpdateByMap 按 map 更新用户字段（支持零值），调用方保证 userID 存在
	UpdateByMap(ctx context.Context, userID string, args map[string]any) (err error)
	// PageFindUser 按等级区间分页查找用户
	PageFindUser(ctx context.Context, level1 int64, level2 int64, pagination pagination.Pagination) (count int64, users []*model.User, err error)
	// PageFindUserWithKeyword 按等级区间 + 关键字分页查找用户
	PageFindUserWithKeyword(ctx context.Context, level1 int64, level2 int64, userID string, nickName string, pagination pagination.Pagination) (count int64, users []*model.User, err error)
	// Page 分页获取用户，查不到不返回错误
	Page(ctx context.Context, pagination pagination.Pagination) (count int64, users []*model.User, err error)
	// IsExist 只要有一个存在就返回 true
	IsExist(ctx context.Context, userIDs []string) (exist bool, err error)
	// GetAllUserID 获取所有用户 ID
	GetAllUserID(ctx context.Context, pagination pagination.Pagination) (int64, []string, error)
	// GetUserByID 按 userID 获取用户
	GetUserByID(ctx context.Context, userID string) (user *model.User, err error)
	// InitOnce 函数内部先查存储中是否已存在，存在则不动，不存在则插入
	InitOnce(ctx context.Context, users []*model.User) (err error)
	// CountTotal 获取用户总数
	CountTotal(ctx context.Context, before *time.Time) (int64, error)
	// CountRangeEverydayTotal 获取区间内每天的用户增量
	CountRangeEverydayTotal(ctx context.Context, start time.Time, end time.Time) (map[string]int64, error)

	SortQuery(ctx context.Context, userIDName map[string]string, asc bool) ([]*model.User, error)

	// 用户自定义指令（command）CRUD
	AddUserCommand(ctx context.Context, userID string, Type int32, UUID string, value string, ex string) error
	DeleteUserCommand(ctx context.Context, userID string, Type int32, UUID string) error
	UpdateUserCommand(ctx context.Context, userID string, Type int32, UUID string, val map[string]any) error
	GetUserCommands(ctx context.Context, userID string, Type int32) ([]*user.CommandInfoResp, error)
	GetAllUserCommands(ctx context.Context, userID string) ([]*user.AllCommandInfoResp, error)
}

type userDatabase struct {
	tx     tx.Tx
	userDB database.User
	cache  cache.UserCache
}

func NewUserDatabase(userDB database.User, cache cache.UserCache, tx tx.Tx) UserDatabase {
	return &userDatabase{userDB: userDB, cache: cache, tx: tx}
}

// InitOnce 幂等初始化：已存在的用户跳过，昵称不一致的更新，缺失的插入
func (u *userDatabase) InitOnce(ctx context.Context, users []*model.User) error {
	// 从用户模型中提取 userID
	userIDs := datautil.Slice(users, func(e *model.User) string {
		return e.UserID
	})

	// 查询数据库中已存在的用户
	existingUsers, err := u.userDB.Find(ctx, userIDs)
	if err != nil {
		return err
	}

	// 找出数据库中缺失的用户
	var (
		missing, update []*model.User
	)
	existMap := datautil.SliceToMap(existingUsers, func(e *model.User) string {
		return e.UserID
	})
	orgMap := datautil.SliceToMap(users, func(e *model.User) string { return e.UserID })
	for k, u1 := range orgMap {
		if u2, ok := existMap[k]; !ok {
			missing = append(missing, u1)
		} else if u1.Nickname != u2.Nickname {
			update = append(update, u1)
		}
	}

	// 为缺失的用户创建记录
	if len(missing) > 0 {
		if err := u.userDB.Create(ctx, missing); err != nil {
			return err
		}
	}
	if len(update) > 0 {
		for i := range update {
			if err := u.userDB.UpdateByMap(ctx, update[i].UserID, map[string]any{"nickname": update[i].Nickname}); err != nil {
				return err
			}
		}
	}

	return nil
}

// FindWithError 获取指定用户信息，userID 不存在时返回错误。
func (u *userDatabase) FindWithError(ctx context.Context, userIDs []string) (users []*model.User, err error) {
	userIDs = datautil.Distinct(userIDs)

	// TODO: 补充逻辑，识别哪些 userID 是重复的、哪些没找到

	users, err = u.cache.GetUsersInfo(ctx, userIDs)
	if err != nil {
		return
	}

	if len(users) != len(userIDs) {
		err = errs.ErrRecordNotFound.WrapMsg("userID not found")
	}
	return
}

// Find 获取指定用户信息。userID 不存在时不返回错误。
func (u *userDatabase) Find(ctx context.Context, userIDs []string) (users []*model.User, err error) {
	return u.cache.GetUsersInfo(ctx, userIDs)
}

func (u *userDatabase) FindByNickname(ctx context.Context, nickname string) (users []*model.User, err error) {
	return u.userDB.TakeByNickname(ctx, nickname)
}

func (u *userDatabase) FindNotification(ctx context.Context, level int64) (users []*model.User, err error) {
	return u.userDB.TakeNotification(ctx, level)
}

func (u *userDatabase) FindSystemAccount(ctx context.Context) (users []*model.User, err error) {
	return u.userDB.TakeGTEAppManagerLevel(ctx, constant.AppNotificationAdmin)
}

// Create 批量插入用户，调用方保证 userID 不重复且不存在于存储中。
func (u *userDatabase) Create(ctx context.Context, users []*model.User) (err error) {
	return u.tx.Transaction(ctx, func(ctx context.Context) error {
		if err = u.userDB.Create(ctx, users); err != nil {
			return err
		}
		return u.cache.DelUsersInfo(datautil.Slice(users, func(e *model.User) string {
			return e.UserID
		})...).ChainExecDel(ctx)
	})
}

// UpdateByMap 按 map 更新用户字段（支持零值），调用方保证 userID 存在。
func (u *userDatabase) UpdateByMap(ctx context.Context, userID string, args map[string]any) (err error) {
	return u.tx.Transaction(ctx, func(ctx context.Context) error {
		if err := u.userDB.UpdateByMap(ctx, userID, args); err != nil {
			return err
		}
		return u.cache.DelUsersInfo(userID).ChainExecDel(ctx)
	})
}

// Page 分页获取用户，查不到不返回错误。
func (u *userDatabase) Page(ctx context.Context, pagination pagination.Pagination) (count int64, users []*model.User, err error) {
	return u.userDB.Page(ctx, pagination)
}

func (u *userDatabase) PageFindUser(ctx context.Context, level1 int64, level2 int64, pagination pagination.Pagination) (count int64, users []*model.User, err error) {
	return u.userDB.PageFindUser(ctx, level1, level2, pagination)
}

func (u *userDatabase) PageFindUserWithKeyword(ctx context.Context, level1 int64, level2 int64, userID, nickName string, pagination pagination.Pagination) (count int64, users []*model.User, err error) {
	return u.userDB.PageFindUserWithKeyword(ctx, level1, level2, userID, nickName, pagination)
}

// IsExist 用户是否存在？只要有一个存在就返回 true。
func (u *userDatabase) IsExist(ctx context.Context, userIDs []string) (exist bool, err error) {
	users, err := u.userDB.Find(ctx, userIDs)
	if err != nil {
		return false, err
	}
	if len(users) > 0 {
		return true, nil
	}
	return false, nil
}

// GetAllUserID 获取所有用户 ID。
func (u *userDatabase) GetAllUserID(ctx context.Context, pagination pagination.Pagination) (total int64, userIDs []string, err error) {
	return u.userDB.GetAllUserID(ctx, pagination)
}

func (u *userDatabase) GetUserByID(ctx context.Context, userID string) (user *model.User, err error) {
	return u.cache.GetUserInfo(ctx, userID)
}

// CountTotal 获取用户总数。
func (u *userDatabase) CountTotal(ctx context.Context, before *time.Time) (count int64, err error) {
	return u.userDB.CountTotal(ctx, before)
}

// CountRangeEverydayTotal 获取区间内的用户增量。
func (u *userDatabase) CountRangeEverydayTotal(ctx context.Context, start time.Time, end time.Time) (map[string]int64, error) {
	return u.userDB.CountRangeEverydayTotal(ctx, start, end)
}

func (u *userDatabase) SortQuery(ctx context.Context, userIDName map[string]string, asc bool) ([]*model.User, error) {
	return u.userDB.SortQuery(ctx, userIDName, asc)
}

func (u *userDatabase) AddUserCommand(ctx context.Context, userID string, Type int32, UUID string, value string, ex string) error {
	return u.userDB.AddUserCommand(ctx, userID, Type, UUID, value, ex)
}

func (u *userDatabase) DeleteUserCommand(ctx context.Context, userID string, Type int32, UUID string) error {
	return u.userDB.DeleteUserCommand(ctx, userID, Type, UUID)
}

func (u *userDatabase) UpdateUserCommand(ctx context.Context, userID string, Type int32, UUID string, val map[string]any) error {
	return u.userDB.UpdateUserCommand(ctx, userID, Type, UUID, val)
}

func (u *userDatabase) GetUserCommands(ctx context.Context, userID string, Type int32) ([]*user.CommandInfoResp, error) {
	commands, err := u.userDB.GetUserCommand(ctx, userID, Type)
	return commands, err
}

func (u *userDatabase) GetAllUserCommands(ctx context.Context, userID string) ([]*user.AllCommandInfoResp, error) {
	commands, err := u.userDB.GetAllUserCommand(ctx, userID)
	return commands, err
}

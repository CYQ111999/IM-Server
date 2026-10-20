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

package servererrs

// UnknownCode 表示错误码未解析或解析结果为 0 时的错误码。
const UnknownCode = 1000

// 各类错误场景的错误码。
const (
	FormattingError      = 10001 // Error in formatting
	HasRegistered        = 10002 // user has already registered
	NotRegistered        = 10003 // user is not registered
	PasswordErr          = 10004 // Password error
	GetIMTokenErr        = 10005 // Error in getting IM token
	RepeatSendCode       = 10006 // Repeat sending code
	MailSendCodeErr      = 10007 // Error in sending code via email
	SmsSendCodeErr       = 10008 // Error in sending code via SMS
	CodeInvalidOrExpired = 10009 // Code is invalid or expired
	RegisterFailed       = 10010 // Registration failed
	ResetPasswordFailed  = 10011 // Resetting password failed
	RegisterLimit        = 10012 // Registration limit exceeded
	LoginLimit           = 10013 // Login limit exceeded
	InvitationError      = 10014 // Error in invitation
)

// 通用错误码。
const (
	NoError = 0 // No error

	DatabaseError = 90002 // Database error (redis/mysql, etc.)
	NetworkError  = 90004 // Network error
	DataError     = 90007 // Data error

	CallbackError = 80000

	// 通用错误码。
	ServerInternalError   = 500  // Server internal error
	ArgsError             = 1001 // Input parameter error
	NoPermissionError     = 1002 // Insufficient permission
	DuplicateKeyError     = 1003
	RecordNotFoundError   = 1004 // Record does not exist
	SecretNotChangedError = 1050 // secret not changed

	// 账号相关错误码。
	UserIDNotFoundError    = 1101 // UserID does not exist or is not registered
	RegisteredAlreadyError = 1102 // user is already registered

	// 群组相关错误码。
	GroupIDNotFoundError  = 1201 // GroupID does not exist
	GroupIDExisted        = 1202 // GroupID already exists
	NotInGroupYetError    = 1203 // Not in the group yet
	DismissedAlreadyError = 1204 // Group has already been dismissed
	GroupTypeNotSupport   = 1205
	GroupRequestHandled   = 1206

	// 关系链相关错误码。
	CanNotAddYourselfError   = 1301 // Cannot add yourself as a friend
	BlockedByPeer            = 1302 // Blocked by the peer
	NotPeersFriend           = 1303 // Not the peer's friend
	RelationshipAlreadyError = 1304 // Already in a friend relationship
	FriendRequestHandled     = 1305 // Friend request has already been handled

	// 消息相关错误码。
	MessageHasReadDisable = 1401
	MutedInGroup          = 1402 // Member muted in the group
	MutedGroup            = 1403 // Group is muted
	MsgAlreadyRevoke      = 1404 // Message already revoked

	// Token 相关错误码。
	TokenExpiredError     = 1501
	TokenInvalidError     = 1502
	TokenMalformedError   = 1503
	TokenNotValidYetError = 1504
	TokenUnknownError     = 1505
	TokenKickedError      = 1506
	TokenNotExistError    = 1507

	// 长连接网关相关错误码。
	ConnOverMaxNumLimit  = 1601
	ConnArgsErr          = 1602
	PushMsgErr           = 1603
	IOSBackgroundPushErr = 1604

	// S3 对象存储相关错误码。
	FileUploadedExpiredError = 1701 // Upload expired
)

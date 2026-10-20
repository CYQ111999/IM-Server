package servererrs

// UnknownCode 表示错误码未解析或解析结果为 0 时的错误码。
const UnknownCode = 1000

// 各类错误场景的错误码。
const (
	FormattingError      = 10001 // 格式化错误
	HasRegistered        = 10002 // 用户已注册
	NotRegistered        = 10003 // 用户未注册
	PasswordErr          = 10004 // 密码错误
	GetIMTokenErr        = 10005 // 获取 IM token 失败
	RepeatSendCode       = 10006 // 重复发送验证码
	MailSendCodeErr      = 10007 // 邮件验证码发送失败
	SmsSendCodeErr       = 10008 // 短信验证码发送失败
	CodeInvalidOrExpired = 10009 // 验证码无效或已过期
	RegisterFailed       = 10010 // 注册失败
	ResetPasswordFailed  = 10011 // 重置密码失败
	RegisterLimit        = 10012 // 超出注册次数限制
	LoginLimit           = 10013 // 超出登录次数限制
	InvitationError      = 10014 // 邀请失败
)

// 通用错误码。
const (
	NoError = 0 // 无错误

	DatabaseError = 90002 // Database error (redis/mysql, etc.)
	NetworkError  = 90004 // 网络错误
	DataError     = 90007 // 数据错误

	CallbackError = 80000

	// 通用错误码。
	ServerInternalError   = 500  // 服务内部错误
	ArgsError             = 1001 // 入参错误
	NoPermissionError     = 1002 // 权限不足
	DuplicateKeyError     = 1003
	RecordNotFoundError   = 1004 // 记录不存在
	SecretNotChangedError = 1050 // 密钥未变更

	// 账号相关错误码。
	UserIDNotFoundError    = 1101 // UserID 不存在或未注册
	RegisteredAlreadyError = 1102 // 用户已注册

	// 群组相关错误码。
	GroupIDNotFoundError  = 1201 // GroupID 不存在
	GroupIDExisted        = 1202 // GroupID 已存在
	NotInGroupYetError    = 1203 // 尚未加入该群
	DismissedAlreadyError = 1204 // 群已被解散
	GroupTypeNotSupport   = 1205
	GroupRequestHandled   = 1206

	// 关系链相关错误码。
	CanNotAddYourselfError   = 1301 // 不能添加自己为好友
	BlockedByPeer            = 1302 // 已被对方拉黑
	NotPeersFriend           = 1303 // 不是对方的好友
	RelationshipAlreadyError = 1304 // 已是好友关系
	FriendRequestHandled     = 1305 // 好友申请已处理

	// 消息相关错误码。
	MessageHasReadDisable = 1401
	MutedInGroup          = 1402 // 群成员已被禁言
	MutedGroup            = 1403 // 群已被禁言
	MsgAlreadyRevoke      = 1404 // 消息已撤回

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
	FileUploadedExpiredError = 1701 // 上传已过期
)

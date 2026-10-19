package controller

import (
	"context"

	"github.com/golang-jwt/jwt/v4"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/storage/cache/cachekey"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/tokenverify"
)

// AuthDatabase 鉴权数据库操作接口（token 的创建/校验/多端互踢，全部走 Redis）
type AuthDatabase interface {
	// GetTokensWithoutError 获取用户的 token  map，结果为空不返回错误
	GetTokensWithoutError(ctx context.Context, userID string, platformID int) (map[string]int, error)

	// GetTemporaryTokensWithoutError 校验临时 token 是否存在
	GetTemporaryTokensWithoutError(ctx context.Context, userID string, platformID int, token string) error
	// CreateToken 创建 token
	CreateToken(ctx context.Context, userID string, platformID int) (string, error)

	// BatchSetTokenMapByUidPid 批量将 token 标记为已踢出
	BatchSetTokenMapByUidPid(ctx context.Context, tokens []string) error

	// SetTokenMapByUidPid 覆盖写入某用户某平台的 token map
	SetTokenMapByUidPid(ctx context.Context, userID string, platformID int, m map[string]int) error
}

type multiLoginConfig struct {
	Policy       int
	MaxNumOneEnd int
}

type authDatabase struct {
	cache        cache.TokenModel
	accessSecret string
	accessExpire int64
	multiLogin   multiLoginConfig
	adminUserIDs []string
}

func NewAuthDatabase(cache cache.TokenModel, accessSecret string, accessExpire int64, multiLogin config.MultiLogin, adminUserIDs []string) AuthDatabase {
	return &authDatabase{cache: cache, accessSecret: accessSecret, accessExpire: accessExpire, multiLogin: multiLoginConfig{
		Policy:       multiLogin.Policy,
		MaxNumOneEnd: multiLogin.MaxNumOneEnd,
	},
		adminUserIDs: adminUserIDs,
	}
}

// GetTokensWithoutError 获取用户的 token map，结果为空不返回错误。
func (a *authDatabase) GetTokensWithoutError(ctx context.Context, userID string, platformID int) (map[string]int, error) {
	return a.cache.GetTokensWithoutError(ctx, userID, platformID)
}

func (a *authDatabase) GetTemporaryTokensWithoutError(ctx context.Context, userID string, platformID int, token string) error {
	return a.cache.HasTemporaryToken(ctx, userID, platformID, token)
}

func (a *authDatabase) SetTokenMapByUidPid(ctx context.Context, userID string, platformID int, m map[string]int) error {
	return a.cache.SetTokenMapByUidPid(ctx, userID, platformID, m)
}

// BatchSetTokenMapByUidPid 批量把 token 置为已踢出状态（解析失败的直接跳过）
func (a *authDatabase) BatchSetTokenMapByUidPid(ctx context.Context, tokens []string) error {
	setMap := make(map[string]map[string]any)
	for _, token := range tokens {
		claims, err := tokenverify.GetClaimFromToken(token, authverify.Secret(a.accessSecret))
		if err != nil {
			continue
		}
		key := cachekey.GetTokenKey(claims.UserID, claims.PlatformID)
		if v, ok := setMap[key]; ok {
			v[token] = constant.KickedToken
		} else {
			setMap[key] = map[string]any{
				token: constant.KickedToken,
			}
		}
	}
	if err := a.cache.BatchSetTokenMapByUidPid(ctx, setMap); err != nil {
		return err
	}
	return nil
}

// CreateToken 创建 token：先按多端登录策略清理/踢出旧 token，再签发新 JWT 并写入缓存
func (a *authDatabase) CreateToken(ctx context.Context, userID string, platformID int) (string, error) {
	tokens, err := a.cache.GetAllTokensWithoutError(ctx, userID)
	if err != nil {
		return "", err
	}

	deleteTokenKey, kickedTokenKey, adminTokens, err := a.checkToken(ctx, tokens, platformID)
	if err != nil {
		return "", err
	}
	if len(deleteTokenKey) != 0 {
		err = a.cache.DeleteTokenByTokenMap(ctx, userID, deleteTokenKey)
		if err != nil {
			return "", err
		}
	}
	if len(kickedTokenKey) != 0 {
		for plt, ks := range kickedTokenKey {
			for _, k := range ks {
				err := a.cache.SetTokenFlagEx(ctx, userID, plt, k, constant.KickedToken)
				if err != nil {
					return "", err
				}
				log.ZDebug(ctx, "kicked token in create token", "token", k)
			}
		}
	}
	if len(adminTokens) != 0 {
		if err = a.cache.DeleteAndSetTemporary(ctx, userID, constant.AdminPlatformID, adminTokens); err != nil {
			return "", err
		}
	}

	// 签发 JWT（HS256，有效期由 accessExpire 控制）
	claims := tokenverify.BuildClaims(userID, platformID, a.accessExpire)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(a.accessSecret))
	if err != nil {
		return "", errs.WrapMsg(err, "token.SignedString")
	}

	if err = a.cache.SetTokenFlagEx(ctx, userID, platformID, tokenString, constant.NormalToken); err != nil {
		return "", err
	}

	return tokenString, nil
}

// checkToken 按多端登录策略检查现有 token，返回（待删除 token、待踢出 token、管理员 token 三类）
func (a *authDatabase) checkToken(ctx context.Context, tokens map[int]map[string]int, platformID int) (map[int][]string, map[int][]string, []string, error) {
	// todo: 旧数据异步删除
	var (
		loginTokenMap  = make(map[int][]string) // map 的 value 长度必须大于 0
		deleteToken    = make(map[int][]string)
		kickToken      = make(map[int][]string)
		adminToken     = make([]string, 0)
		unkickTerminal = ""
	)

	// 第一轮：分离出无效 token（解析失败或已被踢出）与有效登录 token
	for plfID, tks := range tokens {
		for k, v := range tks {
			_, err := tokenverify.GetClaimFromToken(k, authverify.Secret(a.accessSecret))
			if err != nil || v != constant.NormalToken {
				deleteToken[plfID] = append(deleteToken[plfID], k)
			} else {
				if plfID != constant.AdminPlatformID {
					loginTokenMap[plfID] = append(loginTokenMap[plfID], k)
				} else {
					adminToken = append(adminToken, k)
				}
			}
		}
	}

	// 第二轮：按多端登录策略计算需要踢出的 token
	switch a.multiLogin.Policy {
	case constant.DefalutNotKick:
		// 默认不互踢：仅同端数量超过上限时踢最早的
		for plt, ts := range loginTokenMap {
			l := len(ts)
			if platformID == plt {
				l++
			}
			limit := a.multiLogin.MaxNumOneEnd
			if l > limit {
				kickToken[plt] = ts[:l-limit]
			}
		}
	case constant.AllLoginButSameTermKick:
		// 同端互踢：同端只保留最新一个，其他端不受影响
		for plt, ts := range loginTokenMap {
			kickToken[plt] = ts[:len(ts)-1]

			if plt == platformID {
				kickToken[plt] = append(kickToken[plt], ts[len(ts)-1])
			}
		}
	case constant.PCAndOther:
		// PC 端互不踢，其他端合计只保留一个（优先保留 Android）
		unkickTerminal = constant.TerminalPC
		if constant.PlatformIDToClass(platformID) != unkickTerminal {
			for plt, ts := range loginTokenMap {
				if constant.PlatformIDToClass(plt) != unkickTerminal {
					kickToken[plt] = ts
				}
			}
		} else {
			var (
				preKickToken string
				preKickPlt   int
				reserveToken = false
			)
			for plt, ts := range loginTokenMap {
				if constant.PlatformIDToClass(plt) != unkickTerminal {
					// 保留一个其他端的 token
					if !reserveToken {
						reserveToken = true
						kickToken[plt] = ts[:len(ts)-1]
						preKickToken = ts[len(ts)-1]
						preKickPlt = plt
						continue
					} else {
						// 优先保留 Android
						if plt == constant.AndroidPlatformID {
							if preKickToken != "" {
								kickToken[preKickPlt] = append(kickToken[preKickPlt], preKickToken)
							}
							kickToken[plt] = ts[:len(ts)-1]
						} else {
							kickToken[plt] = ts
						}
					}
				}
			}
		}
	case constant.AllLoginButSameClassKick:
		// 同类端互踢：同类别端全部踢出，其他类别各保留一个
		var (
			reserved = make(map[string]struct{})
		)

		for plt, ts := range loginTokenMap {
			if constant.PlatformIDToClass(plt) == constant.PlatformIDToClass(platformID) {
				kickToken[plt] = ts
			} else {
				if _, ok := reserved[constant.PlatformIDToClass(plt)]; !ok {
					reserved[constant.PlatformIDToClass(plt)] = struct{}{}
					kickToken[plt] = ts[:len(ts)-1]
					continue
				} else {
					kickToken[plt] = ts
				}
			}
		}
	default:
		return nil, nil, nil, errs.New("unknown multiLogin policy").Wrap()
	}

	//var adminTokenMaxNum = a.multiLogin.MaxNumOneEnd
	//l := len(adminToken)
	//if platformID == constant.AdminPlatformID {
	//	l++
	//}
	//if l > adminTokenMaxNum {
	//	kickToken = append(kickToken, adminToken[:l-adminTokenMaxNum]...)
	//}
	// 新登录的是管理端时，旧管理端 token 全部作废
	var deleteAdminToken []string
	if platformID == constant.AdminPlatformID {
		deleteAdminToken = adminToken
	}
	return deleteToken, kickToken, deleteAdminToken, nil
}

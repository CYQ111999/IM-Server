package cachekey

const (
	BlackIDsKey = "BLACK_IDS:"
	IsBlackKey  = "IS_BLACK:" // 本地缓存
)

func GetBlackIDsKey(ownerUserID string) string {
	return BlackIDsKey + ownerUserID

}

func GetIsBlackIDsKey(possibleBlackUserID, userID string) string {
	return IsBlackKey + userID + "-" + possibleBlackUserID
}

package model

// SubscribeUserTableName 集合名常量。
const (
	SubscribeUserTableName = "subscribe_user"
)

// SubscribeUser 集合结构体。
type SubscribeUser struct {
	UserID     string   `bson:"user_id"      json:"userID"`
	UserIDList []string `bson:"user_id_list" json:"userIDList"`
}

func (SubscribeUser) TableName() string {
	return SubscribeUserTableName
}

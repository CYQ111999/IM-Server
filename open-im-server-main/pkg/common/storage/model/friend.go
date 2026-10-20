package model

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

// Friend 表示 MongoDB 中好友关系的数据结构。
type Friend struct {
	ID             primitive.ObjectID `bson:"_id"`
	OwnerUserID    string             `bson:"owner_user_id"`
	FriendUserID   string             `bson:"friend_user_id"`
	Remark         string             `bson:"remark"`
	CreateTime     time.Time          `bson:"create_time"`
	AddSource      int32              `bson:"add_source"`
	OperatorUserID string             `bson:"operator_user_id"`
	Ex             string             `bson:"ex"`
	IsPinned       bool               `bson:"is_pinned"`
}

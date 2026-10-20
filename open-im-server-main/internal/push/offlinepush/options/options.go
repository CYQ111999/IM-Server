package options

// Opts 推送可选参数。
type Opts struct {
	Signal        *Signal
	IOSPushSound  string
	IOSBadgeCount bool
	Ex            string
}

// Signal 对应的消息 ID。
type Signal struct {
	ClientMsgID string
}

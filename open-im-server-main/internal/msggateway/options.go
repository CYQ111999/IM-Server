package msggateway

import "time"

type (
	Option  func(opt *configs)
	configs struct {
		// 长连接监听端口
		port int
		// 长连接允许的最大连接数
		maxConnNum int64
		// 连接握手超时时间
		handshakeTimeout time.Duration
		// 允许的最大消息长度
		messageMaxMsgLength int
		// WebSocket 写缓冲区大小，默认 4096（4KB）
		writeBufferSize int
	}
)

func WithPort(port int) Option {
	return func(opt *configs) {
		opt.port = port
	}
}

func WithMaxConnNum(num int64) Option {
	return func(opt *configs) {
		opt.maxConnNum = num
	}
}

func WithHandshakeTimeout(t time.Duration) Option {
	return func(opt *configs) {
		opt.handshakeTimeout = t
	}
}

func WithMessageMaxMsgLength(length int) Option {
	return func(opt *configs) {
		opt.messageMaxMsgLength = length
	}
}

func WithWriteBufferSize(size int) Option {
	return func(opt *configs) {
		opt.writeBufferSize = size
	}
}

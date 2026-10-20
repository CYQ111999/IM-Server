package config

import "github.com/openimsdk/tools/utils/runtimeenv"

const ConfKey = "conf"

const (
	MountConfigFilePath = "CONFIG_PATH"
	DeploymentType      = "DEPLOYMENT_TYPE"
	KUBERNETES          = runtimeenv.Kubernetes
	ETCD                = "etcd"
	//Standalone          = "standalone"
)

const (
	// DefaultDirPerm 用于创建普通目录：所有者可读、写、执行，
	// 组用户和其他用户只读和执行。
	DefaultDirPerm = 0755

	// PrivateFilePerm 用于敏感文件：仅所有者可读写。
	PrivateFilePerm = 0600

	// ExecFilePerm 用于可执行文件：所有者可读、写、执行，
	// 组用户和其他用户只读。
	ExecFilePerm = 0754

	// SharedDirPerm 用于共享目录：所有者和组用户可读、写、执行，
	// 其他用户无权限。
	SharedDirPerm = 0770

	// ReadOnlyDirPerm 用于只读目录：所有者、组用户和其他用户均只读。
	ReadOnlyDirPerm = 0555
)

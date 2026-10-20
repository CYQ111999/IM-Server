package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/field"
)

const (
	DefaultFolderPath = "../config/"
)

// 返回拼接 ../config/ 后的绝对路径，这是 k8s 容器内的配置路径。
func GetDefaultConfigPath() (string, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return "", errs.WrapMsg(err, "failed to get executable path")
	}

	configPath, err := field.OutDir(filepath.Join(filepath.Dir(executablePath), "../config/"))
	if err != nil {
		return "", errs.WrapMsg(err, "failed to get output directory", "outDir", filepath.Join(filepath.Dir(executablePath), "../config/"))
	}
	return configPath, nil
}

// getProjectRoot 返回项目根目录的绝对路径。
func GetProjectRoot() (string, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return "", errs.Wrap(err)
	}
	projectRoot, err := field.OutDir(filepath.Join(filepath.Dir(executablePath), "../../../../.."))
	if err != nil {
		return "", errs.Wrap(err)
	}
	return projectRoot, nil
}

func GetOptionsByNotification(cfg NotificationConfig, sendMessage *bool) msgprocessor.Options {
	opts := msgprocessor.NewOptions()

	if sendMessage != nil {
		cfg.IsSendMsg = *sendMessage
	}
	if cfg.IsSendMsg {
		opts = msgprocessor.WithOptions(opts, msgprocessor.WithUnreadCount(true))
	}
	if cfg.OfflinePush.Enable {
		opts = msgprocessor.WithOptions(opts, msgprocessor.WithOfflinePush(true))
	}
	switch cfg.ReliabilityLevel {
	case constant.UnreliableNotification:
	case constant.ReliableNotificationNoMsg:
		opts = msgprocessor.WithOptions(opts, msgprocessor.WithHistory(true), msgprocessor.WithPersistent())
	}
	opts = msgprocessor.WithOptions(opts, msgprocessor.WithSendMsg(cfg.IsSendMsg))

	return opts
}

// initConfig 从指定路径加载配置到传入的配置结构体中。
// 若指定的配置文件不存在，则尝试从项目默认的 "config" 目录加载。
// 会打印实际使用的配置路径等相关信息。
func initConfig(config any, configName, configFolderPath string) error {
	configFolderPath = filepath.Join(configFolderPath, configName)
	_, err := os.Stat(configFolderPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return errs.WrapMsg(err, "stat config path error", "config Folder Path", configFolderPath)
		}
		path, err := GetProjectRoot()
		if err != nil {
			return err
		}
		configFolderPath = filepath.Join(path, "config", configName)
	}
	data, err := os.ReadFile(configFolderPath)
	if err != nil {
		return errs.WrapMsg(err, "read file error", "config Folder Path", configFolderPath)
	}
	if err = yaml.Unmarshal(data, config); err != nil {
		return errs.WrapMsg(err, "unmarshal yaml error", "config Folder Path", configFolderPath)
	}

	return nil
}

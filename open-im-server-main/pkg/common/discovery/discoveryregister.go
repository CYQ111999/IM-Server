package discovery

import (
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/tools/discovery"
	"github.com/openimsdk/tools/discovery/standalone"
	"github.com/openimsdk/tools/utils/runtimeenv"

	"github.com/openimsdk/tools/discovery/kubernetes"

	"github.com/openimsdk/tools/discovery/etcd"
	"github.com/openimsdk/tools/errs"
)

// NewDiscoveryRegister 根据传入的环境类型创建服务发现与注册客户端。
func NewDiscoveryRegister(discovery *config.Discovery, watchNames []string) (discovery.SvcDiscoveryRegistry, error) {
	if config.Standalone() {
		return standalone.GetSvcDiscoveryRegistry(), nil
	}

	runtimeEnvironment := runtimeenv.RuntimeEnvironment()
	discoveryType := discovery.Enable
	if discoveryType == "" && runtimeEnvironment == config.KUBERNETES {
		discoveryType = config.KUBERNETES
	}
	if discoveryType == config.KUBERNETES && runtimeEnvironment != config.KUBERNETES {
		return nil, errs.New("unsupported discovery type", "type", discoveryType).Wrap()
	}
	switch discoveryType {
	case config.KUBERNETES:
		for i := range watchNames {
			watchNames[i] = strings.Split(watchNames[i], ":")[0]
		}
		return kubernetes.NewConnManager(discovery.Kubernetes.Namespace, watchNames,
			grpc.WithDefaultCallOptions(
				grpc.MaxCallSendMsgSize(1024*1024*20),
			),
			grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		)
	case config.ETCD:
		return etcd.NewSvcDiscoveryRegistry(
			discovery.Etcd.RootDirectory,
			discovery.Etcd.Address,
			watchNames,
			etcd.WithDialTimeout(10*time.Second),
			etcd.WithMaxCallSendMsgSize(20*1024*1024),
			etcd.WithUsernameAndPassword(discovery.Etcd.Username, discovery.Etcd.Password))
	default:
		return nil, errs.New("unsupported discovery type", "type", discoveryType).Wrap()
	}
}
